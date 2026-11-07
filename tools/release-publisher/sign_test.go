package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rv "github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

const (
	apiRepo = "ghcr.io/example/proxima-vpn/api"
	webRepo = "ghcr.io/example/proxima-vpn/web"
)

func ref(repo, hex64 string) string { return repo + "@sha256:" + hex64 }

func hex64(ch byte) string { return strings.Repeat(string(ch), 64) }

func testKey(t *testing.T, seed byte) (ed25519.PrivateKey, string) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	return key, base64.StdEncoding.EncodeToString(key)
}

func withKey(t *testing.T, value string) {
	t.Helper()
	t.Setenv(keyEnv, value)
}

func validArgs(out string) []string {
	return []string{
		"--from-api", ref(apiRepo, hex64('a')),
		"--from-web", ref(webRepo, hex64('b')),
		"--to-api", ref(apiRepo, hex64('c')),
		"--to-web", ref(webRepo, hex64('d')),
		"--release-id", "release-two",
		"--channel", "stable",
		"--sequence", "2",
		"--validity", "72h",
		"--out", out,
	}
}

// decodeEnvelopeCanonical asserts the file holds exactly the compact JSON of
// rv.Envelope (byte equality against a re-marshal, so no trailing newline or
// whitespace survives) and returns its decoded form.
func decodeEnvelopeCanonical(t *testing.T, data []byte) rv.Envelope {
	t.Helper()
	var envelope rv.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("envelope is not JSON: %v", err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, encoded) {
		t.Fatalf("envelope is not the exact compact JSON encoding:\n got %q\nwant %q", data, encoded)
	}
	return envelope
}

func decodePayloadCanonical(t *testing.T, envelope rv.Envelope) rv.Manifest {
	t.Helper()
	payload, err := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if err != nil {
		t.Fatalf("payload base64: %v", err)
	}
	var m rv.Manifest
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, encoded) {
		t.Fatalf("payload is not the exact json.Marshal of rv.Manifest:\n got %q\nwant %q", payload, encoded)
	}
	return m
}

func TestSignProducesVerifierAcceptedCanonicalEnvelope(t *testing.T) {
	key, keyB64 := testKey(t, 7)
	withKey(t, keyB64)
	out := filepath.Join(t.TempDir(), "release-envelope.json")
	var stdout strings.Builder
	if err := runSign(validArgs(out), &stdout); err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("output not written: %v", err)
	}
	if len(data) > rv.MaxEnvelopeSize {
		t.Fatalf("envelope exceeds the verifier limit: %d bytes", len(data))
	}
	if bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("envelope has a trailing newline")
	}
	envelope := decodeEnvelopeCanonical(t, data)
	m := decodePayloadCanonical(t, envelope)

	if m.Schema != 1 || m.Product != product || m.Channel != "stable" || m.ReleaseID != "release-two" || m.Sequence != 2 {
		t.Fatalf("wrong manifest fields: %+v", m)
	}
	if m.From != (rv.Images{API: ref(apiRepo, hex64('a')), Web: ref(webRepo, hex64('b'))}) ||
		m.To != (rv.Images{API: ref(apiRepo, hex64('c')), Web: ref(webRepo, hex64('d'))}) {
		t.Fatalf("wrong transition: %+v", m)
	}
	if m.RollbackCompatible {
		t.Fatal("rollback compatibility must default to false")
	}
	if m.ExpiresAt-m.IssuedAt != int64(72*time.Hour/time.Second) {
		t.Fatalf("validity window is not exactly 72h: %d..%d", m.IssuedAt, m.ExpiresAt)
	}
	if now := time.Now().Unix(); m.IssuedAt > now || now-m.IssuedAt > 604 {
		t.Fatalf("issued_at not conservatively in the past: issued %d, now %d", m.IssuedAt, now)
	}

	// Independent verification with the real verifier, as a host would do.
	verifier, err := rv.NewVerifier(rv.Policy{
		PublicKey:       base64.StdEncoding.EncodeToString(publicKeyOf(key)),
		Channel:         "stable",
		APIRepository:   apiRepo,
		WebRepository:   webRepo,
		MinimumSequence: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.Verify(data, rv.Context{
		HighWater: 1,
		Current:   m.From,
		Now:       time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("verifier rejected the produced envelope: %v", err)
	}
	if verified.Manifest() != m {
		t.Fatal("verifier saw a different manifest")
	}
	if stdout.String() == "" || strings.Contains(stdout.String(), keyB64) {
		t.Fatal("summary must be nonempty and must never contain key material")
	}
}

func TestSignRollbackFlagAndDerivedReleaseID(t *testing.T) {
	_, keyB64 := testKey(t, 8)
	withKey(t, keyB64)
	for _, tc := range []struct {
		name     string
		args     func(out string) []string
		wantID   string
		rollback bool
	}{
		{"derived from tag", func(out string) []string {
			return append(remove(validArgs(out), "--release-id"), "--tag", "v1.2.3")
		}, "v1-2-3-2", false},
		{"upper tag lowercased", func(out string) []string {
			return append(remove(validArgs(out), "--release-id"), "--tag", "V1.2.3")
		}, "v1-2-3-2", false},
		{"numeric tag prefixed", func(out string) []string {
			return append(remove(validArgs(out), "--release-id"), "--tag", "1.2.3")
		}, "rel-1-2-3-2", false},
		{"explicit rollback", func(out string) []string {
			return append(validArgs(out), "--rollback-compatible=true")
		}, "release-two", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "release-envelope.json")
			var stdout strings.Builder
			if err := runSign(tc.args(out), &stdout); err != nil {
				t.Fatalf("sign failed: %v", err)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			m := decodePayloadCanonical(t, decodeEnvelopeCanonical(t, data))
			if m.ReleaseID != tc.wantID {
				t.Fatalf("release id: got %q, want %q", m.ReleaseID, tc.wantID)
			}
			if m.RollbackCompatible != tc.rollback {
				t.Fatalf("rollback_compatible: got %v, want %v", m.RollbackCompatible, tc.rollback)
			}
		})
	}
}

func TestSignFailsClosedWithoutKey(t *testing.T) {
	for _, value := range []string{"", "   "} {
		withKey(t, value)
		out := filepath.Join(t.TempDir(), "release-envelope.json")
		err := runSign(validArgs(out), &strings.Builder{})
		if err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("value %q: expected fail-closed not-configured error, got %v", value, err)
		}
		if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
			t.Fatal("no key must never produce an output file")
		}
	}
}

func TestSignRejectsBadKeys(t *testing.T) {
	split := make([]byte, 64)
	copy(split, bytes.Repeat([]byte{9}, 32)) // seed half
	copy(split[32:], bytes.Repeat([]byte{1}, 32))
	_, goodSeed := testKey(t, 3)
	_, goodFull := testKey(t, 4)
	for _, tc := range []struct {
		name  string
		value string
		want  error
	}{
		{"garbage", "!!!not-base64", errBadKey},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 31)), errBadKey},
		{"too long", base64.StdEncoding.EncodeToString(make([]byte, 33)), errBadKey},
		{"zero seed", base64.StdEncoding.EncodeToString(make([]byte, 32)), errZeroKey},
		{"mismatched halves", base64.StdEncoding.EncodeToString(split), errSplitKey},
		{"seed form accepted", goodSeed, nil},
		{"full key form accepted", goodFull, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withKey(t, tc.value)
			_, err := loadSigningKey()
			if tc.want == nil {
				if err != nil {
					t.Fatalf("valid key rejected: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), tc.value) {
				t.Fatal("error leaks key material")
			}
		})
	}
}

func TestSignRejectsInvalidOptions(t *testing.T) {
	_, keyB64 := testKey(t, 11)
	withKey(t, keyB64)
	for _, tc := range []struct {
		name string
		edit func(args []string) []string
	}{
		{"sequence below floor", func(a []string) []string { return replace(a, "--sequence", "1") }},
		{"sequence zero", func(a []string) []string { return replace(a, "--sequence", "0") }},
		{"sequence overflow", func(a []string) []string { return replace(a, "--sequence", "9007199254740992") }},
		{"channel uppercase", func(a []string) []string { return replace(a, "--channel", "Stable") }},
		{"channel separator", func(a []string) []string { return replace(a, "--channel", "sta.ble") }},
		{"channel empty", func(a []string) []string { return replace(a, "--channel", "") }},
		{"validity zero", func(a []string) []string { return replace(a, "--validity", "0h") }},
		{"validity over cap", func(a []string) []string { return replace(a, "--validity", "169h") }},
		{"validity negative", func(a []string) []string { return replace(a, "--validity", "-1h") }},
		{"from equals to", func(a []string) []string {
			return replace(replace(replace(a, "--to-api", ref(apiRepo, hex64('a'))), "--to-web", ref(webRepo, hex64('b'))), "--sequence", "3")
		}},
		{"api repository differs", func(a []string) []string { return replace(a, "--to-api", "ghcr.io/other/api@sha256:"+hex64('c')) }},
		{"web repository differs", func(a []string) []string { return replace(a, "--to-web", "ghcr.io/other/web@sha256:"+hex64('d')) }},
		{"uppercase digest", func(a []string) []string { return replace(a, "--from-api", ref(apiRepo, hex64('A'))) }},
		{"short digest", func(a []string) []string { return replace(a, "--from-api", apiRepo+"@sha256:"+hex64('a')[:63]) }},
		{"tag instead of digest", func(a []string) []string { return replace(a, "--to-api", apiRepo+":latest") }},
		{"web uses api repository", func(a []string) []string { return replace(a, "--from-web", ref(apiRepo, hex64('b'))) }},
		{"bad release id", func(a []string) []string { return replace(a, "--release-id", "Bad ID") }},
		{"release id URL", func(a []string) []string { return replace(a, "--release-id", "https://evil.test/x") }},
		{"no release id source", func(a []string) []string { return remove(a, "--release-id") }},
		{"no output", func(a []string) []string { return remove(a, "--out") }},
		{"no channel", func(a []string) []string { return remove(a, "--channel") }},
		{"no sequence", func(a []string) []string { return remove(a, "--sequence") }},
		{"positional junk", func(a []string) []string { return append(a, "junk") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "release-envelope.json")
			err := runSign(tc.edit(validArgs(out)), &strings.Builder{})
			if err == nil {
				t.Fatal("expected rejection")
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Fatal("rejected sign must never leave an output file")
			}
			if strings.Contains(err.Error(), keyB64) {
				t.Fatal("error leaks key material")
			}
		})
	}
}

func replace(args []string, flag, value string) []string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			args[i+1] = value
			return args
		}
	}
	panic("flag not found: " + flag)
}

func remove(args []string, flag string) []string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return append(args[:i:i], args[i+2:]...)
		}
	}
	panic("flag not found: " + flag)
}

func TestSignKeepsPreviousFileOnFailureAndReplacesOnSuccess(t *testing.T) {
	_, keyB64 := testKey(t, 12)
	withKey(t, keyB64)
	out := filepath.Join(t.TempDir(), "release-envelope.json")
	if err := os.WriteFile(out, []byte("KEEP"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSign(replace(validArgs(out), "--sequence", "1"), &strings.Builder{}); err == nil {
		t.Fatal("expected rejection")
	}
	data, err := os.ReadFile(out)
	if err != nil || string(data) != "KEEP" {
		t.Fatalf("failed sign must not touch an existing file: %q, %v", data, err)
	}
	if err := runSign(validArgs(out), &strings.Builder{}); err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	if data, err = os.ReadFile(out); err != nil || bytes.Equal(data, []byte("KEEP")) {
		t.Fatal("successful sign must replace the file")
	}
}

func TestSignOutPathMustBeWritable(t *testing.T) {
	_, keyB64 := testKey(t, 13)
	withKey(t, keyB64)
	out := filepath.Join(t.TempDir(), "missing-dir", "release-envelope.json")
	if err := runSign(validArgs(out), &strings.Builder{}); err == nil {
		t.Fatal("expected write failure for a missing output directory")
	}
}

func TestPublicKeyCommand(t *testing.T) {
	key, keyB64 := testKey(t, 14)
	withKey(t, keyB64)
	var stdout strings.Builder
	if err := runPublicKey(nil, &stdout); err != nil {
		t.Fatal(err)
	}
	want := base64.StdEncoding.EncodeToString(publicKeyOf(key))
	if strings.TrimSpace(stdout.String()) != want {
		t.Fatalf("public key mismatch: got %q, want %q", stdout.String(), want)
	}
	withKey(t, "")
	if err := runPublicKey(nil, &stdout); err == nil {
		t.Fatal("public-key must fail closed without a key")
	}
}

func TestKeygenPrintsMatchingFreshPair(t *testing.T) {
	var first, second strings.Builder
	if err := runKeygen(nil, &first); err != nil {
		t.Fatal(err)
	}
	if err := runKeygen(nil, &second); err != nil {
		t.Fatal(err)
	}
	parse := func(out string) (ed25519.PrivateKey, ed25519.PublicKey) {
		var privB64, pubB64 string
		for _, line := range strings.Split(out, "\n") {
			if v, ok := strings.CutPrefix(line, "private-key: "); ok {
				privB64 = v
			}
			if v, ok := strings.CutPrefix(line, "public-key:  "); ok {
				pubB64 = v
			}
		}
		if privB64 == "" || pubB64 == "" {
			t.Fatalf("keygen output incomplete: %q", out)
		}
		priv, err := base64.StdEncoding.Strict().DecodeString(privB64)
		if err != nil || len(priv) != ed25519.PrivateKeySize {
			t.Fatalf("bad private key: %q", privB64)
		}
		pub, err := base64.StdEncoding.Strict().DecodeString(pubB64)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			t.Fatalf("bad public key: %q", pubB64)
		}
		return ed25519.PrivateKey(priv), ed25519.PublicKey(pub)
	}
	priv1, pub1 := parse(first.String())
	priv2, pub2 := parse(second.String())
	if priv1.Equal(priv2) || bytes.Equal(pub1, pub2) {
		t.Fatal("keygen must produce a fresh key each run")
	}
	if !bytes.Equal(publicKeyOf(priv1), pub1) {
		t.Fatal("printed public key does not match the printed private key")
	}
}

func TestMaxSequenceReadsLedger(t *testing.T) {
	_, keyB64 := testKey(t, 15)
	withKey(t, keyB64)
	dir := t.TempDir()
	write := func(name, sequence string) string {
		out := filepath.Join(dir, name)
		if err := runSign(replace(validArgs(out), "--sequence", sequence), &strings.Builder{}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	low := write("low.json", "2")
	high := write("high.json", "5")
	for _, tc := range []struct {
		name  string
		files []string
		want  string
	}{
		{"single", []string{low}, "2\n"},
		{"highest", []string{low, high}, "5\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout strings.Builder
			if err := runMaxSequence(tc.files, &stdout); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != tc.want {
				t.Fatalf("got %q, want %q", stdout.String(), tc.want)
			}
		})
	}
	if err := runMaxSequence(nil, &strings.Builder{}); err == nil {
		t.Fatal("empty ledger must fail closed")
	}
	if err := runMaxSequence([]string{filepath.Join(dir, "missing.json")}, &strings.Builder{}); err == nil {
		t.Fatal("unreadable ledger entry must fail closed")
	}
	data, err := os.ReadFile(high)
	if err != nil {
		t.Fatal(err)
	}
	tampered := filepath.Join(dir, "tampered.json")
	if err := os.WriteFile(tampered, append(append([]byte{}, data...), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMaxSequence([]string{tampered}, &strings.Builder{}); err == nil {
		t.Fatal("noncanonical ledger entry must fail closed")
	}
}

func TestEnvelopeSequenceRejectsAmbiguousJSON(t *testing.T) {
	if _, err := envelopeSequence([]byte(`{"payload":"e30=","signature":"x","schema":1}`)); err == nil {
		t.Fatal("unknown field must be rejected")
	}
	if _, err := envelopeSequence([]byte(`{}`)); err == nil {
		t.Fatal("empty object must be rejected")
	}
	if _, err := envelopeSequence(bytes.Repeat([]byte("x"), rv.MaxEnvelopeSize+1)); err == nil {
		t.Fatal("oversized envelope must be rejected")
	}
}
