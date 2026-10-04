package models

import "time"

type Node struct {
	ID                 string     `json:"id" db:"id"`
	Name               string     `json:"name" db:"name"`
	Country            string     `json:"country" db:"country"`
	Region             string     `json:"region" db:"region"`
	IP                 string     `json:"ip" db:"ip"`
	Port               int        `json:"port" db:"port"`
	APIKey             string     `json:"-" db:"api_key"`
	RegToken           *string    `json:"reg_token,omitempty" db:"reg_token"`
	Status             string     `json:"status" db:"status"`
	LastSeen           *time.Time `json:"last_seen,omitempty" db:"last_seen"`
	XrayVersion        *string    `json:"xray_version,omitempty" db:"xray_version"`
	CPUUsage           *float64   `json:"cpu_usage,omitempty" db:"cpu_usage"`
	MemoryUsage        *float64   `json:"memory_usage,omitempty" db:"memory_usage"`
	DiskUsage          *float64   `json:"disk_usage,omitempty" db:"disk_usage"`
	LoadAvg            *float64   `json:"load_avg,omitempty" db:"load_avg"`
	RealityPrivateKey  *string    `json:"reality_private_key,omitempty" db:"reality_private_key"`
	RealityPublicKey   *string    `json:"reality_public_key,omitempty" db:"reality_public_key"`
	RealityShortID     *string    `json:"reality_short_id,omitempty" db:"reality_short_id"`
	TLSCertPath        *string    `json:"tls_cert_path,omitempty" db:"tls_cert_path"`
	TLSKeyPath         *string    `json:"tls_key_path,omitempty" db:"tls_key_path"`
	TLSCertExpiry      *time.Time `json:"tls_cert_expiry,omitempty" db:"tls_cert_expiry"`
	OSFamily           string     `json:"os_family" db:"os_family"`
	Role               string     `json:"role" db:"role"`
	PublishDirect      bool       `json:"publish_direct" db:"publish_direct"`
	MaxConcurrentConns int        `json:"max_concurrent_conns" db:"max_concurrent_conns"`
	FirewallPreset     string     `json:"firewall_preset" db:"firewall_preset"`
	FirewallPorts      string     `json:"firewall_ports" db:"firewall_ports"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
}

type NodeGroup struct {
	ID        string    `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type NodeGroupNode struct {
	NodeGroupID string `json:"node_group_id" db:"node_group_id"`
	NodeID      string `json:"node_id" db:"node_id"`
}

// NodeChain is a client-facing endpoint. New relayed links select EntryNodeID;
// RelayPoolID is retained only for previously persisted pool-based links. Both
// nil means the client reaches the exit directly.
//
// The client always authenticates against the exit's own credentials - the relay
// never decrypts - so a chain carries no keys of its own and works unchanged
// whatever protocol the exit runs.
type NodeChain struct {
	ID          string  `json:"id" db:"id"`
	Name        string  `json:"name" db:"name"`
	RelayPoolID *string `json:"relay_pool_id,omitempty" db:"relay_pool_id"`
	EntryNodeID *string `json:"entry_node_id,omitempty" db:"entry_node_id"`
	EntryHost   string  `json:"entry_host" db:"entry_host"`
	ExitNodeID  string  `json:"exit_node_id" db:"exit_node_id"`
	ExitPort    int     `json:"exit_port" db:"exit_port"`
	// EntryPort is NULL for a direct chain, which listens nowhere of its own.
	EntryPort         *int       `json:"entry_port,omitempty" db:"entry_port"`
	Transport         string     `json:"transport" db:"transport"`
	Mode              string     `json:"mode" db:"mode"`
	Priority          int        `json:"priority" db:"priority"`
	Enabled           bool       `json:"enabled" db:"enabled"`
	Health            string     `json:"health" db:"health"`
	LastProbeAt       *time.Time `json:"last_probe_at,omitempty" db:"last_probe_at"`
	ProbeRTTMs        *int       `json:"probe_rtt_ms,omitempty" db:"probe_rtt_ms"`
	TrafficMultiplier *float64   `json:"traffic_multiplier,omitempty" db:"traffic_multiplier"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
}

type NodeGroupChain struct {
	NodeGroupID string `json:"node_group_id" db:"node_group_id"`
	ChainID     string `json:"chain_id" db:"chain_id"`
}
