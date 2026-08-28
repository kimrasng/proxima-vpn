package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// fakeTelegram stands in for api.telegram.org. tgbotapi talks to a
// configurable endpoint (NewBotAPIWithAPIEndpoint), so pointing it here lets
// these tests exercise the real command routing, the real authorization gate
// and the real SQL in bot.go - only the outbound HTTP call to Telegram is
// faked. It records every sendMessage so tests can assert on what the bot
// would actually have replied.
type fakeTelegram struct {
	server *httptest.Server

	mu   sync.Mutex
	sent []sentMessage
}

type sentMessage struct {
	ChatID string
	Text   string
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{}

	mux := http.NewServeMux()
	// tgbotapi calls getMe during construction.
	mux.HandleFunc("/bot-test-token/getMe", func(w http.ResponseWriter, r *http.Request) {
		writeTGResult(w, map[string]any{"id": 1, "is_bot": true, "username": "e2e_test_bot"})
	})
	mux.HandleFunc("/bot-test-token/sendMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.sent = append(f.sent, sentMessage{
			ChatID: r.FormValue("chat_id"),
			Text:   r.FormValue("text"),
		})
		f.mu.Unlock()
		writeTGResult(w, map[string]any{"message_id": 1})
	})
	// Anything else (answerCallbackQuery etc.) just succeeds.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeTGResult(w, map[string]any{})
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func writeTGResult(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := json.Marshal(result)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"result": json.RawMessage(body),
	})
}

func (f *fakeTelegram) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

func (f *fakeTelegram) lastText() string {
	msgs := f.messages()
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].Text
}

func (f *fakeTelegram) reset() {
	f.mu.Lock()
	f.sent = nil
	f.mu.Unlock()
}

// newTestBot wires a BotService against the fake Telegram endpoint. db may be
// nil for tests that never reach a query.
func newTestBot(t *testing.T, f *fakeTelegram, db *pgxpool.Pool, adminChatID string) *BotService {
	t.Helper()
	endpoint := f.server.URL + "/bot%s/%s"
	bot, err := tgbotapi.NewBotAPIWithAPIEndpoint("-test-token", endpoint)
	if err != nil {
		t.Fatalf("create test bot: %v", err)
	}
	s := &BotService{
		bot:      bot,
		db:       db,
		chatID:   adminChatID,
		panelURL: "https://panel.test",
	}
	if db != nil {
		s.plan = services.NewPlanService(db)
	}
	return s
}

func command(chatID int64, text string) *tgbotapi.Message {
	// Telegram marks commands with a bot_command entity starting at 0;
	// tgbotapi's IsCommand()/Command() rely on it.
	cmdLen := len(strings.SplitN(text, " ", 2)[0])
	return &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: chatID},
		Text: text,
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: cmdLen},
		},
	}
}

const adminChat = "12345"

var adminChatID int64 = 12345
var strangerChatID int64 = 99999

// --- authorization -------------------------------------------------------

func TestIsAdmin(t *testing.T) {
	f := newFakeTelegram(t)
	s := newTestBot(t, f, nil, adminChat)

	if !s.isAdmin(adminChatID) {
		t.Error("configured admin chat ID should be treated as admin")
	}
	if s.isAdmin(strangerChatID) {
		t.Error("non-admin chat ID must not be treated as admin")
	}
}

// Admin-only commands must be refused for everyone else. This is the gate
// that keeps user management (/adduser, /deluser, /setplan, ...) private, so
// it's asserted per-command rather than once.
func TestAdminOnlyCommandsRejectNonAdmin(t *testing.T) {
	f := newFakeTelegram(t)
	s := newTestBot(t, f, nil, adminChat)

	adminOnly := []string{
		"/help", "/users", "/user a@b.c", "/adduser a@b.c pw name",
		"/deluser a@b.c", "/enable a@b.c", "/disable a@b.c",
		"/stats", "/setplan a@b.c plan", "/traffic a@b.c",
	}

	for _, cmd := range adminOnly {
		f.reset()
		s.handleCommand(command(strangerChatID, cmd))

		msgs := f.messages()
		if len(msgs) != 1 {
			t.Fatalf("%s: expected exactly one reply, got %d", cmd, len(msgs))
		}
		if !strings.Contains(msgs[0].Text, "Unauthorized") {
			t.Errorf("%s: non-admin should get an Unauthorized reply, got %q", cmd, msgs[0].Text)
		}
	}
}

// /start, /link, /mysub, /mystatus are deliberately available to everyone -
// they only ever act on the caller's own linked account.
func TestPublicCommandsAllowNonAdmin(t *testing.T) {
	f := newFakeTelegram(t)
	s := newTestBot(t, f, nil, adminChat)

	f.reset()
	s.handleCommand(command(strangerChatID, "/start"))
	if got := f.lastText(); !strings.Contains(got, "Welcome") {
		t.Errorf("/start should welcome a non-admin, got %q", got)
	}

	// /link with no argument must answer with usage, not Unauthorized, and
	// must not require a DB round-trip to get there.
	f.reset()
	s.handleCommand(command(strangerChatID, "/link"))
	if got := f.lastText(); !strings.Contains(got, "Usage") {
		t.Errorf("/link without args should print usage, got %q", got)
	}
}

func TestUnknownCommandIsReportedToAdmin(t *testing.T) {
	f := newFakeTelegram(t)
	s := newTestBot(t, f, nil, adminChat)

	s.handleCommand(command(adminChatID, "/definitelynotacommand"))
	if got := f.lastText(); !strings.Contains(got, "Unknown command") {
		t.Errorf("expected an unknown-command reply, got %q", got)
	}
}

func TestHelpListsEveryAdminCommand(t *testing.T) {
	f := newFakeTelegram(t)
	s := newTestBot(t, f, nil, adminChat)

	s.handleCommand(command(adminChatID, "/help"))
	help := f.lastText()

	// Every admin command handleCommand actually routes should be
	// discoverable from /help - a command that exists but isn't listed is
	// effectively invisible to the operator.
	for _, cmd := range []string{
		"/users", "/user", "/adduser", "/deluser", "/enable",
		"/disable", "/setplan", "/traffic", "/stats",
	} {
		if !strings.Contains(help, cmd) {
			t.Errorf("/help does not mention %s", cmd)
		}
	}
}

// Callback buttons (pagination, delete confirmation) are admin-gated too:
// they mutate users, so a stray callback from a non-admin must do nothing.
func TestCallbackFromNonAdminIsIgnored(t *testing.T) {
	f := newFakeTelegram(t)
	s := newTestBot(t, f, nil, adminChat)

	s.handleCallback(&tgbotapi.CallbackQuery{
		ID:      "cb1",
		Data:    "deluser_confirm:victim@example.com",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: strangerChatID}},
	})

	if msgs := f.messages(); len(msgs) != 0 {
		t.Errorf("non-admin callback should produce no bot replies, got %d: %+v", len(msgs), msgs)
	}
}

// --- pure helpers --------------------------------------------------------

func TestStatusEmoji(t *testing.T) {
	tests := []struct {
		status   string
		isActive bool
		want     string
	}{
		{"active", true, "✅"},
		{"pending", true, "⏳"},
		{"anything", true, "❓"},
		// An inactive account reads as disabled regardless of its status
		// string - is_active is the harder gate (see handleDisable).
		{"active", false, "❌"},
		{"pending", false, "❌"},
	}

	for _, tc := range tests {
		if got := statusEmoji(tc.status, tc.isActive); got != tc.want {
			t.Errorf("statusEmoji(%q, %v) = %q, want %q", tc.status, tc.isActive, got, tc.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1536 * 1024 * 1024, "1.5 GB"},
	}

	for _, tc := range tests {
		if got := formatBytes(tc.in); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- DB-backed command behaviour ----------------------------------------
//
// These run only when TEST_DATABASE_URL points at a disposable Postgres (CI
// sets it from the workflow's postgres service). They cover the commands
// that actually mutate accounts, where a silent SQL regression would be
// invisible to the pure-function tests above.

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed telegram tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedUser inserts a throwaway active user and returns its email.
func seedUser(t *testing.T, db *pgxpool.Pool, status string, isActive bool) string {
	t.Helper()
	email := "tg-" + crypto.NewUUID() + "@example.com"
	hash, err := crypto.HashPassword("irrelevant")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	_, err = db.Exec(context.Background(),
		`INSERT INTO users (email, name, password_hash, sub_token, status, is_active)
		 VALUES ($1, 'tg test', $2, $3, $4, $5)`,
		email, hash, crypto.NewUUID(), status, isActive)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, email)
	})
	return email
}

func userFlags(t *testing.T, db *pgxpool.Pool, email string) (status string, isActive bool) {
	t.Helper()
	err := db.QueryRow(context.Background(),
		`SELECT status, is_active FROM users WHERE email = $1`, email).Scan(&status, &isActive)
	if err != nil {
		t.Fatalf("read back user: %v", err)
	}
	return status, isActive
}

func TestEnableDisableCommandsUpdateTheUser(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	email := seedUser(t, db, "active", true)

	f.reset()
	s.handleCommand(command(adminChatID, "/disable "+email))
	if _, active := userFlags(t, db, email); active {
		t.Error("/disable should set is_active = false")
	}
	if got := f.lastText(); !strings.Contains(got, "disabled") {
		t.Errorf("/disable reply = %q", got)
	}

	f.reset()
	s.handleCommand(command(adminChatID, "/enable "+email))
	if _, active := userFlags(t, db, email); !active {
		t.Error("/enable should set is_active = true")
	}
	if got := f.lastText(); !strings.Contains(got, "enabled") {
		t.Errorf("/enable reply = %q", got)
	}
}

func TestEnableUnknownUserReportsNotFound(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	s.handleCommand(command(adminChatID, "/enable does-not-exist@example.com"))
	if got := f.lastText(); !strings.Contains(got, "not found") {
		t.Errorf("expected a not-found reply, got %q", got)
	}
}

// The delete-confirmation callback must soft-deactivate (matching the admin
// panel's Delete), never hard-delete: a hard DELETE here would bypass the
// recovery/audit guarantee the panel's "delete" implies.
func TestDeleteConfirmCallbackSoftDeactivates(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	email := seedUser(t, db, "active", true)

	s.handleCallback(&tgbotapi.CallbackQuery{
		ID:      "cb1",
		Data:    "deluser_confirm:" + email,
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: adminChatID}},
	})

	status, active := userFlags(t, db, email) // fails the test if the row is gone
	if active {
		t.Error("delete confirmation should set is_active = false")
	}
	if status != "suspended" {
		t.Errorf("delete confirmation should set status = suspended, got %q", status)
	}
}

func TestDeleteCancelCallbackLeavesUserAlone(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	email := seedUser(t, db, "active", true)

	s.handleCallback(&tgbotapi.CallbackQuery{
		ID:      "cb2",
		Data:    "deluser_cancel:" + email,
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: adminChatID}},
	})

	status, active := userFlags(t, db, email)
	if !active || status != "active" {
		t.Errorf("cancel must not modify the user, got status=%q is_active=%v", status, active)
	}
}

func TestAddUserCreatesAnAccount(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	email := "tg-added-" + crypto.NewUUID() + "@example.com"
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, email)
	})

	s.handleCommand(command(adminChatID, "/adduser "+email+" S0mePassword Test User"))

	status, active := userFlags(t, db, email)
	if status != "active" || !active {
		t.Errorf("new user should be active, got status=%q is_active=%v", status, active)
	}
	// The reply carries the sub token the operator needs; make sure it isn't
	// silently empty.
	if got := f.lastText(); !strings.Contains(got, "Sub Token") {
		t.Errorf("/adduser reply should include the sub token, got %q", got)
	}

	// Multi-word names must survive: handleAddUser joins the remaining args.
	var name string
	if err := db.QueryRow(context.Background(),
		`SELECT name FROM users WHERE email = $1`, email).Scan(&name); err != nil {
		t.Fatalf("read name: %v", err)
	}
	if name != "Test User" {
		t.Errorf("name = %q, want %q", name, "Test User")
	}
}

func TestUserCommandReportsUnknownUser(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	s.handleCommand(command(adminChatID, "/user nobody@example.com"))
	if got := f.lastText(); !strings.Contains(got, "not found") {
		t.Errorf("expected not-found reply, got %q", got)
	}
}

func TestMyStatusWithoutLinkedAccountPromptsToLink(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	s.handleCommand(command(strangerChatID, "/mystatus"))
	if got := f.lastText(); !strings.Contains(got, "/link") {
		t.Errorf("unlinked user should be told to /link, got %q", got)
	}
}

func TestLinkWithInvalidTokenIsRejected(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	s.handleCommand(command(strangerChatID, "/link "+crypto.NewUUID()))
	if got := f.lastText(); !strings.Contains(got, "Invalid token") {
		t.Errorf("expected invalid-token reply, got %q", got)
	}
}

// A valid sub_token links the Telegram account, and /mysub then resolves to
// a subscription URL built from the configured panel URL.
func TestLinkThenMyStatus(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	email := seedUser(t, db, "active", true)
	var subToken string
	if err := db.QueryRow(context.Background(),
		`SELECT sub_token FROM users WHERE email = $1`, email).Scan(&subToken); err != nil {
		t.Fatalf("read sub_token: %v", err)
	}

	// Use a chat ID unlikely to collide with other rows, and clear it after.
	var linkChat int64 = 778899
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(),
			`UPDATE users SET telegram_id = NULL WHERE telegram_id = $1`, linkChat)
	})
	_, _ = db.Exec(context.Background(),
		`UPDATE users SET telegram_id = NULL WHERE telegram_id = $1`, linkChat)

	f.reset()
	s.handleCommand(command(linkChat, "/link "+subToken))
	if got := f.lastText(); !strings.Contains(got, "linked") {
		t.Fatalf("expected link confirmation, got %q", got)
	}

	f.reset()
	s.handleCommand(command(linkChat, "/mystatus"))
	status := f.lastText()
	if !strings.Contains(status, "Your Status") {
		t.Errorf("/mystatus after linking should show status, got %q", status)
	}
	if !strings.Contains(status, "무제한") && !strings.Contains(status, "Unlimited") {
		t.Errorf("/mystatus should report the traffic allowance, got %q", status)
	}
}

func TestStatsCommandReportsSummary(t *testing.T) {
	db := testDB(t)
	f := newFakeTelegram(t)
	s := newTestBot(t, f, db, adminChat)

	s.handleCommand(command(adminChatID, "/stats"))
	got := f.lastText()
	for _, want := range []string{"Total Users", "Active Users", "Total Nodes", "Online Nodes"} {
		if !strings.Contains(got, want) {
			t.Errorf("/stats output missing %q; got %q", want, got)
		}
	}
}

// Guard against a regression in the endpoint wiring: the fake server only
// answers /bot-test-token/*, so if tgbotapi ever stopped substituting the
// token the tests above would silently pass with zero recorded messages.
func TestFakeEndpointActuallyReceivesToken(t *testing.T) {
	f := newFakeTelegram(t)
	s := newTestBot(t, f, nil, adminChat)

	s.sendReply(adminChatID, "ping")

	msgs := f.messages()
	if len(msgs) != 1 {
		t.Fatalf("expected the fake Telegram server to receive 1 message, got %d", len(msgs))
	}
	if msgs[0].ChatID != adminChat {
		t.Errorf("chat_id = %q, want %q", msgs[0].ChatID, adminChat)
	}
	if _, err := url.Parse(f.server.URL); err != nil {
		t.Fatalf("bad fake server URL: %v", err)
	}
}
