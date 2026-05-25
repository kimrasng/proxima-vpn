package crypto

import "github.com/google/uuid"

// NewUUID generates a new UUID v4 string.
func NewUUID() string {
	return uuid.New().String()
}
