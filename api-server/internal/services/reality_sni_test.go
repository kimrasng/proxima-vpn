package services

import (
	"errors"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
)

func TestPHS028ProspectiveRejectsNonCommonAndInvalidListeners(t *testing.T) {
	// Given effective listeners with either no intersection or a malformed name.
	for _, tc := range []struct {
		name      string
		listeners []reality.Listener
		kind      RealitySNIErrorKind
	}{
		{"absent", nil, RealitySNINoListener},
		{"disjoint", []reality.Listener{{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"one.example.test"}}, {Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"two.example.test"}}}, RealitySNIListenerConflict},
		{"invalid", []reality.Listener{{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"bad name"}}}, RealitySNIInvalidListener},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// When the proposed collection is intersected.
			_, err := evaluateProspectiveSNI(ProspectiveRealityListeners{Listeners: tc.listeners}, nil, "")
			// Then the precise semantic failure is returned.
			var semantic *RealitySNIError
			if !errors.As(err, &semantic) || semantic.Kind != tc.kind {
				t.Fatalf("error = %v, want %s", err, tc.kind)
			}
		})
	}
}

func TestPHS028ProspectivePreservesCanonicalAndRepairsAbsentConflict(t *testing.T) {
	// Given an effective listener accepting one common name.
	proposed := ProspectiveRealityListeners{Listeners: []reality.Listener{{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"B.example.test", "a.example.test"}}}}
	canonical := "B.Example.Test."
	// When a canonical value exists, or a previous conflict has no canonical.
	kept, err := evaluateProspectiveSNI(proposed, &canonical, "admin")
	if err != nil {
		t.Fatal(err)
	}
	chosen, err := evaluateProspectiveSNI(proposed, nil, "backfill")
	// Then the accepted value is retained and repair chooses the deterministic first candidate.
	if err != nil || kept.name != canonical || kept.source != "admin" || chosen.name != "a.example.test" || chosen.source != "backfill" {
		t.Fatalf("kept=%+v chosen=%+v err=%v", kept, chosen, err)
	}
}

func TestPHS028ProspectiveRejectsCanonicalMismatchAndMalformedInput(t *testing.T) {
	// Given a proposed listener with only one valid name.
	proposed := ProspectiveRealityListeners{Listeners: []reality.Listener{{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"one.example.test"}}}}
	for _, tc := range []struct {
		name, canonical string
		kind            RealitySNIErrorKind
	}{{"mismatch", "other.example.test", RealitySNIMismatch}, {"malformed", "bad name", RealitySNIMalformed}} {
		t.Run(tc.name, func(t *testing.T) {
			// When validating a stored canonical value against that listener.
			_, err := evaluateProspectiveSNI(proposed, &tc.canonical, "admin")
			// Then it remains a typed semantic failure.
			var semantic *RealitySNIError
			if !errors.As(err, &semantic) || semantic.Kind != tc.kind {
				t.Fatalf("error = %v, want %s", err, tc.kind)
			}
		})
	}
}
