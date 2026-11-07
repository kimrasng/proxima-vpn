package releaseverify_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	rv "github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

func fixture() (rv.Policy, rv.Manifest, rv.Context, ed25519.PrivateKey) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{42}, ed25519.SeedSize))
	p := rv.Policy{PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)),
		Channel: "stable", APIRepository: "ghcr.io/proxima/api", WebRepository: "ghcr.io/proxima/web", MinimumSequence: 10}
	from := rv.Images{API: p.APIRepository + "@sha256:" + strings.Repeat("a", 64), Web: p.WebRepository + "@sha256:" + strings.Repeat("b", 64)}
	to := rv.Images{API: p.APIRepository + "@sha256:" + strings.Repeat("c", 64), Web: p.WebRepository + "@sha256:" + strings.Repeat("d", 64)}
	m := rv.Manifest{Schema: 1, Product: "proxima-vpn", Channel: "stable", ReleaseID: "release-ten", Sequence: 10,
		IssuedAt: 1800000000, ExpiresAt: 1800003600, From: from, To: to, RollbackCompatible: true}
	return p, m, rv.Context{HighWater: 9, Current: from, Now: time.Unix(1800000100, 0)}, key
}

func marshal[T any](t *testing.T, value T) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sign(t *testing.T, payload []byte, key ed25519.PrivateKey) []byte {
	t.Helper()
	sig := ed25519.Sign(key, append([]byte(rv.SignatureDomain), payload...))
	return marshal(t, rv.Envelope{Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(sig)})
}

func verifier(t *testing.T, p rv.Policy) rv.Verifier {
	t.Helper()
	v, err := rv.NewVerifier(p)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifyReturnsAuthenticatedTransition(t *testing.T) {
	p, m, state, key := fixture()
	payload := marshal(t, m)
	got, err := verifier(t, p).Verify(sign(t, payload, key), state)
	if err != nil || got.Manifest() != m || got.PayloadSHA256() != sha256.Sum256(payload) {
		t.Fatalf("incorrect authenticated result: %+v, %v", got.Manifest(), err)
	}
	copy := got.Manifest()
	copy.To.API = "untrusted"
	if got.Manifest() != m {
		t.Fatal("caller mutated verified transition")
	}
}

func TestVerifyRejectsUnauthenticPayloads(t *testing.T) {
	for _, kind := range []string{"wrong key", "changed API", "changed web", "changed sequence", "changed rollback", "wrong domain", "short signature"} {
		t.Run(kind, func(t *testing.T) {
			p, m, state, key := fixture()
			payload := marshal(t, m)
			sig := ed25519.Sign(key, append([]byte(rv.SignatureDomain), payload...))
			switch kind {
			case "wrong key":
				sig = ed25519.Sign(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{43}, 32)), append([]byte(rv.SignatureDomain), payload...))
			case "changed API":
				m.To.API = strings.ReplaceAll(m.To.API, "cccc", "eeee")
			case "changed web":
				m.To.Web = strings.ReplaceAll(m.To.Web, "dddd", "eeee")
			case "changed sequence":
				m.Sequence++
			case "changed rollback":
				m.RollbackCompatible = false
			case "wrong domain":
				sig = ed25519.Sign(key, payload)
			case "short signature":
				sig = sig[:63]
			}
			data := marshal(t, rv.Envelope{Payload: base64.StdEncoding.EncodeToString(marshal(t, m)), Signature: base64.StdEncoding.EncodeToString(sig)})
			if _, err := verifier(t, p).Verify(data, state); !errors.Is(err, rv.ErrSignature) {
				t.Fatalf("expected signature rejection, got %v", err)
			}
		})
	}
}

func TestVerifyRejectsSignedOutOfPolicyTransitions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*rv.Manifest)
		want   error
	}{
		{"schema", func(m *rv.Manifest) { m.Schema = 2 }, rv.ErrManifest},
		{"product", func(m *rv.Manifest) { m.Product = "another-product" }, rv.ErrManifest},
		{"channel", func(m *rv.Manifest) { m.Channel = "beta" }, rv.ErrManifest},
		{"release URL", func(m *rv.Manifest) { m.ReleaseID = "https://host/release" }, rv.ErrManifest},
		{"replay", func(m *rv.Manifest) { m.Sequence = 9 }, rv.ErrReplay},
		{"downgrade", func(m *rv.Manifest) { m.Sequence = 8 }, rv.ErrReplay},
		{"overflow sequence", func(m *rv.Manifest) { m.Sequence = rv.MaxSequence + 1 }, rv.ErrReplay},
		{"expired", func(m *rv.Manifest) { m.ExpiresAt = 1800000100 }, rv.ErrTime},
		{"future", func(m *rv.Manifest) { m.IssuedAt = 1800000101 }, rv.ErrTime},
		{"long validity", func(m *rv.Manifest) { m.ExpiresAt = m.IssuedAt + 604801 }, rv.ErrTime},
		{"reversed validity", func(m *rv.Manifest) { m.ExpiresAt = m.IssuedAt }, rv.ErrTime},
		{"negative issue", func(m *rv.Manifest) { m.IssuedAt = -1 }, rv.ErrTime},
		{"timestamp overflow", func(m *rv.Manifest) { m.ExpiresAt = 1<<63 - 1 }, rv.ErrTime},
		{"no transition", func(m *rv.Manifest) { m.To = m.From }, rv.ErrManifest},
		{"wrong source", func(m *rv.Manifest) { m.From = m.To; m.From.API = fixtureAPI('e') }, rv.ErrManifest},
		{"arbitrary repository", func(m *rv.Manifest) { m.To.API = "evil.test/api@sha256:" + strings.Repeat("c", 64) }, rv.ErrManifest},
		{"repository prefix", func(m *rv.Manifest) { m.To.API = "ghcr.io/proxima/api-extra@sha256:" + strings.Repeat("c", 64) }, rv.ErrManifest},
		{"tag", func(m *rv.Manifest) { m.To.API = "ghcr.io/proxima/api:latest" }, rv.ErrManifest},
		{"URL", func(m *rv.Manifest) { m.To.Web = "https://" + m.To.Web }, rv.ErrManifest},
		{"command", func(m *rv.Manifest) { m.To.Web += ";id" }, rv.ErrManifest},
		{"uppercase digest", func(m *rv.Manifest) { m.To.API = strings.ReplaceAll(m.To.API, "cccc", "CCCC") }, rv.ErrManifest},
		{"short digest", func(m *rv.Manifest) { m.To.API = m.To.API[:len(m.To.API)-1] }, rv.ErrManifest},
		{"role swap", func(m *rv.Manifest) { m.To.API, m.To.Web = m.To.Web, m.To.API }, rv.ErrManifest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, m, state, key := fixture()
			tc.mutate(&m)
			if _, err := verifier(t, p).Verify(sign(t, marshal(t, m), key), state); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func fixtureAPI(c byte) string { return "ghcr.io/proxima/api@sha256:" + strings.Repeat(string(c), 64) }

func TestVerifyEnforcesTrustedBaselineAndFloor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		highWater uint64
		floor     uint64
		want      error
	}{
		{"first authenticated transition", 9, 10, nil},
		{"missing baseline", 0, 10, rv.ErrReplay},
		{"same accepted sequence", 10, 10, rv.ErrReplay},
		{"older than accepted", 11, 10, rv.ErrReplay},
		{"root floor", 8, 11, rv.ErrReplay},
		{"exhausted sequence", rv.MaxSequence, 10, rv.ErrReplay},
		{"corrupt baseline", rv.MaxSequence + 1, 10, rv.ErrReplay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, m, state, key := fixture()
			p.MinimumSequence, state.HighWater = tc.floor, tc.highWater
			_, err := verifier(t, p).Verify(sign(t, marshal(t, m), key), state)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestVerifyTimeBoundariesAndSingleImageUpdate(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
		want error
	}{
		{"at issue", time.Unix(1800000000, 0), nil},
		{"before expiry", time.Unix(1800604799, 999999999), nil},
		{"at expiry", time.Unix(1800604800, 0), rv.ErrTime},
		{"zero clock", time.Time{}, rv.ErrTime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, m, state, key := fixture()
			m.ExpiresAt, m.To.Web, state.Now = m.IssuedAt+604800, m.From.Web, tc.now
			_, err := verifier(t, p).Verify(sign(t, marshal(t, m), key), state)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}
