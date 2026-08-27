package handlers

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

const maxImageSize = 5 * 1024 * 1024

var allowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

type AdminUploadHandler struct {
	cfg *config.StorageConfig
	panelURL string
}

func NewAdminUploadHandler(cfg *config.StorageConfig, panelURL string) *AdminUploadHandler {
	return &AdminUploadHandler{cfg: cfg, panelURL: panelURL}
}

func (h *AdminUploadHandler) UploadImage(c *fiber.Ctx) error {
	file, err := c.FormFile("image")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "image file is required"})
	}

	if file.Size > maxImageSize {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "image must be under 5MB"})
	}

	contentType := file.Header.Get("Content-Type")
	ext, ok := allowedImageTypes[contentType]
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unsupported image type"})
	}

	filename := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)

	src, err := file.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to open file"})
	}
	defer func() { _ = src.Close() }()

	var publicURL string

	if h.cfg.Type == "s3" {
		publicURL, err = h.uploadToS3(c.Context(), src, filename, contentType)
	} else {
		publicURL, err = h.uploadToLocal(src, filename)
	}

	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to upload image"})
	}

	return c.JSON(fiber.Map{"url": publicURL})
}

func (h *AdminUploadHandler) uploadToLocal(src multipart.File, filename string) (string, error) {
	if err := os.MkdirAll(h.cfg.LocalPath, 0755); err != nil {
		return "", fmt.Errorf("create upload dir: %w", err)
	}

	dst, err := os.Create(filepath.Join(h.cfg.LocalPath, filename))
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	// Checked explicitly (not just via the deferred close above) because
	// Close on a just-written file can fail to flush buffered data - a
	// silent failure here would mean the uploaded image looks like it
	// succeeded but is truncated on disk.
	if err := dst.Close(); err != nil {
		return "", fmt.Errorf("close file: %w", err)
	}

	return h.panelURL + "/uploads/" + filename, nil
}

func (h *AdminUploadHandler) uploadToS3(ctx context.Context, src multipart.File, filename, contentType string) (string, error) {
	s3cfg := h.cfg.S3

	// Service-specific endpoint override (s3.Options.BaseEndpoint) instead of
	// the deprecated global aws.EndpointResolverWithOptions - same pattern
	// already used in services/backup.go's newS3Client.
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(s3cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			s3cfg.AccessKey, s3cfg.SecretKey, "",
		)),
	)
	if err != nil {
		return "", fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		if s3cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(s3cfg.Endpoint)
		}
	})

	key := "uploads/" + filename
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s3cfg.Bucket),
		Key:         aws.String(key),
		Body:        src,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("s3 put object: %w", err)
	}

	base := strings.TrimRight(s3cfg.Endpoint, "/")
	if base == "" {
		base = fmt.Sprintf("https://%s.s3.%s.amazonaws.com", s3cfg.Bucket, s3cfg.Region)
		return base + "/" + key, nil
	}
	return fmt.Sprintf("%s/%s/%s", base, s3cfg.Bucket, key), nil
}
