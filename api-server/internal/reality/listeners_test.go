package reality

import (
	"errors"
	"reflect"
	"testing"
)

func TestPHS028Intersection_whenEffectiveSetsOverlap(t *testing.T) {
	// Given
	inbounds := []Listener{
		{Tag: "primary", Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"Z.example", "A.example.", "z.EXAMPLE"}},
		{Tag: "tier", Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"z.example", "a.EXAMPLE", "a.example"}},
		{Tag: "disabled", Protocol: "vless", Security: "reality", ServerNames: []string{"different.example"}},
		{Tag: "tls", Enabled: true, Protocol: "vless", Security: "tls", ServerNames: []string{"different.example"}},
	}
	// When
	got, err := Intersect(inbounds)
	// Then
	if err != nil || got.Status != Valid || !reflect.DeepEqual(got.Candidates, []Hostname{"a.example", "z.example"}) || got.Proposed.String() != "a.example" {
		t.Fatalf("Intersect = %+v, %v", got, err)
	}
}

func TestPHS028Intersection_whenNoEffectiveListenerOrNoCommonName(t *testing.T) {
	// Given / When / Then
	empty, err := Intersect(nil)
	if err != nil || empty.Status != NotApplicable || len(empty.Candidates) != 0 || empty.Proposed.String() != "" {
		t.Fatalf("empty = %+v, %v", empty, err)
	}
	conflict, err := Intersect([]Listener{
		{Tag: "one", Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"one.example"}},
		{Tag: "two", Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"two.example"}},
	})
	if err != nil || conflict.Status != Conflict || conflict.Reason != NoCommonName || conflict.Proposed.String() != "" {
		t.Fatalf("conflict = %+v, %v", conflict, err)
	}
}

func TestPHS028Intersection_whenConfiguredNameIsInvalid(t *testing.T) {
	// Given
	inbounds := []Listener{{Tag: "broken", Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"good.example", "https://wrong.example"}}}
	// When
	_, err := Intersect(inbounds)
	// Then
	var diagnostic *InvalidListenerNameError
	if !errors.As(err, &diagnostic) || diagnostic.Tag != "broken" || diagnostic.Index != 1 || !errors.Is(err, ErrInvalidHostname) {
		t.Fatalf("diagnostic = %v", err)
	}
}

func TestPHS028Intersection_whenEffectiveListenerHasNoNames(t *testing.T) {
	// Given
	listeners := []Listener{{Tag: "empty", Enabled: true, Protocol: "vless", Security: "reality"}}
	// When
	result, err := Intersect(listeners)
	// Then
	if err != nil || result.Status != Conflict || result.Reason != NoCommonName || result.Proposed.String() != "" {
		t.Fatalf("empty names result = %+v, %v", result, err)
	}
}

func TestPHS028Intersection_whenExcludedListenerHasInvalidName(t *testing.T) {
	// Given
	listeners := []Listener{{Tag: "disabled", Protocol: "vless", Security: "reality", ServerNames: []string{"invalid/name"}}, {Tag: "tls", Enabled: true, Protocol: "vless", Security: "tls", ServerNames: []string{"also/invalid"}}}
	// When
	result, err := Intersect(listeners)
	// Then
	if err != nil || result.Status != NotApplicable {
		t.Fatalf("excluded result = %+v, %v", result, err)
	}
}
