package releaseverify

import (
	"crypto/ed25519"
	"regexp"
	"strings"
)

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	hostLabel   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	repoPart    = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	digestHex   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Policy must be provisioned by root through an independently authenticated
// channel. Do not accept it, its key, or repository names from release input.
// MinimumSequence is a root enrollment/revocation floor, not mutable replay state.
type Policy struct {
	PublicKey       string `json:"public_key"`
	Channel         string `json:"channel"`
	APIRepository   string `json:"api_repository"`
	WebRepository   string `json:"web_repository"`
	MinimumSequence uint64 `json:"minimum_sequence"`
}

// Verifier owns a copy of the pinned key and policy and is safe for concurrent
// read-only verification. It does NOT serialize acceptance or remember replays.
type Verifier struct {
	key     [ed25519.PublicKeySize]byte
	channel string
	apiRepo string
	webRepo string
	minimum uint64
}

func NewVerifier(p Policy) (Verifier, error) {
	key, err := canonicalBase64(p.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize ||
		!namePattern.MatchString(p.Channel) || !validRepository(p.APIRepository) ||
		!validRepository(p.WebRepository) || p.APIRepository == p.WebRepository ||
		p.MinimumSequence == 0 || p.MinimumSequence > MaxSequence {
		return Verifier{}, ErrPolicy
	}
	var pinned [ed25519.PublicKeySize]byte
	copy(pinned[:], key)
	if pinned == [ed25519.PublicKeySize]byte{} {
		return Verifier{}, ErrPolicy
	}
	return Verifier{key: pinned, channel: p.Channel, apiRepo: p.APIRepository,
		webRepo: p.WebRepository, minimum: p.MinimumSequence}, nil
}

// Intentionally narrower than general OCI references: fully qualified DNS
// registry, no port, credentials, tag, scheme, query, traversal or shorthand.
func validRepository(repo string) bool {
	if len(repo) > 255 {
		return false
	}
	parts := strings.Split(repo, "/")
	if len(parts) < 2 || !strings.Contains(parts[0], ".") {
		return false
	}
	for _, label := range strings.Split(parts[0], ".") {
		if !hostLabel.MatchString(label) {
			return false
		}
	}
	for _, part := range parts[1:] {
		if !repoPart.MatchString(part) {
			return false
		}
	}
	return true
}

func (v Verifier) validImages(images Images) bool {
	for _, pair := range [][2]string{{images.API, v.apiRepo}, {images.Web, v.webRepo}} {
		digest, ok := strings.CutPrefix(pair[0], pair[1]+"@sha256:")
		if !ok || !digestHex.MatchString(digest) {
			return false
		}
	}
	return true
}
