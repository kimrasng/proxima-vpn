package handlers_test

import (
	"bufio"
	"io"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
	"github.com/proximavpn/proxima-vpn/api-server/internal/server"
)

// oneShotSMTP accepts a single message in plaintext and hands the raw DATA to
// the returned channel.
func oneShotSMTP(t *testing.T) (port int, received <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	out := make(chan string, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		r := bufio.NewReader(conn)
		say := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }

		say("220 fake")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch strings.ToUpper(strings.Fields(line + " ")[0]) {
			case "EHLO", "HELO":
				say("250 fake")
			case "MAIL", "RCPT":
				say("250 ok")
			case "DATA":
				say("354 go")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					b.WriteString(strings.TrimPrefix(l, "."))
				}
				say("250 queued")
				out <- b.String()
			case "QUIT":
				say("221 bye")
				return
			default:
				say("500 ?")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, out
}

// The whole feature through the production wiring: real routes, the real SMTP
// sender configured from config.SMTPConfig, a real database. The link in the
// delivered message is longer than one quoted-printable line, so this also
// proves a soft line break cannot corrupt the token.
func TestPasswordReset_EndToEndThroughRealRoutesAndSMTP(t *testing.T) {
	pool := pwrDB(t)
	t.Setenv("SWAGGER_ENABLED", "false")
	port, received := oneShotSMTP(t)

	cfg := &config.Config{}
	cfg.JWT.Secret = pwrJWTSecret
	cfg.Server.PanelURL = "https://panel.example.com/"
	cfg.SMTP = config.SMTPConfig{Host: "127.0.0.1", Port: port, From: "Proxima <no-reply@example.com>", TLS: config.SMTPTLSNone}
	app := server.NewServer(cfg, pool, nil, nil, nil).App()
	t.Cleanup(func() { _ = app.Shutdown() })

	f := &pwrFixture{t: t, pool: pool, app: app, prefix: "/api/v1"}
	_, email := f.seedUser("active", true)

	res := f.post("/api/v1/auth/forgot-password", map[string]string{"email": email, "language": "ko"})
	if res.status != fiber.StatusOK {
		t.Fatalf("forgot-password: %d %s", res.status, res.body)
	}

	var raw string
	select {
	case raw = <-received:
	case <-time.After(15 * time.Second):
		t.Fatal("no email arrived at the SMTP server")
	}

	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse delivered message: %v\n%s", err, raw)
	}
	if !strings.Contains(msg.Header.Get("To"), email) {
		t.Errorf("To = %q, want %q", msg.Header.Get("To"), email)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	token, link := tokenFromMail(t, mail.Message{Body: string(body)})
	if !strings.HasPrefix(link, "https://panel.example.com/reset-password?token=") {
		t.Fatalf("link = %q, want it anchored on the configured PANEL_URL", link)
	}
	if len(link) < 76 {
		t.Fatalf("link is only %d characters; the test needs one longer than a QP line", len(link))
	}
	if !strings.Contains(string(body), "30분") {
		t.Errorf("language=ko was not honoured:\n%s", body)
	}

	if res := f.post("/api/v1/auth/reset-password", map[string]string{"token": token, "password": pwrNewPassword}); res.status != fiber.StatusOK {
		t.Fatalf("reset-password: %d %s", res.status, res.body)
	}
	if status, jwt := f.login(email, pwrNewPassword); status != fiber.StatusOK || jwt == "" {
		t.Fatalf("login with the new password: %d", status)
	}
	if status, _ := f.login(email, pwrOldPassword); status != fiber.StatusUnauthorized {
		t.Fatalf("login with the old password: %d, want 401", status)
	}
}
