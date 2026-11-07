// Package releaseverify authenticates release transitions without fetching,
// executing, approving, or persisting anything. See ../SIGNED_RELEASES.md.
package releaseverify

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	SignatureDomain = "proxima-vpn/server-updater/release/v1\x00"
	MaxEnvelopeSize = 16384
	MaxSequence     = uint64(1<<53 - 1)
	maxLifetime     = int64(7 * 24 * 60 * 60)
)

var (
	ErrManifest  = errors.New("invalid release manifest")
	ErrSignature = errors.New("release signature rejected")
	ErrPolicy    = errors.New("invalid release trust policy")
	ErrReplay    = errors.New("release replay or downgrade rejected")
	ErrTime      = errors.New("release validity window rejected")
	ErrApproval  = errors.New("release does not match root approval")
)

// Images contains complete repository@sha256 references, never tags or URLs.
type Images struct {
	API string `json:"api"`
	Web string `json:"web"`
}

// Manifest is the v1 signing DTO. Field order and all fields are mandatory:
// payload bytes must equal encoding/json.Marshal of this exact structure.
// A Manifest alone is NOT authenticated. Only Verifier.Verify produces Verified.
type Manifest struct {
	Schema             int    `json:"schema"`
	Product            string `json:"product"`
	Channel            string `json:"channel"`
	ReleaseID          string `json:"release_id"`
	Sequence           uint64 `json:"sequence"`
	IssuedAt           int64  `json:"issued_at"`
	ExpiresAt          int64  `json:"expires_at"`
	From               Images `json:"from"`
	To                 Images `json:"to"`
	RollbackCompatible bool   `json:"rollback_compatible"`
}

// Envelope carries standard padded base64. No algorithm or key selection is
// accepted from the publisher: v1 always uses the locally pinned Ed25519 key.
type Envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// Context must come from trusted local state under the controller lock. Verify
// is read-only: callers MUST durably consume Sequence before any side effect.
// HighWater is the highest accepted sequence, including failed/rolled-back
// attempts. Zero is not an implicit first-install or missing-state fallback.
type Context struct {
	HighWater uint64
	Current   Images
	Now       time.Time
}

// Verified is immutable outside this package. Its zero value is not verified.
type Verified struct {
	manifest Manifest
	digest   [sha256.Size]byte
}

func (v Verified) Manifest() Manifest      { return v.manifest }
func (v Verified) PayloadSHA256() [32]byte { return v.digest }

// Approval is an independent root authorization, never constructed from an
// untrusted manifest by the runtime. Root may disable signed rollback permission.
type Approval struct {
	ID                 string
	From               Images
	To                 Images
	RollbackCompatible bool
}

func (v Verified) Authorize(a Approval) error {
	m := v.manifest
	if m.Sequence == 0 || a.ID != m.ReleaseID || a.From != m.From || a.To != m.To ||
		(a.RollbackCompatible && !m.RollbackCompatible) {
		return ErrApproval
	}
	return nil
}

// Verify checks publisher authenticity, scope, image binding, freshness, and
// monotonic release ordering. It does not grant permission to execute a release.
func (v Verifier) Verify(data []byte, state Context) (Verified, error) {
	if v.minimum == 0 {
		return Verified{}, ErrPolicy
	}
	if state.HighWater == 0 || state.HighWater > MaxSequence {
		return Verified{}, ErrReplay
	}
	var envelope Envelope
	if len(data) > MaxEnvelopeSize || canonicalJSON(data, &envelope) != nil {
		return Verified{}, ErrManifest
	}
	payload, err := canonicalBase64(envelope.Payload)
	if err != nil {
		return Verified{}, ErrManifest
	}
	signature, err := canonicalBase64(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(v.key[:], append([]byte(SignatureDomain), payload...), signature) {
		return Verified{}, ErrSignature
	}
	var m Manifest
	if canonicalJSON(payload, &m) != nil || m.Schema != 1 || m.Product != "proxima-vpn" ||
		m.Channel != v.channel || !namePattern.MatchString(m.ReleaseID) {
		return Verified{}, ErrManifest
	}
	if m.Sequence > MaxSequence || m.Sequence <= state.HighWater || m.Sequence < v.minimum {
		return Verified{}, ErrReplay
	}
	now := state.Now.Unix()
	if m.IssuedAt <= 0 || m.ExpiresAt <= m.IssuedAt ||
		m.ExpiresAt-m.IssuedAt > maxLifetime || state.Now.IsZero() ||
		m.IssuedAt > now || now >= m.ExpiresAt {
		return Verified{}, ErrTime
	}
	if !v.validImages(m.From) || !v.validImages(m.To) || m.From != state.Current || m.From == m.To {
		return Verified{}, ErrManifest
	}
	return Verified{manifest: m, digest: sha256.Sum256(payload)}, nil
}

// The structs here deliberately have no omitempty fields or maps. Byte equality
// enforces one representation, rejecting unknown/duplicate/case-aliased keys,
// missing/null fields, alternate escapes/numbers, whitespace and trailing data.
func canonicalJSON[T any](data []byte, dst *T) error {
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("%w: JSON", ErrManifest)
	}
	encoded, err := json.Marshal(dst)
	if err != nil || !bytes.Equal(data, encoded) {
		return ErrManifest
	}
	return nil
}

func canonicalBase64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || base64.StdEncoding.EncodeToString(b) != s {
		return nil, ErrManifest
	}
	return b, nil
}
