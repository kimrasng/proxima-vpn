package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

type serverReleaseTransport func(*http.Request) (*http.Response, error)

func (f serverReleaseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func serverReleaseRequest(t *testing.T, client *http.Client, method, query, role string) (int, map[string]any) {
	t.Helper()
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("admin_role", role)
		return c.Next()
	})
	app.All("/release", NewServerReleaseHandler(client).Latest)
	resp, err := app.Test(httptest.NewRequest(method, "/release?"+query, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func serverReleaseFixture() map[string]any {
	return map[string]any{
		"tag_name": "v1.2.3", "name": "Release 1.2.3", "published_at": "2026-01-02T03:04:05Z",
		"html_url": "https://github.com/proxima/server/releases/tag/v1.2.3",
		"draft":    false, "prerelease": false, "body": "not returned", "assets": []any{},
	}
}

func TestServerReleaseRejectsInputBeforeHTTP(t *testing.T) {
	cases := []struct {
		query, role, method string
		status              int
	}{
		{"owner=proxima&repo=server", "", "GET", 403},
		{"owner=proxima&repo=server", "admin", "GET", 403},
		{"owner=proxima&repo=server", "owner", "POST", 405},
	}
	for _, query := range []string{
		"", "owner=proxima", "repo=server", "owner=&repo=server",
		"owner=https://evil.test&repo=server", "owner=proxima&repo=https://evil.test",
		"owner=a%2Fb&repo=server", "owner=proxima&repo=..", "owner=proxima&repo=.",
		"owner=-abc&repo=server", "owner=abc-&repo=server", "owner=a--b&repo=server",
		"owner=a_b&repo=server", "owner=%ED%95%9C&repo=server", "owner=a%00&repo=server",
		"owner=a&owner=b&repo=server", "owner=a&repo=b&repo=c", "owner=a&repo=a%3Fb",
		"owner=" + strings.Repeat("a", 40) + "&repo=b", "owner=a&repo=" + strings.Repeat("b", 101),
	} {
		cases = append(cases, struct {
			query, role, method string
			status              int
		}{query, "owner", "GET", 400})
	}
	for _, tc := range cases {
		t.Run(tc.method+tc.role+tc.query, func(t *testing.T) {
			var calls atomic.Int32
			client := &http.Client{Transport: serverReleaseTransport(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, errors.New("unexpected HTTP call")
			})}
			status, _ := serverReleaseRequest(t, client, tc.method, tc.query, tc.role)
			if status != tc.status || calls.Load() != 0 {
				t.Fatalf("status=%d calls=%d; want %d and zero calls", status, calls.Load(), tc.status)
			}
		})
	}
}

// The injected transport routes the fixed GitHub request to a real TLS test
// server without making the production handler accept configurable URLs.
func serverReleaseHTTPClient(t *testing.T, handler http.Handler) *http.Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	transport := client.Transport
	client.Transport = serverReleaseTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.github.com/repos/proxima/server/releases/latest" ||
			r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL)
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > serverReleaseTimeout {
			t.Error("upstream request has no bounded deadline")
		}
		clone := r.Clone(r.Context())
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return transport.RoundTrip(clone)
	})
	return client
}

func TestServerReleaseLatestHTTP(t *testing.T) {
	client := serverReleaseHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") == "" {
			t.Error("missing GitHub request headers")
		}
		if err := json.NewEncoder(w).Encode(serverReleaseFixture()); err != nil {
			t.Error(err)
		}
	}))
	status, body := serverReleaseRequest(t, client, "GET", "owner=proxima&repo=server", "owner")
	if status != 200 || len(body) != 4 || body["tag"] != "v1.2.3" ||
		body["name"] != "Release 1.2.3" || body["published_at"] != "2026-01-02T03:04:05Z" ||
		body["html_url"] != "https://github.com/proxima/server/releases/tag/v1.2.3" {
		t.Fatalf("unexpected result: %d %#v", status, body)
	}
}

func TestServerReleaseUpstreamStatusAndRedirects(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308, 401, 403, 404, 429, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var calls, redirects atomic.Int32
			client := serverReleaseHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "https://evil.test/private")
				w.WriteHeader(code)
				_, _ = io.WriteString(w, "upstream-secret")
			}))
			client.CheckRedirect = func(*http.Request, []*http.Request) error { redirects.Add(1); return nil }
			status, body := serverReleaseRequest(t, client, "GET", "owner=proxima&repo=server", "owner")
			want := 502
			if code == 404 {
				want = 404
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if status != want || calls.Load() != 1 || redirects.Load() != 0 || strings.Contains(string(encoded), "upstream-secret") {
				t.Fatalf("status=%d body=%s calls=%d redirects=%d", status, encoded, calls.Load(), redirects.Load())
			}
			if client.CheckRedirect == nil {
				t.Fatal("caller client mutated")
			}
		})
	}
}

func TestServerReleaseInvalidResponse(t *testing.T) {
	cases := map[string]string{
		"malformed": "{", "null": "null", "trailing": `{} {}`, "oversize": strings.Repeat("x", serverReleaseMaxBody+1),
		"invalid UTF8": string([]byte{0xff}),
	}
	for key, values := range map[string][]any{
		"tag_name":     {"", "  ", "bad\n", strings.Repeat("t", 257), 42},
		"name":         {"bad\n", strings.Repeat("n", 1025)},
		"published_at": {nil, "bad", "0001-01-01T00:00:00Z"},
		"draft":        {true}, "prerelease": {true},
		"html_url": {"http://github.com/proxima/server/releases/tag/v1.2.3", "https://evil.test/proxima/server/releases/tag/v1.2.3",
			"https://github.com/other/server/releases/tag/v1.2.3", "https://github.com/proxima/server/releases/tag/wrong",
			"https://u:p@github.com/proxima/server/releases/tag/v1.2.3", "https://github.com:443/proxima/server/releases/tag/v1.2.3",
			"https://github.com/proxima/server/releases/tag/v1.2.3?secret=1", "https://github.com/proxima/server/releases/tag/v1.2.3#frag", ":bad"},
	} {
		for i, value := range values {
			fixture := serverReleaseFixture()
			fixture[key] = value
			data, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			cases[key+string(rune('A'+i))] = string(data)
		}
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			client := serverReleaseHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, payload)
			}))
			status, body := serverReleaseRequest(t, client, "GET", "owner=proxima&repo=server", "owner")
			if status != 502 || body["error"] != "invalid_release_response" {
				t.Fatalf("status=%d body=%v", status, body)
			}
		})
	}
}

type serverReleaseBrokenBody struct{}

func (serverReleaseBrokenBody) Read([]byte) (int, error) {
	return 0, errors.New("private read failure")
}
func (serverReleaseBrokenBody) Close() error { return nil }

func TestServerReleaseTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"network", errors.New("private transport failure"), 502},
		{"timeout", context.DeadlineExceeded, 504},
		{"read", nil, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Timeout: time.Minute, Transport: serverReleaseTransport(func(r *http.Request) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return &http.Response{StatusCode: 200, Body: serverReleaseBrokenBody{}, Header: make(http.Header), Request: r}, nil
			})}
			status, body := serverReleaseRequest(t, client, "GET", "owner=proxima&repo=server", "owner")
			if status != tc.status || len(body) != 1 || strings.Contains(body["error"].(string), "private") {
				t.Fatalf("status=%d body=%v", status, body)
			}
			if client.Timeout != time.Minute {
				t.Fatal("caller timeout mutated")
			}
		})
	}
}
