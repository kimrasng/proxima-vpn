package server

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
)

var signupMailCode = regexp.MustCompile(`\b[0-9]{6}\b`)

type testSignupMailer struct {
	mu       sync.Mutex
	messages []mail.Message
}

func (m *testSignupMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return nil
}

func (m *testSignupMailer) LatestSignupCode(_ context.Context, email string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		if strings.EqualFold(msg.To, email) && (strings.Contains(strings.ToLower(msg.Subject), "verification") || strings.Contains(msg.Subject, "인증") || strings.Contains(msg.Subject, "验证码")) {
			code := signupMailCode.FindString(msg.Body)
			return code, code != ""
		}
	}
	return "", false
}

type mailerAdapter struct{ mail.Sender }

func (mailerAdapter) LatestSignupCode(context.Context, string) (string, bool) { return "", false }
