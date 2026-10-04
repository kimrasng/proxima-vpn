package server

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/redis/go-redis/v9"
)

type Server struct {
	app           *fiber.App
	config        *config.Config
	db            *pgxpool.Pool
	redis         *redis.Client
	backupService *services.BackupService
	payments      payments.Registry
}

// fiberConfig builds the Fiber config, including the trusted-proxy settings
// that decide what c.IP() reports. EnableTrustedProxyCheck is always on: it is
// what confines ProxyHeader to requests that really came from a configured
// proxy, so an untrusted peer's forwarded header is ignored rather than used as
// a fallback. EnableIPValidation makes c.IP() skip unparseable leading entries
// in the header - a hop-by-hop list starts with whatever the client claimed -
// instead of storing the raw value.
func fiberConfig(cfg *config.Config) fiber.Config {
	return fiber.Config{
		BodyLimit:               10 * 1024 * 1024,
		ProxyHeader:             cfg.Server.ProxyHeader,
		EnableTrustedProxyCheck: true,
		TrustedProxies:          cfg.Server.TrustedProxies,
		EnableIPValidation:      true,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{
				"error": err.Error(),
			})
		},
	}
}

// NewServer creates a Fiber app with standard middleware.
func NewServer(cfg *config.Config, db *pgxpool.Pool, rdb *redis.Client, backupSvc *services.BackupService, paymentProviders payments.Registry) *Server {
	app := fiber.New(fiberConfig(cfg))

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

	// Global rate limiter: 100 req/min per IP. Payment webhooks are exempt -
	// Stripe (and any future hosted provider) retries aggressively from a
	// small set of source IPs, and throttling those retries only delays a
	// legitimate confirmation rather than blocking abuse.
	permitGuard, permitAuth := bandwidthAuthentication(db)
	app.Use(permitGuard, permitAuth)
	app.Use(limiter.New(limiter.Config{
		Next: func(c *fiber.Ctx) bool {
			return strings.HasPrefix(c.Path(), "/webhooks/payments/") || isBandwidthPermitRequest(c)
		},
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
		payments:      paymentProviders,
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
