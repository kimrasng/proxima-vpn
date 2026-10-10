package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudflareDNSError_whenUpstreamFails(t *testing.T) {
	// Given
	tests := []struct {
		name, body, retry string
		status            int
		kind              CloudflareDNSErrorKind
		code              int
		wait              time.Duration
	}{
		{"unauthorized", `{"success":false,"errors":[{"code":10000,"message":"secret"}],"messages":[],"result":null}`, "", 401, CloudflareDNSDurable, 10000, 0},
		{"forbidden", `{"success":false,"errors":[{"code":9109,"message":"secret"}]}`, "", 403, CloudflareDNSDurable, 9109, 0},
		{"rate limit", `{"success":false,"errors":[{"code":1015,"message":"secret"}]}`, "12", 429, CloudflareDNSTransient, 1015, 12 * time.Second},
		{"timeout", `{"success":false,"errors":[]}`, "", 408, CloudflareDNSTransient, 0, 0},
		{"zero retry", `{"success":false,"errors":[]}`, "0", 429, CloudflareDNSTransient, 0, 0},
		{"server", `{"success":false,"errors":[{"code":1,"message":"secret"}]}`, "", 503, CloudflareDNSTransient, 1, 0},
		{"validation", `{"success":false,"errors":[{"code":1004,"message":"secret"}]}`, "", 400, CloudflareDNSDurable, 1004, 0},
		{"logical failure", `{"success":false,"errors":[{"code":42,"message":"secret"}]}`, "", 200, CloudflareDNSDurable, 42, 0},
		{"malformed", `{"success":"secret"}`, "", 200, CloudflareDNSTransient, 0, 0},
		{"missing success", `{"result":[]}`, "", 200, CloudflareDNSTransient, 0, 0},
		{"oversized", strings.Repeat("secret", cloudflareDNSMaxBody/6+1), "", 200, CloudflareDNSTransient, 0, 0},
		{"negative retry", `{"success":false,"errors":[]}`, "-2", 429, CloudflareDNSTransient, 0, 0},
		{"date retry", `{"success":false,"errors":[]}`, "Wed, 21 Oct 2015 07:28:00 GMT", 429, CloudflareDNSTransient, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", tt.retry)
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			c := NewCloudflareDNSClient("token-secret")
			c.baseURL = srv.URL
			c.httpClient = srv.Client()

			// When
			_, err := c.ListExact(context.Background(), "zone", "host.example.com")

			// Then
			var dnsErr *CloudflareDNSError
			if !errors.As(err, &dnsErr) {
				t.Fatalf("want typed error, got %v", err)
			}
			if dnsErr.Operation != "list" || dnsErr.Status != tt.status || dnsErr.Code != tt.code || dnsErr.Kind != tt.kind {
				t.Errorf("wrong classification: %+v", dnsErr)
			}
			if (dnsErr.RetryAfter != nil && *dnsErr.RetryAfter != tt.wait) || ((tt.retry == "12" || tt.retry == "0") && dnsErr.RetryAfter == nil) {
				t.Errorf("retry delay = %v, want %v", dnsErr.RetryAfter, tt.wait)
			}
			if strings.Contains(fmt.Sprintf("%+v", dnsErr), "secret") || strings.Contains(err.Error(), "token-secret") {
				t.Errorf("error leaked provider data: %v", err)
			}
		})
	}
}

func TestCloudflareDNSRejectsRedirect_whenUpstreamRedirects(t *testing.T) {
	// Given
	var followed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			followed = true
			return
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	defer srv.Close()
	c := NewCloudflareDNSClient("secret")
	c.baseURL = srv.URL
	c.httpClient = srv.Client()

	// When
	_, err := c.ListExact(context.Background(), "zone", "host.example.com")

	// Then
	var dnsErr *CloudflareDNSError
	if !errors.As(err, &dnsErr) || followed || dnsErr.Status != http.StatusFound {
		t.Fatalf("redirect followed or not rejected: %v followed=%t", err, followed)
	}
}

func TestCloudflareDNSCancellation_whenContextCanceled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewCloudflareDNSClient("secret")

	// When
	_, err := c.ListExact(ctx, "zone", "host.example.com")

	// Then
	var dnsErr *CloudflareDNSError
	if !errors.As(err, &dnsErr) || dnsErr.Kind != CloudflareDNSTransient || strings.Contains(err.Error(), "secret") {
		t.Fatalf("cancellation classification: %v", err)
	}
}

func TestCloudflareDNSCancellation_whenRequestInFlight(t *testing.T) {
	// Given
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := NewCloudflareDNSClient("secret")
	c.baseURL = srv.URL
	c.httpClient = srv.Client()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := c.ListExact(ctx, "zone", "host.example.com")
		result <- err
	}()
	<-started

	// When
	cancel()

	// Then
	select {
	case err := <-result:
		var dnsErr *CloudflareDNSError
		if !errors.As(err, &dnsErr) || dnsErr.Kind != CloudflareDNSTransient {
			t.Fatalf("in-flight cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request ignored context cancellation")
	}
}
