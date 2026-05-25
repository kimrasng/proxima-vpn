package crypto

import "testing"

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("mypassword123")
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if len(hash) == 0 {
		t.Error("HashPassword() returned empty hash")
	}
}

func TestHashPassword_DifferentHashes(t *testing.T) {
	h1, _ := HashPassword("password")
	h2, _ := HashPassword("password")
	if h1 == h2 {
		t.Error("HashPassword() should produce different hashes for same input (bcrypt salt)")
	}
}

func TestCheckPassword_Valid(t *testing.T) {
	password := "secure-password-456"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if !CheckPassword(hash, password) {
		t.Error("CheckPassword() returned false for valid password")
	}
}

func TestCheckPassword_Invalid(t *testing.T) {
	hash, _ := HashPassword("correct-password")
	if CheckPassword(hash, "wrong-password") {
		t.Error("CheckPassword() returned true for invalid password")
	}
}
