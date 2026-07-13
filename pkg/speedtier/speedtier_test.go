package speedtier

import "testing"

func TestVlessPort(t *testing.T) {
	if got := VlessPort(443, 0); got != 443 {
		t.Errorf("unlimited should use node port 443, got %d", got)
	}
	if got := VlessPort(443, -5); got != 443 {
		t.Errorf("negative limit should use node port 443, got %d", got)
	}
	if got := VlessPort(443, 50); got != PortBase+50 {
		t.Errorf("50 Mbps should map to %d, got %d", PortBase+50, got)
	}
	if got := VlessPort(443, 100000); got != PortBase+MaxMbps {
		t.Errorf("oversized limit should clamp to %d, got %d", PortBase+MaxMbps, got)
	}
}

func TestTagAndParseRoundTrip(t *testing.T) {
	if got := Tag(0); got != TagUnlimited {
		t.Errorf("Tag(0) = %q, want %q", got, TagUnlimited)
	}

	tag := Tag(50)
	mbps, ok := ParseLimitTag(tag)
	if !ok || mbps != 50 {
		t.Errorf("ParseLimitTag(%q) = (%d, %v), want (50, true)", tag, mbps, ok)
	}

	if _, ok := ParseLimitTag(TagUnlimited); ok {
		t.Error("ParseLimitTag should return ok=false for the unlimited tag")
	}
	if _, ok := ParseLimitTag("api"); ok {
		t.Error("ParseLimitTag should return ok=false for unrelated tags")
	}
}
