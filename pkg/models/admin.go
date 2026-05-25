package models

import "time"

type Admin struct {
	ID           string    `json:"id" db:"id"`
	Email        string    `json:"email" db:"email"`
	PasswordHash string    `json:"-" db:"password_hash"`
	TOTPSecret   *string   `json:"totp_secret,omitempty" db:"totp_secret"`
	TOTPEnabled  bool      `json:"totp_enabled" db:"totp_enabled"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}
