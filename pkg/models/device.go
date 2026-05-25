package models

import "time"

type Device struct {
	ID        string    `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	Name      *string   `json:"name,omitempty" db:"name"`
	XrayUUID  string    `json:"xray_uuid" db:"xray_uuid"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
