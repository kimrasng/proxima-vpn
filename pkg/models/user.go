package models

import "time"

type User struct {
	ID              string     `json:"id" db:"id"`
	Email           string     `json:"email" db:"email"`
	PasswordHash    string     `json:"-" db:"password_hash"`
	Name            string     `json:"name" db:"name"`
	SubToken        string     `json:"sub_token" db:"sub_token"`
	PlanID          *string    `json:"plan_id,omitempty" db:"plan_id"`
	PlanStartedAt   *time.Time `json:"plan_started_at,omitempty" db:"plan_started_at"`
	PlanExpiresAt   *time.Time `json:"plan_expires_at,omitempty" db:"plan_expires_at"`
	TrafficUsed     int64      `json:"traffic_used" db:"traffic_used"`
	TrafficResetDay *int       `json:"traffic_reset_day,omitempty" db:"traffic_reset_day"`
	IsActive        bool       `json:"is_active" db:"is_active"`
	Status          string     `json:"status" db:"status"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
}
