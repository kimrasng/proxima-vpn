package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
)

// APIClient communicates with the main server API.
type APIClient struct {
	serverURL  string
	apiKey     string
	nodeID     string
	httpClient *http.Client
}

// NewAPIClient creates a new API client from the agent config.
func NewAPIClient(cfg *config.AgentConfig) *APIClient {
	return &APIClient{
		serverURL: cfg.ServerURL,
		apiKey:    cfg.APIKey,
		nodeID:    cfg.NodeID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// RegisterRequest is the payload for node registration.
type RegisterRequest struct {
	Token       string `json:"reg_token"`
	IP          string `json:"ip"`
	Port        int    `json:"port"`
	XrayVersion string `json:"xray_version"`
	Name        string `json:"name"`
	Country     string `json:"country"`
	Region      string `json:"region"`
}

// RegisterResponse is the response from node registration.
type RegisterResponse struct {
	NodeID string `json:"node_id"`
	APIKey string `json:"api_key"`
}

// TrafficStat represents per-user traffic data.
type TrafficStat struct {
	UUID     string `json:"uuid"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

// StatsPayload is the payload for sending stats.
type StatsPayload struct {
	Traffic     []TrafficStat `json:"traffic"`
	OnlineUUIDs []string      `json:"online_uuids"`
}

// HeartbeatPayload is the payload for heartbeat. ConfigHash and XrayRunning let
// the panel tell "agent alive" apart from "Xray actually serving the config we
// published".
type HeartbeatPayload struct {
	CPU         float64 `json:"cpu_usage"`
	Memory      float64 `json:"memory_usage"`
	Disk        float64 `json:"disk_usage"`
	LoadAvg     float64 `json:"load_avg"`
	NetworkIn   float64 `json:"network_in"`
	NetworkOut  float64 `json:"network_out"`
	XrayVersion string  `json:"xray_version,omitempty"`
	ConfigHash  string  `json:"config_hash,omitempty"`
	XrayRunning bool    `json:"xray_running"`
}

type NodeStatus struct {
	XrayVersion string
	ConfigHash  string
	XrayRunning bool
}

// Register registers this node with the main server.
func (c *APIClient) Register(ctx context.Context, serverURL, token, ip string, port int, xrayVersion, name, country, region string) (*RegisterResponse, error) {
	payload := RegisterRequest{
		Token:       token,
		IP:          ip,
		Port:        port,
		XrayVersion: xrayVersion,
		Name:        name,
		Country:     country,
		Region:      region,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal register request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/nodes/register", serverURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create register request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("register request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("register failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode register response: %w", err)
	}

	return &result, nil
}

// Unregister deletes this node's own record from the panel, authenticated
// with its own API key. Used by `node-agent unregister` (cmd/main.go),
// typically run from scripts/uninstall.sh, so uninstalling a node also
// removes it from the admin panel.
func (c *APIClient) Unregister(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/v1/nodes/%s", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create unregister request: %w", err)
	}
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("unregister request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unregister failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// GetConfig fetches the Xray configuration from the server.
func (c *APIClient) GetConfig(ctx context.Context) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/nodes/%s/config", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create config request: %w", err)
	}
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get config request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get config failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return io.ReadAll(resp.Body)
}

type ConfigDigest struct {
	Hash          string             `json:"hash"`
	StructureHash string             `json:"structure_hash"`
	UsersHash     string             `json:"users_hash"`
	Users         []ConfigDigestUser `json:"users"`
}

type ConfigDigestUser struct {
	InboundTag string `json:"inbound_tag"`
	UUID       string `json:"uuid"`
	Email      string `json:"email"`
	Flow       string `json:"flow"`
	Level      uint32 `json:"level"`
}

// GetConfigDigest fetches the config fingerprint instead of the whole config,
// so an unchanged node costs a few hundred bytes per poll. StructureHash tells
// a users-only change apart from one needing a restart.
func (c *APIClient) GetConfigDigest(ctx context.Context) (ConfigDigest, error) {
	var digest ConfigDigest

	url := fmt.Sprintf("%s/api/v1/nodes/%s/config/digest", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return digest, fmt.Errorf("create config digest request: %w", err)
	}
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return digest, fmt.Errorf("get config digest request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return digest, fmt.Errorf("get config digest failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	if err := json.NewDecoder(resp.Body).Decode(&digest); err != nil {
		return digest, fmt.Errorf("decode config digest: %w", err)
	}
	return digest, nil
}

// SendHeartbeat sends system metrics to the server.
func (c *APIClient) SendHeartbeat(ctx context.Context, cpu, memory, disk, loadAvg, networkIn, networkOut float64, status NodeStatus) error {
	payload := HeartbeatPayload{
		CPU:         cpu,
		Memory:      memory,
		Disk:        disk,
		LoadAvg:     loadAvg,
		NetworkIn:   networkIn,
		NetworkOut:  networkOut,
		XrayVersion: status.XrayVersion,
		ConfigHash:  status.ConfigHash,
		XrayRunning: status.XrayRunning,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal heartbeat: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/nodes/%s/heartbeat", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create heartbeat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("heartbeat request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("heartbeat failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

type InboundConfig struct {
	ID       string          `json:"id"`
	Protocol string          `json:"protocol"`
	Port     int             `json:"port"`
	Tag      string          `json:"tag"`
	Settings json.RawMessage `json:"settings"`
	Enabled  bool            `json:"enabled"`
}

func (c *APIClient) GetInbounds(ctx context.Context) ([]InboundConfig, error) {
	url := fmt.Sprintf("%s/api/v1/nodes/%s/inbounds", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create inbounds request: %w", err)
	}
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get inbounds request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get inbounds failed (status %d): %s", resp.StatusCode, string(body))
	}

	var inbounds []InboundConfig
	if err := json.NewDecoder(resp.Body).Decode(&inbounds); err != nil {
		return nil, fmt.Errorf("decode inbounds: %w", err)
	}
	return inbounds, nil
}

// XrayUpdateInfo is the response from the server's xray update-check endpoint.
type XrayUpdateInfo struct {
	TargetVersion string `json:"target_version"`
}

// CheckXrayUpdate asks the server whether a newer Xray-core version is
// targeted for this node. Returns ("", false, nil) when up to date.
func (c *APIClient) CheckXrayUpdate(ctx context.Context, currentVersion string) (string, bool, error) {
	url := fmt.Sprintf("%s/api/v1/nodes/%s/xray-update", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, fmt.Errorf("create xray update check request: %w", err)
	}
	req.Header.Set("X-Node-Key", c.apiKey)
	req.Header.Set("X-Xray-Version", currentVersion)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("xray update check request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", false, fmt.Errorf("xray update check failed (status %d): %s", resp.StatusCode, string(body))
	}

	var info XrayUpdateInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", false, fmt.Errorf("decode xray update response: %w", err)
	}
	if info.TargetVersion == "" || info.TargetVersion == currentVersion {
		return "", false, nil
	}
	return info.TargetVersion, true, nil
}

// WireGuardPeer is a peer that should currently be admitted on this node's
// WireGuard interface.
type WireGuardPeer struct {
	PublicKey  string `json:"public_key"`
	AllowedIPs string `json:"allowed_ips"`
}

// GetWireGuardPeers fetches the current set of eligible WireGuard peers from
// the server.
func (c *APIClient) GetWireGuardPeers(ctx context.Context) ([]WireGuardPeer, error) {
	url := fmt.Sprintf("%s/api/v1/nodes/%s/wireguard/peers", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create wireguard peers request: %w", err)
	}
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get wireguard peers request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get wireguard peers failed (status %d): %s", resp.StatusCode, string(body))
	}

	var peers []WireGuardPeer
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return nil, fmt.Errorf("decode wireguard peers: %w", err)
	}
	return peers, nil
}

// GetHysteria2Users fetches the current set of xray_uuids eligible to
// authenticate against this node's Hysteria2 server.
func (c *APIClient) GetHysteria2Users(ctx context.Context) ([]string, error) {
	url := fmt.Sprintf("%s/api/v1/nodes/%s/hysteria2/users", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create hysteria2 users request: %w", err)
	}
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get hysteria2 users request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get hysteria2 users failed (status %d): %s", resp.StatusCode, string(body))
	}

	var uuids []string
	if err := json.NewDecoder(resp.Body).Decode(&uuids); err != nil {
		return nil, fmt.Errorf("decode hysteria2 users: %w", err)
	}
	return uuids, nil
}

func (c *APIClient) SendStats(ctx context.Context, stats []TrafficStat, onlineUUIDs []string) error {
	payload := StatsPayload{
		Traffic:     stats,
		OnlineUUIDs: onlineUUIDs,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/nodes/%s/stats", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create stats request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stats request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stats failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// TLSDomain is the domain/email an admin requested for this node via the
// panel's "Issue Certificate" action.
type TLSDomain struct {
	Domain string `json:"domain"`
	Email  string `json:"email"`
}

// GetTLSDomain fetches the TLS domain/email currently requested for this
// node, if any.
func (c *APIClient) GetTLSDomain(ctx context.Context) (TLSDomain, error) {
	url := fmt.Sprintf("%s/api/v1/nodes/%s/tls-domain", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return TLSDomain{}, fmt.Errorf("create tls domain request: %w", err)
	}
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TLSDomain{}, fmt.Errorf("get tls domain request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return TLSDomain{}, fmt.Errorf("get tls domain failed (status %d): %s", resp.StatusCode, string(body))
	}

	var td TLSDomain
	if err := json.NewDecoder(resp.Body).Decode(&td); err != nil {
		return TLSDomain{}, fmt.Errorf("decode tls domain: %w", err)
	}
	return td, nil
}

// ReportTLSCert reports the local file paths of a certificate this node-agent
// just obtained via ACME, so the server can wire them into this node's
// vmess_ws/trojan_tls inbounds.
func (c *APIClient) ReportTLSCert(ctx context.Context, certFile, keyFile string) error {
	body, err := json.Marshal(struct {
		CertFile string `json:"cert_file"`
		KeyFile  string `json:"key_file"`
	}{CertFile: certFile, KeyFile: keyFile})
	if err != nil {
		return fmt.Errorf("marshal tls cert report: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/nodes/%s/tls-cert", c.serverURL, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create tls cert report request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tls cert report request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tls cert report failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}
