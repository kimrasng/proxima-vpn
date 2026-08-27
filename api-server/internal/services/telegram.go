package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// TelegramService sends alert messages via Telegram Bot API. The
// enabled/bot_token/chat_id used for each send are resolved fresh from the
// admin Settings table (telegram_enabled/telegram_bot_token/telegram_chat_id,
// see AdminSettingsHandler in handlers/admin_settings.go), falling back to
// the config.yaml values this service was constructed with when a setting
// hasn't been saved yet. Previously these fields were read only once at
// process startup, so toggling Telegram on/off (or changing the token) in
// the admin panel had no effect on the running server. This does not cover
// the interactive Telegram bot (telegram.BotService, /users /adduser etc.)
// which still requires a restart to pick up a new token - only these
// one-off alert sends (node offline, new registration, traffic thresholds).
type TelegramService struct {
	db              *pgxpool.Pool
	fallbackToken   string
	fallbackChatID  string
	fallbackEnabled bool
	client          *http.Client
}

// NewTelegramService creates a TelegramService. cfg is the config.yaml
// fallback used until (or unless) the admin sets the equivalent keys in the
// settings table. db may be nil (e.g. in tests), in which case the fallback
// is always used.
func NewTelegramService(db *pgxpool.Pool, cfg config.TelegramConfig) *TelegramService {
	return &TelegramService{
		db:              db,
		fallbackToken:   cfg.BotToken,
		fallbackChatID:  cfg.ChatID,
		fallbackEnabled: cfg.Enabled,
		client:          &http.Client{},
	}
}

// resolve returns the enabled/token/chatID to use for this call.
func (s *TelegramService) resolve(ctx context.Context) (enabled bool, token, chatID string) {
	enabled, token, chatID = s.fallbackEnabled, s.fallbackToken, s.fallbackChatID
	if s.db == nil {
		return
	}

	rows, err := s.db.Query(ctx,
		`SELECT key, value FROM settings WHERE key IN ('telegram_enabled','telegram_bot_token','telegram_chat_id')`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		switch key {
		case "telegram_enabled":
			enabled = value == "true"
		case "telegram_bot_token":
			if value != "" {
				token = value
			}
		case "telegram_chat_id":
			if value != "" {
				chatID = value
			}
		}
	}
	return
}

// SendAlert sends a message via Telegram. Returns nil if disabled.
func (s *TelegramService) SendAlert(ctx context.Context, message string) error {
	enabled, token, chatID := s.resolve(ctx)
	if !enabled {
		return nil
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)

	payload := map[string]string{
		"chat_id":    chatID,
		"text":       message,
		"parse_mode": "HTML",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	return nil
}

// NotifyNodeOffline sends an alert when a node goes offline.
func (s *TelegramService) NotifyNodeOffline(ctx context.Context, nodeName string) error {
	msg := fmt.Sprintf("🔴 <b>Node Offline</b>\nNode <code>%s</code> is no longer responding.", nodeName)
	return s.SendAlert(ctx, msg)
}

// NotifyNewRegistration sends an alert when a new user registers.
func (s *TelegramService) NotifyNewRegistration(ctx context.Context, userEmail string) error {
	msg := fmt.Sprintf("👤 <b>New Registration</b>\nUser <code>%s</code> has signed up.", userEmail)
	return s.SendAlert(ctx, msg)
}

// NotifyExpiryWarning sends an alert when a user's plan is about to expire.
func (s *TelegramService) NotifyExpiryWarning(ctx context.Context, userEmail string, daysLeft int) error {
	msg := fmt.Sprintf("⏰ <b>Expiry Warning</b>\nUser <code>%s</code> plan expires in %d day(s).", userEmail, daysLeft)
	return s.SendAlert(ctx, msg)
}
