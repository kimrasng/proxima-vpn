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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// BackupEntry represents a single backup file in S3.
type BackupEntry struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
}

// BackupService handles database backups and S3 uploads. S3 credentials and
// the backup schedule are resolved fresh on every call from the admin
// Settings table (s3_endpoint/s3_bucket/s3_access_key/s3_secret_key/
// s3_region/backup_schedule, see AdminSettingsHandler in
// handlers/admin_settings.go), falling back to the static config.yaml values
// this service was constructed with when a setting hasn't been saved yet.
// Previously these fields were read only once at process startup, so editing
// them in the admin panel had no effect on the running server.
type BackupService struct {
	db         *pgxpool.Pool
	dbURL      string
	fallbackS3 config.S3Config
	fallback   time.Duration
	cancel     context.CancelFunc
}

// NewBackupService creates a BackupService. s3Cfg/schedule are the
// config.yaml fallback used until (or unless) the admin sets the equivalent
// keys in the settings table.
func NewBackupService(db *pgxpool.Pool, dbURL string, s3Cfg config.S3Config, schedule string) *BackupService {
	return &BackupService{
		db:         db,
		dbURL:      dbURL,
		fallbackS3: s3Cfg,
		fallback:   parseInterval(schedule),
	}
}

// resolveS3 returns the S3 config to use for this call: settings-table
// values where present and non-empty, config.yaml values otherwise.
func (s *BackupService) resolveS3(ctx context.Context) config.S3Config {
	cfg := s.fallbackS3

	rows, err := s.db.Query(ctx,
		`SELECT key, value FROM settings WHERE key IN ('s3_endpoint','s3_bucket','s3_access_key','s3_secret_key','s3_region')`)
	if err != nil {
		return cfg
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil || value == "" {
			continue
		}
		switch key {
		case "s3_endpoint":
			cfg.Endpoint = value
		case "s3_bucket":
			cfg.Bucket = value
		case "s3_access_key":
			cfg.AccessKey = value
		case "s3_secret_key":
			cfg.SecretKey = value
		case "s3_region":
			cfg.Region = value
		}
	}
	return cfg
}

// resolveInterval returns the backup schedule to use for this tick:
// settings-table 'backup_schedule' where present and parseable, config.yaml
// otherwise.
func (s *BackupService) resolveInterval(ctx context.Context) time.Duration {
	var value string
	err := s.db.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'backup_schedule'`).Scan(&value)
	if err != nil || value == "" {
		return s.fallback
	}
	return parseInterval(value)
}

func newS3Client(cfg config.S3Config) *s3.Client {
	return s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	})
}

// RunBackup executes pg_dump, compresses the output, and uploads to S3.
func (s *BackupService) RunBackup(ctx context.Context) (string, error) {
	s3Cfg := s.resolveS3(ctx)
	if s3Cfg.Bucket == "" {
		return "", fmt.Errorf("backup not configured: no S3 bucket set (config.yaml backup.s3.bucket or the S3 Bucket admin setting)")
	}

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

	_, err = newS3Client(s3Cfg).PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s3Cfg.Bucket),
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

// StartScheduler checks every minute whether a backup is due, re-resolving
// the schedule (and S3 config, via RunBackup) from the settings table each
// time so admin panel changes take effect without a restart.
func (s *BackupService) StartScheduler(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	log.Println("[BackupService] scheduler started (checking every 1m for a due backup)")

	var lastRun time.Time
	for {
		select {
		case <-ctx.Done():
			log.Println("[BackupService] scheduler stopped")
			return
		case <-ticker.C:
			interval := s.resolveInterval(ctx)
			if interval <= 0 {
				continue
			}
			if !lastRun.IsZero() && time.Since(lastRun) < interval {
				continue
			}
			if _, err := s.RunBackup(ctx); err != nil {
				log.Printf("[BackupService] backup failed: %v", err)
			}
			lastRun = time.Now()
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
	s3Cfg := s.resolveS3(ctx)
	if s3Cfg.Bucket == "" {
		return nil, fmt.Errorf("backup not configured: no S3 bucket set")
	}

	prefix := "backups/"
	output, err := newS3Client(s3Cfg).ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(s3Cfg.Bucket),
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
	s3Cfg := s.resolveS3(ctx)
	if s3Cfg.Bucket == "" {
		return nil, "", fmt.Errorf("backup not configured: no S3 bucket set")
	}

	output, err := newS3Client(s3Cfg).GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s3Cfg.Bucket),
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
