package crypto

import (
	"crypto/rand"
	"encoding/base64"
)

// GenerateRandomString generates a URL-safe base64 encoded random string of the given length.
func GenerateRandomString(length int) string {
	byteLen := (length*3 + 3) / 4
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(b)[:length]
}

// GenerateRandomPassword generates a 16-character random password.
func GenerateRandomPassword() string {
	return GenerateRandomString(16)
}
