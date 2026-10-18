package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"mime"
	"mime/quotedprintable"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/pkg/lang"
)

const testLink = "https://panel.example.com/reset-password?token=AbC_123-xyz"

func newTestSender(t *testing.T, port int, mode string, user, pass string, pool *x509.CertPool) *SMTPSender {
	t.Helper()
	s, err := NewSMTPSender(Config{
		Host: "127.0.0.1", Port: port, Username: user, Password: pass,
		From: "Proxima <no-reply@example.com>", TLS: mode,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if pool != nil {
		s.tlsConfig = &tls.Config{ServerName: "127.0.0.1", RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return s
}

func sendCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// parseCaptured decodes the captured message into headers and the decoded body.
func parseCaptured(t *testing.T, raw string) (netmail.Header, string) {
	t.Helper()
	m, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v\n%s", err, raw)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(m.Body))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m.Header, string(body)
}

func decodedSubject(t *testing.T, h netmail.Header) string {
	t.Helper()
	s, err := new(mime.WordDecoder).DecodeHeader(h.Get("Subject"))
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	return s
}

func TestSend_PlainNone_DeliversWithoutAuthEvenWhenCredentialsAreConfigured(t *testing.T) {
	srv := newFakeSMTP(t, func(f *fakeSMTP) { f.auth = true })
	s := newTestSender(t, srv.port(), config.SMTPTLSNone, "user", "hunter2", nil)

	err := s.Send(sendCtx(t), Message{To: "alice@example.org", Subject: "Hello", Body: "Body line\nlink " + testLink + "\n"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	verbs, authN, _, mails := srv.snapshot()
	if authN != 0 {
		t.Fatalf("server saw %d AUTH attempts in tls=none mode, want 0 (verbs %v)", authN, verbs)
	}
	for _, v := range verbs {
		if v == "AUTH" || v == "STARTTLS" {
			t.Fatalf("unexpected %s in tls=none mode: %v", v, verbs)
		}
	}
	if len(mails) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(mails))
	}
	m := mails[0]
	if !strings.Contains(m.from, "<no-reply@example.com>") {
		t.Errorf("MAIL FROM = %q", m.from)
	}
	if len(m.rcpts) != 1 || !strings.Contains(m.rcpts[0], "<alice@example.org>") {
		t.Errorf("RCPT TO = %v", m.rcpts)
	}
	h, body := parseCaptured(t, m.data)
	if got := h.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}
	if !strings.Contains(body, testLink) {
		t.Errorf("body lost the link: %q", body)
	}
	if got := h.Get("Message-Id"); !strings.HasSuffix(got, "@example.com>") {
		t.Errorf("Message-ID = %q", got)
	}
}

func TestSend_StartTLS_RefusesToDowngradeAndNeverSendsCredentials(t *testing.T) {
	// The server does not advertise STARTTLS at all.
	srv := newFakeSMTP(t, func(f *fakeSMTP) { f.auth = true })
	s := newTestSender(t, srv.port(), config.SMTPTLSStartTLS, "user", "hunter2", nil)

	err := s.Send(sendCtx(t), Message{To: "alice@example.org", Subject: "Hello", Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("Send error = %v, want a STARTTLS refusal", err)
	}

	verbs, authN, _, mails := srv.snapshot()
	if authN != 0 || len(mails) != 0 {
		t.Fatalf("downgrade leaked: auth=%d mails=%d verbs=%v", authN, len(mails), verbs)
	}
	for _, v := range verbs {
		if v == "MAIL" || v == "RCPT" || v == "DATA" {
			t.Fatalf("mail command %s issued on an unencrypted connection: %v", v, verbs)
		}
	}
}

func TestSend_StartTLS_AuthenticatesOnlyAfterTheUpgrade(t *testing.T) {
	tlsCfg, pool := selfSignedTLS(t)
	srv := newFakeSMTP(t, func(f *fakeSMTP) { f.tlsCfg, f.starttls, f.auth = tlsCfg, true, true })
	s := newTestSender(t, srv.port(), config.SMTPTLSStartTLS, "user", "hunter2", pool)

	if err := s.Send(sendCtx(t), Message{To: "alice@example.org", Subject: "Hello", Body: "x"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	verbs, authN, authOver, mails := srv.snapshot()
	if authN != 1 || len(authOver) != 1 || !authOver[0] {
		t.Fatalf("AUTH attempts = %d over TLS %v, want exactly one, encrypted (verbs %v)", authN, authOver, verbs)
	}
	if len(mails) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(mails))
	}
}

func TestSend_StartTLS_RejectsUntrustedCertificate(t *testing.T) {
	tlsCfg, _ := selfSignedTLS(t)
	srv := newFakeSMTP(t, func(f *fakeSMTP) { f.tlsCfg, f.starttls, f.auth = tlsCfg, true, true })
	// No trust pool: the default (system roots) must reject the self-signed cert.
	s := newTestSender(t, srv.port(), config.SMTPTLSStartTLS, "user", "hunter2", nil)

	if err := s.Send(sendCtx(t), Message{To: "alice@example.org", Subject: "Hello", Body: "x"}); err == nil {
		t.Fatal("Send succeeded against an untrusted certificate")
	}
	if _, authN, _, mails := srv.snapshot(); authN != 0 || len(mails) != 0 {
		t.Fatalf("credentials or mail sent after a failed handshake: auth=%d mails=%d", authN, len(mails))
	}
}

func TestSend_ImplicitTLS_Delivers(t *testing.T) {
	tlsCfg, pool := selfSignedTLS(t)
	srv := newFakeSMTP(t, func(f *fakeSMTP) { f.tlsCfg, f.implicit, f.auth = tlsCfg, true, true })
	s := newTestSender(t, srv.port(), config.SMTPTLSImplicit, "user", "hunter2", pool)

	if err := s.Send(sendCtx(t), Message{To: "alice@example.org", Subject: "Hello", Body: "x"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, authN, authOver, mails := srv.snapshot()
	if authN != 1 || !authOver[0] || len(mails) != 1 {
		t.Fatalf("auth=%d over=%v mails=%d, want one encrypted AUTH and one message", authN, authOver, len(mails))
	}
}

func TestSend_NeutralisesHeaderInjectionInSubject(t *testing.T) {
	srv := newFakeSMTP(t, nil)
	s := newTestSender(t, srv.port(), config.SMTPTLSNone, "", "", nil)

	subject := "Reset\r\nBcc: attacker@evil.example\r\nX-Injected: yes"
	if err := s.Send(sendCtx(t), Message{To: "alice@example.org", Subject: subject, Body: "x"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, _, _, mails := srv.snapshot()
	if len(mails) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(mails))
	}
	headerBlock := strings.SplitN(mails[0].data, "\r\n\r\n", 2)[0]
	for _, line := range strings.Split(headerBlock, "\r\n") {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "bcc:") || strings.HasPrefix(lower, "x-injected:") {
			t.Fatalf("injected header present: %q\n%s", line, headerBlock)
		}
	}
	h, _ := parseCaptured(t, mails[0].data)
	if h.Get("Bcc") != "" || h.Get("X-Injected") != "" {
		t.Fatalf("parsed headers contain injected values: %v", h)
	}
	if len(mails[0].rcpts) != 1 {
		t.Fatalf("RCPT TO = %v, want only the real recipient", mails[0].rcpts)
	}
}

func TestSend_RejectsInjectedRecipient(t *testing.T) {
	srv := newFakeSMTP(t, nil)
	s := newTestSender(t, srv.port(), config.SMTPTLSNone, "", "", nil)

	for _, to := range []string{
		"alice@example.org\r\nBcc: evil@example.net",
		"alice@example.org\nRCPT TO:<evil@example.net>",
		"alice@example.org, bob@example.org",
		"",
	} {
		if err := s.Send(sendCtx(t), Message{To: to, Subject: "s", Body: "b"}); err == nil {
			t.Errorf("Send accepted recipient %q", to)
		}
	}
	if verbs, _, _, mails := srv.snapshot(); len(mails) != 0 || len(verbs) != 0 {
		t.Fatalf("a rejected recipient still reached the server: verbs=%v mails=%d", verbs, len(mails))
	}
}

func TestNewSMTPSender_RejectsInjectedFrom(t *testing.T) {
	for _, from := range []string{
		"Proxima <no-reply@example.com>\r\nBcc: evil@example.net",
		"Proxima\n<no-reply@example.com>",
		"",
		"not an address",
	} {
		if _, err := NewSMTPSender(Config{Host: "127.0.0.1", Port: 25, From: from, TLS: config.SMTPTLSNone}); err == nil {
			t.Errorf("NewSMTPSender accepted From %q", from)
		}
	}
}

func TestSanitizeHeader_FlattensControlCharacters(t *testing.T) {
	got := sanitizeHeader("a\r\nb\tc\x00d  e")
	if got != "a b c d e" {
		t.Fatalf("sanitizeHeader = %q", got)
	}
}

func TestPasswordResetEmail_IsLocalizedAndCarriesTheLink(t *testing.T) {
	cases := []struct {
		language lang.Code
		subject  string
		minutes  string
		ignore   string
	}{
		{lang.English, "Reset your Proxima password", "30 minutes", "ignore this email"},
		{lang.Korean, "Proxima 비밀번호 재설정 안내", "30분", "이 메일을 무시"},
		{lang.Chinese, "重置您的 Proxima 密码", "30 分钟", "请忽略此邮件"},
		{lang.Unset, "Reset your Proxima password", "30 minutes", "ignore this email"},
		{lang.Code("fr"), "Reset your Proxima password", "30 minutes", "ignore this email"},
	}
	for _, tc := range cases {
		t.Run(string(tc.language), func(t *testing.T) {
			subject, body := PasswordResetEmail(tc.language, testLink, 30*time.Minute)
			if subject != tc.subject {
				t.Errorf("subject = %q, want %q", subject, tc.subject)
			}
			for _, want := range []string{testLink, tc.minutes, tc.ignore} {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q:\n%s", want, body)
				}
			}
			if strings.Contains(body, "<") && strings.Contains(strings.ToLower(body), "<html") {
				t.Errorf("body must be plain text:\n%s", body)
			}
		})
	}
}

func TestSend_LocalizedMessageSurvivesTheWire(t *testing.T) {
	srv := newFakeSMTP(t, nil)
	s := newTestSender(t, srv.port(), config.SMTPTLSNone, "", "", nil)

	for _, language := range []lang.Code{lang.Korean, lang.Chinese, lang.English} {
		subject, body := PasswordResetEmail(language, testLink, 30*time.Minute)
		if err := s.Send(sendCtx(t), Message{To: "alice@example.org", Subject: subject, Body: body}); err != nil {
			t.Fatalf("Send(%s): %v", language, err)
		}
	}

	_, _, _, mails := srv.snapshot()
	if len(mails) != 3 {
		t.Fatalf("delivered %d messages, want 3", len(mails))
	}
	for i, language := range []lang.Code{lang.Korean, lang.Chinese, lang.English} {
		wantSubject, wantBody := PasswordResetEmail(language, testLink, 30*time.Minute)
		h, body := parseCaptured(t, mails[i].data)
		if got := decodedSubject(t, h); got != wantSubject {
			t.Errorf("%s subject = %q, want %q", language, got, wantSubject)
		}
		if strings.ReplaceAll(body, "\r\n", "\n") != wantBody {
			t.Errorf("%s body mismatch:\n got %q\nwant %q", language, body, wantBody)
		}
		if !strings.Contains(body, testLink) {
			t.Errorf("%s body lost the link", language)
		}
	}
}

func TestNewFromConfig_DisabledWithoutHostAndNilInterface(t *testing.T) {
	sender, err := NewFromConfig(config.SMTPConfig{})
	if err != nil || sender != nil {
		t.Fatalf("NewFromConfig(empty) = %v, %v; want nil, nil", sender, err)
	}

	// An invalid configuration must yield a true nil interface, not a typed nil.
	sender, err = NewFromConfig(config.SMTPConfig{Host: "smtp.example.com", Port: 587, TLS: "bogus", From: "a@example.com"})
	if err == nil || sender != nil {
		t.Fatalf("NewFromConfig(invalid) = %v, %v; want nil, error", sender, err)
	}
}
