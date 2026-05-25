package crypto

import (
	"regexp"
	"testing"
)

func TestNewUUID(t *testing.T) {
	id := NewUUID()
	uuidRegex := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRegex.MatchString(id) {
		t.Errorf("NewUUID() = %q, not a valid UUID v4", id)
	}
}

func TestNewUUID_Unique(t *testing.T) {
	a := NewUUID()
	b := NewUUID()
	if a == b {
		t.Error("NewUUID() generated duplicate UUIDs")
	}
}
