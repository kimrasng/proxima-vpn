package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/buildinfo"
)

func newReleaseWatchTest(t *testing.T, client *http.Client) (*ServerReleaseWatch, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	w, err := NewServerReleaseWatch(dir, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	return w, dir
}

func releaseWatchRequest(t *testing.T, w *ServerReleaseWatch, method, role, body string) (int, ServerReleaseWatchState) {
	t.Helper()
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("admin_role", role); return c.Next() })
	app.Get("/watch", w.State)
	app.All("/watch", w.Configure)
	req := httptest.NewRequest(method, "/watch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var state ServerReleaseWatchState
	if resp.StatusCode == 200 {
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, state
}

func TestReleaseWatchOwnerPersistence(t *testing.T) {
	w, dir := newReleaseWatchTest(t, nil)
	body := `{"owner":"proxima","repo":"server","enabled":true}`
	for _, role := range []string{"", "admin", "viewer"} {
		for _, method := range []string{"GET", "PUT"} {
			status, _ := releaseWatchRequest(t, w, method, role, body)
			if status != 403 {
				t.Fatalf("%s %s: %d", role, method, status)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, serverReleaseWatchFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unauthorized write: %v", err)
	}
	status, state := releaseWatchRequest(t, w, "PUT", "owner", body)
	if status != 200 || !state.Config.Enabled || state.Latest != nil || state.UpdateAvailable != nil {
		t.Fatalf("save: %d %+v", status, state)
	}
	info, err := os.Stat(filepath.Join(dir, serverReleaseWatchFile))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	if other, err := NewServerReleaseWatch(dir, nil); err == nil {
		_ = other.Close()
		t.Fatal("second writer accepted")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewServerReleaseWatch(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	status, state = releaseWatchRequest(t, reopened, "GET", "owner", "")
	if status != 200 || state.Config.Owner != "proxima" || state.Config.Repo != "server" || !state.Config.Enabled {
		t.Fatalf("restart lost selection: %+v", state)
	}
}

func TestReleaseWatchRejectsUnsafeStorage(t *testing.T) {
	for _, kind := range []string{"public directory", "directory symlink", "file symlink", "public file", "invalid file", "lock symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "public directory":
				err = os.Chmod(dir, 0755)
			case "directory symlink":
				link := filepath.Join(t.TempDir(), "link")
				err = os.Symlink(dir, link)
				dir = link
			case "file symlink":
				err = os.Symlink("other.json", filepath.Join(dir, serverReleaseWatchFile))
			case "public file":
				err = os.WriteFile(filepath.Join(dir, serverReleaseWatchFile), []byte(`{}`), 0644)
			case "invalid file":
				err = os.WriteFile(filepath.Join(dir, serverReleaseWatchFile), []byte(`{}`), 0600)
			case "lock symlink":
				err = os.Symlink("other", filepath.Join(dir, ".release-watch.lock"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if w, err := NewServerReleaseWatch(dir, nil); err == nil {
				_ = w.Close()
				t.Fatal("unsafe storage accepted")
			}
		})
	}
}

func TestReleaseWatchInvalidConfigDoesNotReplaceSelection(t *testing.T) {
	w, _ := newReleaseWatchTest(t, nil)
	if code, _ := releaseWatchRequest(t, w, "PUT", "owner", `{"owner":"proxima","repo":"server","enabled":true}`); code != 200 {
		t.Fatal(code)
	}
	for _, body := range []string{
		`null`, `{}`, `{"owner":"proxima","repo":".."}`, `{"owner":"https://evil.test","repo":"server"}`,
		`{"owner":"proxima","repo":"server","url":"http://localhost"}`, `{"owner":"proxima","repo":"server","interval":1}`,
		`{"owner":"proxima","repo":"server"} {}`, `{"owner":"proxima","repo":"a/b"}`,
	} {
		if code, _ := releaseWatchRequest(t, w, "PUT", "owner", body); code != 400 {
			t.Fatalf("%s: %d", body, code)
		}
	}
	if code, _ := releaseWatchRequest(t, w, "POST", "owner", `{}`); code != 405 {
		t.Fatal(code)
	}
	_, state := releaseWatchRequest(t, w, "GET", "owner", "")
	if state.Config.Repo != "server" || !state.Config.Enabled {
		t.Fatalf("selection replaced: %+v", state)
	}
}

func TestReleaseWatchSaveFailureIsNotSuccess(t *testing.T) {
	w, dir := newReleaseWatchTest(t, nil)
	if err := os.Mkdir(filepath.Join(dir, serverReleaseWatchFile), 0700); err != nil {
		t.Fatal(err)
	}
	code, _ := releaseWatchRequest(t, w, "PUT", "owner", `{"owner":"proxima","repo":"server","enabled":true}`)
	_, state := releaseWatchRequest(t, w, "GET", "owner", "")
	if code != 500 || state.Config.Enabled || state.Config.Owner != "" {
		t.Fatalf("failed write published: %d %+v", code, state)
	}
}

func releaseWatchClient(t *testing.T, calls *atomic.Int32, payload string) *http.Client {
	t.Helper()
	return &http.Client{Transport: serverReleaseTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" || !strings.HasSuffix(r.URL.Path, "/releases/latest") || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("unsafe request: %v", r)
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > serverReleaseTimeout {
			t.Error("missing bounded deadline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header), Request: r}, nil
	})}
}

// fasthttp starts a process-lifetime Date-header cache on its first request.
// Initialize that cache outside synctest, whose bubbles forbid leaked workers.
func primeReleaseWatchHTTP(t *testing.T) {
	t.Helper()
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestReleaseWatchAutomaticIntervalAndCancellation(t *testing.T) {
	primeReleaseWatchHTTP(t)
	// synctest advances virtual time only when all goroutines are blocked;
	// these sleeps assert the actual hourly scheduling contract, not timing luck.
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		data, err := json.Marshal(serverReleaseFixture())
		if err != nil {
			t.Fatal(err)
		}
		w, _ := newReleaseWatchTest(t, releaseWatchClient(t, &calls, string(data)))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		synctest.Wait()
		if calls.Load() != 0 {
			t.Fatal("unconfigured watcher called GitHub")
		}
		if code, _ := releaseWatchRequest(t, w, "PUT", "owner", `{"owner":"proxima","repo":"server","enabled":true}`); code != 200 {
			t.Fatal(code)
		}
		synctest.Wait()
		_, state := releaseWatchRequest(t, w, "GET", "owner", "")
		if calls.Load() != 1 || state.Latest == nil || state.CheckedAt == nil || state.NextCheckAt == nil || state.Comparison != "unknown" || state.UpdateAvailable != nil {
			t.Fatalf("initial automatic check: calls=%d state=%+v", calls.Load(), state)
		}
		if err := w.Run(ctx); !errors.Is(err, ErrReleaseWatchRunning) {
			t.Fatalf("duplicate Run: %v", err)
		}
		for range 3 {
			releaseWatchRequest(t, w, "PUT", "owner", `{"owner":"proxima","repo":"server","enabled":true}`)
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("configuration bypassed interval")
		}
		time.Sleep(time.Hour - time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("early request")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("hourly check missing: %d", calls.Load())
		}
		releaseWatchRequest(t, w, "PUT", "owner", `{"owner":"proxima","repo":"server","enabled":false}`)
		synctest.Wait()
		time.Sleep(2 * time.Hour)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("disabled watcher called GitHub")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		_, state = releaseWatchRequest(t, w, "GET", "owner", "")
		if state.Running || state.NextCheckAt != nil {
			t.Fatalf("shutdown state: %+v", state)
		}
	})
}

func TestReleaseWatchBuildComparison(t *testing.T) {
	primeReleaseWatchHTTP(t)
	for _, tc := range []struct {
		version, repository string
		dirty               bool
		comparison          string
		known               bool
	}{
		{"v1.0.0", "proxima/server", false, "update_available", true},
		{"v1.2.3", "proxima/server", false, "current", true},
		{"v2.0.0", "proxima/server", false, "ahead", true},
		{"dev", "proxima/server", false, "unknown", false},
		{"v1.0.0", "other/project", false, "unknown", false},
		{"v1.0.0", "", false, "unknown", false},
		{"v1.0.0", "proxima/server", true, "unknown", false},
	} {
		t.Run(tc.version+tc.repository+tc.comparison, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls atomic.Int32
				data, err := json.Marshal(serverReleaseFixture())
				if err != nil {
					t.Fatal(err)
				}
				w, _ := newReleaseWatchTest(t, releaseWatchClient(t, &calls, string(data)))
				w.state.Current = buildinfo.Info{Version: tc.version, Repository: tc.repository, Modified: tc.dirty}
				releaseWatchRequest(t, w, "PUT", "owner", `{"owner":"proxima","repo":"server","enabled":true}`)
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- w.Run(ctx) }()
				synctest.Wait()
				_, state := releaseWatchRequest(t, w, "GET", "owner", "")
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if state.Comparison != tc.comparison || (state.UpdateAvailable != nil) != tc.known || state.Current.Version != tc.version {
					t.Fatalf("comparison: %+v", state)
				}
				if tc.known && *state.UpdateAvailable != (tc.comparison == "update_available") {
					t.Fatalf("update flag: %+v", state)
				}
			})
		})
	}
}

func TestReleaseWatchDiscardsInFlightOldRepository(t *testing.T) {
	primeReleaseWatchHTTP(t)
	synctest.Test(t, func(t *testing.T) {
		started, unblock := make(chan struct{}), make(chan struct{})
		client := &http.Client{Transport: serverReleaseTransport(func(r *http.Request) (*http.Response, error) {
			close(started)
			<-unblock
			data, err := json.Marshal(serverReleaseFixture())
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header), Request: r}, nil
		})}
		w, _ := newReleaseWatchTest(t, client)
		releaseWatchRequest(t, w, "PUT", "owner", `{"owner":"proxima","repo":"server","enabled":true}`)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		<-started
		releaseWatchRequest(t, w, "PUT", "owner", `{"owner":"proxima","repo":"different","enabled":true}`)
		close(unblock)
		synctest.Wait()
		_, state := releaseWatchRequest(t, w, "GET", "owner", "")
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if state.Latest != nil || state.CheckedAt != nil || state.Config.Repo != "different" {
			t.Fatalf("stale response published: %+v", state)
		}
	})
}

func TestReleaseWatchRejectsUpstreamAndRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 307, 404, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			client := serverReleaseHTTPClient(t, http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				rw.Header().Set("Location", "https://evil.test/private")
				rw.WriteHeader(status)
				_, _ = io.WriteString(rw, "secret upstream response")
			}))
			w, _ := newReleaseWatchTest(t, client)
			release, code := w.fetch(context.Background(), ServerReleaseWatchConfig{Owner: "proxima", Repo: "server"})
			want := "release_unavailable"
			if status == 404 {
				want = "release_not_found"
			}
			if release != nil || code != want || requests.Load() != 1 {
				t.Fatalf("release=%v code=%s requests=%d", release, code, requests.Load())
			}
		})
	}
}

func TestReleaseWatchInvalidUpstreamResponse(t *testing.T) {
	cases := []string{"null", "{}", "{", "{} {}", string([]byte{0xff}), strings.Repeat("x", serverReleaseMaxBody+1)}
	for key, value := range map[string]any{
		"tag_name": "bad\n", "published_at": "not-a-date", "draft": true, "prerelease": true,
		"html_url": "https://evil.test/proxima/server/releases/tag/v1.2.3",
	} {
		fixture := serverReleaseFixture()
		fixture[key] = value
		data, err := json.Marshal(fixture)
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, string(data))
	}
	for _, payload := range cases {
		var calls atomic.Int32
		w, _ := newReleaseWatchTest(t, releaseWatchClient(t, &calls, payload))
		release, code := w.fetch(context.Background(), ServerReleaseWatchConfig{Owner: "proxima", Repo: "server"})
		if release != nil || code != "invalid_release_response" {
			t.Fatalf("invalid upstream accepted: %v %s", release, code)
		}
	}
}

func TestReleaseWatchCancelsActiveRequest(t *testing.T) {
	primeReleaseWatchHTTP(t)
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		client := &http.Client{Transport: serverReleaseTransport(func(r *http.Request) (*http.Response, error) {
			close(started)
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		w, _ := newReleaseWatchTest(t, client)
		releaseWatchRequest(t, w, "PUT", "owner", `{"owner":"proxima","repo":"server","enabled":true}`)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		<-started
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		_, state := releaseWatchRequest(t, w, "GET", "owner", "")
		if state.Running || state.Latest != nil || state.Error != "release_unavailable" || state.UpdateAvailable != nil {
			t.Fatalf("shutdown: %+v", state)
		}
	})
}
