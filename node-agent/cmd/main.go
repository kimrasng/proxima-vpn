package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"github.com/proximavpn/proxima-vpn/node-agent/internal/process"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/shaper"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/stats"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/updater"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

// version is set at build time via -ldflags "-X main.version=...` (see Makefile).
var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:   "node-agent",
		Short: "Proxima VPN Node Agent",
	}

	rootCmd.AddCommand(registerCmd())
	rootCmd.AddCommand(runCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
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
			runner := xray.NewXrayRunner("", "")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			xrayConfig, err := apiClient.GetConfig(ctx)
			if err != nil {
				return fmt.Errorf("fetch initial config: %w", err)
			}
			if err := runner.WriteConfig(xrayConfig); err != nil {
				return fmt.Errorf("write xray config: %w", err)
			}
			if err := runner.Start(); err != nil {
				return fmt.Errorf("start xray: %w", err)
			}
			defer func() { _ = runner.Stop() }()
			applyShaping(xrayConfig)

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
				collector := stats.NewCollector(statsClient, apiClient, stats.DefaultInterval)
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

		go heartbeatLoop(ctx, apiClient, xrayVersion)
		go configPollLoop(ctx, apiClient, runner, xrayConfig)
		go inboundsPollLoop(ctx, apiClient)
		go updateCheckLoop(ctx, updater.NewUpdater(version, "", cfg.ServerURL, cfg.NodeID, cfg.APIKey))
		go xrayUpdateLoop(ctx, apiClient, runner, xrayVersion)
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
// version, shared between heartbeatLoop (reads it every 30s) and
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

func heartbeatLoop(ctx context.Context, apiClient *client.APIClient, xrayVersion *versionHolder) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m := stats.CollectSysMetrics()
			if err := apiClient.SendHeartbeat(ctx, m.CPU, m.Memory, m.Disk, m.LoadAvg, m.NetworkIn, m.NetworkOut, xrayVersion.Get()); err != nil {
				log.Printf("heartbeat: %v", err)
			}
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

// xrayUpdateLoop periodically checks whether the admin requested a different
// Xray-core version for this node (nodes.xray_target_version) and, if so,
// downloads it from GitHub, swaps the binary, and restarts Xray in place.
func xrayUpdateLoop(ctx context.Context, apiClient *client.APIClient, runner *xray.XrayRunner, xrayVersion *versionHolder) {
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
			if err := runner.Stop(); err != nil {
				log.Printf("xray update: stop failed: %v", err)
				continue
			}
			if err := runner.UpdateBinary(ctx, target); err != nil {
				log.Printf("xray update: download/replace failed: %v", err)
				if startErr := runner.Start(); startErr != nil {
					log.Printf("xray update: restart after failed update also failed: %v", startErr)
				}
				continue
			}
			if err := runner.Start(); err != nil {
				log.Printf("xray update: restart failed: %v", err)
				continue
			}
			if v, err := runner.Version(); err == nil {
				xrayVersion.Set(v)
				log.Printf("xray updated to %s", v)
			}
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

func configPollLoop(ctx context.Context, apiClient *client.APIClient, runner *xray.XrayRunner, lastConfig []byte) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	lastHash := sha256.Sum256(lastConfig)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newConfig, err := apiClient.GetConfig(ctx)
			if err != nil {
				log.Printf("config poll: %v", err)
				continue
			}

			newHash := sha256.Sum256(newConfig)
			if bytes.Equal(lastHash[:], newHash[:]) {
				continue
			}

			log.Println("config changed, restarting xray...")
			if err := runner.WriteConfig(newConfig); err != nil {
				log.Printf("write new config: %v", err)
				continue
			}
			if err := runner.Restart(); err != nil {
				log.Printf("restart xray: %v", err)
				continue
			}
			applyShaping(newConfig)
			lastHash = newHash
		}
	}
}

// applyShaping installs tc bandwidth limits for the speed-limited inbounds
// present in the given Xray config. Best-effort: failures are logged only.
func applyShaping(config []byte) {
	tiers := shaper.TiersFromConfig(config)
	if err := shaper.Apply("", tiers); err != nil {
		log.Printf("traffic shaping: %v", err)
		return
	}
	if len(tiers) > 0 {
		log.Printf("applied speed limits to %d tier(s)", len(tiers))
	}
}

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
