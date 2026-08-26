package models

import "time"

type Device struct {
	ID           string    `json:"id" db:"id"`
	UserID       string    `json:"user_id" db:"user_id"`
	Name         *string   `json:"name,omitempty" db:"name"`
	XrayUUID     string    `json:"xray_uuid" db:"xray_uuid"`
	WGPrivateKey *string   `json:"-" db:"wg_private_key"`
	WGPublicKey  *string   `json:"wg_public_key,omitempty" db:"wg_public_key"`
	WGAddress    *string   `json:"wg_address,omitempty" db:"wg_address"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}
