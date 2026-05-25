package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// BackupEntry represents a single backup file in S3.
type BackupEntry struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
}

// BackupService handles database backups and S3 uploads.
type BackupService struct {
	dbURL    string
	s3Client *s3.Client
	bucket   string
	interval time.Duration
	cancel   context.CancelFunc
}

// NewBackupService creates a BackupService with the given DB URL, S3 config, and schedule interval.
func NewBackupService(dbURL string, s3Cfg config.S3Config, schedule string) *BackupService {
	interval := parseInterval(schedule)

	s3Client := s3.New(s3.Options{
		Region:      s3Cfg.Region,
		BaseEndpoint: aws.String(s3Cfg.Endpoint),
		Credentials: credentials.NewStaticCredentialsProvider(s3Cfg.AccessKey, s3Cfg.SecretKey, ""),
	})

	return &BackupService{
		dbURL:    dbURL,
		s3Client: s3Client,
		bucket:   s3Cfg.Bucket,
		interval: interval,
	}
}

// RunBackup executes pg_dump, compresses the output, and uploads to S3.
func (s *BackupService) RunBackup(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "pg_dump", s.dbURL)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pg_dump: %w", err)
	}

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(output); err != nil {
		return "", fmt.Errorf("gzip write: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", fmt.Errorf("gzip close: %w", err)
	}

	key := fmt.Sprintf("backups/proxima-vpn-%s.sql.gz", time.Now().UTC().Format("20060102-150405"))

	_, err = s.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(compressed.Bytes()),
		ContentType: aws.String("application/gzip"),
	})
	if err != nil {
		return "", fmt.Errorf("s3 upload: %w", err)
	}

	log.Printf("[BackupService] backup uploaded: %s", key)
	return key, nil
}

// StartScheduler runs backups at the configured interval.
func (s *BackupService) StartScheduler(ctx context.Context) {
	if s.interval <= 0 {
		log.Println("[BackupService] no schedule configured, skipping auto-backup")
		return
	}

	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	log.Printf("[BackupService] scheduler started (interval: %s)", s.interval)

	for {
		select {
		case <-ctx.Done():
			log.Println("[BackupService] scheduler stopped")
			return
		case <-ticker.C:
			if _, err := s.RunBackup(ctx); err != nil {
				log.Printf("[BackupService] backup failed: %v", err)
			}
		}
	}
}

// Stop cancels the backup scheduler.
func (s *BackupService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *BackupService) ListBackups(ctx context.Context) ([]BackupEntry, error) {
	prefix := "backups/"
	output, err := s.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 list: %w", err)
	}

	entries := make([]BackupEntry, 0, len(output.Contents))
	for _, obj := range output.Contents {
		entries = append(entries, BackupEntry{
			Key:          aws.ToString(obj.Key),
			Size:         aws.ToInt64(obj.Size),
			LastModified: aws.ToTime(obj.LastModified),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastModified.After(entries[j].LastModified)
	})

	return entries, nil
}

func (s *BackupService) GetBackup(ctx context.Context, key string) (io.ReadCloser, string, error) {
	output, err := s.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", fmt.Errorf("s3 get: %w", err)
	}
	return output.Body, aws.ToString(output.ContentType), nil
}

func parseInterval(schedule string) time.Duration {
	if schedule == "" {
		return 0
	}
	d, err := time.ParseDuration(schedule)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}
