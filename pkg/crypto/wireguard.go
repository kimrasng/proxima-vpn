package crypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
)

// GenerateWireGuardKeypair generates an X25519 keypair for WireGuard, returning
// the private and public keys encoded as standard base64 (with padding) - the
// format produced by `wg genkey`/`wg pubkey` and expected by wg-quick config
// files and WireGuard client apps. Mathematically identical to
// GenerateRealityKeypair; only the encoding differs.
func GenerateWireGuardKeypair() (privateKey, publicKey string, err error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	privateKey = base64.StdEncoding.EncodeToString(priv.Bytes())
	publicKey = base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
	return privateKey, publicKey, nil
}

// DeriveWireGuardPublicKey computes the public key for a standard-base64
// WireGuard private key. Used to report a node's WireGuard server public key
// to subscribing clients without persisting it separately - the server
// keypair itself lives in the node's wireguard inbound settings
// (inbounds.settings.private_key, see node-agent/cmd/main.go:buildWireGuardConfig).
func DeriveWireGuardPublicKey(privateKey string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(privateKey)
	if err != nil {
		return "", err
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()), nil
}
