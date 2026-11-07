package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReadinessRejectsFalsePositiveResponses(t *testing.T) {
	for _, tc := range []struct {
		service string
		code    int
		body    string
		ok      bool
	}{
		{"api", 200, `{"status":"ok"}`, true},
		{"api", 200, `{"status":"starting"}`, false},
		{"api", 200, `<html>proxy page</html>`, false},
		{"api", 302, `{"status":"ok"}`, false},
		{"web", 200, "", false},
		{"web", 200, "<html>Proxima</html>", true},
		{"web", 503, "maintenance", false},
		{"web", 200, strings.Repeat("x", 65537), false},
	} {
		h := httpReadiness{&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Hostname() != "127.0.0.1" {
				t.Fatal("external readiness")
			}
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}}
		if err := h.probe(context.Background(), tc.service); (err == nil) != tc.ok {
			t.Fatalf("%s %d accepted=%v", tc.service, tc.code, err == nil)
		}
	}
}

func TestReadinessDoesNotFollowRedirects(t *testing.T) {
	var followed bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			followed = true
			w.WriteHeader(200)
			return
		}
		http.Redirect(w, r, "/redirected", 302)
	}))
	defer s.Close()
	h := newReadiness()
	resp, err := h.client.Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 || followed {
		t.Fatal("readiness followed redirect")
	}
}

func TestUnixHTTPApplySurvivesClientDisconnect(t *testing.T) {
	c, f, a := fixture(t)
	// Real Unix HTTP transport, fake Docker only. t.TempDir() paths exceed the
	// kernel AF_UNIX limit on macOS, so the socket uses a short /tmp path. The
	// fixture cannot chown a root-owned socket on macOS; production listenSocket
	// owns that boundary.
	dir, err := os.MkdirTemp("/tmp", "upd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "control.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(c)
	served := make(chan error, 1)
	go func() { served <- s.Serve(l) }()
	defer func() { s.Close(); <-served }()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
	defer client.CloseIdleConnections()
	resp, err := client.Post("http://localhost/v1/apply", "application/json", strings.NewReader(`{"release_id":"`+a.ID+`","maintenance":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatal(resp.StatusCode)
	}
	state := waitOperation(t, c)
	if state.Operation.Phase != "succeeded" || f.unsafe {
		t.Fatalf("transport operation failed: %+v", state.Operation)
	}
}

func TestCommandExecutorHonorsCancelledDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (commandExecutor{t.TempDir()}).Run(ctx, "/usr/bin/false")
	if err != errUncertain {
		t.Fatalf("deadline treated as certain: %v", err)
	}
}
