package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLegacyAPIWithoutReadyCannotCertifyRollback(t *testing.T) {
	c, _, a := fixture(t)
	h := httpReadiness{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/ready" {
			t.Error("readiness used a legacy liveness fallback")
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"error":"not_found"}`)), Header: make(http.Header)}, nil
	})}}
	// Exercise the real probe without waiting for the 60-second polling budget.
	c.readiness = readyFunc(func(ctx context.Context, service string) error {
		return h.probe(ctx, service)
	})
	if _, err := c.accept(a.ID); err != nil {
		t.Fatal(err)
	}
	s := waitOperation(t, c)
	if s.Operation.Phase != "manual_recovery" || s.Operation.Error != "rollback_failed" || s.CurrentKnown || s.HighWater != 2 {
		t.Fatalf("legacy readiness incorrectly certified rollback: %+v", s)
	}
}
