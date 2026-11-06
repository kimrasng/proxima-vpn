package nodeprov

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrHistoryInvalid = errors.New("invalid connection history")

type HistoryU64 uint64
type HistoryUUID string
type HistoryTime string

const historyTimeFormat = "2006-01-02T15:04:05.000000000Z"

func (v HistoryU64) String() string { return strconv.FormatUint(uint64(v), 10) }
func (v HistoryU64) MarshalJSON() ([]byte, error) {
	return []byte(`"` + v.String() + `"`), nil
}
func (v *HistoryU64) UnmarshalJSON(data []byte) error {
	if len(data) < 3 || data[0] != '"' || data[len(data)-1] != '"' {
		return fmt.Errorf("%w: U64 must be a decimal string", ErrHistoryInvalid)
	}
	s := string(data[1 : len(data)-1])
	if len(s) > 1 && s[0] == '0' {
		return fmt.Errorf("%w: U64 leading zero", ErrHistoryInvalid)
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return fmt.Errorf("%w: U64 digit", ErrHistoryInvalid)
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: U64 range", ErrHistoryInvalid)
	}
	*v = HistoryU64(n)
	return nil
}

func (v HistoryUUID) Validate() error {
	s := string(v)
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return fmt.Errorf("%w: UUID", ErrHistoryInvalid)
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return fmt.Errorf("%w: UUID separators", ErrHistoryInvalid)
			}
		default:
			if !strings.ContainsRune("0123456789abcdef", c) {
				return fmt.Errorf("%w: UUID lowercase hex", ErrHistoryInvalid)
			}
		}
	}
	return nil
}

func NewHistoryUUID() (HistoryUUID, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("history UUID randomness: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return HistoryUUID(h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]), nil
}

func HistoryTimestamp(t time.Time) HistoryTime {
	return HistoryTime(t.UTC().Format(historyTimeFormat))
}
func (v HistoryTime) Validate() error {
	t, err := time.Parse(historyTimeFormat, string(v))
	if err != nil || t.Format(historyTimeFormat) != string(v) {
		return fmt.Errorf("%w: UTC nanosecond time", ErrHistoryInvalid)
	}
	return nil
}

func (v *HistoryUUID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("%w: UUID string", ErrHistoryInvalid)
	}
	u := HistoryUUID(s)
	if err := u.Validate(); err != nil {
		return err
	}
	*v = u
	return nil
}
func (v *HistoryTime) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("%w: time string", ErrHistoryInvalid)
	}
	t := HistoryTime(s)
	if err := t.Validate(); err != nil {
		return err
	}
	*v = t
	return nil
}
