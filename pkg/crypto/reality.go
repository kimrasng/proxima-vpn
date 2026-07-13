package crypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
)

// GenerateRealityKeypair generates an X25519 keypair for Xray REALITY, returning
// the private and public keys encoded as base64 (raw, URL-safe) - the same
// encoding accepted by Xray's `privateKey`/`publicKey` REALITY config fields.
func GenerateRealityKeypair() (privateKey, publicKey string, err error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	privateKey = base64.RawURLEncoding.EncodeToString(priv.Bytes())
	publicKey = base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
	return privateKey, publicKey, nil
}

// GenerateRealityShortID returns a random hex short ID for Xray REALITY
// (8 hex characters, i.e. 4 random bytes).
func GenerateRealityShortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
