package nodeprov

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// CanonicalHistoryJSON sorts object keys, preserves array order, emits minimal
// JSON string escapes and UTF-8 (including U+2028/U+2029), and accepts only the
// nonnegative small integers used by schema_version and ports. U64s stay strings.
// It also rejects duplicate keys and malformed Unicode.
func CanonicalHistoryJSON(data []byte) ([]byte, error) {
	v, err := historyJSON(data)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	if err := historyCanonical(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func historyCanonical(b *strings.Builder, v any) error {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i != 0 {
				b.WriteByte(',')
			}
			historyQuote(b, k)
			b.WriteByte(':')
			if err := historyCanonical(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, item := range x {
			if i != 0 {
				b.WriteByte(',')
			}
			if err := historyCanonical(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case string:
		historyQuote(b, x)
	case json.Number:
		n, err := strconv.ParseUint(string(x), 10, 16)
		if err != nil || strconv.FormatUint(n, 10) != string(x) {
			return historyInvalid("canonical small integer")
		}
		b.WriteString(string(x))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case nil:
		b.WriteString("null")
	default:
		return historyInvalid("canonical value")
	}
	return nil
}

func historyQuote(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte("0123456789abcdef"[r>>4])
				b.WriteByte("0123456789abcdef"[r&15])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func HistoryEventsDigest(events []HistoryEvent) (string, error) {
	if events == nil {
		events = []HistoryEvent{}
	}
	for _, e := range events {
		if _, err := EncodeHistoryEvent(e); err != nil {
			return "", err
		}
	}
	b, err := json.Marshal(events)
	if err != nil {
		return "", err
	}
	b, err = CanonicalHistoryJSON(b)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
