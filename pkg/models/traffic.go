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

type PlanRequest struct {
	ID         string     `json:"id" db:"id"`
	UserID     string     `json:"user_id" db:"user_id"`
	PlanID     string     `json:"plan_id" db:"plan_id"`
	Status     string     `json:"status" db:"status"`
	ReviewedBy *string    `json:"reviewed_by,omitempty" db:"reviewed_by"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty" db:"reviewed_at"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
}
