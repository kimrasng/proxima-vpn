// Package mail sends the panel's transactional email over SMTP using only the
// standard library (net/smtp and crypto/tls).
//
// Handlers depend on the Sender interface, never on SMTPSender, so they can be
// tested with a fake. Two properties are enforced here rather than left to the
// caller: credentials are never sent over an unencrypted connection, and a
// header value can never smuggle in extra headers or SMTP commands.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	netmail "net/mail"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// Message is a plain-text email. HTML is deliberately unsupported.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers a Message. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Config is the transport configuration for an SMTPSender. It carries the
// password as a plain string because it is built from config.SMTPConfig at the
// wiring boundary; it must not be logged.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// TLS is config.SMTPTLSStartTLS, config.SMTPTLSImplicit or config.SMTPTLSNone.
	TLS string
}

const (
	dialTimeout    = 10 * time.Second
	sessionTimeout = 30 * time.Second
)

// SMTPSender delivers mail through one SMTP server.
type SMTPSender struct {
	cfg  Config
	from *netmail.Address
	// tlsConfig overrides the default TLS settings; tests use it to trust a
	// self-signed certificate. Nil means "verify against the system roots".
	tlsConfig *tls.Config
}

// NewFromConfig builds the sender for the panel configuration. It returns a nil
// Sender and no error when mail is not configured (no host), so callers can
// treat "nil" as "feature disabled".
func NewFromConfig(c config.SMTPConfig) (Sender, error) {
	if strings.TrimSpace(c.Host) == "" {
		return nil, nil
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	sender, err := NewSMTPSender(Config{
		Host:     c.Host,
		Port:     c.Port,
		Username: c.Username,
		Password: c.SMTPPassword(),
		From:     c.From,
		TLS:      c.TLS,
	})
	if err != nil {
		// Return an untyped nil: a nil *SMTPSender stored in a Sender would
		// compare non-nil and defeat every "mailer == nil" check.
		return nil, err
	}
	return sender, nil
}

// NewSMTPSender validates cfg and returns a sender.
func NewSMTPSender(cfg Config) (*SMTPSender, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New("mail: SMTP host is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("mail: SMTP port out of range")
	}
	switch cfg.TLS {
	case config.SMTPTLSStartTLS, config.SMTPTLSImplicit, config.SMTPTLSNone:
	default:
		return nil, fmt.Errorf("mail: unknown TLS mode %q", cfg.TLS)
	}
	from, err := parseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("mail: invalid From address: %w", err)
	}
	return &SMTPSender{cfg: cfg, from: from}, nil
}

// Send delivers msg. The context bounds the whole session.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	to, err := parseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("mail: invalid recipient: %w", err)
	}
	raw, err := s.buildMessage(to, msg, time.Now())
	if err != nil {
		return err
	}
	return s.deliver(ctx, to.Address, raw)
}

// parseAddress accepts one RFC 5322 address and rejects anything containing a
// control character, which is what a header or command injection needs.
func parseAddress(v string) (*netmail.Address, error) {
	if strings.IndexFunc(v, isControl) >= 0 {
		return nil, errors.New("address contains control characters")
	}
	addr, err := netmail.ParseAddress(strings.TrimSpace(v))
	if err != nil {
		return nil, err
	}
	return addr, nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// sanitizeHeader flattens a value to a single line. Control characters become
// spaces instead of being dropped so "a\r\nBcc: x" cannot collapse into a
// different, still-plausible header.
func sanitizeHeader(v string) string {
	v = strings.Map(func(r rune) rune {
		if isControl(r) {
			return ' '
		}
		return r
	}, v)
	return strings.Join(strings.Fields(v), " ")
}

// buildMessage renders the RFC 5322 message with CRLF line endings.
func (s *SMTPSender) buildMessage(to *netmail.Address, msg Message, now time.Time) ([]byte, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("mail: generating message id: %w", err)
	}
	domain := "localhost"
	if at := strings.LastIndexByte(s.from.Address, '@'); at >= 0 {
		domain = s.from.Address[at+1:]
	}

	var b bytes.Buffer
	header := func(key, value string) { b.WriteString(key + ": " + value + "\r\n") }
	header("From", s.from.String())
	header("To", (&netmail.Address{Address: to.Address}).String())
	header("Subject", mime.QEncoding.Encode("UTF-8", sanitizeHeader(msg.Subject)))
	header("Date", now.UTC().Format(time.RFC1123Z))
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=UTF-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(msg.Body)); err != nil {
		return nil, fmt.Errorf("mail: encoding body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("mail: encoding body: %w", err)
	}
	return b.Bytes(), nil
}

func (s *SMTPSender) tlsSettings() *tls.Config {
	if s.tlsConfig != nil {
		return s.tlsConfig
	}
	return &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
}

// helloName is the EHLO identity: the domain of the From address, an FQDN the
// operator controls, rather than a container's random hostname.
func (s *SMTPSender) helloName() string {
	if at := strings.LastIndexByte(s.from.Address, '@'); at >= 0 && at+1 < len(s.from.Address) {
		return s.from.Address[at+1:]
	}
	return "localhost"
}

func (s *SMTPSender) deliver(ctx context.Context, rcpt string, data []byte) error {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Timeout: dialTimeout}

	var (
		conn net.Conn
		err  error
	)
	if s.cfg.TLS == config.SMTPTLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: s.tlsSettings()}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp: connecting to %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(sessionTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("smtp: setting deadline: %w", err)
	}
	// Unblocks a stuck read/write when the caller cancels before the deadline.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: greeting: %w", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Hello(s.helloName()); err != nil {
		return fmt.Errorf("smtp: EHLO: %w", err)
	}

	if s.cfg.TLS == config.SMTPTLSStartTLS {
		// Never fall back to plaintext: a server that stops advertising
		// STARTTLS (or a man in the middle stripping it) must fail the send.
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp: server does not offer STARTTLS; refusing to continue unencrypted")
		}
		if err := c.StartTLS(s.tlsSettings()); err != nil {
			return fmt.Errorf("smtp: STARTTLS: %w", err)
		}
	}

	// Credentials only ever travel over an encrypted connection. With
	// SMTP_TLS=none the session is delivered unauthenticated.
	if s.cfg.Username != "" && s.cfg.TLS != config.SMTPTLSNone {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server does not support AUTH")
		}
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp: authentication failed: %w", err)
		}
	}

	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(rcpt); err != nil {
		return fmt.Errorf("smtp: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp: writing message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: finishing message: %w", err)
	}
	// The message is accepted once DATA completes; a QUIT failure is noise.
	_ = c.Quit()
	return nil
}
