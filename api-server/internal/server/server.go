package server

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type Server struct {
	app           *fiber.App
	config        *config.Config
	db            *pgxpool.Pool
	redis         *redis.Client
	backupService *services.BackupService
}

// NewServer creates a Fiber app with standard middleware.
func NewServer(cfg *config.Config, db *pgxpool.Pool, rdb *redis.Client, backupSvc *services.BackupService) *Server {
	app := fiber.New(fiber.Config{
		BodyLimit: 10 * 1024 * 1024,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{
				"error": err.Error(),
			})
		},
	})

	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
	}))
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		Format:     "${time} | ${status} | ${latency} | ${ip} | ${method} | ${path} | ${error}\n",
		TimeFormat: "2006-01-02 15:04:05",
	}))

	// Security headers
	app.Use(func(c *fiber.Ctx) error {
		c.Set("X-Frame-Options", "DENY")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-XSS-Protection", "1; mode=block")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		return c.Next()
	})

	// Global rate limiter: 100 req/min per IP
	app.Use(limiter.New(limiter.Config{
		Max:        100,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "rate limit exceeded, try again later",
			})
		},
	}))

	// CORS based on PanelURL
	allowOrigins := "*"
	if cfg.Server.PanelURL != "" {
		allowOrigins = cfg.Server.PanelURL
	}
	app.Use(cors.New(cors.Config{
		AllowOrigins:     allowOrigins,
		AllowMethods:     "GET,POST,PUT,DELETE,PATCH,OPTIONS",
		AllowHeaders:     "Origin,Content-Type,Accept,Authorization",
		AllowCredentials: cfg.Server.PanelURL != "",
	}))

	s := &Server{
		app:           app,
		config:        cfg,
		db:            db,
		redis:         rdb,
		backupService: backupSvc,
	}

	s.registerRoutes()

	return s
}

// Start listens on the configured port and handles graceful shutdown.
func (s *Server) Start() error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		_ = s.app.Shutdown()
	}()

	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)
	return s.app.Listen(addr)
}

// App returns the underlying Fiber app (useful for testing).
func (s *Server) App() *fiber.App {
	return s.app
}
