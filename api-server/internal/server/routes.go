package server

import (
	"os"
	"strings"
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

const signupCodeMaxRequestsPerIP = 5

func (s *Server) registerRoutes() {
	if os.Getenv("SWAGGER_ENABLED") != "false" {
		s.app.Get("/swagger/*", swagger.HandlerDefault)
	}

	api := s.app.Group("/api/v1")
	api.Get("/promo/:code", handlers.NewPromoLandingHandler(s.db).Get)

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

	adminAccounts := handlers.NewAdminAccountsHandler(s.db)
	admin.Get("/me", adminAccounts.Me)
	admin.Patch("/me", adminAccounts.UpdateMe)
	admin.Post("/me/password", loginLimiter, adminAccounts.ChangeMyPassword(adminAuthHandler.IssueToken))
	admin.Get("/admins/directory", adminAccounts.Directory)
	owners := admin.Group("/admins", middleware.RequireAdminOwner())
	owners.Get("", adminAccounts.List)
	owners.Post("", adminAccounts.Create)
	owners.Patch("/:id", adminAccounts.Update)
	owners.Post("/:id/password", adminAccounts.ResetPassword)
	owners.Delete("/:id", adminAccounts.Delete)

	admin2FA := handlers.NewAdmin2FAHandler(s.db)
	twoFA := admin.Group("/auth/2fa")
	twoFA.Get("/setup", admin2FA.Setup)
	twoFA.Get("/status", admin2FA.Status)
	twoFA.Post("/enable", admin2FA.Enable)
	twoFA.Post("/disable", admin2FA.Disable)

	adminNodeHandler := handlers.NewAdminNodeHandler(s.db, s.redis, s.config.Server.PanelURL)
	adminNodes := admin.Group("/nodes")
	adminNodes.Post("/token", adminNodeHandler.GenerateToken)
	adminNodes.Get("/", adminNodeHandler.ListNodes)
	// Registered before "/:id" so the literal path is not captured as a node id.
	adminNodes.Get("/event-filters", adminNodeHandler.GetNodeEventFilters)
	adminNodes.Get("/:id", adminNodeHandler.GetNode)
	adminNodes.Put("/:id", adminNodeHandler.UpdateNode)
	adminNodes.Put("/:id/reality-target", adminNodeHandler.SetRealityTarget)
	adminNodes.Delete("/:id", adminNodeHandler.DeleteNode)
	adminNodes.Get("/:id/xray", adminNodeHandler.GetXrayVersion)
	adminNodes.Post("/:id/xray/update", adminNodeHandler.UpdateXray)
	adminNodes.Get("/:id/tls", adminNodeHandler.GetTLSStatus)
	adminNodes.Post("/:id/tls/issue", adminNodeHandler.IssueCertificate)
	adminNodes.Get("/:id/metrics", adminNodeHandler.GetMetricsHistory)
	adminNodes.Get("/:id/events", adminNodeHandler.GetNodeEvents)

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

	adminNodeChainHandler := handlers.NewAdminNodeChainHandler(s.db)
	nodeChains := admin.Group("/node-chains")
	nodeChains.Get("/", adminNodeChainHandler.List)
	nodeChains.Post("/", adminNodeChainHandler.Create)
	nodeChains.Post("/batch", adminNodeChainHandler.Batch)
	nodeChains.Patch("/:id", adminNodeChainHandler.Update)
	nodeChains.Delete("/:id", adminNodeChainHandler.Delete)
	nodeChains.Put("/:id/groups", adminNodeChainHandler.SetGroups)

	adminPlanHandler := handlers.NewAdminPlanHandler(s.db)
	plans := admin.Group("/plans")
	plans.Post("/", adminPlanHandler.Create)
	plans.Get("/", adminPlanHandler.List)
	plans.Get("/:id", adminPlanHandler.Get)
	plans.Put("/:id", adminPlanHandler.Update)
	plans.Delete("/:id", adminPlanHandler.Delete)
	plans.Get("/:id/routes", adminPlanHandler.GetRoutes)
	plans.Put("/:id/routes", adminPlanHandler.SetRoutes)

	adminUserHandler := handlers.NewAdminUserHandler(s.db)
	adminUsers := admin.Group("/users")
	adminUsers.Post("/", adminUserHandler.Create)
	adminUsers.Get("/", adminUserHandler.List)
	adminUsers.Get("/:id", adminUserHandler.Get)
	adminUsers.Get("/:id/traffic", adminUserHandler.GetTraffic)
	adminUsers.Get("/:id/client-addresses", handlers.NewAdminClientAddressHandler(s.db, s.redis).Get)
	adminUsers.Get("/:id/tunnel-sessions", handlers.NewAdminTunnelSessionHandler(s.db, s.redis).Get)
	adminUsers.Get("/:id/login-history", adminUserHandler.GetLoginHistory)
	adminUsers.Put("/:id", adminUserHandler.Update)
	adminUsers.Delete("/:id", adminUserHandler.Delete)
	adminUsers.Post("/:id/reset-traffic", adminUserHandler.ResetTraffic)
	resetTicketHandler := handlers.NewResetTicketHandler(s.db)
	adminUsers.Get("/:id/reset-tickets", resetTicketHandler.ListForUser)
	adminUsers.Delete("/:id/reset-tickets/:ticketId", resetTicketHandler.Revoke)
	adminPlanGrantHandler := handlers.NewAdminPlanGrantHandler(s.db)
	adminUsers.Get("/:id/orders", adminPlanGrantHandler.ListOrders)
	adminUsers.Post("/:id/plan-grants", adminPlanGrantHandler.Grant)
	adminUsers.Get("/:id/orders/:orderId/receipt", handlers.NewUserCheckoutHandler(s.db, s.payments).AdminReceipt)

	adminOrderHandler := handlers.NewAdminOrderHandler(s.db)
	adminOrders := admin.Group("/orders")
	adminOrders.Get("/", adminOrderHandler.List)
	adminOrders.Get("/:id/audit", adminOrderHandler.Audit)
	adminOrders.Post("/:id/pay", adminOrderHandler.MarkPaid)
	adminOrders.Post("/:id/cancel", adminOrderHandler.Cancel)

	adminPromotionHandler := handlers.NewAdminPromotionHandler(s.db)
	adminPromotions := admin.Group("/promotions")
	adminPromotions.Get("/", adminPromotionHandler.List)
	adminPromotions.Post("/", adminPromotionHandler.Create)
	adminPromotions.Put("/:id", adminPromotionHandler.Update)
	adminPromotions.Delete("/:id", adminPromotionHandler.Delete)

	admin.Get("/reset-tickets", resetTicketHandler.ListGrants)

	oauthHandler := handlers.NewOAuthHandler(s.db, s.config.Server.PanelURL)
	oauthApps := admin.Group("/oauth-apps")
	oauthApps.Get("/", oauthHandler.ListClients)
	oauthApps.Get("/scopes", oauthHandler.Scopes)
	oauthApps.Post("/", oauthHandler.CreateClient)
	oauthApps.Put("/:id", oauthHandler.UpdateClient)
	oauthApps.Post("/:id/secret", oauthHandler.RotateSecret)
	oauthApps.Delete("/:id", oauthHandler.DeleteClient)
	oauthApps.Get("/:id/users", oauthHandler.ListAppUsers)
	oauthApps.Delete("/:id/users", oauthHandler.RevokeAllAppUsers)
	oauthApps.Delete("/:id/users/:user_id", oauthHandler.RevokeAppUser)
	admin.Post("/reset-tickets", resetTicketHandler.Grant)

	adminSettingsHandler := handlers.NewAdminSettingsHandler(s.db)
	admin.Get("/settings", adminSettingsHandler.List)
	admin.Put("/settings", adminSettingsHandler.Update)
	settingsTestLimiter := limiter.New(limiter.Config{
		Max: 10, Expiration: time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string { return "settings-test:" + c.IP() },
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "too many connection tests, try again in a minute"})
		},
	})
	admin.Post("/settings/test/s3", settingsTestLimiter, adminSettingsHandler.TestS3)
	admin.Post("/settings/test/telegram", settingsTestLimiter, adminSettingsHandler.TestTelegram)

	adminAnnouncementHandler := handlers.NewAdminAnnouncementHandler(s.db)
	freeTrials := handlers.NewAdminFreeTrialHandler(s.db)
	admin.Get("/free-trials", freeTrials.List)
	admin.Post("/free-trials", freeTrials.Create)
	admin.Put("/free-trials/:id", freeTrials.Update)
	admin.Delete("/free-trials/:id", freeTrials.Delete)
	affiliates := handlers.NewAdminAffiliateHandler(s.db)
	admin.Get("/affiliates", affiliates.List)
	admin.Post("/affiliates", affiliates.Create)
	admin.Put("/affiliates/:id", affiliates.Update)
	admin.Delete("/affiliates/:id", affiliates.Delete)
	admin.Get("/affiliates/:id/commissions", affiliates.Commissions)
	admin.Put("/affiliates/:id/commissions/:commissionId/paid", affiliates.MarkPaid)

	adminAnnouncements := admin.Group("/announcements")
	adminAnnouncements.Get("/", adminAnnouncementHandler.List)
	adminAnnouncements.Post("/", adminAnnouncementHandler.Create)
	adminAnnouncements.Post("/preview", adminAnnouncementHandler.Preview)
	adminAnnouncements.Put("/:id", adminAnnouncementHandler.Update)
	adminAnnouncements.Delete("/:id", adminAnnouncementHandler.Delete)

	guideHandler := handlers.NewGuideHandler(s.db)
	adminGuides := admin.Group("/guides")
	adminGuides.Get("/", guideHandler.ListGuides)
	supportHandler := handlers.NewSupportHandler(s.db, s.mailer, s.config.Server.PanelURL)
	adminSupport := admin.Group("/support")
	adminSupport.Get("/summary", supportHandler.AdminSummary)
	adminSupport.Get("/tickets", supportHandler.AdminListTickets)
	adminSupport.Get("/tickets/:id", supportHandler.AdminGetTicket)
	adminSupport.Post("/tickets/:id/messages", supportHandler.AdminReply)
	adminSupport.Patch("/tickets/:id", supportHandler.AdminUpdateTicket)
	adminSupport.Delete("/tickets/:id", supportHandler.AdminDeleteTicket)
	adminSupport.Get("/categories", supportHandler.AdminListCategories)
	adminSupport.Post("/categories", supportHandler.AdminCreateCategory)
	adminSupport.Put("/categories/:id", supportHandler.AdminUpdateCategory)
	adminSupport.Delete("/categories/:id", supportHandler.AdminDeleteCategory)
	adminSupport.Get("/faqs", supportHandler.AdminListFAQs)
	adminSupport.Post("/faqs", supportHandler.AdminCreateFAQ)
	adminSupport.Put("/faqs/:id", supportHandler.AdminUpdateFAQ)
	adminSupport.Delete("/faqs/:id", supportHandler.AdminDeleteFAQ)

	adminGuides.Post("/", guideHandler.CreateGuide)
	adminGuides.Post("/preview", guideHandler.Preview)
	adminGuides.Get("/categories", guideHandler.ListCategories)
	adminGuides.Post("/categories", guideHandler.CreateCategory)
	adminGuides.Put("/categories/:id", guideHandler.UpdateCategory)
	adminGuides.Delete("/categories/:id", guideHandler.DeleteCategory)
	adminGuides.Put("/:id", guideHandler.UpdateGuide)
	adminGuides.Delete("/:id", guideHandler.DeleteGuide)

	adminUploadHandler := handlers.NewAdminUploadHandler(&s.config.Storage, s.config.Server.PanelURL)
	admin.Post("/uploads/image", adminUploadHandler.UploadImage)

	adminStatsHandler := handlers.NewAdminStatsHandler(s.db, services.NewOnlineTracker(s.redis))
	admin.Get("/stats", adminStatsHandler.GetDashboardStats)
	admin.Get("/stats/traffic-history", adminStatsHandler.GetTrafficHistory)
	admin.Get("/stats/node-traffic", adminStatsHandler.GetNodeTraffic)
	admin.Get("/stats/alerts", adminStatsHandler.GetAlerts)
	admin.Get("/activity", adminStatsHandler.GetActivity)
	admin.Get("/online-users", adminStatsHandler.GetOnlineUsers)
	adminEvictions := handlers.NewAdminUUIDEvictionHandler(s.db)
	admin.Get("/uuid-evictions", adminEvictions.List)
	admin.Get("/concurrency-enforcement", handlers.GetConcurrencyEnforcement)
	admin.Post("/uuid-evictions/:uuid/retry", adminEvictions.Retry)
	admin.Post("/devices/:id/terminate", adminStatsHandler.TerminateSession)

	adminAlertHandler := handlers.NewAdminAlertHandler(s.db)
	admin.Patch("/alerts/:id", adminAlertHandler.Patch)

	adminSubDomainHandler := handlers.NewAdminSubscriptionDomainHandler(s.db)
	subDomains := admin.Group("/subscription-domains")
	subDomains.Get("/", adminSubDomainHandler.List)
	subDomains.Post("/", adminSubDomainHandler.Create)
	subDomains.Get("/health", adminSubDomainHandler.Health)
	subDomains.Put("/:id", adminSubDomainHandler.Update)
	subDomains.Delete("/:id", adminSubDomainHandler.Delete)

	adminRoutingHandler := handlers.NewAdminRoutingHandler(s.db)
	routingProfile := admin.Group("/routing-profile")
	routingProfile.Get("/", adminRoutingHandler.Get)
	routingProfile.Put("/", adminRoutingHandler.Update)
	routingProfile.Delete("/", adminRoutingHandler.Reset)
	routingProfile.Post("/preview", adminRoutingHandler.Preview)
	admin.Get("/node-tags", adminRoutingHandler.NodeTags)
	admin.Get("/rule-sets/catalog", adminRoutingHandler.RuleSetCatalog)
	admin.Get("/rule-sets/contents", adminRoutingHandler.RuleSetContents)
	admin.Post("/rule-sets/decode", adminRoutingHandler.DecodeRuleSet)

	if s.backupService != nil {
		adminBackupHandler := handlers.NewAdminBackupHandler(s.backupService)
		admin.Post("/backup/trigger", adminBackupHandler.TriggerBackup)
		admin.Get("/backup/list", adminBackupHandler.ListBackups)
		admin.Get("/backup/download", adminBackupHandler.DownloadBackup)
	}

	nodeAgentHandler := handlers.NewNodeAgentHandler(s.db, s.redis, services.ManagedEntryDNSIntentConfig{
		Enabled: s.config.ManagedEntryDNS.Active(), ZoneID: s.config.ManagedEntryDNS.ZoneID,
		BaseDomain: s.config.ManagedEntryDNS.BaseDomain,
	})
	api.Post("/nodes/register", nodeAgentHandler.Register)

	nodeAgent := api.Group("/nodes/:id")
	nodeAgent.Use(middleware.NodeAPIKeyMiddleware(s.db))
	nodeAgent.Delete("/", nodeAgentHandler.Unregister)
	nodeAgent.Get("/config", nodeAgentHandler.Config)
	nodeAgent.Get("/config/digest", nodeAgentHandler.ConfigDigest)
	nodeAgent.Post("/heartbeat", nodeAgentHandler.Heartbeat)
	nodeAgent.Post("/stats", nodeAgentHandler.Stats)
	nodeAgent.Post("/bandwidth/permit", nodeAgentHandler.BandwidthPermit)
	nodeAgent.Post("/active-uuids", nodeAgentHandler.ActiveUUIDReport)
	// Literal path registered separately; both remain under node authentication.
	nodeAgent.Post("/active-uuids/generation", nodeAgentHandler.BeginActiveUUIDGeneration)
	nodeAgent.Post("/devices/admit", nodeAgentHandler.AdmitDevice)
	nodeAgent.Get("/revoked-devices", nodeAgentHandler.RevokedDevices)
	nodeAgent.Post("/revoked-devices/ack", nodeAgentHandler.AcknowledgeRevocation)
	nodeAgent.Get("/inbounds", nodeAgentHandler.GetInbounds)
	nodeAgent.Get("/wireguard/peers", nodeAgentHandler.GetWireGuardPeers)
	nodeAgent.Get("/relay-rules", nodeAgentHandler.GetRelayRules)
	nodeAgent.Get("/exit-rules", nodeAgentHandler.GetExitRules)
	nodeAgent.Get("/role", nodeAgentHandler.GetRole)
	nodeAgent.Get("/hysteria2/users", nodeAgentHandler.GetHysteria2Users)
	nodeAgent.Get("/tls-domain", nodeAgentHandler.GetTLSDomain)
	nodeAgent.Post("/tls-cert", nodeAgentHandler.ReportTLSCert)
	nodeAgent.Get("/update", nodeAgentHandler.CheckUpdate)
	nodeAgent.Get("/update/download", nodeAgentHandler.DownloadUpdate)
	nodeAgent.Get("/xray-update", nodeAgentHandler.CheckXrayUpdate)

	userAuthHandler := handlers.NewUserAuthHandler(s.db, s.config.JWT.Secret, s.parseUserExpiry(), services.NewTelegramService(s.db, s.config.Telegram), s.mailer, s.config.Server.PanelURL)
	if wa := passkeyWebAuthn(s.config.Server.PanelURL); wa != nil {
		userAuthHandler.SetPasskeyServices(s.redis, wa, strings.HasPrefix(s.config.Server.PanelURL, "https://"))
	}
	if s.testMailerCodeLookup {
		// Only the explicitly enabled fake sender can expose codes, never SMTP.
		if fakeMailer, ok := s.mailer.(*testSignupMailer); ok {
			adminTest := api.Group("/__test__", middleware.AdminJWTMiddleware(s.config.JWT.Secret, s.db))
			adminTest.Get("/signup-email-code", func(c *fiber.Ctx) error {
				c.Set(fiber.HeaderCacheControl, "no-store")
				email := strings.ToLower(strings.TrimSpace(c.Query("email")))
				if email == "" {
					return c.SendStatus(fiber.StatusBadRequest)
				}
				code, found := fakeMailer.LatestSignupCode(c.UserContext(), email)
				if !found {
					return c.SendStatus(fiber.StatusNotFound)
				}
				return c.JSON(fiber.Map{"code": code})
			})
		}
	}

	auth := api.Group("/auth")
	signupCodeLimiter := limiter.New(limiter.Config{Max: signupCodeMaxRequestsPerIP, Expiration: 15 * time.Minute, KeyGenerator: func(c *fiber.Ctx) string { return c.IP() }, LimitReached: func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "too many verification code requests; try again later"})
	}})
	auth.Post("/register/request-code", signupCodeLimiter, userAuthHandler.RequestSignupEmailCode)
	// Sending and verifying have independent budgets so requesting a code does
	// not consume the attempts needed to verify it. Both verification aliases
	// share the same limiter; the database also bounds guesses per challenge.
	signupVerifyLimiter := limiter.New(limiter.Config{Max: 10, Expiration: 15 * time.Minute, KeyGenerator: func(c *fiber.Ctx) string { return c.IP() }})
	auth.Post("/register/verify-code", signupVerifyLimiter, userAuthHandler.VerifySignupEmailCode)
	auth.Post("/register", signupVerifyLimiter, userAuthHandler.Register)
	auth.Get("/signup-policy", userAuthHandler.SignupPolicy)
	auth.Post("/login", loginLimiter, userAuthHandler.Login)
	auth.Post("/passkey/login/begin", loginLimiter, userAuthHandler.BeginPasskeyLogin)
	auth.Post("/passkey/login/finish", loginLimiter, userAuthHandler.FinishPasskeyLogin)

	// Password reset is stricter than login: each accepted request can send an
	// email, and the reset endpoint guesses at 256-bit tokens. Both are keyed
	// by client address (c.IP(), which only honours the proxy header from a
	// trusted proxy) on top of the global 100 req/min limiter.
	forgotPasswordLimiter := limiter.New(limiter.Config{
		Max:        5,
		Expiration: 15 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many password reset requests, try again later",
			})
		},
	})
	resetPasswordLimiter := limiter.New(limiter.Config{
		Max:        10,
		Expiration: 15 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many password reset attempts, try again later",
			})
		},
	})
	auth.Post("/forgot-password", forgotPasswordLimiter, userAuthHandler.ForgotPassword)
	auth.Post("/reset-password", resetPasswordLimiter, userAuthHandler.ResetPassword)

	passkeyRegistrationLimiter := limiter.New(limiter.Config{
		Max: 10, Expiration: time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string { return c.IP() },
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "too many passkey requests, try again later"})
		},
	})

	user := api.Group("/user")
	user.Use(middleware.UserJWTMiddleware(s.config.JWT.Secret, s.db))

	userPlanHandler := handlers.NewUserPlanHandler(s.db)
	user.Get("/plans", userPlanHandler.ListPlans)
	user.Get("/nodes", userPlanHandler.ListNodes)

	userOrderHandler := handlers.NewUserOrderHandler(s.db, s.config.Payments, s.payments)
	user.Post("/orders", userOrderHandler.Create)
	user.Get("/orders", userOrderHandler.List)
	user.Post("/orders/:id/cancel", userOrderHandler.Cancel)
	user.Patch("/orders/:id/promotion", userOrderHandler.ApplyPromotion)
	user.Post("/free-trials/redeem", handlers.NewUserFreeTrialHandler(s.db).Redeem)

	userCheckoutHandler := handlers.NewUserCheckoutHandler(s.db, s.payments)
	user.Post("/orders/:id/checkout", userCheckoutHandler.Start)
	user.Post("/orders/:id/checkout/confirm", userCheckoutHandler.Confirm)
	user.Get("/payment-providers", userCheckoutHandler.ListProviders)
	user.Get("/orders/:id/receipt", userCheckoutHandler.Receipt)

	userDeviceHandler := handlers.NewUserDeviceHandler(s.db)
	user.Post("/devices", userDeviceHandler.Create)
	user.Get("/devices", userDeviceHandler.List)
	user.Delete("/devices/:id", userDeviceHandler.Delete)

	userSubDomainHandler := handlers.NewUserSubscriptionDomainHandler(s.db)
	user.Get("/subscription-domains", userSubDomainHandler.List)

	userPortalHandler := handlers.NewUserPortalHandler(s.db, s.redis)
	user.Get("/summary", userPortalHandler.GetSummary)
	user.Get("/reset-tickets", resetTicketHandler.ListMine)
	user.Get("/reset-tickets/history", resetTicketHandler.ListMyHistory)
	user.Post("/reset-tickets/:id/redeem", resetTicketHandler.Redeem)
	user.Get("/profile", userPortalHandler.GetProfile)
	user.Get("/passkeys", userAuthHandler.ListPasskeys)
	user.Post("/passkeys/register/begin", passkeyRegistrationLimiter, userAuthHandler.BeginPasskeyRegistration)
	user.Post("/passkeys/register/finish", passkeyRegistrationLimiter, userAuthHandler.FinishPasskeyRegistration)
	user.Delete("/passkeys/:id", userAuthHandler.DeletePasskey)
	user.Put("/profile", userPortalHandler.UpdateProfile)
	user.Get("/traffic", userPortalHandler.GetTrafficStats)
	user.Post("/sub-token/regenerate", userPortalHandler.RegenerateSubToken)
	user.Get("/announcements", userPortalHandler.ListLocalizedAnnouncements)
	userGuideHandler := handlers.NewGuideHandler(s.db)
	user.Get("/guides", userGuideHandler.ListPublished)
	user.Get("/guides/:id", userGuideHandler.GetPublished)

	user.Get("/oauth/authorize", oauthHandler.AuthorizeInfo)
	user.Post("/oauth/authorize", oauthHandler.AuthorizeDecision)
	user.Get("/oauth/grants", oauthHandler.ListGrants)
	user.Delete("/oauth/grants/:client_id", oauthHandler.RevokeGrant)

	// Token endpoints authenticate the client app, not a user session, and are
	// limited per address so a secret cannot be guessed at line rate.
	oauthTokenLimiter := limiter.New(limiter.Config{
		Max: 60, Expiration: time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string { return c.IP() },
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "slow_down", "error_description": "too many token requests"})
		},
	})
	oauthPublic := api.Group("/oauth")
	oauthPublic.Post("/token", oauthTokenLimiter, oauthHandler.Token)
	oauthPublic.Post("/revoke", oauthTokenLimiter, oauthHandler.Revoke)

	// /api/v1/client is the OAuth resource API: the portal's own handlers,
	// each behind the scope that covers it.
	clientAPI := api.Group("/client", middleware.OAuthBearerMiddleware(oauthHandler.Service()))
	clientAPI.Get("/profile", middleware.RequireScope(services.ScopeProfile), oauthHandler.ClientProfile)
	clientAPI.Get("/subscription", middleware.RequireScope(services.ScopeSubscriptionRead), oauthHandler.ClientSubscription)
	clientAPI.Get("/nodes", middleware.RequireScope(services.ScopeNodesRead), userPlanHandler.ListNodes)
	clientAPI.Get("/summary", middleware.RequireScope(services.ScopePlanRead), userPortalHandler.GetSummary)
	clientAPI.Get("/reset-tickets", middleware.RequireScope(services.ScopeResetTicketsRead), resetTicketHandler.ListMine)
	clientAPI.Get("/reset-tickets/history", middleware.RequireScope(services.ScopeResetTicketsRead), resetTicketHandler.ListMyHistory)
	clientAPI.Post("/reset-tickets/:id/redeem", middleware.RequireScope(services.ScopeResetTicketsRedeem), resetTicketHandler.Redeem)

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
	sub.Get("/:sub_token", subLimiter, subscriptionHandler.GetAccountSubscription)

	userSupportLimiter := limiter.New(limiter.Config{
		Max: 30, Expiration: time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string { return c.Locals("user_id").(string) },
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "too many support requests, try again shortly"})
		},
	})
	userSupport := user.Group("/support")
	userSupport.Get("/faq", supportHandler.ListFAQ)
	userSupport.Get("/tickets", supportHandler.ListTickets)
	userSupport.Post("/tickets", userSupportLimiter, supportHandler.CreateTicket)
	userSupport.Get("/tickets/:id", supportHandler.GetTicket)
	userSupport.Post("/tickets/:id/messages", userSupportLimiter, supportHandler.PostMessage)
	userSupport.Post("/tickets/:id/close", supportHandler.CloseTicket)

	// Outside /api/v1 and outside any JWT-protected group: a hosted payment
	// provider authenticates its callback with a signature, not a bearer
	// token, and the rate limiter above already exempts this prefix.
	paymentWebhookHandler := handlers.NewPaymentWebhookHandler(s.db, s.payments)
	webhooks := s.app.Group("/webhooks/payments")
	webhooks.Post("/:provider", paymentWebhookHandler.Receive)

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

	s.app.Static("/uploads", "/app/uploads", fiber.Static{
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
