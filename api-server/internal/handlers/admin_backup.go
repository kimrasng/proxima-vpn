package handlers

import (
	"fmt"
	"path/filepath"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type AdminBackupHandler struct {
	backup *services.BackupService
}

func NewAdminBackupHandler(backup *services.BackupService) *AdminBackupHandler {
	return &AdminBackupHandler{backup: backup}
}

// TriggerBackup triggers a manual backup.
// @Summary Trigger backup
// @Description Triggers a manual database backup
// @Tags admin-backup
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/backup/trigger [post]
func (h *AdminBackupHandler) TriggerBackup(c *fiber.Ctx) error {
	path, err := h.backup.RunBackup(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "backup failed: " + err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "backup completed",
		"path":    path,
	})
}

// ListBackups returns available backups.
// @Summary List backups
// @Description Returns a list of available backups
// @Tags admin-backup
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/backup/list [get]
func (h *AdminBackupHandler) ListBackups(c *fiber.Ctx) error {
	entries, err := h.backup.ListBackups(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list backups: " + err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"backups": entries,
	})
}

// DownloadBackup downloads a backup file.
// @Summary Download backup
// @Description Downloads a backup file by key (defaults to latest)
// @Tags admin-backup
// @Produce octet-stream
// @Param key query string false "Backup key"
// @Success 200 {file} file
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/backup/download [get]
func (h *AdminBackupHandler) DownloadBackup(c *fiber.Ctx) error {
	key := c.Query("key")

	if key == "" {
		entries, err := h.backup.ListBackups(c.Context())
		if err != nil || len(entries) == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "no backups available",
			})
		}
		key = entries[0].Key
	}

	body, contentType, err := h.backup.GetBackup(c.Context(), key)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to download backup: " + err.Error(),
		})
	}
	defer body.Close()

	filename := filepath.Base(key)
	c.Set("Content-Type", contentType)
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	return c.SendStream(body)
}
