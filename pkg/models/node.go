package models

import "time"

type Node struct {
	ID                string     `json:"id" db:"id"`
	Name              string     `json:"name" db:"name"`
	Country           string     `json:"country" db:"country"`
	Region            string     `json:"region" db:"region"`
	IP                string     `json:"ip" db:"ip"`
	Port              int        `json:"port" db:"port"`
	APIKey            string     `json:"-" db:"api_key"`
	RegToken          *string    `json:"reg_token,omitempty" db:"reg_token"`
	Status            string     `json:"status" db:"status"`
	LastSeen          *time.Time `json:"last_seen,omitempty" db:"last_seen"`
	XrayVersion       *string    `json:"xray_version,omitempty" db:"xray_version"`
	CPUUsage          *float64   `json:"cpu_usage,omitempty" db:"cpu_usage"`
	MemoryUsage       *float64   `json:"memory_usage,omitempty" db:"memory_usage"`
	DiskUsage         *float64   `json:"disk_usage,omitempty" db:"disk_usage"`
	LoadAvg           *float64   `json:"load_avg,omitempty" db:"load_avg"`
	RealityPrivateKey *string    `json:"reality_private_key,omitempty" db:"reality_private_key"`
	RealityPublicKey  *string    `json:"reality_public_key,omitempty" db:"reality_public_key"`
	RealityShortID    *string    `json:"reality_short_id,omitempty" db:"reality_short_id"`
	TLSCertPath       *string    `json:"tls_cert_path,omitempty" db:"tls_cert_path"`
	TLSKeyPath        *string    `json:"tls_key_path,omitempty" db:"tls_key_path"`
	TLSCertExpiry     *time.Time `json:"tls_cert_expiry,omitempty" db:"tls_cert_expiry"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
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
