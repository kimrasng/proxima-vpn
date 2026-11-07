package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var errInvalid = errors.New("invalid input")
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var imagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9./_-]+@sha256:[a-f0-9]{64}$`)
var envPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

const releaseWatchDir = "/var/lib/proxima-release-watch"

type Images struct {
	API string `json:"api"`
	Web string `json:"web"`
}

type Config struct {
	Project        string            `json:"project"`
	Network        string            `json:"network"`
	AppVolume      string            `json:"app_volume"`
	UploadsVolume  string            `json:"uploads_volume"`
	APIEnvironment map[string]string `json:"api_environment"`
	SocketGID      int               `json:"socket_gid"`
}

type Approval struct {
	ID                 string `json:"id"`
	From               Images `json:"from"`
	To                 Images `json:"to"`
	RollbackCompatible bool   `json:"rollback_compatible"`
}

func strictJSON(data []byte, dst interface{}) error {
	// Reject duplicate keys as well as unknown fields and trailing JSON values.
	d := json.NewDecoder(bytes.NewReader(data))
	var value func() error
	value = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return errInvalid
				}
				seen[k] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return errInvalid
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return fmt.Errorf("%w: %v", errInvalid, err)
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return errInvalid
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", errInvalid, err)
	}
	return nil
}

// Trusted paths must have no symlink or non-root-writable component. Root is
// the approval authority; neither the API UID nor its socket group may write.
func trustedPath(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		s, ok := st.Sys().(*syscall.Stat_t)
		if !ok || s.Uid != 0 || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("%w: untrusted path %s", errInvalid, p)
		}
		if p == "/" {
			return nil
		}
	}
}

func readTrusted(path string, dst interface{}) error {
	if err := trustedPath(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > 65536 {
		return errInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return err
	}
	return strictJSON(data, dst)
}

func (c Config) validate() error {
	for _, n := range []string{c.Project, c.Network, c.AppVolume, c.UploadsVolume} {
		if !namePattern.MatchString(n) {
			return errInvalid
		}
	}
	if c.SocketGID <= 0 || c.AppVolume == c.UploadsVolume {
		return errInvalid
	}
	for k, v := range c.APIEnvironment {
		if !envPattern.MatchString(k) || len(v) > 8192 {
			return errInvalid
		}
	}
	if c.APIEnvironment["DATABASE_URL"] == "" || c.APIEnvironment["REDIS_URL"] == "" {
		return fmt.Errorf("%w: explicit database and redis URLs required", errInvalid)
	}
	if path := c.APIEnvironment["PROXIMA_RELEASE_WATCH_DIR"]; path != "" && path != releaseWatchDir {
		return fmt.Errorf("%w: release watch directory must use the fixed host path", errInvalid)
	}
	return nil
}

func (a Approval) validate() error {
	if a.From == a.To {
		return errInvalid
	}
	return a.validateImages()
}

func (a Approval) validateImages() error {
	if !namePattern.MatchString(a.ID) {
		return errInvalid
	}
	for _, image := range []string{a.From.API, a.From.Web, a.To.API, a.To.Web} {
		if !imagePattern.MatchString(image) {
			return errInvalid
		}
	}
	return nil
}

func (a Approval) bootstrap(cfg Config) ([]byte, error) {
	if a.From != a.To || a.validateImages() != nil {
		return nil, errInvalid
	}
	return cfg.compose(a.From)
}
