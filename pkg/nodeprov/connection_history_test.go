package nodeprov

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func historyFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/connection_history_v1/flow_end.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestHistoryStrictEventFixtures(t *testing.T) {
	// Given a full conforming snapshot and independently specified corruptions.
	valid := historyFixture(t)
	e, err := DecodeHistoryEvent(valid)
	if err != nil {
		t.Fatal(err)
	}
	if *e.Bytes.Upload != 19 || *e.Bytes.Download != 7 || !e.Bytes.Final {
		t.Fatal("wrong final snapshot")
	}
	data, err := os.ReadFile("testdata/connection_history_v1/invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Old, New string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			if !bytes.Contains(valid, []byte(tt.Old)) {
				t.Fatal("fixture mutation did not match")
			}
			// When a malformed frame crosses the only decode boundary.
			_, err := DecodeHistoryEvent(bytes.Replace(valid, []byte(tt.Old), []byte(tt.New), 1))
			// Then it is rejected, including duplicate, unknown and absent members.
			if !errors.Is(err, ErrHistoryInvalid) {
				t.Fatalf("accepted corruption: %v", err)
			}
			var embedded HistoryEvent
			err = json.Unmarshal(bytes.Replace(valid, []byte(tt.Old), []byte(tt.New), 1), &embedded)
			if !errors.Is(err, ErrHistoryInvalid) {
				t.Fatalf("embedded event bypassed strictness: %v", err)
			}
		})
	}
}

func TestHistoryRejectsFramingAndEncoding(t *testing.T) {
	valid := historyFixture(t)
	for name, line := range map[string][]byte{
		"missing LF":     bytes.TrimSuffix(valid, []byte("\n")),
		"BOM":            append([]byte{0xef, 0xbb, 0xbf}, valid...),
		"two objects":    append(append([]byte{}, valid...), valid...),
		"pretty newline": bytes.Replace(valid, []byte(`,"event_id"`), []byte(",\n\"event_id\""), 1),
		"invalid UTF8":   bytes.Replace(valid, []byte("example.test"), []byte{0xff}, 1),
		"oversize":       append([]byte(strings.Repeat(" ", HistoryMaxEventLine)), valid...),
		"NaN":            bytes.Replace(valid, []byte(`"upload":"19"`), []byte(`"upload":NaN`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeHistoryEvent(line)
			if !errors.Is(err, ErrHistoryInvalid) {
				t.Fatalf("accepted invalid frame: %v", err)
			}
		})
	}
}

func TestHistoryU64RangeAndCanonicalSpelling(t *testing.T) {
	for _, raw := range []string{`"0"`, `"18446744073709551615"`} {
		var n HistoryU64
		if err := json.Unmarshal([]byte(raw), &n); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(n)
		if err != nil || string(got) != raw {
			t.Fatalf("%s: %s %v", raw, got, err)
		}
	}
	for _, raw := range []string{`""`, `"-1"`, `"01"`, `"1e1"`, `" 1"`, `1`, `null`, `"18446744073709551616"`, `"\u0031"`} {
		var n HistoryU64
		if err := json.Unmarshal([]byte(raw), &n); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestHistoryControlStrictness(t *testing.T) {
	valid := `{"op":"apply","config_revision":"7","collection_epoch":"22222222-2222-4222-8222-222222222222","enabled":true,"bindings":[]}` + "\n"
	if _, err := DecodeHistoryControl([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		strings.Replace(valid, `"bindings":[]`, `"bindings":null`, 1),
		strings.Replace(valid, `"enabled":true`, `"enabled":null`, 1),
		strings.Replace(valid, `"enabled":true`, `"enabled":true,"enabled":true`, 1),
		strings.Replace(valid, `"enabled":true`, `"Enabled":true`, 1),
		strings.Replace(valid, `"config_revision":"7"`, `"config_revision":7`, 1),
		strings.Replace(valid, `"bindings":[]`, `"bindings":[],"password":"x"`, 1),
	} {
		if _, err := DecodeHistoryControl([]byte(line)); err == nil {
			t.Fatalf("accepted %s", line)
		}
	}
}
