package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultConfigPath = "/etc/node-agent/config.json"

// AgentConfig holds the node agent configuration persisted locally.
type AgentConfig struct {
	NodeID    string `json:"node_id"`
	APIKey    string `json:"api_key"`
	ServerURL string `json:"server_url"`
}

// Load reads the agent config from the given JSON file path.
func Load(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

// Save writes the agent config to the given JSON file path.
func Save(path string, cfg *AgentConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}
