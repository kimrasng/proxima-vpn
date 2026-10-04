package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/cert"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/process"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/relay"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/shaper"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/stats"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/updater"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// version is stamped at build time via -ldflags "-X main.version=..." by the
// Makefile's build-agent target and api-server/Dockerfile's AGENT_VERSION arg.
var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:   "node-agent",
		Short: "Proxima VPN Node Agent",
	}

	rootCmd.AddCommand(registerCmd())
	rootCmd.AddCommand(unregisterCmd())
	rootCmd.AddCommand(runCmd())
	rootCmd.AddCommand(versionCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the agent version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	}
}

func registerCmd() *cobra.Command {
	var (
		serverURL string
		token     string
		name      string
		country   string
		region    string
		port      int
	)

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register this node with the main server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ip := detectIP()
			if ip == "" {
				return fmt.Errorf("could not detect server IP")
			}

			apiClient := client.NewAPIClient(&config.AgentConfig{ServerURL: serverURL})

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			xrayVersion, err := xray.NewXrayRunner("", "").Version()
			if err != nil {
				xrayVersion = "unknown"
			}

			resp, err := apiClient.Register(ctx, serverURL, token, ip, port, xrayVersion, name, country, region)
			if err != nil {
				return fmt.Errorf("registration failed: %w", err)
			}

			cfg := &config.AgentConfig{
				NodeID:    resp.NodeID,
				APIKey:    resp.APIKey,
				ServerURL: serverURL,
			}

			if err := config.Save(config.DefaultConfigPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Printf("Node registered successfully.\n  Node ID: %s\n  Config:  %s\n", resp.NodeID, config.DefaultConfigPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&serverURL, "server", "", "Main server URL (required)")
	cmd.Flags().StringVar(&token, "token", "", "Registration token (required)")
	cmd.Flags().StringVar(&name, "name", "", "Node name")
	cmd.Flags().StringVar(&country, "country", "", "Country code")
	cmd.Flags().StringVar(&region, "region", "", "Region")
	cmd.Flags().IntVar(&port, "port", 443, "Service port")
	_ = cmd.MarkFlagRequired("server")
	_ = cmd.MarkFlagRequired("token")

	return cmd
}

// unregisterCmd deletes this node's own record from the panel (see
// NodeAgentHandler.Unregister in api-server/internal/handlers/node_agent.go),
// using the API key saved locally by `register`. scripts/uninstall.sh runs
// this before removing the local install so uninstalling a node also removes
// it from the admin panel, instead of leaving a stale entry that previously
// had to be deleted by hand.
func unregisterCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "unregister",
		Short: "Remove this node's registration from the panel",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			apiClient := client.NewAPIClient(cfg)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := apiClient.Unregister(ctx); err != nil {
				return fmt.Errorf("unregister failed: %w", err)
			}

			fmt.Println("Node unregistered from panel successfully.")
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", config.DefaultConfigPath, "Config file path")

	return cmd
}

func runCmd() *cobra.Command {
	var (
		configPath string
		tlsDomain  string
		tlsEmail   string
		certDir    string
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the node agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			apiClient := client.NewAPIClient(cfg)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Role decides what this host runs at all, so it is read before anything
			// starts. A relay forwards packets in the kernel and admits no clients,
			// so it has no Xray config: starting Xray there fails on an empty inbound
			// list, and superviseLoop would then restart it forever while the node
			// reported itself healthy.
			role, err := apiClient.GetRole(ctx)
			if err != nil {
				return fmt.Errorf("fetch node role: %w", err)
			}
			if !role.Valid() {
				return fmt.Errorf("control plane reported unknown node role %q", role)
			}
			policyManager := relay.NewManager()
			if err := waitForPolicy(ctx, role, apiClient, policyManager); err != nil {
				return fmt.Errorf("bootstrap node policy: %w", err)
			}
			if !role.Exits() {
				return runRelay(ctx, cfg, apiClient, policyManager)
			}

			runner := xray.NewXrayRunner("", "")

			xrayConfig, err := apiClient.GetConfig(ctx)
			if err != nil {
				return fmt.Errorf("fetch initial config: %w", err)
			}
			state := newNodeState(xrayConfig)
			egressServer := deviceegress.New(apiClient.RequestBandwidthPermit)
			egressServer.SetAdmitter(apiClient.AdmitDevice)
			// No SOCKS authentication until an authoritative revocation snapshot
			// has been fetched; stale state cannot preserve idle associations.
			egressServer.RevocationUnavailable()
			if err := egressServer.Start(ctx, devicebandwidth.ListenAddress); err != nil {
				// Legacy unlimited configs can still run, but managed configs
				// are rejected by the materializer if the listener is unavailable.
				log.Printf("device egress unavailable: %v", err)
			}
			defer func() { _ = egressServer.Close() }()
			egress := newDeviceEgress(egressServer.SetCredentials)
			egress.ready = func() bool { return ctx.Err() == nil && egressServer.Addr() != nil }
			state.deviceCapability = egress.Capability
			runner.SetConfigTransform(egress.TransformConfig)
			runner.SetConfigRestoreGuard(egress.CanRestore)
			if err := runner.WriteConfig(xrayConfig); err != nil {
				return fmt.Errorf("write xray config: %w", err)
			}
			if err := runner.Start(); err != nil {
				return fmt.Errorf("start xray: %w", err)
			}
			defer func() {
				state.processMu.Lock()
				defer state.processMu.Unlock()
				_ = runner.Stop()
			}()

			applyShaping(xrayConfig, state)

			// Learn the structure digest for the config just started, rather than
			// waiting for configPollLoop's first tick to happen to find the digest
			// unchanged. Without this, a user added during that first interval is
			// indistinguishable from a structural change and forces a restart -
			// dropping every live connection for a routine account edit.
			if digest, err := apiClient.GetConfigDigest(ctx); err != nil {
				log.Printf("warning: initial config digest: %v", err)
			} else if digest.Hash == state.ConfigHash() {
				state.setStructureHash(digest.StructureHash)
			}

			xrayVersion := &versionHolder{}
			if v, err := runner.Version(); err == nil {
				xrayVersion.Set(v)
			} else {
				log.Printf("warning: could not detect xray version: %v", err)
			}

			time.Sleep(2 * time.Second)

			statsClient, err := xray.NewStatsClient(runner.GRPCAddr())
			if err != nil {
				log.Printf("warning: could not connect to xray stats: %v", err)
			}

			if statsClient != nil {
				collector := stats.NewCollector(statsClient, apiClient, stats.DefaultInterval, state.ProvisionedEmails)
				collector.Start(ctx)
				defer collector.Stop()
				defer func() { _ = statsClient.Close() }()
			}

			if tlsDomain != "" {
				cm := cert.NewCertManager(tlsDomain, tlsEmail, certDir)
				if certPath, keyPath, err := cm.ObtainOrRenew(ctx); err != nil {
					log.Printf("warning: initial cert obtain failed: %v", err)
				} else if err := apiClient.ReportTLSCert(ctx, certPath, keyPath); err != nil {
					log.Printf("warning: report tls cert failed: %v", err)
				}
				cm.StartAutoRenew(ctx)
				defer cm.Stop()
			}

			go activeUUIDReportLoop(ctx, apiClient, egressServer)
			go revokedUUIDPollLoop(ctx, apiClient.GetRevokedDevices, apiClient.AcknowledgeRevocation, egressServer)
			go heartbeatLoop(ctx, apiClient, runner, xrayVersion, state)
			go superviseLoop(ctx, runner, state)
			go configPollLoop(ctx, apiClient, runner, statsClient, state)
			go inboundsPollLoop(ctx, apiClient)
			go policyPollLoop(ctx, role, apiClient, policyManager)
			go updateCheckLoop(ctx, updater.NewUpdater(version, "", cfg.ServerURL, cfg.NodeID, cfg.APIKey))
			go xrayUpdateLoop(ctx, apiClient, runner, xrayVersion, state)
			go tlsPollLoop(ctx, apiClient, certDir, tlsDomain)

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			<-sigCh

			log.Println("shutting down...")
			cancel()
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", config.DefaultConfigPath, "Config file path")
	cmd.Flags().StringVar(&tlsDomain, "tls-domain", "", "Domain for Let's Encrypt TLS cert")
	cmd.Flags().StringVar(&tlsEmail, "tls-email", "", "Email for Let's Encrypt")
	cmd.Flags().StringVar(&certDir, "cert-dir", "/etc/node-agent/certs", "Certificate storage directory")

	return cmd
}

// versionHolder is a concurrency-safe box for the currently-running Xray
// version, shared between heartbeatLoop (reads it every beat) and
// xrayUpdateLoop (updates it after a successful binary swap).
type versionHolder struct {
	mu sync.RWMutex
	v  string
}

func (h *versionHolder) Set(v string) {
	h.mu.Lock()
	h.v = v
	h.mu.Unlock()
}

func (h *versionHolder) Get() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.v
}

// nodeState tracks what Xray is actually running, as opposed to what the server
// has published: the heartbeat reports configHash, and configPollLoop diffs
// against users to decide restart vs. incremental update.
type nodeState struct {
	// processMu serializes complete write/restart/rollback and gRPC sequences,
	// not just individual runner methods. Network downloads happen outside it.
	processMu            sync.Mutex
	restartBlocked       bool // protected by processMu; unsafe disk config must not respawn
	deviceCapability     func() (bool, int)
	legacyShapingCleared bool // protected by processMu
	mu                   sync.RWMutex
	config               []byte
	configHash           string
	structureHash        string
	usersHash            string
	users                map[userKey]xray.VLESSUser

	// xrayGen counts Xray (re)starts. syncUsers reads the user set, then makes
	// gRPC calls without the lock held; if the supervisor respawns Xray in that
	// window, writing the result back would claim users the new process never
	// received. Comparing the generation across the gap detects that.
	xrayGen uint64

	shapingOK    bool
	shapingTiers int
	shapingErr   string
}

func (s *nodeState) setShaping(ok bool, tiers int, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shapingOK = ok
	s.shapingTiers = tiers
	s.shapingErr = reason
}

func (s *nodeState) Shaping() (ok bool, tiers int, reason string) {
	s.mu.RLock()
	ok, tiers, reason = s.shapingOK, s.shapingTiers, s.shapingErr
	config := s.config
	s.mu.RUnlock()
	if s.deviceCapability != nil {
		cfg, err := parseDeviceConfig(config)
		if err != nil {
			return false, 0, "invalid device egress metadata"
		}
		if len(cfg.devices) > 0 {
			ready, count := s.deviceCapability()
			if !ready || count != len(cfg.devices) {
				return false, len(cfg.devices), "local device egress unavailable or generation not applied"
			}
		}
	}
	return ok, tiers, reason
}

func (s *nodeState) ShapingStatus() (hash string, ok bool, count int, reason, mode string) {
	// Pair the canonical hash with the applied generation; never acknowledge
	// the new limiter topology while an old Xray process is still running.
	s.processMu.Lock()
	defer s.processMu.Unlock()
	hash = s.ConfigHash()
	ok, count, reason = s.Shaping()
	mode = s.ShapingMode()
	return
}

func (s *nodeState) ShapingMode() string {
	if s.deviceCapability == nil {
		return ""
	}
	ok, _, _ := s.Shaping()
	ready, count := s.deviceCapability()
	if ok && ready && count > 0 {
		return "device_global_v1"
	}
	return ""
}

// userKey keys on email because that is how Xray's RemoveUserOperation
// addresses a user.
type userKey struct {
	InboundTag string
	Email      string
}

func newNodeState(config []byte) *nodeState {
	s := &nodeState{users: vlessUsersOfConfig(config), shapingOK: true}
	s.setConfig(config, "")
	return s
}

// vlessUsersOfConfig reads the VLESS clients out of the config Xray is about to
// run. Seeded empty instead, the first incremental sync re-adds users Xray
// already has, and Xray rejects a duplicate email - collapsing every sync into
// the restart the handler API exists to avoid.
func vlessUsersOfConfig(config []byte) map[userKey]xray.VLESSUser {
	users := map[userKey]xray.VLESSUser{}

	var parsed struct {
		Inbounds []struct {
			Protocol string `json:"protocol"`
			Tag      string `json:"tag"`
			Settings struct {
				Clients []struct {
					ID    string `json:"id"`
					Email string `json:"email"`
					Flow  string `json:"flow"`
					Level uint32 `json:"level"`
				} `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		log.Printf("warning: could not read initial user set from config: %v", err)
		return users
	}

	for _, ib := range parsed.Inbounds {
		if ib.Protocol != "vless" {
			continue
		}
		for _, c := range ib.Settings.Clients {
			users[userKey{InboundTag: ib.Tag, Email: c.Email}] = xray.VLESSUser{
				UUID:  c.ID,
				Email: c.Email,
				Flow:  c.Flow,
				Level: c.Level,
			}
		}
	}
	return users
}

func (s *nodeState) setConfig(config []byte, structureHash string) {
	sum := sha256.Sum256(config)
	s.mu.Lock()
	s.config = config
	s.configHash = hex.EncodeToString(sum[:])
	s.structureHash = structureHash
	s.mu.Unlock()
}

func (s *nodeState) StructureHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.structureHash
}

// setStructureHash records the digest for the config already running.
func (s *nodeState) setStructureHash(structureHash string) {
	s.mu.Lock()
	s.structureHash = structureHash
	s.mu.Unlock()
}

func (s *nodeState) XrayGen() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.xrayGen
}

// noteXrayRestarted bumps the generation and reseeds users from the config the
// new process loaded, invalidating any sync already in flight.
func (s *nodeState) noteXrayRestarted(users map[userKey]xray.VLESSUser) {
	s.mu.Lock()
	s.xrayGen++
	s.usersHash = ""
	s.users = users
	s.mu.Unlock()
}

// noteXrayRestartedWith is noteXrayRestarted for a deliberate restart onto a
// known config, where the server's user digest does describe what Xray loaded.
func (s *nodeState) noteXrayRestartedWith(usersHash string, users map[userKey]xray.VLESSUser) {
	s.mu.Lock()
	s.xrayGen++
	s.usersHash = usersHash
	s.users = users
	s.mu.Unlock()
}

// setUsersIfGen writes only if Xray has not restarted since gen was read,
// reporting whether the write landed.
func (s *nodeState) setUsersIfGen(gen uint64, usersHash string, users map[userKey]xray.VLESSUser) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.xrayGen != gen {
		return false
	}
	s.usersHash = usersHash
	s.users = users
	return true
}

func (s *nodeState) ConfigHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configHash
}

func (s *nodeState) Config() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *nodeState) UsersHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.usersHash
}

// UsersSnapshot copies the set so callers can diff without holding the lock
// across gRPC calls.
// ProvisionedEmails lists the client emails Xray currently serves, which is the
// only way to address its per-email online map.
func (s *nodeState) ProvisionedEmails() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{}, len(s.users))
	out := make([]string, 0, len(s.users))
	for k := range s.users {
		if _, dup := seen[k.Email]; dup {
			continue
		}
		seen[k.Email] = struct{}{}
		out = append(out, k.Email)
	}
	return out
}

func (s *nodeState) UsersSnapshot() map[userKey]xray.VLESSUser {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[userKey]xray.VLESSUser, len(s.users))
	for k, v := range s.users {
		out[k] = v
	}
	return out
}

// runRelay serves a node that only forwards. It installs the relay rules and
// reports health, and starts none of the tunnel services - a relay has no
// inbounds, no clients and no certificates, so there is nothing for Xray,
// Hysteria2 or WireGuard to serve.
func runRelay(ctx context.Context, cfg *config.AgentConfig, apiClient *client.APIClient, policyManager *relay.Manager) error {
	log.Println("node role is relay: forwarding only, tunnel services not started")

	go heartbeatLoop(ctx, apiClient, nil, nil, nil)
	go policyPollLoop(ctx, nodeprov.RoleRelay, apiClient, policyManager)
	go updateCheckLoop(ctx, updater.NewUpdater(version, "", cfg.ServerURL, cfg.NodeID, cfg.APIKey))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down")
	return nil
}

// revokedUUIDPollLoop fetches the complete central snapshot every 500ms.
// The independent watchdog closes idle sessions when a fetch stalls beyond the
// maximum snapshot age, rather than waiting for the request timeout.
func revokedUUIDPollLoop(ctx context.Context, fetch func(context.Context) (client.RevocationSnapshot, error), ack func(context.Context, client.Revocation) error, server *deviceegress.Server) {
	const maxSnapshotAge = 5 * time.Second
	var mu sync.Mutex
	var lastSuccess time.Time
	available := false
	server.RevocationUnavailable()
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				if available && time.Since(lastSuccess) >= maxSnapshotAge {
					available = false
					server.RevocationUnavailable()
				}
				mu.Unlock()
			}
		}
	}()
	defer func() { <-watchdogDone }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		snapshot, err := fetch(ctx)
		mu.Lock()
		if ctx.Err() != nil {
			mu.Unlock()
			return
		}
		if err != nil {
			log.Printf("revoked UUID poll: %v", err)
			available = false
			server.RevocationUnavailable()
		} else {
			server.ReconcileRevokedUUIDs(snapshot.RevokedUUIDs)
			lastSuccess = time.Now()
			available = true
		}
		mu.Unlock()
		if err == nil {
			// Acknowledge only after local sessions have been closed.
			for _, revocation := range snapshot.Revocations {
				if ctx.Err() != nil {
					return
				}
				if err := ack(ctx, revocation); err != nil {
					log.Printf("revocation ack: %v", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// heartbeatInterval also sets how fresh the panel's resource figures can be:
// nothing shows a change sooner than the next beat. Kept well under the
// server's offline threshold (scheduler/node_monitor.go) so a single dropped
// beat cannot flip a healthy node to offline.
const heartbeatInterval = 10 * time.Second

// heartbeatLoop reports this node's health. runner and state are nil on a relay,
// which runs no Xray and shapes nothing: there is no process to report on, so the
// status it sends says so rather than inventing a reading.
func heartbeatLoop(ctx context.Context, apiClient *client.APIClient, runner *xray.XrayRunner, xrayVersion *versionHolder, state *nodeState) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m := stats.CollectSysMetrics()
			status := client.NodeStatus{ShapingOK: true}
			if state != nil {
				configHash, shapingOK, shapingCount, shapingErr, shapingMode := state.ShapingStatus()
				status.ConfigHash = configHash
				status.ShapingOK = shapingOK
				status.ShapingTiers = shapingCount
				status.ShapingError = shapingErr
				status.ShapingMode = shapingMode
			}
			if runner != nil {
				status.XrayVersion = xrayVersion.Get()
				status.XrayRunning = runner.IsRunning()
			}
			if err := apiClient.SendHeartbeat(ctx, m.CPU, m.Memory, m.Disk, m.LoadAvg, m.NetworkIn, m.NetworkOut, status); err != nil {
				log.Printf("heartbeat: %v", err)
			}
		}
	}
}

// superviseLoop restarts Xray if it dies on its own - previously a crashed or
// OOM-killed Xray stayed dead while the agent kept reporting healthy. Attempts
// back off so a config Xray refuses cannot become a hot spawn loop.
func superviseLoop(ctx context.Context, runner *xray.XrayRunner, state *nodeState) {
	const (
		checkInterval = 10 * time.Second
		maxBackoff    = 5 * time.Minute
	)

	backoff := time.Duration(0)
	var nextAttempt time.Time

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state.processMu.Lock()
			if state.restartBlocked || ctx.Err() != nil {
				state.processMu.Unlock()
				continue
			}
			if runner.IsRunning() {
				backoff = 0
				nextAttempt = time.Time{}
				state.processMu.Unlock()
				continue
			}
			if time.Now().Before(nextAttempt) {
				state.processMu.Unlock()
				continue
			}

			log.Println("supervisor: xray is not running, restarting")
			if err := runner.Start(); err != nil {
				if backoff == 0 {
					backoff = checkInterval
				} else if backoff < maxBackoff {
					backoff *= 2
				}
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				nextAttempt = time.Now().Add(backoff)
				log.Printf("supervisor: restart failed, retrying in %s: %v", backoff, err)
				state.processMu.Unlock()
				continue
			}

			backoff = 0
			nextAttempt = time.Time{}
			applyShaping(state.Config(), state)
			// A respawned Xray only knows its config file's users, losing any
			// the agent added over the handler API; the empty hash re-arms
			// reconciliation on the next poll.
			state.noteXrayRestarted(vlessUsersOfConfig(state.Config()))
			log.Println("supervisor: xray restarted")
			state.processMu.Unlock()
		}
	}
}

// updateCheckLoop periodically checks whether a newer node-agent binary is
// targeted for this node and, if so, downloads and applies it, then exits so
// the process supervisor (systemd Restart=always, see scripts/install.sh)
// restarts with the new binary.
func updateCheckLoop(ctx context.Context, upd *updater.Updater) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			target, available, err := upd.CheckUpdate(ctx)
			if err != nil {
				log.Printf("node-agent update check: %v", err)
				continue
			}
			if !available {
				continue
			}
			log.Printf("node-agent update available: %s, downloading...", target)
			if err := upd.PerformUpdate(ctx, target); err != nil {
				log.Printf("node-agent self-update failed: %v", err)
				continue
			}
			log.Println("node-agent self-update applied, restarting...")
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
			return
		}
	}
}

// xrayUpdateLoop applies an admin-requested Xray-core version change. Staging
// and validation happen while the old Xray still serves - stopping first, as
// this used to, made every slow download an outage of that length.
func xrayUpdateLoop(ctx context.Context, apiClient *client.APIClient, runner *xray.XrayRunner, xrayVersion *versionHolder, state *nodeState) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			target, available, err := apiClient.CheckXrayUpdate(ctx, xrayVersion.Get())
			if err != nil {
				log.Printf("xray update check: %v", err)
				continue
			}
			if !available {
				continue
			}

			log.Printf("xray update available: %s, downloading...", target)
			staged, err := runner.StageBinary(ctx, target)
			if err != nil {
				log.Printf("xray update: download/validate failed, keeping current version: %v", err)
				continue
			}

			state.processMu.Lock()
			if state.restartBlocked || ctx.Err() != nil {
				state.processMu.Unlock()
				runner.DiscardBinary(staged)
				continue
			}
			if err := runner.Stop(); err != nil {
				log.Printf("xray update: stop failed: %v", err)
				runner.DiscardBinary(staged)
				state.processMu.Unlock()
				continue
			}
			if err := runner.CommitBinary(staged); err != nil {
				log.Printf("xray update: install failed: %v", err)
				runner.DiscardBinary(staged)
				if startErr := runner.Start(); startErr != nil {
					log.Printf("xray update: restart after failed install also failed: %v", startErr)
				} else {
					state.noteXrayRestarted(vlessUsersOfConfig(state.Config()))
				}
				state.processMu.Unlock()
				continue
			}

			if err := runner.Start(); err != nil {
				log.Printf("xray update: new binary failed to start, rolling back: %v", err)
				if rbErr := runner.RestoreBinary(); rbErr != nil {
					log.Printf("xray update: binary rollback failed: %v", rbErr)
					state.processMu.Unlock()
					continue
				}
				if startErr := runner.Start(); startErr != nil {
					log.Printf("xray update: restart on previous binary failed: %v", startErr)
				} else {
					state.noteXrayRestarted(vlessUsersOfConfig(state.Config()))
				}
				state.processMu.Unlock()
				continue
			}

			applyShaping(state.Config(), state)
			state.noteXrayRestarted(vlessUsersOfConfig(state.Config()))
			if v, err := runner.Version(); err == nil {
				xrayVersion.Set(v)
				log.Printf("xray updated to %s", v)
			}
			state.processMu.Unlock()
		}
	}
}

// tlsPollLoop polls the server for a TLS domain the admin requested via the
// panel's "Issue Certificate" button (AdminNodeHandler.IssueCertificate,
// api-server/internal/handlers/admin_node.go) and, once one appears, obtains
// the certificate via ACME and reports the resulting file paths back so the
// server can wire them into this node's vmess_ws/trojan_tls inbounds (see
// buildVmessWS/buildTrojanTLS in services/xray_config.go). Previously that
// button only wrote a DB column nothing ever read, so vmess_ws/trojan_tls
// inbounds were silently dropped from every generated config no matter what
// the admin did.
//
// staticDomain is the --tls-domain flag: if the operator provided one at
// startup, cert issuance for that domain is already handled by
// cert.NewCertManager/StartAutoRenew in runCmd, so this loop stays out of
// the way rather than potentially requesting a second, different cert.
func tlsPollLoop(ctx context.Context, apiClient *client.APIClient, certDir, staticDomain string) {
	if staticDomain != "" {
		return
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	check := func() {
		td, err := apiClient.GetTLSDomain(ctx)
		if err != nil {
			log.Printf("tls domain poll: %v", err)
			return
		}
		if td.Domain == "" {
			return
		}

		cm := cert.NewCertManager(td.Domain, td.Email, certDir)
		certPath, keyPath, err := cm.ObtainOrRenew(ctx)
		if err != nil {
			log.Printf("tls cert obtain/renew for %s: %v", td.Domain, err)
			return
		}
		if err := apiClient.ReportTLSCert(ctx, certPath, keyPath); err != nil {
			log.Printf("tls cert report: %v", err)
		}
	}

	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// configPollLoop keeps the node's Xray in sync with the server. It polls the
// digest rather than the full config, applies users-only changes over the
// handler API so routine account edits stop dropping live connections, and rolls
// back if a structural change fails to start.
func configPollLoop(
	ctx context.Context,
	apiClient *client.APIClient,
	runner *xray.XrayRunner,
	statsClient *xray.StatsClient,
	state *nodeState,
) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			digest, err := apiClient.GetConfigDigest(ctx)
			if err != nil {
				log.Printf("config digest poll: %v", err)
				continue
			}

			applyConfigDigest(ctx, apiClient, runner, statsClient, state, digest)
		}
	}
}

// applyConfigDigest serializes an entire reconciliation with supervisor and
// binary updates. Validate local topology before any gRPC user is admitted.
func applyConfigDigest(ctx context.Context, apiClient *client.APIClient, runner *xray.XrayRunner, statsClient *xray.StatsClient, state *nodeState, digest client.ConfigDigest) {
	state.processMu.Lock()
	defer state.processMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if !state.restartBlocked && digest.Hash == state.ConfigHash() {
		if !digestUsersMatchConfig(digest, state.Config()) {
			log.Println("config poll: digest users do not match canonical config; refusing live admission")
			return
		}
		state.setStructureHash(digest.StructureHash)
		if digest.UsersHash != state.UsersHash() && !syncUsers(ctx, statsClient, state, digest) {
			log.Println("config poll: user reconciliation incomplete, retrying next tick")
		}
		return
	}
	newConfig, err := apiClient.GetConfig(ctx)
	if err != nil {
		log.Printf("config poll: %v", err)
		return
	}
	sum := sha256.Sum256(newConfig)
	if hex.EncodeToString(sum[:]) != digest.Hash {
		log.Println("config poll: configuration changed between digest and fetch, retrying next tick")
		return
	}
	if !digestUsersMatchConfig(digest, newConfig) {
		log.Println("config poll: digest users do not match fetched canonical config; retrying next tick")
		return
	}
	incremental := !state.restartBlocked && usersOnlyChange(state, digest) && sameLocalStructure(state.Config(), newConfig)
	state.setShaping(false, 0, "applying device egress generation")
	if err := runner.WriteConfig(newConfig); err != nil {
		log.Printf("write new config failed; stopping xray rather than serving obsolete policy: %v", err)
		state.restartBlocked = true
		state.setShaping(false, 0, "device egress config rejected")
		_ = runner.Stop()
		return
	}
	if incremental && syncUsers(ctx, statsClient, state, digest) {
		state.setConfig(newConfig, digest.StructureHash)
		applyShaping(newConfig, state)
		return
	}
	log.Println("config changed, restarting xray...")
	if err := runner.Restart(); err != nil {
		log.Printf("restart xray on new config: %v", err)
		rollbackConfig(runner, state)
		return
	}
	state.restartBlocked = false
	applyShaping(newConfig, state)
	state.setConfig(newConfig, digest.StructureHash)
	state.noteXrayRestartedWith(digest.UsersHash, vlessUsersOfConfig(newConfig))
}

func digestUsersMatchConfig(digest client.ConfigDigest, canonical []byte) bool {
	want := vlessUsersOfConfig(canonical)
	got := usersFromDigest(digest)
	if len(want) != len(got) {
		return false
	}
	for key, user := range want {
		if actual, exists := got[key]; !exists || actual != user {
			return false
		}
	}
	return true
}

// rollbackConfig only re-admits a previous generation if the restore guard
// proves it remains authorized under the latest policy. Call with processMu.
func rollbackConfig(runner *xray.XrayRunner, state *nodeState) {
	state.restartBlocked = true
	state.setShaping(false, 0, "xray config failed; safe rollback unavailable")
	_ = runner.Stop()
	if !runner.HasBackupConfig() {
		log.Println("rollback: no previous config available")
		return
	}

	prev, err := runner.RestoreConfig()
	if err != nil {
		log.Printf("rollback: restore previous config: %v", err)
		return
	}
	if err := runner.Restart(); err != nil {
		log.Printf("rollback: restart on previous config failed: %v", err)
		return
	}

	state.restartBlocked = false
	applyShaping(prev, state)
	// Clear structureHash: the agent no longer knows the server-side structure
	// digest for the config now running, and an empty value forces the next
	// change through the restart path rather than an unsafe incremental one.
	state.setConfig(prev, "")
	state.noteXrayRestarted(vlessUsersOfConfig(prev))
	log.Println("rollback: restarted xray on previous config")
}

// usersOnlyChange reports whether a digest differs from the running state only
// in its user set. StructureHash is authoritative here: it is computed from the
// config with client lists emptied, so an equal value means nothing but the
// users moved. An empty local StructureHash means the agent has not yet seen a
// digest (cold start), so it cannot claim the structure matches.
func usersOnlyChange(state *nodeState, digest client.ConfigDigest) bool {
	local := state.StructureHash()
	if local == "" || digest.StructureHash == "" {
		return false
	}
	if local != digest.StructureHash {
		return false
	}
	return digest.UsersHash != state.UsersHash()
}

// usersFromDigest converts a digest's user list into nodeState's keyed form.
func usersFromDigest(digest client.ConfigDigest) map[userKey]xray.VLESSUser {
	out := make(map[userKey]xray.VLESSUser, len(digest.Users))
	for _, u := range digest.Users {
		out[userKey{InboundTag: u.InboundTag, Email: u.Email}] = xray.VLESSUser{
			UUID:  u.UUID,
			Email: u.Email,
			Flow:  u.Flow,
			Level: u.Level,
		}
	}
	return out
}

// syncUsers applies the running/wanted user diff over Xray's handler API,
// reporting whether the whole set now matches. A false return tells the caller
// to fall back to a restart.
func syncUsers(ctx context.Context, statsClient *xray.StatsClient, state *nodeState, digest client.ConfigDigest) bool {
	if statsClient == nil {
		return false
	}

	want := usersFromDigest(digest)
	// Snapshot the generation alongside the user set: everything below mutates
	// a specific Xray process, and the supervisor may replace it mid-flight.
	gen := state.XrayGen()
	live := state.UsersSnapshot()
	applied := make(map[userKey]xray.VLESSUser, len(live))
	for k, v := range live {
		applied[k] = v
	}

	ok := true

	for key := range live {
		if _, keep := want[key]; keep {
			continue
		}
		if err := statsClient.RemoveVLESSUser(ctx, key.InboundTag, key.Email); err != nil {
			log.Printf("sync users: %v", err)
			ok = false
			continue
		}
		delete(applied, key)
	}

	for key, u := range want {
		if existing, exists := applied[key]; exists && existing == u {
			continue
		}
		// Xray rejects an add for an email already on the inbound, so replace
		// rather than add when the credentials changed under the same email.
		if _, exists := applied[key]; exists {
			if err := statsClient.RemoveVLESSUser(ctx, key.InboundTag, key.Email); err != nil {
				log.Printf("sync users: %v", err)
				ok = false
				continue
			}
			delete(applied, key)
		}
		if err := statsClient.AddVLESSUser(ctx, key.InboundTag, u); err != nil {
			log.Printf("sync users: %v", err)
			ok = false
			continue
		}
		applied[key] = u
	}

	if !ok {
		// Keep what landed so the next poll retries only the remainder; the
		// empty hash keeps reconciliation armed. Skipped if Xray restarted
		// underneath us - the supervisor's reseed is the accurate state then.
		if !state.setUsersIfGen(gen, "", applied) {
			log.Println("sync users: xray restarted mid-sync, discarding partial result")
		}
		return false
	}

	if !state.setUsersIfGen(gen, digest.UsersHash, applied) {
		log.Println("sync users: xray restarted mid-sync, discarding result")
		return false
	}
	log.Printf("synced users without restart: %d active", len(applied))
	return true
}

// applyShaping uses central per-device permits for managed outbounds, never a
// shared tc speed tier. Clear inherited tier shaping once on migration.
func applyShaping(config []byte, state *nodeState) {
	cfg, err := parseDeviceConfig(config)
	if err != nil {
		state.setShaping(false, 0, "invalid device egress metadata")
		return
	}
	if !state.legacyShapingCleared {
		if err := shaper.Apply("", nil); err != nil {
			log.Printf("warning: remove legacy tier shaping: %v", err)
		}
		state.legacyShapingCleared = true
	}
	state.setShaping(true, len(cfg.devices), "")
}

// relayRulePollInterval matches the inbound poll: relay rules change when an
// operator edits a chain or a pool, which is the same human timescale.
const relayRulePollInterval = 30 * time.Second

func inboundsPollLoop(ctx context.Context, apiClient *client.APIClient) {
	var hy2Manager *process.Hysteria2Manager
	var wgManager *process.WireGuardManager
	wgPeers := map[string]process.WireGuardPeer{}
	var hy2Users []string

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	applyInbounds(ctx, apiClient, &hy2Manager, &wgManager, &wgPeers, &hy2Users)

	for {
		select {
		case <-ctx.Done():
			if hy2Manager != nil {
				_ = hy2Manager.Stop()
			}
			if wgManager != nil {
				_ = wgManager.Stop()
			}
			return
		case <-ticker.C:
			applyInbounds(ctx, apiClient, &hy2Manager, &wgManager, &wgPeers, &hy2Users)
		}
	}
}

func applyInbounds(ctx context.Context, apiClient *client.APIClient, hy2 **process.Hysteria2Manager, wg **process.WireGuardManager, wgPeers *map[string]process.WireGuardPeer, hy2Users *[]string) {
	inbounds, err := apiClient.GetInbounds(ctx)
	if err != nil {
		log.Printf("inbounds poll: %v", err)
		return
	}

	wantHy2 := false
	wantWG := false
	var hy2Port int
	var wgPort int

	for _, ib := range inbounds {
		if !ib.Enabled {
			continue
		}
		switch ib.Protocol {
		case "hysteria2":
			wantHy2 = true
			hy2Port = ib.Port
		case "wireguard":
			wantWG = true
			wgPort = ib.Port
		}
	}

	if wantHy2 {
		if *hy2 == nil {
			*hy2 = process.NewHysteria2Manager("", "")
		}

		users, err := apiClient.GetHysteria2Users(ctx)
		if err != nil {
			log.Printf("hysteria2 users poll: %v", err)
			users = *hy2Users // keep serving the last known-good set
		}
		slices.Sort(users)

		usersChanged := !slices.Equal(users, *hy2Users)
		if !(*hy2).IsRunning() || usersChanged {
			cfg := buildHysteria2Config(hy2Port, inbounds, users)
			// UpdateConfig only restarts the process if it's already running
			// (see Hysteria2Manager.UpdateConfig); an explicit Start() covers
			// the cold-start case below.
			if err := (*hy2).UpdateConfig(cfg); err != nil {
				log.Printf("hysteria2 update config: %v", err)
			} else {
				*hy2Users = users
				if !(*hy2).IsRunning() {
					if err := (*hy2).Start(); err != nil {
						log.Printf("hysteria2 start: %v", err)
					} else {
						log.Printf("hysteria2 started on port %d with %d user(s)", hy2Port, len(users))
					}
				} else {
					log.Printf("hysteria2 users changed, restarted with %d user(s)", len(users))
				}
			}
		}
	} else if *hy2 != nil && (*hy2).IsRunning() {
		if err := (*hy2).Stop(); err != nil {
			log.Printf("hysteria2 stop: %v", err)
		} else {
			log.Println("hysteria2 stopped (no enabled inbound)")
			*hy2Users = nil
		}
	}

	if wantWG {
		if *wg == nil {
			*wg = process.NewWireGuardManager("wg0", wgPort, "")
		}
		if !(*wg).IsRunning() {
			cfg := buildWireGuardConfig(wgPort, inbounds)
			if err := (*wg).GenerateConfig(cfg); err != nil {
				log.Printf("wireguard generate config: %v", err)
			} else if err := (*wg).Start(); err != nil {
				log.Printf("wireguard start: %v", err)
			} else {
				log.Printf("wireguard started on port %d", wgPort)
				// A freshly-started interface always has zero peers (see
				// buildWireGuardConfig); forget whatever we'd previously
				// applied so the sync below re-adds everyone.
				*wgPeers = map[string]process.WireGuardPeer{}
			}
		}
		if (*wg).IsRunning() {
			syncWireGuardPeers(ctx, apiClient, *wg, wgPeers)
		}
	} else if *wg != nil && (*wg).IsRunning() {
		if err := (*wg).Stop(); err != nil {
			log.Printf("wireguard stop: %v", err)
		} else {
			log.Println("wireguard stopped (no enabled inbound)")
			*wgPeers = map[string]process.WireGuardPeer{}
		}
	}
}

// syncWireGuardPeers fetches the currently-eligible peer set from the server
// and diffs it against wgPeers (the set last applied to the running
// interface), calling AddPeer/RemovePeer (node-agent/internal/process/
// wireguard.go) only for what changed. This runs every inboundsPollLoop tick
// (30s) so suspensions/expiries/new devices reach the interface without a
// full wg-quick restart.
func syncWireGuardPeers(ctx context.Context, apiClient *client.APIClient, wg *process.WireGuardManager, applied *map[string]process.WireGuardPeer) {
	peers, err := apiClient.GetWireGuardPeers(ctx)
	if err != nil {
		log.Printf("wireguard peers poll: %v", err)
		return
	}

	want := make(map[string]process.WireGuardPeer, len(peers))
	for _, p := range peers {
		if p.PublicKey == "" || p.AllowedIPs == "" {
			continue
		}
		want[p.PublicKey] = process.WireGuardPeer{PublicKey: p.PublicKey, AllowedIPs: p.AllowedIPs}
	}

	for pubkey := range *applied {
		if _, ok := want[pubkey]; ok {
			continue
		}
		if err := wg.RemovePeer(pubkey); err != nil {
			log.Printf("wireguard remove peer: %v", err)
			continue
		}
		delete(*applied, pubkey)
	}

	added := 0
	for pubkey, p := range want {
		if existing, ok := (*applied)[pubkey]; ok && existing.AllowedIPs == p.AllowedIPs {
			continue
		}
		if err := wg.AddPeer(p); err != nil {
			log.Printf("wireguard add peer: %v", err)
			continue
		}
		(*applied)[pubkey] = p
		added++
	}

	if added > 0 || len(*applied) != len(want) {
		log.Printf("wireguard peers synced: %d active", len(*applied))
	}
}

// buildHysteria2Config builds a per-user Hysteria2 server config: each
// eligible device's xray_uuid is admitted as both its own username and
// password (auth type "userpass"), so suspending or deleting a device
// revokes its Hysteria2 access on the next sync, the same as every other
// protocol. See subscription/singbox.go:singboxHysteria2, which already
// expects `password: userUUID` on the client side. Falls back to a single
// node-wide password from inbound settings (legacy behavior) only if no
// devices are currently eligible.
func buildHysteria2Config(port int, inbounds []client.InboundConfig, users []string) process.Hysteria2Config {
	cfg := process.Hysteria2Config{
		Listen: fmt.Sprintf(":%d", port),
		TLS: process.Hysteria2TLS{
			Cert: "/etc/node-agent/certs/cert.pem",
			Key:  "/etc/node-agent/certs/key.pem",
		},
	}

	if len(users) > 0 {
		userpass := make(map[string]string, len(users))
		for _, uuid := range users {
			userpass[uuid] = uuid
		}
		cfg.Auth = &process.Hysteria2Auth{Type: "userpass", UserPass: userpass}
		return cfg
	}

	for _, ib := range inbounds {
		if ib.Protocol != "hysteria2" || !ib.Enabled {
			continue
		}
		var s struct {
			Password string `json:"password"`
		}
		if err := json.Unmarshal(ib.Settings, &s); err == nil && s.Password != "" {
			cfg.Auth = &process.Hysteria2Auth{Type: "password", Password: s.Password}
		}
	}

	return cfg
}

func buildWireGuardConfig(port int, inbounds []client.InboundConfig) process.WireGuardConfig {
	// 10.66.0.0/16 matches the device tunnel-IP pool allocated by the API
	// server (see handlers/user_device.go:wgAddressPoolBase); a /16 on the
	// interface routes the whole pool through wg0 regardless of which node a
	// given device's peer entry ends up on. Overridden by settings.address if
	// the admin (or ensureWireGuardServerSettings) set one explicitly.
	cfg := process.WireGuardConfig{
		ListenPort: port,
		Address:    "10.66.0.1/16",
	}

	for _, ib := range inbounds {
		if ib.Protocol != "wireguard" || !ib.Enabled {
			continue
		}
		var s struct {
			PrivateKey string `json:"private_key"`
			Address    string `json:"address"`
		}
		if err := json.Unmarshal(ib.Settings, &s); err == nil {
			if s.PrivateKey != "" {
				cfg.PrivateKey = s.PrivateKey
			}
			if s.Address != "" {
				cfg.Address = s.Address
			}
		}
	}

	return cfg
}

func detectIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
