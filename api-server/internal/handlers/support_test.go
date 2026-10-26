package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
)

const supportTestPanelURL = "https://panel.example.test"

type supportMailer struct {
	sent chan mail.Message
	err  error
}

func newSupportMailer(err error) *supportMailer {
	return &supportMailer{sent: make(chan mail.Message, 16), err: err}
}

func (m *supportMailer) Send(_ context.Context, msg mail.Message) error {
	m.sent <- msg
	return m.err
}

func (m *supportMailer) next(t *testing.T) mail.Message {
	t.Helper()
	select {
	case msg := <-m.sent:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no email was sent")
		return mail.Message{}
	}
}

func (m *supportMailer) assertNone(t *testing.T) {
	t.Helper()
	select {
	case msg := <-m.sent:
		t.Fatalf("unexpected email to %s: %q", msg.To, msg.Subject)
	default:
	}
}

type supportFixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	app  *fiber.App
	seq  int
}

func newSupportFixture(t *testing.T, sender mail.Sender) *supportFixture {
	t.Helper()
	pool := signupTestDB(t)
	h := NewSupportHandler(pool, sender, supportTestPanelURL)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", c.Get("X-Test-User"))
		c.Locals("email", c.Get("X-Test-Admin"))
		return c.Next()
	})
	user := app.Group("/user/support")
	user.Get("/faq", h.ListFAQ)
	user.Get("/tickets", h.ListTickets)
	user.Post("/tickets", h.CreateTicket)
	user.Get("/tickets/:id", h.GetTicket)
	user.Post("/tickets/:id/messages", h.PostMessage)
	user.Post("/tickets/:id/close", h.CloseTicket)
	admin := app.Group("/admin/support")
	admin.Get("/summary", h.AdminSummary)
	admin.Get("/tickets", h.AdminListTickets)
	admin.Get("/tickets/:id", h.AdminGetTicket)
	admin.Post("/tickets/:id/messages", h.AdminReply)
	admin.Patch("/tickets/:id", h.AdminUpdateTicket)
	admin.Delete("/tickets/:id", h.AdminDeleteTicket)
	admin.Get("/categories", h.AdminListCategories)
	admin.Post("/categories", h.AdminCreateCategory)
	admin.Put("/categories/:id", h.AdminUpdateCategory)
	admin.Delete("/categories/:id", h.AdminDeleteCategory)
	admin.Get("/faqs", h.AdminListFAQs)
	admin.Post("/faqs", h.AdminCreateFAQ)
	admin.Put("/faqs/:id", h.AdminUpdateFAQ)
	admin.Delete("/faqs/:id", h.AdminDeleteFAQ)
	return &supportFixture{t: t, pool: pool, app: app}
}

func (f *supportFixture) do(method, path, userID, adminEmail string, body any) (int, []byte) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", userID)
	req.Header.Set("X-Test-Admin", adminEmail)
	res, err := f.app.Test(req, -1)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (f *supportFixture) expect(want int, method, path, userID, adminEmail string, body any) []byte {
	f.t.Helper()
	got, raw := f.do(method, path, userID, adminEmail, body)
	if got != want {
		f.t.Fatalf("%s %s = %d, want %d: %s", method, path, got, want, raw)
	}
	return raw
}

func (f *supportFixture) newUser(language string) (id, email string) {
	f.t.Helper()
	f.seq++
	email = fmt.Sprintf("support-%d-%d@example.test", time.Now().UnixNano(), f.seq)
	err := f.pool.QueryRow(context.Background(),
		`INSERT INTO users(email,password_hash,name,status,language) VALUES($1,'x','Support Tester','active',$2) RETURNING id::text`,
		email, language).Scan(&id)
	if err != nil {
		f.t.Fatalf("seed user: %v", err)
	}
	f.t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	return id, email
}

func (f *supportFixture) categoryID(slug string) string {
	f.t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(), `SELECT id::text FROM support_categories WHERE slug = $1`, slug).Scan(&id); err != nil {
		f.t.Fatalf("category %s: %v", slug, err)
	}
	return id
}

func (f *supportFixture) open(userID, categoryID, subject, body string) string {
	f.t.Helper()
	raw := f.expect(201, "POST", "/user/support/tickets", userID, "", map[string]string{"category_id": categoryID, "subject": subject, "body": body})
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		f.t.Fatalf("create response %s: %v", raw, err)
	}
	return out.ID
}

type supportThread struct {
	Ticket struct {
		Status string `json:"status"`
		Unread bool   `json:"unread"`
	} `json:"ticket"`
	Messages []struct {
		AuthorType  string `json:"author_type"`
		AuthorLabel string `json:"author_label"`
		Body        string `json:"body"`
	} `json:"messages"`
}

func (f *supportFixture) thread(userID, ticketID string) supportThread {
	f.t.Helper()
	raw := f.expect(200, "GET", "/user/support/tickets/"+ticketID, userID, "", nil)
	var out supportThread
	if err := json.Unmarshal(raw, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *supportFixture) listedUnread(userID, ticketID string) (found, unread bool, status string) {
	f.t.Helper()
	raw := f.expect(200, "GET", "/user/support/tickets", userID, "", nil)
	var list []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Unread bool   `json:"unread"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		f.t.Fatal(err)
	}
	for _, item := range list {
		if item.ID == ticketID {
			return true, item.Unread, item.Status
		}
	}
	return false, false, ""
}

func TestSupportConversationAndReplyEmail(t *testing.T) {
	m := newSupportMailer(nil)
	f := newSupportFixture(t, m)
	userID, email := f.newUser("ko")
	category := f.categoryID("billing")
	const admin = "ops@example.test"

	a := f.open(userID, category, "  결제가   안돼요 ", "주문 번호 123")
	if found, unread, status := f.listedUnread(userID, a); !found || unread || status != "open" {
		t.Fatalf("fresh ticket: found=%v unread=%v status=%q", found, unread, status)
	}

	raw := f.expect(200, "GET", "/admin/support/tickets?status=open&q="+"%EA%B2%B0%EC%A0%9C", "", admin, nil)
	var inbox struct {
		Tickets []struct {
			ID        string `json:"id"`
			Subject   string `json:"subject"`
			UserEmail string `json:"user_email"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal(raw, &inbox); err != nil {
		t.Fatal(err)
	}
	var inInbox bool
	for _, item := range inbox.Tickets {
		if item.ID == a {
			inInbox = true
			if item.UserEmail != email || item.Subject != "결제가 안돼요" {
				t.Fatalf("inbox row = %+v", item)
			}
		}
	}
	if !inInbox {
		t.Fatalf("ticket %s missing from the admin inbox: %s", a, raw)
	}

	f.expect(201, "POST", "/admin/support/tickets/"+a+"/messages", "", admin, map[string]string{"body": "확인해 보겠습니다."})
	first := m.next(t)
	if first.To != email || !strings.Contains(first.Body, supportTestPanelURL+"/portal/tickets/"+a) {
		t.Fatalf("reply email = to %q body %q", first.To, first.Body)
	}

	if found, unread, status := f.listedUnread(userID, a); !found || !unread || status != "answered" {
		t.Fatalf("after reply: found=%v unread=%v status=%q", found, unread, status)
	}
	thread := f.thread(userID, a)
	if len(thread.Messages) != 2 || thread.Messages[0].AuthorType != "user" || thread.Messages[1].AuthorType != "admin" {
		t.Fatalf("thread = %+v", thread.Messages)
	}
	if thread.Messages[1].AuthorLabel != "" {
		t.Fatalf("the operator's address leaked to the user: %q", thread.Messages[1].AuthorLabel)
	}
	if _, unread, _ := f.listedUnread(userID, a); unread {
		t.Fatal("reading the thread did not clear the unread flag")
	}

	f.expect(201, "POST", "/user/support/tickets/"+a+"/messages", userID, "", map[string]string{"body": "감사합니다, 추가로 문의드려요."})
	if got := f.thread(userID, a).Ticket.Status; got != "open" {
		t.Fatalf("status after user reply = %q, want open", got)
	}
	f.expect(200, "POST", "/user/support/tickets/"+a+"/close", userID, "", nil)
	f.expect(409, "POST", "/user/support/tickets/"+a+"/messages", userID, "", map[string]string{"body": "one more"})
	f.expect(409, "POST", "/admin/support/tickets/"+a+"/messages", "", admin, map[string]string{"body": "late reply"})

	b := f.open(userID, category, "second question", "hello")
	f.expect(201, "POST", "/admin/support/tickets/"+b+"/messages", "", admin, map[string]string{"body": "second answer"})
	second := m.next(t)
	if !strings.Contains(second.Body, "/portal/tickets/"+b) || strings.Contains(second.Body, a) {
		t.Fatalf("second email does not point at the second ticket only: %q", second.Body)
	}
	m.assertNone(t)
}

func TestSupportTicketsBelongToTheirOwner(t *testing.T) {
	f := newSupportFixture(t, nil)
	owner, _ := f.newUser("en")
	other, _ := f.newUser("en")
	id := f.open(owner, f.categoryID("account"), "private", "secret details")

	f.expect(404, "GET", "/user/support/tickets/"+id, other, "", nil)
	f.expect(404, "POST", "/user/support/tickets/"+id+"/messages", other, "", map[string]string{"body": "intrusion"})
	f.expect(404, "POST", "/user/support/tickets/"+id+"/close", other, "", nil)
	f.expect(404, "GET", "/user/support/tickets/not-a-uuid", owner, "", nil)
	if found, _, _ := f.listedUnread(other, id); found {
		t.Fatal("another user's list contains the ticket")
	}
	thread := f.thread(owner, id)
	if thread.Ticket.Status != "open" || len(thread.Messages) != 1 {
		t.Fatalf("ticket was changed by a non-owner: %+v", thread)
	}
}

func TestSupportInputLimits(t *testing.T) {
	f := newSupportFixture(t, nil)
	userID, _ := f.newUser("en")
	category := f.categoryID("plans")
	create := func(want int, category, subject, body string) {
		t.Helper()
		f.expect(want, "POST", "/user/support/tickets", userID, "", map[string]string{"category_id": category, "subject": subject, "body": body})
	}
	create(400, category, "", "body")
	create(400, category, "subject", "   ")
	create(400, category, strings.Repeat("가", 121), "body")
	create(400, category, "subject", strings.Repeat("a", 5001))
	create(400, category, "bad\x00subject", "body")
	create(400, "not-a-uuid", "subject", "body")
	create(400, "00000000-0000-4000-8000-000000000000", "subject", "body")
	create(201, category, strings.Repeat("가", 120), strings.Repeat("a", 5000))

	inactive := f.categoryID("apps")
	if _, err := f.pool.Exec(context.Background(), `UPDATE support_categories SET is_active = FALSE WHERE id = $1`, inactive); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `UPDATE support_categories SET is_active = TRUE WHERE id = $1`, inactive)
	})
	create(400, inactive, "subject", "body")
}

func TestSupportOpenTicketCap(t *testing.T) {
	f := newSupportFixture(t, nil)
	userID, _ := f.newUser("en")
	category := f.categoryID("clients")
	var first string
	for i := 0; i < supportMaxOpenPerUser; i++ {
		id := f.open(userID, category, fmt.Sprintf("question %d", i), "body")
		if first == "" {
			first = id
		}
	}
	f.expect(429, "POST", "/user/support/tickets", userID, "", map[string]string{"category_id": category, "subject": "one too many", "body": "body"})
	f.expect(200, "POST", "/user/support/tickets/"+first+"/close", userID, "", nil)
	f.open(userID, category, "fits again", "body")
}

func TestSupportReplySucceedsWhenEmailCannotBeSent(t *testing.T) {
	t.Run("smtp error", func(t *testing.T) {
		m := newSupportMailer(errors.New("smtp unavailable"))
		f := newSupportFixture(t, m)
		userID, _ := f.newUser("zh")
		id := f.open(userID, f.categoryID("billing"), "subject", "body")
		f.expect(201, "POST", "/admin/support/tickets/"+id+"/messages", "", "ops@example.test", map[string]string{"body": "answer"})
		m.next(t)
		if got := len(f.thread(userID, id).Messages); got != 2 {
			t.Fatalf("messages = %d, want 2", got)
		}
	})
	t.Run("mail not configured", func(t *testing.T) {
		f := newSupportFixture(t, nil)
		userID, _ := f.newUser("en")
		id := f.open(userID, f.categoryID("billing"), "subject", "body")
		f.expect(201, "POST", "/admin/support/tickets/"+id+"/messages", "", "ops@example.test", map[string]string{"body": "answer"})
		if got := len(f.thread(userID, id).Messages); got != 2 {
			t.Fatalf("messages = %d, want 2", got)
		}
	})
}

func TestSupportAdminTicketControls(t *testing.T) {
	f := newSupportFixture(t, nil)
	userID, _ := f.newUser("en")
	id := f.open(userID, f.categoryID("billing"), "subject", "body")
	const admin = "ops@example.test"

	f.expect(400, "PATCH", "/admin/support/tickets/"+id, "", admin, map[string]string{"status": "bogus"})
	f.expect(400, "PATCH", "/admin/support/tickets/"+id, "", admin, map[string]string{})
	f.expect(200, "PATCH", "/admin/support/tickets/"+id, "", admin, map[string]string{"status": "closed", "priority": "high"})
	if got := f.thread(userID, id).Ticket.Status; got != "closed" {
		t.Fatalf("status = %q, want closed", got)
	}
	f.expect(200, "PATCH", "/admin/support/tickets/"+id, "", admin, map[string]string{"status": "open"})
	f.expect(201, "POST", "/user/support/tickets/"+id+"/messages", userID, "", map[string]string{"body": "reopened and replying"})
	f.expect(200, "DELETE", "/admin/support/tickets/"+id, "", admin, nil)
	f.expect(404, "GET", "/admin/support/tickets/"+id, "", admin, nil)
	f.expect(404, "PATCH", "/admin/support/tickets/00000000-0000-4000-8000-000000000000", "", admin, map[string]string{"status": "open"})
}

func TestSupportHelpContentManagement(t *testing.T) {
	f := newSupportFixture(t, nil)
	userID, _ := f.newUser("en")
	const admin = "ops@example.test"
	needle := fmt.Sprintf("needle%d", time.Now().UnixNano())
	slug := fmt.Sprintf("t-%d", time.Now().UnixNano())

	f.expect(400, "POST", "/admin/support/categories", "", admin, map[string]any{"slug": "Bad Slug", "names": map[string]string{"en": "x"}})
	f.expect(400, "POST", "/admin/support/categories", "", admin, map[string]any{"slug": slug, "names": map[string]string{}})
	raw := f.expect(201, "POST", "/admin/support/categories", "", admin, map[string]any{"slug": slug, "names": map[string]string{"ko": "테스트", "en": "Test"}, "sort_order": 999})
	var category struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &category); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM support_categories WHERE slug = $1`, slug)
	})
	f.expect(409, "POST", "/admin/support/categories", "", admin, map[string]any{"slug": slug, "names": map[string]string{"en": "dup"}})

	f.expect(400, "POST", "/admin/support/faqs", "", admin, map[string]any{"category_id": "00000000-0000-4000-8000-000000000000", "questions": map[string]string{"en": "q"}, "answers": map[string]string{"en": "a"}})
	f.expect(400, "POST", "/admin/support/faqs", "", admin, map[string]any{"category_id": category.ID, "questions": map[string]string{"en": "q"}, "answers": map[string]string{}})
	raw = f.expect(201, "POST", "/admin/support/faqs", "", admin, map[string]any{"category_id": category.ID, "questions": map[string]string{"en": "How do I " + needle + "?"}, "answers": map[string]string{"en": "Like this."}})
	var faq struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &faq); err != nil {
		t.Fatal(err)
	}

	faqCount := func(query string) int {
		t.Helper()
		raw := f.expect(200, "GET", "/user/support/faq"+query, userID, "", nil)
		var out struct {
			FAQs []struct {
				CategoryID string `json:"category_id"`
			} `json:"faqs"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, item := range out.FAQs {
			if item.CategoryID == category.ID {
				n++
			}
		}
		return n
	}
	if got := faqCount("?q=" + needle); got != 1 {
		t.Fatalf("search for the new answer found %d", got)
	}
	var wildcard struct {
		FAQs []json.RawMessage `json:"faqs"`
	}
	if err := json.Unmarshal(f.expect(200, "GET", "/user/support/faq?q=%25", userID, "", nil), &wildcard); err != nil {
		t.Fatal(err)
	}
	if len(wildcard.FAQs) != 0 {
		t.Fatalf("a literal %% search matched %d answers; wildcards must be escaped", len(wildcard.FAQs))
	}

	f.expect(200, "PUT", "/admin/support/faqs/"+faq.ID, "", admin, map[string]any{"is_active": false})
	if got := faqCount(""); got != 0 {
		t.Fatalf("an inactive answer is still shown to users (%d)", got)
	}
	f.expect(200, "PUT", "/admin/support/faqs/"+faq.ID, "", admin, map[string]any{"is_active": true})
	f.expect(200, "DELETE", "/admin/support/categories/"+category.ID, "", admin, nil)
	if got := faqCount(""); got != 0 {
		t.Fatalf("deleting a category left %d answers behind", got)
	}
	f.expect(404, "DELETE", "/admin/support/categories/"+category.ID, "", admin, nil)
}
