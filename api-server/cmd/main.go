package main

import (
	"context"
	"log"
	"os"

	_ "github.com/proximavpn/proxima-vpn/api-server/docs"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/metrics"
	"github.com/proximavpn/proxima-vpn/api-server/internal/scheduler"
	"github.com/proximavpn/proxima-vpn/api-server/internal/server"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/api-server/internal/telegram"
)

const version = "0.1.0"

// @title Proxima VPN API
// @version 0.1.0
// @description VPN Panel Management API
// @host localhost:2053
// @BasePath /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func main() {
	configPath := "config.yaml"
	if v := os.Getenv("CONFIG_PATH"); v != "" {
		configPath = v
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	ctx := context.Background()

	db, err := database.NewPostgresPool(ctx, cfg.Database)
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}
	defer db.Close()

	if err := database.Migrate(ctx, db); err != nil {
		log.Fatalf("failed to migrate database: %v", err)
	}

	if err := database.SeedAdmin(ctx, db); err != nil {
		log.Fatalf("failed to seed admin: %v", err)
	}

	rdb, err := database.NewRedisClient(ctx, cfg.Redis)
	if err != nil {
		log.Fatalf("failed to connect to redis: %v", err)
	}
	defer rdb.Close()

	log.Printf("proxima-vpn api-server v%s starting on %s:%d", version, cfg.Server.Host, cfg.Server.Port)

	trafficReset := scheduler.NewTrafficResetScheduler(db)
	go trafficReset.Start(ctx)

	expiryCheck := scheduler.NewExpiryCheckScheduler(db)
	go expiryCheck.Start(ctx)

	telegramSvc := services.NewTelegramService(cfg.Telegram)

	nodeMonitor := scheduler.NewNodeMonitorScheduler(db, telegramSvc)
	go nodeMonitor.Start(ctx)

	go metrics.StartGaugeUpdater(ctx, db)

	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" {
		botSvc, err := telegram.NewBotService(cfg.Telegram, db, rdb, cfg.Server.PanelURL)
		if err != nil {
			log.Printf("telegram bot init failed: %v", err)
		} else {
			botSvc.Start(ctx)
		}
	}

	var backupSvc *services.BackupService
	if cfg.Backup.S3.Bucket != "" {
		backupSvc = services.NewBackupService(cfg.Database.URL, cfg.Backup.S3, cfg.Backup.Schedule)
		go backupSvc.StartScheduler(ctx)
	}

	srv := server.NewServer(cfg, db, rdb, backupSvc)
	if err := srv.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}

	trafficReset.Stop()
	expiryCheck.Stop()
	nodeMonitor.Stop()
	if backupSvc != nil {
		backupSvc.Stop()
	}
}
