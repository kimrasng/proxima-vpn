package reality

import (
	"fmt"
	"slices"
)

type Status string

const (
	NotApplicable Status = "not_applicable"
	Conflict      Status = "conflict"
	Valid         Status = "valid"
)

type ConflictReason string

const NoCommonName ConflictReason = "no_common_name"

type Listener struct {
	Tag         string
	Enabled     bool
	Protocol    string
	Security    string
	ServerNames []string
}

type Result struct {
	Status     Status
	Reason     ConflictReason
	Candidates []Hostname
	Proposed   Hostname
}

type InvalidListenerNameError struct {
	Tag   string
	Index int
	Value string
	Cause error
}

func (e *InvalidListenerNameError) Error() string {
	return fmt.Sprintf("Reality listener %q server name %d (%q): %v", e.Tag, e.Index, e.Value, e.Cause)
}

func (e *InvalidListenerNameError) Unwrap() error { return e.Cause }

// Intersect uses only published VLESS Reality listeners. It never modifies
// their configured server names; normalization is for comparison alone.
func Intersect(listeners []Listener) (Result, error) {
	var common map[Hostname]struct{}
	effective := 0
	for _, listener := range listeners {
		if !listener.Enabled || listener.Protocol != "vless" || listener.Security != "reality" {
			continue
		}
		effective++
		current := make(map[Hostname]struct{}, len(listener.ServerNames))
		for index, raw := range listener.ServerNames {
			name, err := NormalizeHostname(raw)
			if err != nil {
				return Result{}, &InvalidListenerNameError{Tag: listener.Tag, Index: index, Value: raw, Cause: err}
			}
			current[name] = struct{}{}
		}
		if common == nil {
			common = current
			continue
		}
		for name := range common {
			if _, ok := current[name]; !ok {
				delete(common, name)
			}
		}
	}
	if effective == 0 {
		return Result{Status: NotApplicable}, nil
	}
	if len(common) == 0 {
		return Result{Status: Conflict, Reason: NoCommonName}, nil
	}
	candidates := make([]Hostname, 0, len(common))
	for name := range common {
		candidates = append(candidates, name)
	}
	slices.Sort(candidates)
	return Result{Status: Valid, Candidates: candidates, Proposed: candidates[0]}, nil
}
