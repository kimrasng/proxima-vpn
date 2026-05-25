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
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/cert"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/process"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/stats"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

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

			resp, err := apiClient.Register(ctx, serverURL, token, ip, port, "xray-latest", name, country, region)
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
			defer runner.Stop()

			time.Sleep(2 * time.Second)

			statsClient, err := xray.NewStatsClient(runner.GRPCAddr())
			if err != nil {
				log.Printf("warning: could not connect to xray stats: %v", err)
			}

			if statsClient != nil {
				collector := stats.NewCollector(statsClient, apiClient, stats.DefaultInterval)
				collector.Start(ctx)
				defer collector.Stop()
				defer statsClient.Close()
			}

			if tlsDomain != "" {
				cm := cert.NewCertManager(tlsDomain, tlsEmail, certDir)
				if _, _, err := cm.ObtainOrRenew(ctx); err != nil {
					log.Printf("warning: initial cert obtain failed: %v", err)
				}
				cm.StartAutoRenew(ctx)
				defer cm.Stop()
			}

		go heartbeatLoop(ctx, apiClient)
		go configPollLoop(ctx, apiClient, runner, xrayConfig)
		go inboundsPollLoop(ctx, apiClient)

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

func heartbeatLoop(ctx context.Context, apiClient *client.APIClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m := stats.CollectSysMetrics()
			if err := apiClient.SendHeartbeat(ctx, m.CPU, m.Memory, m.Disk, m.LoadAvg); err != nil {
				log.Printf("heartbeat: %v", err)
			}
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
			lastHash = newHash
		}
	}
}

func inboundsPollLoop(ctx context.Context, apiClient *client.APIClient) {
	var hy2Manager *process.Hysteria2Manager
	var wgManager *process.WireGuardManager

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	applyInbounds(ctx, apiClient, &hy2Manager, &wgManager)

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
			applyInbounds(ctx, apiClient, &hy2Manager, &wgManager)
		}
	}
}

func applyInbounds(ctx context.Context, apiClient *client.APIClient, hy2 **process.Hysteria2Manager, wg **process.WireGuardManager) {
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
		if !(*hy2).IsRunning() {
			cfg := buildHysteria2Config(hy2Port, inbounds)
			if err := (*hy2).UpdateConfig(cfg); err != nil {
				log.Printf("hysteria2 update config: %v", err)
			} else {
				log.Printf("hysteria2 started on port %d", hy2Port)
			}
		}
	} else if *hy2 != nil && (*hy2).IsRunning() {
		if err := (*hy2).Stop(); err != nil {
			log.Printf("hysteria2 stop: %v", err)
		} else {
			log.Println("hysteria2 stopped (no enabled inbound)")
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
			}
		}
	} else if *wg != nil && (*wg).IsRunning() {
		if err := (*wg).Stop(); err != nil {
			log.Printf("wireguard stop: %v", err)
		} else {
			log.Println("wireguard stopped (no enabled inbound)")
		}
	}
}

func buildHysteria2Config(port int, inbounds []client.InboundConfig) process.Hysteria2Config {
	cfg := process.Hysteria2Config{
		Listen: fmt.Sprintf(":%d", port),
		TLS: process.Hysteria2TLS{
			Cert: "/etc/node-agent/certs/cert.pem",
			Key:  "/etc/node-agent/certs/key.pem",
		},
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
	cfg := process.WireGuardConfig{
		ListenPort: port,
		Address:    "10.0.0.1/24",
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
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
