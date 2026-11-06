package nodeprov

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestCanonicalHistoryDigestFixtures(t *testing.T) {
	// Given static expected bytes/hashes generated independently with JavaScript
	// JSON.stringify, recursive key sorting and Bun's SHA-256 implementation.
	data, err := os.ReadFile("testdata/connection_history_v1/canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Input        string `json:"input"`
		Canonical    string `json:"canonical"`
		SHA256       string `json:"sha256"`
		EventsSHA256 string `json:"events_sha256"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	// When recursively canonicalizing alternate key order and escape spellings.
	got, err := CanonicalHistoryJSON([]byte(fixture.Input))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(got)
	if string(got) != fixture.Canonical || hex.EncodeToString(sum[:]) != fixture.SHA256 {
		t.Fatalf("canonical mismatch: %s", got)
	}
	e, err := DecodeHistoryEvent(historyFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := HistoryEventsDigest([]HistoryEvent{e})
	if err != nil {
		t.Fatal(err)
	}
	if digest != fixture.EventsSHA256 {
		t.Fatalf("event array digest %s", digest)
	}
	reordered, err := CanonicalHistoryJSON([]byte(fixture.Canonical))
	if err != nil || string(reordered) != fixture.Canonical {
		t.Fatal("canonical output not stable")
	}
}

func TestCanonicalHistoryRejectsAmbiguousJSON(t *testing.T) {
	for _, raw := range []string{
		`{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `{"a":{"b":1,"b":2}}`,
		`{"a":1e0}`, `{"a":1.0}`, `{"a":65536}`, `{"a":-0}`,
		`{"a":"\ud800"}`, `{"a":"\udc00"}`, `{"a":1} {}`,
	} {
		if _, err := CanonicalHistoryJSON([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
