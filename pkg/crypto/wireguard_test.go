package crypto

import (
	"encoding/base64"
	"testing"
)

func TestGenerateWireGuardKeypair(t *testing.T) {
	priv, pub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("GenerateWireGuardKeypair: %v", err)
	}

	privBytes, err := base64.StdEncoding.DecodeString(priv)
	if err != nil {
		t.Fatalf("private key is not standard base64: %v", err)
	}
	if len(privBytes) != 32 {
		t.Fatalf("private key length = %d, want 32", len(privBytes))
	}

	pubBytes, err := base64.StdEncoding.DecodeString(pub)
	if err != nil {
		t.Fatalf("public key is not standard base64: %v", err)
	}
	if len(pubBytes) != 32 {
		t.Fatalf("public key length = %d, want 32", len(pubBytes))
	}

	if priv == pub {
		t.Fatal("private and public key must differ")
	}

	priv2, _, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("GenerateWireGuardKeypair (2nd call): %v", err)
	}
	if priv == priv2 {
		t.Fatal("two calls produced the same private key")
	}
}

func TestDeriveWireGuardPublicKey(t *testing.T) {
	priv, pub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("GenerateWireGuardKeypair: %v", err)
	}

	derived, err := DeriveWireGuardPublicKey(priv)
	if err != nil {
		t.Fatalf("DeriveWireGuardPublicKey: %v", err)
	}
	if derived != pub {
		t.Fatalf("derived public key %q != generated public key %q", derived, pub)
	}

	if _, err := DeriveWireGuardPublicKey("not-base64!!"); err == nil {
		t.Fatal("expected error for invalid base64 input")
	}
}
