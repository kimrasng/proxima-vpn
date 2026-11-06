package nodeprov

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"unicode/utf8"
)

// historyJSON checks duplicates before decoding (encoding/json otherwise accepts
// the last value), rejects malformed Unicode, and retains number spelling.
func historyJSON(data []byte) (any, error) {
	if !utf8.Valid(data) || !json.Valid(data) {
		return nil, fmt.Errorf("%w: JSON encoding", ErrHistoryInvalid)
	}
	// encoding/json replaces unpaired UTF-16 surrogates. Do not permit that
	// lossy conversion at a hashing/identity boundary.
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		n, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return nil, fmt.Errorf("%w: Unicode escape", ErrHistoryInvalid)
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return nil, fmt.Errorf("%w: lone low surrogate", ErrHistoryInvalid)
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(data) || string(data[i+1:i+3]) != `\u` {
				return nil, fmt.Errorf("%w: lone high surrogate", ErrHistoryInvalid)
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return nil, fmt.Errorf("%w: surrogate pair", ErrHistoryInvalid)
			}
			i += 6
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return historyValue(d, 0)
}

func historyValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("%w: nesting depth", ErrHistoryInvalid)
	}
	t, err := d.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: JSON token", ErrHistoryInvalid)
	}
	switch t {
	case json.Delim('{'):
		m := make(map[string]any)
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, fmt.Errorf("%w: JSON key", ErrHistoryInvalid)
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("%w: object key", ErrHistoryInvalid)
			}
			if _, exists := m[key]; exists {
				return nil, fmt.Errorf("%w: duplicate member", ErrHistoryInvalid)
			}
			m[key], err = historyValue(d, depth+1)
			if err != nil {
				return nil, err
			}
		}
		if _, err := d.Token(); err != nil {
			return nil, fmt.Errorf("%w: object end", ErrHistoryInvalid)
		}
		return m, nil
	case json.Delim('['):
		a := make([]any, 0)
		for d.More() {
			v, err := historyValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		if _, err := d.Token(); err != nil {
			return nil, fmt.Errorf("%w: array end", ErrHistoryInvalid)
		}
		return a, nil
	default:
		return t, nil
	}
}

// Every member is required. Reflection is restricted to the wire boundary and
// avoids a second, independently maintained schema for presence/null checks.
func historyShape(v any, typ reflect.Type) error {
	if typ.Kind() == reflect.Pointer {
		if v == nil {
			return nil
		}
		return historyShape(v, typ.Elem())
	}
	if v == nil {
		return fmt.Errorf("%w: nonnullable member", ErrHistoryInvalid)
	}
	switch typ.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok || len(m) != typ.NumField() {
			return fmt.Errorf("%w: exact object members required", ErrHistoryInvalid)
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			value, exists := m[f.Tag.Get("json")]
			if !exists {
				return fmt.Errorf("%w: missing %s", ErrHistoryInvalid, f.Tag.Get("json"))
			}
			if err := historyShape(value, f.Type); err != nil {
				return err
			}
		}
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%w: array required", ErrHistoryInvalid)
		}
		for _, item := range a {
			if err := historyShape(item, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeHistory(data []byte, dst any) error {
	v, err := historyJSON(data)
	if err != nil {
		return err
	}
	if err := historyShape(v, reflect.TypeOf(dst).Elem()); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("%w: member type", ErrHistoryInvalid)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON", ErrHistoryInvalid)
	}
	return nil
}

func DecodeHistoryEvent(line []byte) (HistoryEvent, error) {
	var e HistoryEvent
	if len(line) > HistoryMaxEventLine || len(line) == 0 || line[len(line)-1] != '\n' ||
		bytes.ContainsAny(line[:len(line)-1], "\r\n") {
		return e, fmt.Errorf("%w: JSONL framing", ErrHistoryInvalid)
	}
	if err := decodeHistory(line, &e); err != nil {
		return e, err
	}
	return e, e.Validate()
}

// UnmarshalJSON also protects embedded events in API batch bodies. Calling
// encoding/json directly must not silently bypass duplicate/presence checks.
func (e *HistoryEvent) UnmarshalJSON(data []byte) error {
	type wireEvent HistoryEvent
	var wire wireEvent
	if len(data)+1 > HistoryMaxEventLine {
		return historyInvalid("event size")
	}
	if err := decodeHistory(data, &wire); err != nil {
		return err
	}
	event := HistoryEvent(wire)
	if err := event.Validate(); err != nil {
		return err
	}
	*e = event
	return nil
}

func EncodeHistoryEvent(e HistoryEvent) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("encode history: %w", err)
	}
	if len(b)+1 > HistoryMaxEventLine {
		return nil, fmt.Errorf("%w: event size", ErrHistoryInvalid)
	}
	return append(b, '\n'), nil
}
