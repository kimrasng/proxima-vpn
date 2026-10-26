package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
)

const (
	supportSubjectMax        = 120
	supportBodyMax           = 5000
	supportNameMax           = 80
	supportQuestionMax       = 300
	supportAnswerMax         = 5000
	supportMaxOpenPerUser    = 10
	supportMaxMessagesTicket = 200
)

type SupportHandler struct {
	db       *pgxpool.Pool
	mailer   mail.Sender
	linkBase string
}

func NewSupportHandler(db *pgxpool.Pool, mailer mail.Sender, panelURL string) *SupportHandler {
	return &SupportHandler{db: db, mailer: mailer, linkBase: resetLinkBase(panelURL)}
}

type supportCategoryOut struct {
	ID        string            `json:"id"`
	Slug      string            `json:"slug"`
	Names     map[string]string `json:"names"`
	SortOrder int               `json:"sort_order"`
	IsActive  bool              `json:"is_active"`
}

type supportFAQOut struct {
	ID         string            `json:"id"`
	CategoryID string            `json:"category_id"`
	Questions  map[string]string `json:"questions"`
	Answers    map[string]string `json:"answers"`
	SortOrder  int               `json:"sort_order"`
	IsActive   bool              `json:"is_active"`
}

type supportTicketOut struct {
	ID            string            `json:"id"`
	CategoryID    string            `json:"category_id"`
	CategoryNames map[string]string `json:"category_names"`
	Subject       string            `json:"subject"`
	Status        string            `json:"status"`
	Priority      string            `json:"priority"`
	Unread        bool              `json:"unread"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	LastReplyAt   *time.Time        `json:"last_reply_at"`
}

type supportMessageOut struct {
	ID          string    `json:"id"`
	AuthorType  string    `json:"author_type"`
	AuthorLabel string    `json:"author_label,omitempty"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
}

type supportBodyRequest struct {
	Body string `json:"body"`
}

type createSupportTicketRequest struct {
	CategoryID string `json:"category_id"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
}

func supportFail(c *fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{"error": msg})
}

func supportServerError(c *fiber.Ctx, what string, err error) error {
	log.Printf("[support] %s: %v", what, err)
	return supportFail(c, fiber.StatusInternalServerError, "internal server error")
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func runeLenBetween(s string, min, max int) bool {
	n := utf8.RuneCountInString(s)
	return n >= min && n <= max
}

func hasControlRunes(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return true
		}
	}
	return false
}

func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func cleanLocalized(in map[string]string, max int, requireAny bool) (map[string]string, bool) {
	out := map[string]string{}
	for _, code := range []string{"ko", "en", "zh"} {
		v := strings.TrimSpace(in[code])
		if v == "" {
			continue
		}
		if !runeLenBetween(v, 1, max) || hasControlRunes(v) {
			return nil, false
		}
		out[code] = v
	}
	if requireAny && len(out) == 0 {
		return nil, false
	}
	return out, true
}

func localizedJSON(m map[string]string) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func supportUserID(c *fiber.Ctx) string {
	return c.Locals("user_id").(string)
}

// ListFAQ handles GET /api/v1/user/support/faq.
// @Summary Support help
// @Description Active help categories and answers shown before opening a ticket
// @Tags user-support
// @Produce json
// @Security BearerAuth
// @Router /user/support/faq [get]
func (h *SupportHandler) ListFAQ(c *fiber.Ctx) error {
	ctx := c.UserContext()
	catRows, err := h.db.Query(ctx, `SELECT id::text, slug, names, sort_order, is_active FROM support_categories WHERE is_active ORDER BY sort_order, slug`)
	if err != nil {
		return supportServerError(c, "list categories", err)
	}
	defer catRows.Close()
	categories := make([]supportCategoryOut, 0)
	for catRows.Next() {
		var o supportCategoryOut
		if err := catRows.Scan(&o.ID, &o.Slug, &o.Names, &o.SortOrder, &o.IsActive); err != nil {
			return supportServerError(c, "scan category", err)
		}
		categories = append(categories, o)
	}
	if err := catRows.Err(); err != nil {
		return supportServerError(c, "categories", err)
	}

	query := `SELECT f.id::text, f.category_id::text, f.questions, f.answers, f.sort_order, f.is_active
		FROM support_faqs f JOIN support_categories c ON c.id = f.category_id
		WHERE f.is_active AND c.is_active`
	args := []any{}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		if !runeLenBetween(q, 1, 100) {
			return supportFail(c, fiber.StatusBadRequest, "search text is too long")
		}
		query += ` AND (f.questions::text ILIKE $1 ESCAPE '\' OR f.answers::text ILIKE $1 ESCAPE '\')`
		args = append(args, likePattern(q))
	}
	query += ` ORDER BY c.sort_order, f.sort_order, f.created_at`
	faqRows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		return supportServerError(c, "list faqs", err)
	}
	defer faqRows.Close()
	faqs := make([]supportFAQOut, 0)
	for faqRows.Next() {
		var o supportFAQOut
		if err := faqRows.Scan(&o.ID, &o.CategoryID, &o.Questions, &o.Answers, &o.SortOrder, &o.IsActive); err != nil {
			return supportServerError(c, "scan faq", err)
		}
		faqs = append(faqs, o)
	}
	if err := faqRows.Err(); err != nil {
		return supportServerError(c, "faqs", err)
	}
	return c.JSON(fiber.Map{"categories": categories, "faqs": faqs})
}

// CreateTicket handles POST /api/v1/user/support/tickets.
// @Summary Open a support ticket
// @Tags user-support
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /user/support/tickets [post]
func (h *SupportHandler) CreateTicket(c *fiber.Ctx) error {
	var req createSupportTicketRequest
	if err := c.BodyParser(&req); err != nil {
		return supportFail(c, fiber.StatusBadRequest, "invalid request body")
	}
	subject := singleLine(req.Subject)
	body := strings.TrimSpace(req.Body)
	if !runeLenBetween(subject, 1, supportSubjectMax) || hasControlRunes(subject) {
		return supportFail(c, fiber.StatusBadRequest, "subject must be 1-120 characters")
	}
	if !runeLenBetween(body, 1, supportBodyMax) || hasControlRunes(body) {
		return supportFail(c, fiber.StatusBadRequest, "message must be 1-5000 characters")
	}
	if !isUUID(req.CategoryID) {
		return supportFail(c, fiber.StatusBadRequest, "choose a category")
	}

	userID := supportUserID(c)
	ctx := c.UserContext()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return supportServerError(c, "begin create", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialises one user's concurrent submissions so the open-ticket cap holds.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "support:"+userID); err != nil {
		return supportServerError(c, "lock create", err)
	}
	var categoryOK bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM support_categories WHERE id = $1 AND is_active)`, req.CategoryID).Scan(&categoryOK); err != nil {
		return supportServerError(c, "check category", err)
	}
	if !categoryOK {
		return supportFail(c, fiber.StatusBadRequest, "choose a category")
	}
	var open int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM support_tickets WHERE user_id = $1 AND status <> 'closed'`, userID).Scan(&open); err != nil {
		return supportServerError(c, "count open", err)
	}
	if open >= supportMaxOpenPerUser {
		return supportFail(c, fiber.StatusTooManyRequests, "too many open tickets; close one before opening another")
	}
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO support_tickets (user_id, category_id, subject) VALUES ($1, $2, $3) RETURNING id::text`,
		userID, req.CategoryID, subject,
	).Scan(&id); err != nil {
		return supportServerError(c, "insert ticket", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO support_messages (ticket_id, author_type, body) VALUES ($1, 'user', $2)`, id, body); err != nil {
		return supportServerError(c, "insert first message", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return supportServerError(c, "commit create", err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
}

// ListTickets handles GET /api/v1/user/support/tickets.
// @Summary List my support tickets
// @Tags user-support
// @Produce json
// @Security BearerAuth
// @Router /user/support/tickets [get]
func (h *SupportHandler) ListTickets(c *fiber.Ctx) error {
	rows, err := h.db.Query(c.UserContext(),
		`SELECT t.id::text, COALESCE(t.category_id::text, ''), COALESCE(c.names, '{}'::jsonb), t.subject,
		        t.status, t.priority, t.user_unread, t.created_at, t.updated_at, t.last_reply_at
		 FROM support_tickets t LEFT JOIN support_categories c ON c.id = t.category_id
		 WHERE t.user_id = $1 ORDER BY t.updated_at DESC LIMIT 200`, supportUserID(c))
	if err != nil {
		return supportServerError(c, "list tickets", err)
	}
	defer rows.Close()
	out := make([]supportTicketOut, 0)
	for rows.Next() {
		var t supportTicketOut
		if err := rows.Scan(&t.ID, &t.CategoryID, &t.CategoryNames, &t.Subject, &t.Status, &t.Priority, &t.Unread, &t.CreatedAt, &t.UpdatedAt, &t.LastReplyAt); err != nil {
			return supportServerError(c, "scan ticket", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return supportServerError(c, "tickets", err)
	}
	return c.JSON(out)
}

// GetTicket handles GET /api/v1/user/support/tickets/:id.
// @Summary Get one of my tickets with its messages
// @Tags user-support
// @Produce json
// @Security BearerAuth
// @Router /user/support/tickets/{id} [get]
func (h *SupportHandler) GetTicket(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	ctx := c.UserContext()
	var t supportTicketOut
	err := h.db.QueryRow(ctx,
		`SELECT t.id::text, COALESCE(t.category_id::text, ''), COALESCE(c.names, '{}'::jsonb), t.subject,
		        t.status, t.priority, t.user_unread, t.created_at, t.updated_at, t.last_reply_at
		 FROM support_tickets t LEFT JOIN support_categories c ON c.id = t.category_id
		 WHERE t.id = $1 AND t.user_id = $2`, id, supportUserID(c),
	).Scan(&t.ID, &t.CategoryID, &t.CategoryNames, &t.Subject, &t.Status, &t.Priority, &t.Unread, &t.CreatedAt, &t.UpdatedAt, &t.LastReplyAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	if err != nil {
		return supportServerError(c, "get ticket", err)
	}
	if t.Unread {
		if _, err := h.db.Exec(ctx, `UPDATE support_tickets SET user_unread = FALSE WHERE id = $1`, id); err != nil {
			log.Printf("[support] mark read: %v", err)
		}
	}
	// The operator's address is never sent to users, so author_label stays empty.
	rows, err := h.db.Query(ctx, `SELECT id::text, author_type, body, created_at FROM support_messages WHERE ticket_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return supportServerError(c, "list messages", err)
	}
	defer rows.Close()
	messages := make([]supportMessageOut, 0)
	for rows.Next() {
		var m supportMessageOut
		if err := rows.Scan(&m.ID, &m.AuthorType, &m.Body, &m.CreatedAt); err != nil {
			return supportServerError(c, "scan message", err)
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return supportServerError(c, "messages", err)
	}
	t.Unread = false
	return c.JSON(fiber.Map{"ticket": t, "messages": messages})
}

// PostMessage handles POST /api/v1/user/support/tickets/:id/messages.
// @Summary Reply on one of my tickets
// @Tags user-support
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /user/support/tickets/{id}/messages [post]
func (h *SupportHandler) PostMessage(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	var req supportBodyRequest
	if err := c.BodyParser(&req); err != nil {
		return supportFail(c, fiber.StatusBadRequest, "invalid request body")
	}
	body := strings.TrimSpace(req.Body)
	if !runeLenBetween(body, 1, supportBodyMax) || hasControlRunes(body) {
		return supportFail(c, fiber.StatusBadRequest, "message must be 1-5000 characters")
	}
	ctx := c.UserContext()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return supportServerError(c, "begin reply", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM support_tickets WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, supportUserID(c)).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	if err != nil {
		return supportServerError(c, "lock ticket", err)
	}
	if status == "closed" {
		return supportFail(c, fiber.StatusConflict, "this ticket is closed; open a new one")
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM support_messages WHERE ticket_id = $1`, id).Scan(&count); err != nil {
		return supportServerError(c, "count messages", err)
	}
	if count >= supportMaxMessagesTicket {
		return supportFail(c, fiber.StatusTooManyRequests, "this ticket has too many messages; open a new one")
	}
	var m supportMessageOut
	if err := tx.QueryRow(ctx,
		`INSERT INTO support_messages (ticket_id, author_type, body) VALUES ($1, 'user', $2) RETURNING id::text, author_type, body, created_at`, id, body,
	).Scan(&m.ID, &m.AuthorType, &m.Body, &m.CreatedAt); err != nil {
		return supportServerError(c, "insert message", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE support_tickets SET status = 'open', admin_unread = TRUE, user_unread = FALSE, updated_at = NOW() WHERE id = $1`, id); err != nil {
		return supportServerError(c, "touch ticket", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return supportServerError(c, "commit reply", err)
	}
	return c.Status(fiber.StatusCreated).JSON(m)
}

// CloseTicket handles POST /api/v1/user/support/tickets/:id/close.
// @Summary Close one of my tickets
// @Tags user-support
// @Produce json
// @Security BearerAuth
// @Router /user/support/tickets/{id}/close [post]
func (h *SupportHandler) CloseTicket(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	tag, err := h.db.Exec(c.UserContext(),
		`UPDATE support_tickets SET status = 'closed', closed_at = COALESCE(closed_at, NOW()), updated_at = NOW() WHERE id = $1 AND user_id = $2`,
		id, supportUserID(c))
	if err != nil {
		return supportServerError(c, "close ticket", err)
	}
	if tag.RowsAffected() == 0 {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	return c.JSON(fiber.Map{"message": "ticket closed"})
}

func (h *SupportHandler) fetchTicketUser(ctx context.Context, ticketID string) (email, language, subject string, err error) {
	err = h.db.QueryRow(ctx,
		`SELECT u.email, u.language, t.subject FROM support_tickets t JOIN users u ON u.id = t.user_id WHERE t.id = $1`, ticketID,
	).Scan(&email, &language, &subject)
	return
}
