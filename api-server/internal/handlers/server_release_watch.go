package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/buildinfo"
	"golang.org/x/mod/semver"
)

const serverReleaseWatchInterval = time.Hour
const serverReleaseWatchFile = "server-release-watch.json"

var ErrReleaseWatchStorage = errors.New("release watch requires a private operator-provisioned directory")
var ErrReleaseWatchRunning = errors.New("release watch already running or closed")

type ServerReleaseWatchConfig struct {
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	Enabled bool   `json:"enabled"`
}

type ServerWatchedRelease struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
}

type ServerReleaseWatchState struct {
	Config          ServerReleaseWatchConfig `json:"config"`
	Current         buildinfo.Info           `json:"current"`
	Latest          *ServerWatchedRelease    `json:"latest"`
	CheckedAt       *time.Time               `json:"checked_at"`
	NextCheckAt     *time.Time               `json:"next_check_at"`
	Error           string                   `json:"error,omitempty"`
	Comparison      string                   `json:"comparison"`
	UpdateAvailable *bool                    `json:"update_available"`
	IntervalSeconds int                      `json:"interval_seconds"`
	Running         bool                     `json:"running"`
}

// ServerReleaseWatch owns one private file and one cancellable polling loop.
// Mount State and Configure after AdminJWTMiddleware and RequireAdminOwner.
// Start Run in a server-lifetime goroutine, cancel and join it before Close.
// This is discovery only: no downloads, deployments, or "update succeeded" state.
type ServerReleaseWatch struct {
	mu         sync.Mutex
	root       *os.Root
	lock       *os.File
	client     http.Client
	state      ServerReleaseWatchState
	generation uint64
	wake       chan struct{}
	closed     bool
}

// NewServerReleaseWatch opens an EXISTING absolute private directory (0700,
// owned by the API process UID). Its ancestors and mount must be controlled by
// the operator, outside uploads/backups/static roots; never accept this path
// from an HTTP request or settings table. In root-run containers this is a
// root-owned file. Non-root deployments must provision it for the service UID.
// The directory must be durable; no ephemeral or in-memory fallback is used.
// An exclusive file lock rejects a second process using the same directory.
// client is a trusted transport injection only; nil uses normal TLS verification.
func NewServerReleaseWatch(directory string, client *http.Client) (*ServerReleaseWatch, error) {
	if !filepath.IsAbs(directory) {
		return nil, ErrReleaseWatchStorage
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("release watch directory: %w", err)
	}
	if !info.IsDir() || !privateReleaseFile(info, 0700) {
		return nil, ErrReleaseWatchStorage
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	w := &ServerReleaseWatch{root: root, client: NewServerReleaseHandler(client).client, wake: make(chan struct{}, 1)}
	w.state = ServerReleaseWatchState{Current: buildinfo.Current(), Comparison: "unknown", IntervalSeconds: int(serverReleaseWatchInterval.Seconds())}
	if err := w.openStorage(); err != nil {
		return nil, errors.Join(err, w.Close())
	}
	return w, nil
}

func privateReleaseFile(info os.FileInfo, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm() == mode && info.Mode()&os.ModeSymlink == 0
}

func (w *ServerReleaseWatch) openStorage() error {
	info, err := w.root.Stat(".")
	if err != nil {
		return err
	}
	if !info.IsDir() || !privateReleaseFile(info, 0700) {
		return ErrReleaseWatchStorage
	}
	// Root.OpenFile may resolve contained symlinks itself, so reject the link
	// before opening as well. The private directory excludes untrusted writers.
	info, err = w.root.Lstat(".release-watch.lock")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && (!info.Mode().IsRegular() || !privateReleaseFile(info, 0600)) {
		return ErrReleaseWatchStorage
	}
	lock, err := w.root.OpenFile(".release-watch.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	w.lock = lock
	info, err = lock.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !privateReleaseFile(info, 0600) {
		return ErrReleaseWatchStorage
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("release watch storage already in use: %w", err)
	}
	info, err = w.root.Lstat(serverReleaseWatchFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !privateReleaseFile(info, 0600) || info.Size() > 4096 {
		return ErrReleaseWatchStorage
	}
	f, err := w.root.OpenFile(serverReleaseWatchFile, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return err
	}
	cfg, err := parseReleaseWatchConfig(data)
	if err != nil {
		return fmt.Errorf("invalid saved release watch configuration: %w", err)
	}
	w.state.Config = cfg
	return nil
}

func parseReleaseWatchConfig(data []byte) (ServerReleaseWatchConfig, error) {
	var cfg ServerReleaseWatchConfig
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	err := d.Decode(&cfg)
	if err != nil {
		return cfg, err
	}
	if d.Decode(new(json.RawMessage)) != io.EOF || len(data) > 4096 || !utf8.Valid(data) ||
		!serverReleaseOwner.MatchString(cfg.Owner) || strings.Contains(cfg.Owner, "--") ||
		!serverReleaseRepo.MatchString(cfg.Repo) || cfg.Repo == "." || cfg.Repo == ".." {
		return cfg, errors.New("invalid_repository")
	}
	return cfg, nil
}

// Close releases storage; the caller must first cancel and join Run.
func (w *ServerReleaseWatch) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state.Running {
		return ErrReleaseWatchRunning
	}
	if w.closed {
		return nil
	}
	w.closed = true
	var err error
	if w.lock != nil {
		err = w.lock.Close()
	}
	return errors.Join(err, w.root.Close())
}

func (w *ServerReleaseWatch) State(c *fiber.Ctx) error {
	if role, _ := c.Locals("admin_role").(string); role != "owner" {
		return c.SendStatus(http.StatusForbidden)
	}
	if c.Method() != http.MethodGet {
		c.Set("Allow", "GET")
		return c.SendStatus(http.StatusMethodNotAllowed)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	c.Set("Cache-Control", "no-store")
	return c.JSON(w.state)
}

// Configure persists selection before publishing it. Saving never performs a
// network request; even repeated repository changes cannot bypass the interval.
func (w *ServerReleaseWatch) Configure(c *fiber.Ctx) error {
	if role, _ := c.Locals("admin_role").(string); role != "owner" {
		return c.SendStatus(http.StatusForbidden)
	}
	if c.Method() != http.MethodPut {
		c.Set("Allow", "PUT")
		return c.SendStatus(http.StatusMethodNotAllowed)
	}
	cfg, err := parseReleaseWatchConfig(c.Body())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_repository"})
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return c.SendStatus(http.StatusServiceUnavailable)
	}
	if err := w.save(cfg); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "release_watch_save_failed"})
	}
	w.generation++
	w.state.Config = cfg
	w.state.Latest, w.state.CheckedAt = nil, nil
	w.state.Error, w.state.Comparison, w.state.UpdateAvailable = "", "unknown", nil
	select {
	case w.wake <- struct{}{}:
	default:
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(w.state)
}

func (w *ServerReleaseWatch) save(cfg ServerReleaseWatchConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := ".release-watch-" + hex.EncodeToString(nonce[:])
	f, err := w.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = w.root.Remove(name) }()
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	if err := errors.Join(writeErr, syncErr, f.Close()); err != nil {
		return err
	}
	if err := w.root.Rename(name, serverReleaseWatchFile); err != nil {
		return err
	}
	dir, err := w.root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// Run checks immediately when configured, then at most once per hour globally
// (including failures and selection changes). Cancellation interrupts HTTP I/O.
// Only one Run is allowed; the parent owns starting, cancelling and joining it.
func (w *ServerReleaseWatch) Run(ctx context.Context) error {
	w.mu.Lock()
	if w.closed || w.state.Running {
		w.mu.Unlock()
		return ErrReleaseWatchRunning
	}
	w.state.Running = true
	w.mu.Unlock()
	defer func() { w.mu.Lock(); w.state.Running = false; w.state.NextCheckAt = nil; w.mu.Unlock() }()
	timer := time.NewTimer(0)
	defer timer.Stop()
	var next time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		case <-w.wake:
		}
		if ctx.Err() != nil {
			return nil
		}
		w.mu.Lock()
		cfg, generation := w.state.Config, w.generation
		if !cfg.Enabled {
			w.state.NextCheckAt = nil
			w.mu.Unlock()
			continue
		}
		now := time.Now()
		if now.Before(next) {
			w.state.NextCheckAt = &next
			w.mu.Unlock()
			timer.Reset(time.Until(next))
			continue
		}
		next = now.Add(serverReleaseWatchInterval)
		nextCopy := next
		w.state.NextCheckAt = &nextCopy
		w.mu.Unlock()
		release, code := w.fetch(ctx, cfg)
		w.mu.Lock()
		if generation == w.generation {
			checked := time.Now()
			w.state.CheckedAt, w.state.Error, w.state.Latest = &checked, code, release
			w.state.Comparison, w.state.UpdateAvailable = "unknown", nil
			current := w.state.Current
			if release != nil && !current.Modified && strings.EqualFold(current.Repository, cfg.Owner+"/"+cfg.Repo) && semver.IsValid(current.Version) && semver.IsValid(release.Tag) {
				cmp := semver.Compare(current.Version, release.Tag)
				available := cmp < 0
				w.state.UpdateAvailable = &available
				switch {
				case cmp < 0:
					w.state.Comparison = "update_available"
				case cmp == 0:
					w.state.Comparison = "current"
				default:
					w.state.Comparison = "ahead"
				}
			}
		}
		w.mu.Unlock()
		timer.Reset(time.Until(next))
	}
}

func (w *ServerReleaseWatch) fetch(ctx context.Context, cfg ServerReleaseWatchConfig) (*ServerWatchedRelease, string) {
	ctx, cancel := context.WithTimeout(ctx, serverReleaseTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+cfg.Owner+"/"+cfg.Repo+"/releases/latest", nil)
	if err != nil {
		return nil, "release_unavailable"
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Proxima-Release-Watch")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, "release_unavailable"
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "release_not_found"
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "release_unavailable"
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, serverReleaseMaxBody+1))
	if err != nil || len(data) > serverReleaseMaxBody || !utf8.Valid(data) {
		return nil, "invalid_release_response"
	}
	var r struct {
		Tag         string `json:"tag_name"`
		Name        string `json:"name"`
		PublishedAt string `json:"published_at"`
		HTMLURL     string `json:"html_url"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
	}
	if json.Unmarshal(data, &r) != nil {
		return nil, "invalid_release_response"
	}
	published, dateErr := time.Parse(time.RFC3339, r.PublishedAt)
	link, urlErr := url.Parse(r.HTMLURL)
	if !serverReleaseText(r.Tag, 256) || len(r.Name) > 1024 || (r.Name != "" && !serverReleaseText(r.Name, 1024)) ||
		dateErr != nil || published.IsZero() || r.Draft || r.Prerelease || urlErr != nil ||
		link.Scheme != "https" || link.Host != "github.com" || link.User != nil || link.RawQuery != "" || link.ForceQuery || link.Fragment != "" ||
		!strings.EqualFold(strings.TrimSuffix(link.Path, "/releases/tag/"+r.Tag), "/"+cfg.Owner+"/"+cfg.Repo) || !strings.HasSuffix(link.Path, "/releases/tag/"+r.Tag) {
		return nil, "invalid_release_response"
	}
	return &ServerWatchedRelease{Tag: r.Tag, Name: r.Name, PublishedAt: r.PublishedAt, HTMLURL: r.HTMLURL}, ""
}
