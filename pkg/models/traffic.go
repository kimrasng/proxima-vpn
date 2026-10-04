package models

import "time"

type TrafficLog struct {
	ID       string    `json:"id" db:"id"`
	DeviceID string    `json:"device_id" db:"device_id"`
	NodeID   string    `json:"node_id" db:"node_id"`
	UpBytes  int64     `json:"up_bytes" db:"up_bytes"`
	DnBytes  int64     `json:"dn_bytes" db:"dn_bytes"`
	LoggedAt time.Time `json:"logged_at" db:"logged_at"`
}
