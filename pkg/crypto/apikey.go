package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// GenerateAPIKey generates a 256-bit random hex string (64 characters).
func GenerateAPIKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// HashAPIKey returns the SHA-256 hex digest of the given API key.
func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
