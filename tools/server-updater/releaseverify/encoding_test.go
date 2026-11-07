package releaseverify_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	rv "github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

func TestVerifyRejectsAmbiguousSignedJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(string) string
	}{
		{"unknown command", func(s string) string { return strings.Replace(s, `"schema":1`, `"command":"id","schema":1`, 1) }},
		{"unknown URL", func(s string) string {
			return strings.Replace(s, `"schema":1`, `"url":"https://evil.test","schema":1`, 1)
		}},
		{"duplicate", func(s string) string { return strings.Replace(s, `"schema":1`, `"schema":1,"schema":1`, 1) }},
		{"case alias", func(s string) string { return strings.Replace(s, `"schema"`, `"Schema"`, 1) }},
		{"escaped alias", func(s string) string { return strings.Replace(s, `"schema"`, `"\u0073chema"`, 1) }},
		{"missing boolean", func(s string) string { return strings.Replace(s, `,"rollback_compatible":false`, "", 1) }},
		{"null boolean", func(s string) string {
			return strings.Replace(s, `"rollback_compatible":false`, `"rollback_compatible":null`, 1)
		}},
		{"duplicate image", func(s string) string { return strings.Replace(s, `"from":{`, `"from":{"api":"evil",`, 1) }},
		{"nested unknown", func(s string) string { return strings.Replace(s, `"from":{`, `"from":{"url":"evil",`, 1) }},
		{"exponent", func(s string) string { return strings.Replace(s, `"sequence":10`, `"sequence":1e1`, 1) }},
		{"negative sequence", func(s string) string { return strings.Replace(s, `"sequence":10`, `"sequence":-1`, 1) }},
		{"trailing JSON", func(s string) string { return s + `{}` }},
		{"trailing whitespace", func(s string) string { return s + "\n" }},
		{"null", func(string) string { return "null" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, m, state, key := fixture()
			m.RollbackCompatible = false
			data := sign(t, []byte(tc.edit(string(marshal(t, m)))), key)
			if _, err := verifier(t, p).Verify(data, state); !errors.Is(err, rv.ErrManifest) {
				t.Fatalf("expected canonical rejection, got %v", err)
			}
		})
	}
}

func TestVerifyRejectsMalformedEnvelopes(t *testing.T) {
	p, m, state, key := fixture()
	valid := sign(t, marshal(t, m), key)
	for _, data := range [][]byte{
		nil, []byte("null"), []byte(`{}`), append(bytes.Clone(valid), '\n'),
		bytes.Replace(valid, []byte(`"payload"`), []byte(`"Payload"`), 1),
		bytes.Replace(valid, []byte(`"payload":`), []byte(`"url":"https://evil.test","payload":`), 1),
		bytes.Replace(valid, []byte(`"payload":`), []byte(`"payload":"invalid","payload":`), 1),
		bytes.Repeat([]byte("x"), rv.MaxEnvelopeSize+1),
		marshal(t, rv.Envelope{Payload: "!", Signature: "!"}),
		marshal(t, rv.Envelope{Payload: base64.StdEncoding.EncodeToString(marshal(t, m)) + "\n", Signature: "!"}),
	} {
		if _, err := verifier(t, p).Verify(data, state); !errors.Is(err, rv.ErrManifest) {
			t.Fatalf("expected malformed envelope rejection, got %v", err)
		}
	}
}

func TestVerifyRejectsNoncanonicalSignatureEncoding(t *testing.T) {
	p, m, state, key := fixture()
	payload := marshal(t, m)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(rv.SignatureDomain), payload...)))
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	index := strings.IndexByte(alphabet, sig[len(sig)-3])
	badPadding := sig[:len(sig)-3] + string(alphabet[index+1]) + "=="
	for _, encoded := range []string{sig + "\n", strings.TrimRight(sig, "="), badPadding} {
		data := marshal(t, rv.Envelope{Payload: base64.StdEncoding.EncodeToString(payload), Signature: encoded})
		if _, err := verifier(t, p).Verify(data, state); !errors.Is(err, rv.ErrSignature) {
			t.Fatalf("expected signature encoding rejection, got %v", err)
		}
	}
}

func TestNewVerifierRejectsUntrustedPolicyShapes(t *testing.T) {
	for _, mutate := range []func(*rv.Policy){
		func(p *rv.Policy) { p.PublicKey = "" },
		func(p *rv.Policy) { p.PublicKey += "\n" },
		func(p *rv.Policy) { p.PublicKey = base64.StdEncoding.EncodeToString(make([]byte, 32)) },
		func(p *rv.Policy) { p.PublicKey = base64.StdEncoding.EncodeToString(make([]byte, 31)) },
		func(p *rv.Policy) { p.Channel = "" },
		func(p *rv.Policy) { p.MinimumSequence = 0 },
		func(p *rv.Policy) { p.MinimumSequence = rv.MaxSequence + 1 },
		func(p *rv.Policy) { p.APIRepository = p.WebRepository },
		func(p *rv.Policy) { p.APIRepository = "https://ghcr.io/proxima/api" },
		func(p *rv.Policy) { p.APIRepository = "ghcr.io/proxima/../api" },
		func(p *rv.Policy) { p.APIRepository = "ghcr.io/proxima/api:latest" },
		func(p *rv.Policy) { p.WebRepository = "user:pass@ghcr.io/proxima/web" },
		func(p *rv.Policy) { p.WebRepository = "ghcr.io:443/proxima/web" },
		func(p *rv.Policy) { p.WebRepository = "local/web" },
		func(p *rv.Policy) { p.WebRepository = "ghcr.io//web" },
		func(p *rv.Policy) { p.WebRepository = "-bad.io/web" },
		func(p *rv.Policy) { p.WebRepository = "ghcr.io/" + strings.Repeat("a", 256) },
	} {
		p, _, _, _ := fixture()
		mutate(&p)
		if _, err := rv.NewVerifier(p); !errors.Is(err, rv.ErrPolicy) {
			t.Fatalf("accepted policy %+v: %v", p, err)
		}
	}
}

func TestZeroVerifierAndVerifiedFailClosed(t *testing.T) {
	_, m, state, key := fixture()
	if _, err := (rv.Verifier{}).Verify(sign(t, marshal(t, m), key), state); !errors.Is(err, rv.ErrPolicy) {
		t.Fatalf("zero verifier: %v", err)
	}
	if err := (rv.Verified{}).Authorize(rv.Approval{}); !errors.Is(err, rv.ErrApproval) {
		t.Fatalf("zero verified authorized: %v", err)
	}
}

func FuzzVerifyNeverPanics(f *testing.F) {
	p, m, state, key := fixture()
	v, err := rv.NewVerifier(p)
	if err != nil {
		f.Fatal(err)
	}
	payload, err := json.Marshal(m)
	if err != nil {
		f.Fatal(err)
	}
	sig := ed25519.Sign(key, append([]byte(rv.SignatureDomain), payload...))
	valid, err := json.Marshal(rv.Envelope{Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(sig)})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"payload":"","signature":""}`))
	f.Add(bytes.Repeat([]byte("x"), rv.MaxEnvelopeSize+1))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := v.Verify(data, state)
		if err == nil && got.Manifest().Sequence <= state.HighWater {
			t.Fatal("accepted non-monotonic release")
		}
	})
}

func FuzzVerifyAuthenticatedPayload(f *testing.F) {
	p, m, state, key := fixture()
	v, err := rv.NewVerifier(p)
	if err != nil {
		f.Fatal(err)
	}
	payload, err := json.Marshal(m)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(payload)
	f.Add([]byte(`{"schema":1,"schema":1}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > rv.MaxEnvelopeSize {
			return
		}
		got, err := v.Verify(sign(t, payload, key), state)
		if err != nil {
			return
		}
		m := got.Manifest()
		if !bytes.Equal(marshal(t, m), payload) || m.From != state.Current || m.To == m.From ||
			m.Sequence <= state.HighWater || m.Sequence < p.MinimumSequence ||
			m.IssuedAt > state.Now.Unix() || m.ExpiresAt <= state.Now.Unix() {
			t.Fatal("authenticated invalid transition accepted")
		}
	})
}
