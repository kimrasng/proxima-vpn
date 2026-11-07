package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// keyEnv is the only accepted key input channel. Never add a CLI flag or a
// file path for the private key: argv, arguments and files leak through
// process listings, CI logs, shell history and core dumps. The environment
// of the signing step does not. The key is also never printed or included in
// any error, summary or exit path.
const keyEnv = "PROXIMA_RELEASE_SIGNING_KEY"

var errNoKey = fmt.Errorf(
	"signing key is not configured: set %s from the protected GitHub secret RELEASE_SIGNING_KEY; refusing to sign",
	keyEnv)

var (
	errBadKey   = errors.New("invalid signing key: want standard padded base64 of a 64-byte Ed25519 private key or a 32-byte seed")
	errZeroKey  = errors.New("invalid signing key: key material is all zeros")
	errSplitKey = errors.New("invalid signing key: the public half does not match the seed")
)

// loadSigningKey reads and parses the publisher's Ed25519 private key. It
// fails closed on an absent, empty, malformed, zero or internally
// inconsistent key and never echoes key material back.
func loadSigningKey() (ed25519.PrivateKey, error) {
	raw := strings.TrimSpace(os.Getenv(keyEnv))
	if raw == "" {
		return nil, errNoKey
	}
	b, err := base64.StdEncoding.Strict().DecodeString(raw)
	if err != nil {
		return nil, errBadKey
	}
	switch len(b) {
	case ed25519.SeedSize:
		if isZero(b) {
			return nil, errZeroKey
		}
		return ed25519.NewKeyFromSeed(b), nil
	case ed25519.PrivateKeySize:
		key := ed25519.PrivateKey(b)
		if isZero(key[:ed25519.SeedSize]) {
			return nil, errZeroKey
		}
		if !key.Equal(ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])) {
			return nil, errSplitKey
		}
		return key, nil
	default:
		return nil, errBadKey
	}
}

func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// publicKeyOf derives the verifier-side public key of the loaded signing key.
func publicKeyOf(key ed25519.PrivateKey) ed25519.PublicKey {
	pub, _ := key.Public().(ed25519.PublicKey)
	return pub
}

// runPublicKey prints the policy public key for the private key in the
// environment, so the operator can provision
// /etc/proxima-updater/release-policy.json without handling the private key
// again. The public key is not secret.
func runPublicKey(_ []string, stdout io.Writer) error {
	key, err := loadSigningKey()
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(publicKeyOf(key)))
	return nil
}

// runKeygen prints a freshly generated private/public key pair. Run this
// once, locally, when enrolling a publisher: paste the private key into the
// GitHub secret RELEASE_SIGNING_KEY and the public key into the root policy
// file. The private key is printed exactly once, here, at creation time.
func runKeygen(_ []string, stdout io.Writer) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "private-key: "+base64.StdEncoding.EncodeToString(priv))
	fmt.Fprintln(stdout, "public-key:  "+base64.StdEncoding.EncodeToString(pub))
	fmt.Fprintln(stdout, "store the private key in the GitHub secret RELEASE_SIGNING_KEY")
	fmt.Fprintln(stdout, "provision the public key in /etc/proxima-updater/release-policy.json")
	return nil
}
