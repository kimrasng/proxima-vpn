package crypto

import "golang.org/x/crypto/bcrypt"

// HashPassword hashes a plaintext password using bcrypt.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword returns true if the plaintext password matches the bcrypt hash.
func CheckPassword(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
