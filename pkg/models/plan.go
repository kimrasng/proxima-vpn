package models

import "time"

type Plan struct {
	ID           string  `json:"id" db:"id"`
	Name         string  `json:"name" db:"name"`
	TrafficLimit *int64  `json:"traffic_limit,omitempty" db:"traffic_limit"`
	DurationDays int     `json:"duration_days" db:"duration_days"`
	MaxDevices   int     `json:"max_devices" db:"max_devices"`
	SpeedLimit   *int    `json:"speed_limit,omitempty" db:"speed_limit"`
	NodeGroupID  string  `json:"node_group_id" db:"node_group_id"`
	IsActive     bool    `json:"is_active" db:"is_active"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}
