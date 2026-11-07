package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

// Exercise real HTTP response/redirect parsing and body limits, routing only
// the test transport to a TLS server. Production never accepts a custom URL.
func releaseTestClient(t *testing.T, h http.Handler) *http.Client {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	c := newReleaseHTTPClient()
	tr := c.Transport.(*http.Transport)
	tr.TLSClientConfig.RootCAs = x509.NewCertPool()
	tr.TLSClientConfig.RootCAs.AddCert(s.Certificate())
	tr.TLSClientConfig.ServerName = "example.com"
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
	}
	t.Cleanup(c.CloseIdleConnections)
	return c
}

func TestFetchReleaseRedirectBoundary(t *testing.T) {
	const asset = "https://release-assets.githubusercontent.com/github-production-release-asset/123/abc-def?sig=secret"
	for _, tc := range []struct {
		name, location string
		second, ok     bool
	}{
		{"direct", "", false, true},
		{"cdn", asset, false, true},
		{"second redirect", asset, true, false},
		{"other repo", "https://github.com/attacker/repo/releases/download/v1/release-envelope.json", false, false},
		{"suffix host", "https://release-assets.githubusercontent.com.attacker.test/github-production-release-asset/123/abc", false, false},
		{"downgrade", "http://release-assets.githubusercontent.com/github-production-release-asset/123/abc", false, false},
		{"port", "https://release-assets.githubusercontent.com:443/github-production-release-asset/123/abc", false, false},
		{"credentials", "https://user:secret@release-assets.githubusercontent.com/github-production-release-asset/123/abc", false, false},
		{"fragment", asset + "#x", false, false},
		{"encoded path", "https://release-assets.githubusercontent.com/github-production-release-asset/123/%61bc", false, false},
		{"traversal", "https://release-assets.githubusercontent.com/github-production-release-asset/123/../abc", false, false},
		{"wrong asset path", "https://release-assets.githubusercontent.com/other/123/abc", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			client := releaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
					t.Error("credentials or referrer sent")
				}
				if n == 1 && (r.Host != "github.com" || r.URL.Path != "/kimrasng/proxima-vpn/releases/download/v1.2.3/release-envelope.json" || r.URL.RawQuery != "") {
					t.Error("request escaped fixed repository/asset")
				}
				if tc.location != "" && (n == 1 || tc.second) {
					w.Header().Set("Location", tc.location)
					w.WriteHeader(http.StatusFound)
					return
				}
				fmt.Fprint(w, "envelope bytes")
			}))
			b, err := fetchRelease(context.Background(), client, "v1.2.3")
			wantCalls := int32(1)
			if tc.name == "cdn" || tc.second {
				wantCalls = 2
			}
			if calls.Load() != wantCalls {
				t.Fatalf("unauthorized request issued: %d", calls.Load())
			}
			if tc.ok {
				if err != nil || string(b) != "envelope bytes" {
					t.Fatalf("download: %q %v", b, err)
				}
			} else if !errors.Is(err, errReleaseDownload) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("redirect accepted or leaked: %v", err)
			}
		})
	}
}

func TestFetchReleaseResponseBounds(t *testing.T) {
	for _, kind := range []string{"limit", "overflow", "chunked overflow", "encoding", "partial", "headers", "cancelled", "bad tag"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			client := releaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				size := releaseverify.MaxEnvelopeSize
				switch kind {
				case "overflow", "chunked overflow":
					size++
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
				case "partial":
					w.WriteHeader(http.StatusPartialContent)
				case "headers":
					w.Header().Set("X-Large", strings.Repeat("x", 32*1024))
				}
				if kind == "chunked overflow" {
					w.(http.Flusher).Flush()
				}
				fmt.Fprint(w, strings.Repeat("x", size))
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			tag := "v1"
			if kind == "bad tag" {
				tag = "v1/../../elsewhere"
			}
			data, err := fetchRelease(ctx, client, tag)
			if kind == "limit" {
				if err != nil || len(data) != releaseverify.MaxEnvelopeSize {
					t.Fatalf("limit rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("unbounded or invalid response accepted")
			}
			if (kind == "cancelled" || kind == "bad tag") && calls.Load() != 0 {
				t.Fatal("invalid request reached network")
			}
		})
	}
}
