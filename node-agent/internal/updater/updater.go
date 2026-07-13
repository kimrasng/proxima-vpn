package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Updater checks for and performs binary self-updates.
type Updater struct {
	currentVersion string
	binaryPath     string
	serverURL      string
	nodeID         string
	apiKey         string
	httpClient     *http.Client
}

// UpdateInfo is the response from the server's update check endpoint.
type UpdateInfo struct {
	TargetVersion string `json:"target_version"`
	DownloadURL   string `json:"download_url"`
}

// NewUpdater creates a new Updater instance.
// If binaryPath is empty, it will be resolved via os.Executable().
func NewUpdater(currentVersion, binaryPath, serverURL, nodeID, apiKey string) *Updater {
	if binaryPath == "" {
		exe, err := os.Executable()
		if err == nil {
			binaryPath = exe
		}
	}
	return &Updater{
		currentVersion: currentVersion,
		binaryPath:     binaryPath,
		serverURL:      serverURL,
		nodeID:         nodeID,
		apiKey:         apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// CheckUpdate contacts the server to see if a newer version is available.
// Returns the new version string and whether an update is available.
func (u *Updater) CheckUpdate(ctx context.Context) (newVersion string, available bool, err error) {
	url := fmt.Sprintf("%s/api/v1/nodes/%s/update", u.serverURL, u.nodeID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, fmt.Errorf("create update check request: %w", err)
	}
	req.Header.Set("X-Node-Key", u.apiKey)
	req.Header.Set("X-Agent-Version", u.currentVersion)
	req.Header.Set("X-Agent-OS", runtime.GOOS)
	req.Header.Set("X-Agent-Arch", runtime.GOARCH)

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("update check request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("update check returned status %d", resp.StatusCode)
	}

	var info UpdateInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", false, fmt.Errorf("decode update response: %w", err)
	}

	if info.TargetVersion == "" || info.TargetVersion == u.currentVersion {
		return "", false, nil
	}

	return info.TargetVersion, true, nil
}

// PerformUpdate downloads the new binary and replaces the current one atomically.
// After a successful update, the caller should exit so systemd can restart with the new binary.
func (u *Updater) PerformUpdate(ctx context.Context, targetVersion string) error {
	// Build download URL from server
	downloadURL := fmt.Sprintf("%s/api/v1/nodes/%s/update/download?version=%s&os=%s&arch=%s",
		u.serverURL, u.nodeID, targetVersion, runtime.GOOS, runtime.GOARCH)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("X-Node-Key", u.apiKey)

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	dir := filepath.Dir(u.binaryPath)
	tmpFile, err := os.CreateTemp(dir, "node-agent-update-*")
	if err != nil {
			tmpFile, err = os.CreateTemp("", "node-agent-update-*")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
	}
	tmpPath := tmpFile.Name()

	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write update binary: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("chmod update binary: %w", err)
	}

	// Atomic rename to replace current binary
	if err := os.Rename(tmpPath, u.binaryPath); err != nil {
		return fmt.Errorf("rename update binary: %w", err)
	}

	success = true
	return nil
}
