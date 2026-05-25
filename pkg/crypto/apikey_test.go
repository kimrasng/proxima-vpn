package crypto

import (
	"encoding/hex"
	"testing"
)

func TestGenerateAPIKey_Length(t *testing.T) {
	key := GenerateAPIKey()
	if len(key) != 64 {
		t.Errorf("GenerateAPIKey() length = %d, want 64", len(key))
	}
}

func TestGenerateAPIKey_ValidHex(t *testing.T) {
	key := GenerateAPIKey()
	_, err := hex.DecodeString(key)
	if err != nil {
		t.Errorf("GenerateAPIKey() not valid hex: %v", err)
	}
}

func TestGenerateAPIKey_Unique(t *testing.T) {
	a := GenerateAPIKey()
	b := GenerateAPIKey()
	if a == b {
		t.Error("GenerateAPIKey() generated duplicate keys")
	}
}

func TestHashAPIKey(t *testing.T) {
	key := "test-api-key-123"
	hash := HashAPIKey(key)
	if len(hash) != 64 {
		t.Errorf("HashAPIKey() length = %d, want 64", len(hash))
	}
	_, err := hex.DecodeString(hash)
	if err != nil {
		t.Errorf("HashAPIKey() not valid hex: %v", err)
	}
}

func TestHashAPIKey_Deterministic(t *testing.T) {
	key := "same-key"
	h1 := HashAPIKey(key)
	h2 := HashAPIKey(key)
	if h1 != h2 {
		t.Error("HashAPIKey() not deterministic")
	}
}

func TestHashAPIKey_DifferentKeys(t *testing.T) {
	h1 := HashAPIKey("key-1")
	h2 := HashAPIKey("key-2")
	if h1 == h2 {
		t.Error("HashAPIKey() same hash for different keys")
	}
}
