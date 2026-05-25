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

// HeartbeatPayload is the payload for heartbeat.
type HeartbeatPayload struct {
	CPU        float64 `json:"cpu_usage"`
	Memory     float64 `json:"memory_usage"`
	Disk       float64 `json:"disk_usage"`
	LoadAvg    float64 `json:"load_avg"`
	NetworkIn  float64 `json:"network_in"`
	NetworkOut float64 `json:"network_out"`
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get config failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return io.ReadAll(resp.Body)
}

// SendHeartbeat sends system metrics to the server.
func (c *APIClient) SendHeartbeat(ctx context.Context, cpu, memory, disk, loadAvg, networkIn, networkOut float64) error {
	payload := HeartbeatPayload{
		CPU:        cpu,
		Memory:     memory,
		Disk:       disk,
		LoadAvg:    loadAvg,
		NetworkIn:  networkIn,
		NetworkOut: networkOut,
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stats failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}
