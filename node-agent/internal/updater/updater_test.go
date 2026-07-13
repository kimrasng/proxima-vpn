package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckUpdateNoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	u := NewUpdater("v1.0.0", "/tmp/agent", srv.URL, "node-1", "key")
	version, available, err := u.CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate error: %v", err)
	}
	if available {
		t.Errorf("expected no update available, got version %q", version)
	}
}

func TestCheckUpdateAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Node-Key"); got != "key" {
			t.Errorf("expected X-Node-Key header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"target_version":"v2.0.0","download_url":"http://x/dl"}`))
	}))
	defer srv.Close()

	u := NewUpdater("v1.0.0", "/tmp/agent", srv.URL, "node-1", "key")
	version, available, err := u.CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate error: %v", err)
	}
	if !available || version != "v2.0.0" {
		t.Errorf("expected v2.0.0 available, got version=%q available=%v", version, available)
	}
}

func TestCheckUpdateSameVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"target_version":"v1.0.0"}`))
	}))
	defer srv.Close()

	u := NewUpdater("v1.0.0", "/tmp/agent", srv.URL, "node-1", "key")
	_, available, err := u.CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate error: %v", err)
	}
	if available {
		t.Error("expected no update when target equals current version")
	}
}

func TestPerformUpdateReplacesBinary(t *testing.T) {
	newBinary := []byte("#!/bin/sh\necho updated\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(newBinary)
	}))
	defer srv.Close()

	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "node-agent")
	if err := os.WriteFile(binaryPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("seed binary: %v", err)
	}

	u := NewUpdater("v1.0.0", binaryPath, srv.URL, "node-1", "key")
	if err := u.PerformUpdate(context.Background(), "v2.0.0"); err != nil {
		t.Fatalf("PerformUpdate error: %v", err)
	}

	got, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read updated binary: %v", err)
	}
	if string(got) != string(newBinary) {
		t.Errorf("binary not replaced, got %q", string(got))
	}
}
