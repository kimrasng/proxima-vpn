package server

import (
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/swagger"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/middleware"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func (s *Server) registerRoutes() {
	if os.Getenv("SWAGGER_ENABLED") != "false" {
		s.app.Get("/swagger/*", swagger.HandlerDefault)
	}

	api := s.app.Group("/api/v1")

	loginLimiter := limiter.New(limiter.Config{
		Max:        10,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many login attempts, try again later",
			})
		},
	})

	admin := api.Group("/admin")

	adminAuthHandler := handlers.NewAdminAuthHandler(s.db, s.config.JWT.Secret, s.parseAdminExpiry())
	admin.Post("/auth/login", loginLimiter, adminAuthHandler.Login)

	admin.Use(middleware.AdminJWTMiddleware(s.config.JWT.Secret, s.db))

	admin2FA := handlers.NewAdmin2FAHandler(s.db)
	twoFA := admin.Group("/auth/2fa")
	twoFA.Get("/setup", admin2FA.Setup)
	twoFA.Get("/status", admin2FA.Status)
	twoFA.Post("/enable", admin2FA.Enable)
	twoFA.Post("/disable", admin2FA.Disable)

	adminNodeHandler := handlers.NewAdminNodeHandler(s.db, s.config.Server.PanelURL)
	adminNodes := admin.Group("/nodes")
	adminNodes.Post("/token", adminNodeHandler.GenerateToken)
	adminNodes.Get("/", adminNodeHandler.ListNodes)
	adminNodes.Get("/:id", adminNodeHandler.GetNode)
	adminNodes.Put("/:id", adminNodeHandler.UpdateNode)
	adminNodes.Delete("/:id", adminNodeHandler.DeleteNode)
	adminNodes.Get("/:id/xray", adminNodeHandler.GetXrayVersion)
	adminNodes.Post("/:id/xray/update", adminNodeHandler.UpdateXray)
	adminNodes.Get("/:id/tls", adminNodeHandler.GetTLSStatus)
	adminNodes.Post("/:id/tls/issue", adminNodeHandler.IssueCertificate)

	adminInboundHandler := handlers.NewAdminInboundHandler(s.db)
	admin.Get("/nodes/:nodeId/inbounds", adminInboundHandler.List)
	admin.Post("/nodes/:nodeId/inbounds", adminInboundHandler.Create)
	admin.Put("/inbounds/:id", adminInboundHandler.Update)
	admin.Delete("/inbounds/:id", adminInboundHandler.Delete)
	admin.Put("/inbounds/:id/toggle", adminInboundHandler.Toggle)

	adminNodeGroupHandler := handlers.NewAdminNodeGroupHandler(s.db)
	nodeGroups := admin.Group("/node-groups")
	nodeGroups.Post("/", adminNodeGroupHandler.Create)
	nodeGroups.Get("/", adminNodeGroupHandler.List)
	nodeGroups.Get("/:id", adminNodeGroupHandler.Get)
	nodeGroups.Put("/:id", adminNodeGroupHandler.Update)
	nodeGroups.Delete("/:id", adminNodeGroupHandler.Delete)
	nodeGroups.Put("/:id/nodes", adminNodeGroupHandler.SetNodes)

	adminPlanHandler := handlers.NewAdminPlanHandler(s.db)
	plans := admin.Group("/plans")
	plans.Post("/", adminPlanHandler.Create)
	plans.Get("/", adminPlanHandler.List)
	plans.Get("/:id", adminPlanHandler.Get)
	plans.Put("/:id", adminPlanHandler.Update)
	plans.Delete("/:id", adminPlanHandler.Delete)

	adminUserTemplateHandler := handlers.NewAdminUserTemplateHandler(s.db)
	userTemplates := admin.Group("/user-templates")
	userTemplates.Post("/", adminUserTemplateHandler.Create)
	userTemplates.Get("/", adminUserTemplateHandler.List)
	userTemplates.Put("/:id", adminUserTemplateHandler.Update)
	userTemplates.Delete("/:id", adminUserTemplateHandler.Delete)

	adminUserHandler := handlers.NewAdminUserHandler(s.db)
	adminUsers := admin.Group("/users")
	adminUsers.Post("/", adminUserHandler.Create)
	adminUsers.Get("/", adminUserHandler.List)
	adminUsers.Get("/:id", adminUserHandler.Get)
	adminUsers.Put("/:id", adminUserHandler.Update)
	adminUsers.Delete("/:id", adminUserHandler.Delete)
	adminUsers.Post("/:id/reset-traffic", adminUserHandler.ResetTraffic)

	adminPlanRequestHandler := handlers.NewAdminPlanRequestHandler(s.db)
	adminPlanRequests := admin.Group("/plan-requests")
	adminPlanRequests.Get("/", adminPlanRequestHandler.List)
	adminPlanRequests.Put("/:id", adminPlanRequestHandler.Review)

	admin.Group("/stats")

	adminSettingsHandler := handlers.NewAdminSettingsHandler(s.db)
	admin.Get("/settings", adminSettingsHandler.List)
	admin.Put("/settings", adminSettingsHandler.Update)

	adminAnnouncementHandler := handlers.NewAdminAnnouncementHandler(s.db)
	adminAnnouncements := admin.Group("/announcements")
	adminAnnouncements.Get("/", adminAnnouncementHandler.List)
	adminAnnouncements.Post("/", adminAnnouncementHandler.Create)
	adminAnnouncements.Put("/:id", adminAnnouncementHandler.Update)
	adminAnnouncements.Delete("/:id", adminAnnouncementHandler.Delete)

	adminStatsHandler := handlers.NewAdminStatsHandler(s.db, services.NewOnlineTracker(s.redis))
	admin.Get("/stats", adminStatsHandler.GetDashboardStats)
	admin.Get("/stats/traffic-history", adminStatsHandler.GetTrafficHistory)
	admin.Get("/online-users", adminStatsHandler.GetOnlineUsers)

	if s.backupService != nil {
		adminBackupHandler := handlers.NewAdminBackupHandler(s.backupService)
		admin.Post("/backup/trigger", adminBackupHandler.TriggerBackup)
		admin.Get("/backup/list", adminBackupHandler.ListBackups)
		admin.Get("/backup/download", adminBackupHandler.DownloadBackup)
	}

	nodeAgentHandler := handlers.NewNodeAgentHandler(s.db, s.redis)
	api.Post("/nodes/register", nodeAgentHandler.Register)

	nodeAgent := api.Group("/nodes/:id")
	nodeAgent.Use(middleware.NodeAPIKeyMiddleware(s.db))
	nodeAgent.Get("/config", nodeAgentHandler.Config)
	nodeAgent.Post("/heartbeat", nodeAgentHandler.Heartbeat)
	nodeAgent.Post("/stats", nodeAgentHandler.Stats)
	nodeAgent.Get("/inbounds", nodeAgentHandler.GetInbounds)

	userAuthHandler := handlers.NewUserAuthHandler(s.db, s.config.JWT.Secret, s.parseUserExpiry())
	auth := api.Group("/auth")
	auth.Post("/register", userAuthHandler.Register)
	auth.Post("/login", loginLimiter, userAuthHandler.Login)

	user := api.Group("/user")
	user.Use(middleware.UserJWTMiddleware(s.config.JWT.Secret))

	userPlanHandler := handlers.NewUserPlanHandler(s.db)
	user.Post("/plan-requests", userPlanHandler.CreateRequest)
	user.Get("/plan-requests", userPlanHandler.ListRequests)
	user.Get("/plans", userPlanHandler.ListPlans)

	userDeviceHandler := handlers.NewUserDeviceHandler(s.db)
	user.Post("/devices", userDeviceHandler.Create)
	user.Get("/devices", userDeviceHandler.List)
	user.Delete("/devices/:id", userDeviceHandler.Delete)

	userPortalHandler := handlers.NewUserPortalHandler(s.db, s.redis)
	user.Get("/profile", userPortalHandler.GetProfile)
	user.Put("/profile", userPortalHandler.UpdateProfile)
	user.Get("/traffic", userPortalHandler.GetTrafficStats)
	user.Post("/sub-token/regenerate", userPortalHandler.RegenerateSubToken)
	user.Get("/announcements", userPortalHandler.ListAnnouncements)

	api.Group("/nodes")

	subscriptionHandler := handlers.NewSubscriptionHandler(s.db, s.config.Subscription.UpdateInterval)
	subLimiter := limiter.New(limiter.Config{
		Max:        60,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.Params("sub_token")
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "subscription rate limit exceeded",
			})
		},
	})
	sub := s.app.Group("/sub")
	sub.Get("/:sub_token/:device_id", subLimiter, subscriptionHandler.GetSubscription)

	s.app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	s.app.Get("/metrics", adaptor.HTTPHandler(promhttp.Handler()))

	s.app.Static("/scripts", "/app/scripts", fiber.Static{
		Browse: false,
	})

	s.app.Static("/downloads", "/app/downloads", fiber.Static{
		Browse: false,
	})
}

func (s *Server) parseAdminExpiry() time.Duration {
	d, err := time.ParseDuration(s.config.JWT.AdminExpiry)
	if err != nil {
		return 8 * time.Hour
	}
	return d
}

func (s *Server) parseUserExpiry() time.Duration {
	d, err := time.ParseDuration(s.config.JWT.UserExpiry)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}
