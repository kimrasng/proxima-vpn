package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// TelegramService sends alert messages via Telegram Bot API.
type TelegramService struct {
	botToken string
	chatID   string
	enabled  bool
	client   *http.Client
}

// NewTelegramService creates a TelegramService from config.
func NewTelegramService(cfg config.TelegramConfig) *TelegramService {
	return &TelegramService{
		botToken: cfg.BotToken,
		chatID:   cfg.ChatID,
		enabled:  cfg.Enabled,
		client:   &http.Client{},
	}
}

// SendAlert sends a message via Telegram. Returns nil if disabled.
func (s *TelegramService) SendAlert(ctx context.Context, message string) error {
	if !s.enabled {
		return nil
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", s.botToken)

	payload := map[string]string{
		"chat_id":    s.chatID,
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
	defer resp.Body.Close()

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
