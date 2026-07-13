package handlers

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/gofiber/fiber/v2"
)

// downloadsDir is where cross-compiled node-agent binaries are shipped in the
// container image (see api-server/Dockerfile).
const downloadsDir = "/app/downloads"

// allowedUpdateOS and allowedUpdateArch restrict the file that can be served by
// the download endpoint, preventing path traversal via os/arch query params.
var allowedUpdateOS = map[string]bool{"linux": true}
var allowedUpdateArch = map[string]bool{"amd64": true, "arm64": true}

type updateInfoResponse struct {
	TargetVersion string `json:"target_version"`
	DownloadURL   string `json:"download_url"`
}

// CheckUpdate reports whether a newer node-agent binary is available for the node.
// The target version is configured globally via the `agent_target_version` setting.
// Returns 204 No Content when no update is configured or the agent is up to date.
// @Summary Node agent update check
// @Description Reports the target node-agent version for self-update
// @Tags node-agent
// @Produce json
// @Param id path string true "Node ID"
// @Success 200 {object} updateInfoResponse
// @Success 204 "No update available"
// @Failure 401 {object} map[string]string
// @Router /nodes/{id}/update [get]
func (h *NodeAgentHandler) CheckUpdate(c *fiber.Ctx) error {
	nodeID := c.Locals("node_id").(string)
	currentVersion := c.Get("X-Agent-Version")

	var targetVersion string
	err := h.db.QueryRow(
		context.Background(),
		`SELECT value FROM settings WHERE key = 'agent_target_version'`,
	).Scan(&targetVersion)
	if err != nil {
		// No target configured: nothing to update to.
		return c.SendStatus(fiber.StatusNoContent)
	}

	if targetVersion == "" || targetVersion == currentVersion {
		return c.SendStatus(fiber.StatusNoContent)
	}

	downloadURL := fmt.Sprintf("%s/api/v1/nodes/%s/update/download", c.BaseURL(), nodeID)
	return c.JSON(updateInfoResponse{
		TargetVersion: targetVersion,
		DownloadURL:   downloadURL,
	})
}

// DownloadUpdate serves the node-agent binary for the requested os/arch.
// @Summary Download node agent binary
// @Description Serves the node-agent binary matching the requested os/arch
// @Tags node-agent
// @Produce octet-stream
// @Param id path string true "Node ID"
// @Param os query string true "Target OS (linux)"
// @Param arch query string true "Target architecture (amd64|arm64)"
// @Success 200 {file} binary
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /nodes/{id}/update/download [get]
func (h *NodeAgentHandler) DownloadUpdate(c *fiber.Ctx) error {
	goos := c.Query("os")
	goarch := c.Query("arch")

	if !allowedUpdateOS[goos] || !allowedUpdateArch[goarch] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "unsupported os/arch",
		})
	}

	filename := fmt.Sprintf("node-agent-%s-%s", goos, goarch)
	path := filepath.Join(downloadsDir, filename)

	if err := c.SendFile(path, false); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "binary not available",
		})
	}
	return nil
}
