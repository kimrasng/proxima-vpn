package models

import (
	"encoding/json"
	"time"
)

type Setting struct {
	Key       string          `json:"key" db:"key"`
	Value     json.RawMessage `json:"value" db:"value"`
	UpdatedAt time.Time       `json:"updated_at" db:"updated_at"`
}
