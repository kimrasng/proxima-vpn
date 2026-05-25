package telegram

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// BotService handles Telegram bot commands and interactions.
type BotService struct {
	bot      *tgbotapi.BotAPI
	db       *pgxpool.Pool
	rdb      *redis.Client
	chatID   string
	panelURL string
}

// NewBotService creates a new Telegram bot service.
func NewBotService(cfg config.TelegramConfig, db *pgxpool.Pool, rdb *redis.Client, panelURL string) (*BotService, error) {
	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return nil, err
	}

	log.Printf("telegram bot authorized as @%s", bot.Self.UserName)

	return &BotService{
		bot:      bot,
		db:       db,
		rdb:      rdb,
		chatID:   cfg.ChatID,
		panelURL: panelURL,
	}, nil
}

// Start begins polling for updates and processing commands.
// It runs the update loop in a goroutine and returns immediately.
func (s *BotService) Start(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := s.bot.GetUpdatesChan(u)

	go func() {
		for {
			select {
			case <-ctx.Done():
				s.bot.StopReceivingUpdates()
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				if update.CallbackQuery != nil {
					s.handleCallback(update.CallbackQuery)
					continue
				}
				if update.Message == nil || !update.Message.IsCommand() {
					continue
				}
				s.handleCommand(update.Message)
			}
		}
	}()

	go s.startTrafficAlertLoop(ctx)
}

func (s *BotService) isAdmin(chatID int64) bool {
	return fmt.Sprintf("%d", chatID) == s.chatID
}

func (s *BotService) sendReply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	if _, err := s.bot.Send(msg); err != nil {
		log.Printf("telegram bot send error: %v", err)
	}
}

func (s *BotService) handleCommand(msg *tgbotapi.Message) {
	// Non-admin commands — available to all users
	switch msg.Command() {
	case "start":
		if s.isAdmin(msg.Chat.ID) {
			s.sendReply(msg.Chat.ID, "👋 Welcome to Proxima VPN Bot!\n\n"+
				"I can help you manage your VPN account, check connection status, and more.\n\n"+
				"Use /help to see available commands.")
		} else {
			s.sendReply(msg.Chat.ID, "👋 Welcome to Proxima VPN Bot!\n\n"+
				"Link your VPN account with:\n<code>/link &lt;sub_token&gt;</code>\n\n"+
				"Then use /mysub and /mystatus to manage your subscription.")
		}
		return
	case "link":
		s.handleLink(msg)
		return
	case "mysub":
		s.handleMySub(msg)
		return
	case "mystatus":
		s.handleMyStatus(msg)
		return
	}

	// Admin-only commands
	if !s.isAdmin(msg.Chat.ID) {
		s.sendReply(msg.Chat.ID, "⛔ Unauthorized.")
		return
	}

	switch msg.Command() {
	case "help":
		s.sendReply(msg.Chat.ID, "📋 <b>Available Commands</b>\n\n"+
			"/users - List users\n"+
			"/user &lt;email&gt; - Show user details\n"+
			"/adduser &lt;email&gt; &lt;password&gt; &lt;name&gt; - Create user\n"+
			"/deluser &lt;email&gt; - Delete user\n"+
			"/enable &lt;email&gt; - Enable user\n"+
			"/disable &lt;email&gt; - Disable user\n"+
			"/setplan &lt;email&gt; &lt;plan_name&gt; - Assign plan to user\n"+
			"/traffic &lt;email&gt; - Show user traffic usage\n"+
			"/stats - Dashboard statistics")
	case "users":
		s.handleUsersPage(msg.Chat.ID, 1)
	case "user":
		s.handleUser(msg)
	case "adduser":
		s.handleAddUser(msg)
	case "deluser":
		s.handleDelUser(msg)
	case "enable":
		s.handleEnable(msg)
	case "disable":
		s.handleDisable(msg)
	case "stats":
		s.handleStats(msg)
	case "setplan":
		s.handleSetPlan(msg)
	case "traffic":
		s.handleTraffic(msg)
	default:
		s.sendReply(msg.Chat.ID, "Unknown command. Use /help to see available commands.")
	}
}

func (s *BotService) handleUsersPage(chatID int64, page int) {
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * 10
	ctx := context.Background()

	rows, err := s.db.Query(ctx,
		`SELECT email, name, status, is_active FROM users ORDER BY created_at DESC LIMIT 10 OFFSET $1`, offset)
	if err != nil {
		s.sendReply(chatID, "❌ Failed to query users.")
		return
	}
	defer rows.Close()

	var lines []string
	i := offset + 1
	for rows.Next() {
		var email, name, status string
		var isActive bool
		if err := rows.Scan(&email, &name, &status, &isActive); err != nil {
			continue
		}
		emoji := statusEmoji(status, isActive)
		lines = append(lines, fmt.Sprintf("%d. %s %s (%s)", i, emoji, email, name))
		i++
	}

	if len(lines) == 0 {
		s.sendReply(chatID, "📭 No users found.")
		return
	}

	text := fmt.Sprintf("👥 <b>Users</b> (page %d)\n\n%s", page, strings.Join(lines, "\n"))

	var rowButtons []tgbotapi.InlineKeyboardButton
	if page > 1 {
		rowButtons = append(rowButtons, tgbotapi.NewInlineKeyboardButtonData("◀ Prev", fmt.Sprintf("users_page:%d", page-1)))
	}
	if len(lines) == 10 {
		rowButtons = append(rowButtons, tgbotapi.NewInlineKeyboardButtonData("Next ▶", fmt.Sprintf("users_page:%d", page+1)))
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	if len(rowButtons) > 0 {
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(rowButtons...))
	}
	if _, err := s.bot.Send(msg); err != nil {
		log.Printf("telegram bot send error: %v", err)
	}
}

func (s *BotService) handleLink(msg *tgbotapi.Message) {
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 1 {
		s.sendReply(msg.Chat.ID, "Usage: /link &lt;sub_token&gt;")
		return
	}
	subToken := args[0]
	ctx := context.Background()

	var userID string
	err := s.db.QueryRow(ctx, `SELECT id FROM users WHERE sub_token = $1`, subToken).Scan(&userID)
	if err != nil {
		s.sendReply(msg.Chat.ID, "❌ Invalid token.")
		return
	}

	_, err = s.db.Exec(ctx, `UPDATE users SET telegram_id = $1, updated_at = NOW() WHERE id = $2`, msg.Chat.ID, userID)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			s.sendReply(msg.Chat.ID, "❌ This Telegram account is already linked.")
			return
		}
		s.sendReply(msg.Chat.ID, "❌ Failed to link account.")
		return
	}

	s.sendReply(msg.Chat.ID, "✅ Account linked! Use /mysub to get your subscription URL.")
}

func (s *BotService) handleMySub(msg *tgbotapi.Message) {
	ctx := context.Background()

	var subToken string
	var deviceID *string
	err := s.db.QueryRow(ctx,
		`SELECT u.sub_token, d.id FROM users u LEFT JOIN devices d ON d.user_id = u.id WHERE u.telegram_id = $1 LIMIT 1`,
		msg.Chat.ID,
	).Scan(&subToken, &deviceID)
	if err != nil {
		s.sendReply(msg.Chat.ID, "Use /link &lt;sub_token&gt; to connect your account.")
		return
	}
	if deviceID == nil {
		s.sendReply(msg.Chat.ID, "No devices registered. Add a device in the web panel.")
		return
	}

	url := fmt.Sprintf("%s/sub/%s/%s", s.panelURL, subToken, *deviceID)
	s.sendReply(msg.Chat.ID, "🔗 Your subscription URL:\n"+url)
}

func (s *BotService) handleMyStatus(msg *tgbotapi.Message) {
	ctx := context.Background()

	var name, status string
	var trafficUsed int64
	var trafficLimit *int64
	var planName *string
	var planExpiresAt *time.Time

	err := s.db.QueryRow(ctx,
		`SELECT u.name, u.status, u.traffic_used, p.traffic_limit, p.name, u.plan_expires_at
		 FROM users u LEFT JOIN plans p ON u.plan_id = p.id
		 WHERE u.telegram_id = $1`,
		msg.Chat.ID,
	).Scan(&name, &status, &trafficUsed, &trafficLimit, &planName, &planExpiresAt)
	if err != nil {
		s.sendReply(msg.Chat.ID, "Use /link &lt;sub_token&gt; to connect your account.")
		return
	}

	plan := "None"
	if planName != nil {
		plan = *planName
	}
	expires := "N/A"
	if planExpiresAt != nil {
		expires = planExpiresAt.Format("2006-01-02")
	}
	trafficLimitStr := "Unlimited"
	if trafficLimit != nil && *trafficLimit > 0 {
		trafficLimitStr = formatBytes(*trafficLimit)
	}

	text := fmt.Sprintf("📊 <b>Your Status</b>\n\n"+
		"👤 Name: %s\n"+
		"📦 Plan: %s\n"+
		"📈 Traffic: %s / %s\n"+
		"⏰ Expires: %s\n"+
		"🔵 Status: %s",
		name, plan, formatBytes(trafficUsed), trafficLimitStr, expires, status)

	s.sendReply(msg.Chat.ID, text)
}

func (s *BotService) handleUser(msg *tgbotapi.Message) {
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 1 {
		s.sendReply(msg.Chat.ID, "Usage: /user &lt;email&gt;")
		return
	}
	email := args[0]
	ctx := context.Background()

	var name, status string
	var isActive bool
	var trafficUsed int64
	var trafficLimit *int64
	var planName *string
	var planExpiresAt *time.Time
	var deviceCount int

	err := s.db.QueryRow(ctx,
		`SELECT u.name, u.status, u.is_active, u.traffic_used, p.traffic_limit,
		        p.name, u.plan_expires_at
		 FROM users u
		 LEFT JOIN plans p ON u.plan_id = p.id
		 WHERE u.email = $1`, email,
	).Scan(&name, &status, &isActive, &trafficUsed, &trafficLimit, &planName, &planExpiresAt)
	if err != nil {
		s.sendReply(msg.Chat.ID, fmt.Sprintf("❌ User <b>%s</b> not found.", email))
		return
	}

	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM devices WHERE user_id = (SELECT id FROM users WHERE email = $1)`, email).Scan(&deviceCount)

	plan := "None"
	if planName != nil {
		plan = *planName
	}
	expires := "N/A"
	if planExpiresAt != nil {
		expires = planExpiresAt.Format("2006-01-02")
	}
	trafficLimitStr := "Unlimited"
	if trafficLimit != nil {
		trafficLimitStr = formatBytes(*trafficLimit)
	}

	text := fmt.Sprintf("👤 <b>%s</b>\n\n"+
		"📧 Email: %s\n"+
		"📊 Status: %s %s\n"+
		"📦 Plan: %s\n"+
		"📈 Traffic: %s / %s\n"+
		"📅 Expires: %s\n"+
		"📱 Devices: %d",
		name, email, statusEmoji(status, isActive), status,
		plan, formatBytes(trafficUsed), trafficLimitStr, expires, deviceCount)

	s.sendReply(msg.Chat.ID, text)
}

func (s *BotService) handleAddUser(msg *tgbotapi.Message) {
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 3 {
		s.sendReply(msg.Chat.ID, "Usage: /adduser &lt;email&gt; &lt;password&gt; &lt;name&gt;")
		return
	}
	email := args[0]
	password := args[1]
	name := strings.Join(args[2:], " ")

	hash, err := crypto.HashPassword(password)
	if err != nil {
		s.sendReply(msg.Chat.ID, "❌ Failed to hash password.")
		return
	}
	subToken := crypto.NewUUID()

	ctx := context.Background()
	_, err = s.db.Exec(ctx,
		`INSERT INTO users (id, email, name, password_hash, sub_token, status, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, 'active', true, NOW(), NOW())`,
		crypto.NewUUID(), email, name, hash, subToken)
	if err != nil {
		s.sendReply(msg.Chat.ID, fmt.Sprintf("❌ Failed to create user: %s", err.Error()))
		return
	}

	s.sendReply(msg.Chat.ID, fmt.Sprintf("✅ User created\n\n"+
		"📧 Email: %s\n"+
		"👤 Name: %s\n"+
		"🔑 Sub Token: <code>%s</code>", email, name, subToken))
}

func (s *BotService) handleDelUser(msg *tgbotapi.Message) {
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 1 {
		s.sendReply(msg.Chat.ID, "Usage: /deluser &lt;email&gt;")
		return
	}
	email := args[0]

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes, delete", "deluser_confirm:"+email),
			tgbotapi.NewInlineKeyboardButtonData("❌ No, cancel", "deluser_cancel:"+email),
		),
	)

	reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("⚠️ Are you sure you want to delete user <b>%s</b>?", email))
	reply.ParseMode = "HTML"
	reply.ReplyMarkup = keyboard
	if _, err := s.bot.Send(reply); err != nil {
		log.Printf("telegram bot send error: %v", err)
	}
}

func (s *BotService) handleEnable(msg *tgbotapi.Message) {
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 1 {
		s.sendReply(msg.Chat.ID, "Usage: /enable &lt;email&gt;")
		return
	}
	email := args[0]
	ctx := context.Background()

	tag, err := s.db.Exec(ctx, `UPDATE users SET is_active = true, updated_at = NOW() WHERE email = $1`, email)
	if err != nil || tag.RowsAffected() == 0 {
		s.sendReply(msg.Chat.ID, fmt.Sprintf("❌ User <b>%s</b> not found.", email))
		return
	}

	s.sendReply(msg.Chat.ID, fmt.Sprintf("✅ User <b>%s</b> enabled.", email))
}

func (s *BotService) handleDisable(msg *tgbotapi.Message) {
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 1 {
		s.sendReply(msg.Chat.ID, "Usage: /disable &lt;email&gt;")
		return
	}
	email := args[0]
	ctx := context.Background()

	tag, err := s.db.Exec(ctx, `UPDATE users SET is_active = false, updated_at = NOW() WHERE email = $1`, email)
	if err != nil || tag.RowsAffected() == 0 {
		s.sendReply(msg.Chat.ID, fmt.Sprintf("❌ User <b>%s</b> not found.", email))
		return
	}

	s.sendReply(msg.Chat.ID, fmt.Sprintf("✅ User <b>%s</b> disabled.", email))
}

func (s *BotService) handleStats(msg *tgbotapi.Message) {
	ctx := context.Background()

	var totalUsers, activeUsers, totalNodes, onlineNodes int64

	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&totalUsers)
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE status = 'active' AND is_active = true`).Scan(&activeUsers)
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&totalNodes)
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM nodes WHERE status = 'online'`).Scan(&onlineNodes)

	text := fmt.Sprintf("📊 <b>Dashboard Stats</b>\n\n"+
		"👥 Total Users: %d\n"+
		"✅ Active Users: %d\n"+
		"🖥 Total Nodes: %d\n"+
		"🟢 Online Nodes: %d",
		totalUsers, activeUsers, totalNodes, onlineNodes)

	s.sendReply(msg.Chat.ID, text)
}

func (s *BotService) handleCallback(cb *tgbotapi.CallbackQuery) {
	if !s.isAdmin(cb.Message.Chat.ID) {
		return
	}

	callback := tgbotapi.NewCallback(cb.ID, "")
	s.bot.Request(callback)

	data := cb.Data
	switch {
	case strings.HasPrefix(data, "users_page:"):
		pageStr := strings.TrimPrefix(data, "users_page:")
		page, _ := strconv.Atoi(pageStr)
		s.handleUsersPage(cb.Message.Chat.ID, page)

	case strings.HasPrefix(data, "deluser_confirm:"):
		email := strings.TrimPrefix(data, "deluser_confirm:")
		ctx := context.Background()

		tag, err := s.db.Exec(ctx, `DELETE FROM users WHERE email = $1`, email)
		if err != nil || tag.RowsAffected() == 0 {
			s.sendReply(cb.Message.Chat.ID, fmt.Sprintf("❌ Failed to delete user <b>%s</b>.", email))
			return
		}
		s.sendReply(cb.Message.Chat.ID, fmt.Sprintf("🗑 User <b>%s</b> deleted.", email))

	case strings.HasPrefix(data, "deluser_cancel:"):
		email := strings.TrimPrefix(data, "deluser_cancel:")
		s.sendReply(cb.Message.Chat.ID, fmt.Sprintf("↩️ Deletion of <b>%s</b> cancelled.", email))
	}
}

func (s *BotService) handleSetPlan(msg *tgbotapi.Message) {
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 2 {
		s.sendReply(msg.Chat.ID, "Usage: /setplan &lt;email&gt; &lt;plan_name&gt;")
		return
	}
	email := args[0]
	planName := strings.Join(args[1:], " ")
	ctx := context.Background()

	var planID string
	var durationDays int
	err := s.db.QueryRow(ctx,
		`SELECT id, duration_days FROM plans WHERE name = $1 AND is_active = true`, planName,
	).Scan(&planID, &durationDays)
	if err != nil {
		s.sendReply(msg.Chat.ID, fmt.Sprintf("❌ Plan <b>%s</b> not found or inactive.", planName))
		return
	}

	tag, err := s.db.Exec(ctx,
		`UPDATE users SET plan_id = $1, plan_started_at = NOW(), plan_expires_at = NOW() + make_interval(days => $2), status = 'active', updated_at = NOW() WHERE email = $3`,
		planID, durationDays, email)
	if err != nil {
		s.sendReply(msg.Chat.ID, fmt.Sprintf("❌ Failed to assign plan: %s", err.Error()))
		return
	}
	if tag.RowsAffected() == 0 {
		s.sendReply(msg.Chat.ID, fmt.Sprintf("❌ User <b>%s</b> not found.", email))
		return
	}

	s.sendReply(msg.Chat.ID, fmt.Sprintf("✅ Plan <b>%s</b> assigned to <b>%s</b> (%d days).", planName, email, durationDays))
}

func (s *BotService) handleTraffic(msg *tgbotapi.Message) {
	args := strings.Fields(msg.CommandArguments())
	if len(args) < 1 {
		s.sendReply(msg.Chat.ID, "Usage: /traffic &lt;email&gt;")
		return
	}
	email := args[0]
	ctx := context.Background()

	var trafficUsed int64
	var trafficLimit *int64
	err := s.db.QueryRow(ctx,
		`SELECT u.traffic_used, p.traffic_limit FROM users u LEFT JOIN plans p ON u.plan_id = p.id WHERE u.email = $1`, email,
	).Scan(&trafficUsed, &trafficLimit)
	if err != nil {
		s.sendReply(msg.Chat.ID, fmt.Sprintf("❌ User <b>%s</b> not found.", email))
		return
	}

	if trafficLimit == nil || *trafficLimit == 0 {
		s.sendReply(msg.Chat.ID, fmt.Sprintf("📈 <b>%s</b>\nUsed: %s / Unlimited", email, formatBytes(trafficUsed)))
		return
	}

	pct := float64(trafficUsed) / float64(*trafficLimit) * 100
	s.sendReply(msg.Chat.ID, fmt.Sprintf("📈 <b>%s</b>\nUsed: %s / %s (%.1f%%)",
		email, formatBytes(trafficUsed), formatBytes(*trafficLimit), pct))
}

// SendAlert sends an alert message to the configured admin chat.
func (s *BotService) SendAlert(message string) {
	chatID, err := strconv.ParseInt(s.chatID, 10, 64)
	if err != nil {
		log.Printf("telegram bot: invalid chat ID for alert: %v", err)
		return
	}
	msg := tgbotapi.NewMessage(chatID, message)
	msg.ParseMode = "HTML"
	if _, err := s.bot.Send(msg); err != nil {
		log.Printf("telegram bot alert send error: %v", err)
	}
}

// startTrafficAlertLoop runs a periodic check for users exceeding traffic thresholds.
func (s *BotService) startTrafficAlertLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkTrafficThresholds(ctx)
		}
	}
}

func (s *BotService) checkTrafficThresholds(ctx context.Context) {
	// Check 80% threshold
	rows, err := s.db.Query(ctx,
		`SELECT u.email FROM users u JOIN plans p ON u.plan_id = p.id
		 WHERE p.traffic_limit IS NOT NULL AND p.traffic_limit > 0
		   AND u.traffic_used >= p.traffic_limit * 0.8
		   AND u.traffic_used < p.traffic_limit
		   AND u.status = 'active'`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var email string
			if err := rows.Scan(&email); err != nil {
				continue
			}
			key := "traffic_alert:80:" + email
			if set, _ := s.rdb.SetNX(ctx, key, "1", 24*time.Hour).Result(); set {
				s.SendAlert(fmt.Sprintf("⚠️ User <b>%s</b> reached 80%% traffic limit", email))
			}
		}
	}

	// Check 100% threshold
	rows2, err := s.db.Query(ctx,
		`SELECT u.email FROM users u JOIN plans p ON u.plan_id = p.id
		 WHERE p.traffic_limit IS NOT NULL AND p.traffic_limit > 0
		   AND u.traffic_used >= p.traffic_limit
		   AND u.status = 'active'`)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var email string
			if err := rows2.Scan(&email); err != nil {
				continue
			}
			key := "traffic_alert:100:" + email
			if set, _ := s.rdb.SetNX(ctx, key, "1", 24*time.Hour).Result(); set {
				s.SendAlert(fmt.Sprintf("🚫 User <b>%s</b> exceeded traffic limit", email))
			}
		}
	}

	rows3, err := s.db.Query(ctx,
		`SELECT email, plan_expires_at FROM users
		 WHERE status = 'active'
		   AND plan_expires_at IS NOT NULL
		   AND plan_expires_at > NOW()
		   AND plan_expires_at <= NOW() + INTERVAL '3 days'`)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var email string
			var expiresAt time.Time
			if err := rows3.Scan(&email, &expiresAt); err != nil {
				continue
			}
			daysLeft := int(time.Until(expiresAt).Hours()/24) + 1
			key := fmt.Sprintf("expiry_alert:%s:%d", email, daysLeft)
			if set, _ := s.rdb.SetNX(ctx, key, "1", 24*time.Hour).Result(); set {
				s.SendAlert(fmt.Sprintf("⏰ User <b>%s</b> plan expires in %d day(s)", email, daysLeft))
			}
		}
	}
}

func statusEmoji(status string, isActive bool) string {
	if !isActive {
		return "❌"
	}
	switch status {
	case "active":
		return "✅"
	case "pending":
		return "⏳"
	default:
		return "❓"
	}
}

func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
