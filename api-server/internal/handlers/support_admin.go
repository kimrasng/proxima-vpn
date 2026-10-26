package handlers

import (
	"context"
	"errors"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/proximavpn/proxima-vpn/api-server/internal/mail"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

var supportSlugPattern = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

type adminSupportTicketOut struct {
	supportTicketOut
	UserID    string `json:"user_id"`
	UserEmail string `json:"user_email"`
	UserName  string `json:"user_name"`

	AssignedAdminID    *string `json:"assigned_admin_id"`
	AssignedAdminEmail *string `json:"assigned_admin_email"`
	AssignedAdminName  *string `json:"assigned_admin_name"`
}

type adminReplyRequest struct {
	Body       string `json:"body"`
	NotifyUser bool   `json:"notify_user"`
}

func adminLabel(c *fiber.Ctx) string {
	email, _ := c.Locals("email").(string)
	return email
}

func isSupportConstraintError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23505")
}

// ListTickets handles GET /api/v1/admin/support/tickets.
// @Summary List support tickets
// @Tags admin-support
// @Produce json
// @Security BearerAuth
// @Router /admin/support/tickets [get]
func (h *SupportHandler) AdminListTickets(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	where := []string{"1=1"}
	args := []any{}
	if st := c.Query("status"); st != "" {
		if st != "open" && st != "answered" && st != "closed" {
			return supportFail(c, fiber.StatusBadRequest, "invalid status")
		}
		args = append(args, st)
		where = append(where, "t.status = $"+strconv.Itoa(len(args)))
	}
	switch c.Query("assignee") {
	case "", "all":
	case "mine":
		args = append(args, adminIDOf(c))
		where = append(where, "t.assigned_admin_id = $"+strconv.Itoa(len(args)))
	case "unassigned":
		where = append(where, "t.assigned_admin_id IS NULL")
	default:
		return supportFail(c, fiber.StatusBadRequest, "invalid assignee filter")
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		if !runeLenBetween(q, 1, 100) {
			return supportFail(c, fiber.StatusBadRequest, "search text is too long")
		}
		args = append(args, likePattern(q))
		n := strconv.Itoa(len(args))
		where = append(where, "(t.subject ILIKE $"+n+" ESCAPE '\\' OR u.email ILIKE $"+n+" ESCAPE '\\' OR u.name ILIKE $"+n+" ESCAPE '\\')")
	}
	whereSQL := strings.Join(where, " AND ")
	ctx := c.UserContext()
	var total int
	if err := h.db.QueryRow(ctx, "SELECT count(*) FROM support_tickets t JOIN users u ON u.id = t.user_id WHERE "+whereSQL, args...).Scan(&total); err != nil {
		return supportServerError(c, "admin count", err)
	}
	args = append(args, limit, (page-1)*limit)
	rows, err := h.db.Query(ctx,
		`SELECT t.id::text, COALESCE(t.category_id::text, ''), COALESCE(cat.names, '{}'::jsonb), t.subject, t.status, t.priority,
		        t.admin_unread, t.created_at, t.updated_at, t.last_reply_at, u.id::text, u.email, u.name,
		        t.assigned_admin_id::text, aa.email, aa.name
		 FROM support_tickets t JOIN users u ON u.id = t.user_id LEFT JOIN support_categories cat ON cat.id = t.category_id
		 LEFT JOIN admins aa ON aa.id = t.assigned_admin_id
		 WHERE `+whereSQL+` ORDER BY (t.status = 'open') DESC, t.updated_at DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return supportServerError(c, "admin list", err)
	}
	defer rows.Close()
	out := make([]adminSupportTicketOut, 0)
	for rows.Next() {
		var t adminSupportTicketOut
		if err := rows.Scan(&t.ID, &t.CategoryID, &t.CategoryNames, &t.Subject, &t.Status, &t.Priority, &t.Unread, &t.CreatedAt, &t.UpdatedAt, &t.LastReplyAt, &t.UserID, &t.UserEmail, &t.UserName, &t.AssignedAdminID, &t.AssignedAdminEmail, &t.AssignedAdminName); err != nil {
			return supportServerError(c, "admin scan", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return supportServerError(c, "admin rows", err)
	}
	return c.JSON(fiber.Map{"tickets": out, "total": total, "page": page, "limit": limit})
}

// AdminSummary handles GET /api/v1/admin/support/summary.
// @Summary Support ticket counters
// @Tags admin-support
// @Produce json
// @Security BearerAuth
// @Router /admin/support/summary [get]
func (h *SupportHandler) AdminSummary(c *fiber.Ctx) error {
	var open, answered, closed, unread, mine, unassigned int
	err := h.db.QueryRow(c.UserContext(),
		`SELECT count(*) FILTER (WHERE status = 'open'), count(*) FILTER (WHERE status = 'answered'),
		        count(*) FILTER (WHERE status = 'closed'), count(*) FILTER (WHERE admin_unread AND status <> 'closed'),
		        count(*) FILTER (WHERE assigned_admin_id = $1 AND status <> 'closed'),
		        count(*) FILTER (WHERE assigned_admin_id IS NULL AND status <> 'closed')
		 FROM support_tickets`, adminIDOf(c)).Scan(&open, &answered, &closed, &unread, &mine, &unassigned)
	if err != nil {
		return supportServerError(c, "summary", err)
	}
	return c.JSON(fiber.Map{"open": open, "answered": answered, "closed": closed, "unread": unread, "mine": mine, "unassigned": unassigned})
}

// AdminGetTicket handles GET /api/v1/admin/support/tickets/:id.
// @Summary Get a support ticket thread
// @Tags admin-support
// @Produce json
// @Security BearerAuth
// @Router /admin/support/tickets/{id} [get]
func (h *SupportHandler) AdminGetTicket(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	ctx := c.UserContext()
	var t adminSupportTicketOut
	err := h.db.QueryRow(ctx,
		`SELECT t.id::text, COALESCE(t.category_id::text, ''), COALESCE(cat.names, '{}'::jsonb), t.subject, t.status, t.priority,
		        t.admin_unread, t.created_at, t.updated_at, t.last_reply_at, u.id::text, u.email, u.name,
		        t.assigned_admin_id::text, aa.email, aa.name
		 FROM support_tickets t JOIN users u ON u.id = t.user_id LEFT JOIN support_categories cat ON cat.id = t.category_id
		 LEFT JOIN admins aa ON aa.id = t.assigned_admin_id
		 WHERE t.id = $1`, id,
	).Scan(&t.ID, &t.CategoryID, &t.CategoryNames, &t.Subject, &t.Status, &t.Priority, &t.Unread, &t.CreatedAt, &t.UpdatedAt, &t.LastReplyAt, &t.UserID, &t.UserEmail, &t.UserName, &t.AssignedAdminID, &t.AssignedAdminEmail, &t.AssignedAdminName)
	if errors.Is(err, pgx.ErrNoRows) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	if err != nil {
		return supportServerError(c, "admin get", err)
	}
	if t.Unread {
		if _, err := h.db.Exec(ctx, `UPDATE support_tickets SET admin_unread = FALSE WHERE id = $1`, id); err != nil {
			log.Printf("[support] mark admin read: %v", err)
		}
		t.Unread = false
	}
	rows, err := h.db.Query(ctx, `SELECT id::text, author_type, author_label, body, created_at FROM support_messages WHERE ticket_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return supportServerError(c, "admin messages", err)
	}
	defer rows.Close()
	messages := make([]supportMessageOut, 0)
	for rows.Next() {
		var m supportMessageOut
		if err := rows.Scan(&m.ID, &m.AuthorType, &m.AuthorLabel, &m.Body, &m.CreatedAt); err != nil {
			return supportServerError(c, "admin scan message", err)
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return supportServerError(c, "admin message rows", err)
	}
	return c.JSON(fiber.Map{"ticket": t, "messages": messages})
}

// AdminReply handles POST /api/v1/admin/support/tickets/:id/messages.
// @Summary Reply to a support ticket; emails the user only when notify_user is true
// @Tags admin-support
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/support/tickets/{id}/messages [post]
func (h *SupportHandler) AdminReply(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	var req adminReplyRequest
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
		return supportServerError(c, "begin admin reply", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM support_tickets WHERE id = $1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	if err != nil {
		return supportServerError(c, "lock ticket", err)
	}
	if status == "closed" {
		return supportFail(c, fiber.StatusConflict, "ticket is closed; reopen it before replying")
	}
	var m supportMessageOut
	if err := tx.QueryRow(ctx,
		`INSERT INTO support_messages (ticket_id, author_type, author_label, body) VALUES ($1, 'admin', $2, $3)
		 RETURNING id::text, author_type, author_label, body, created_at`, id, adminLabel(c), body,
	).Scan(&m.ID, &m.AuthorType, &m.AuthorLabel, &m.Body, &m.CreatedAt); err != nil {
		return supportServerError(c, "insert admin message", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE support_tickets SET status = 'answered', user_unread = TRUE, admin_unread = FALSE, last_reply_at = NOW(), updated_at = NOW() WHERE id = $1`, id); err != nil {
		return supportServerError(c, "touch ticket", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return supportServerError(c, "commit admin reply", err)
	}
	if req.NotifyUser {
		h.notifyReply(id)
	}
	return c.Status(fiber.StatusCreated).JSON(m)
}

func (h *SupportHandler) notifyReply(ticketID string) {
	if h.mailer == nil || h.linkBase == "" {
		log.Printf("[support] reply notification skipped for ticket %s: mail is not configured", ticketID)
		return
	}
	// Fiber route parameters reference a request buffer reused after the handler returns.
	ticketID = strings.Clone(ticketID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		email, language, subject, err := h.fetchTicketUser(ctx, ticketID)
		if err != nil {
			log.Printf("[support] reply notification lookup for ticket %s failed: %v", ticketID, err)
			return
		}
		mailSubject, mailBody := mail.SupportReplyEmail(normalizeResetLanguage(language), subject, h.linkBase+"/portal/tickets/"+ticketID)
		if err := h.mailer.Send(ctx, mail.Message{To: email, Subject: mailSubject, Body: mailBody}); err != nil {
			log.Printf("[support] reply notification for ticket %s failed: %v", ticketID, err)
		}
	}()
}

type updateSupportTicketRequest struct {
	Status   *string `json:"status"`
	Priority *string `json:"priority"`
	// AssignedAdminID assigns the ticket; an empty string unassigns it and an
	// absent field leaves the assignee unchanged.
	AssignedAdminID *string `json:"assigned_admin_id"`
}

// AdminUpdateTicket handles PATCH /api/v1/admin/support/tickets/:id.
// @Summary Change a ticket's status or priority
// @Tags admin-support
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/support/tickets/{id} [patch]
func (h *SupportHandler) AdminUpdateTicket(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	var req updateSupportTicketRequest
	if err := c.BodyParser(&req); err != nil {
		return supportFail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if req.Status == nil && req.Priority == nil && req.AssignedAdminID == nil {
		return supportFail(c, fiber.StatusBadRequest, "nothing to update")
	}
	if req.AssignedAdminID != nil && *req.AssignedAdminID != "" && !isUUID(*req.AssignedAdminID) {
		return supportFail(c, fiber.StatusBadRequest, "invalid assignee")
	}
	if req.Status != nil && *req.Status != "open" && *req.Status != "answered" && *req.Status != "closed" {
		return supportFail(c, fiber.StatusBadRequest, "invalid status")
	}
	if req.Priority != nil && *req.Priority != "low" && *req.Priority != "normal" && *req.Priority != "high" {
		return supportFail(c, fiber.StatusBadRequest, "invalid priority")
	}
	ctx := c.UserContext()
	if req.AssignedAdminID != nil && *req.AssignedAdminID != "" {
		var active bool
		err := h.db.QueryRow(ctx, `SELECT is_active FROM admins WHERE id = $1`, *req.AssignedAdminID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
			return supportFail(c, fiber.StatusBadRequest, "assignee must be an active admin")
		}
		if err != nil {
			return supportServerError(c, "load assignee", err)
		}
	}
	tag, err := h.db.Exec(ctx,
		`UPDATE support_tickets SET
		   status = COALESCE($2, status),
		   priority = COALESCE($3, priority),
		   closed_at = CASE WHEN $2::text = 'closed' THEN COALESCE(closed_at, NOW()) WHEN $2::text IS NOT NULL THEN NULL ELSE closed_at END,
		   assigned_admin_id = CASE WHEN $4::boolean THEN NULLIF($5, '')::uuid ELSE assigned_admin_id END,
		   updated_at = NOW()
		 WHERE id = $1`, id, req.Status, req.Priority, req.AssignedAdminID != nil, derefOrEmpty(req.AssignedAdminID))
	if err != nil {
		return supportServerError(c, "update ticket", err)
	}
	if tag.RowsAffected() == 0 {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	if req.AssignedAdminID != nil {
		services.NewActivityService(h.db).Log(ctx, services.Record{
			EventType:  services.EventSupportTicketAssigned,
			Severity:   services.SeverityInfo,
			ActorType:  "admin",
			ActorID:    adminIDOf(c),
			ActorLabel: adminEmail(c),
			Detail:     map[string]any{"ticket_id": id, "assignee_id": *req.AssignedAdminID},
		})
	}
	return c.JSON(fiber.Map{"message": "ticket updated"})
}

// AdminDeleteTicket handles DELETE /api/v1/admin/support/tickets/:id.
// @Summary Delete a support ticket
// @Tags admin-support
// @Produce json
// @Security BearerAuth
// @Router /admin/support/tickets/{id} [delete]
func (h *SupportHandler) AdminDeleteTicket(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	tag, err := h.db.Exec(c.UserContext(), `DELETE FROM support_tickets WHERE id = $1`, id)
	if err != nil {
		return supportServerError(c, "delete ticket", err)
	}
	if tag.RowsAffected() == 0 {
		return supportFail(c, fiber.StatusNotFound, "ticket not found")
	}
	return c.JSON(fiber.Map{"message": "ticket deleted"})
}

type supportCategoryRequest struct {
	Slug      *string           `json:"slug"`
	Names     map[string]string `json:"names"`
	SortOrder *int              `json:"sort_order"`
	IsActive  *bool             `json:"is_active"`
}

// AdminListCategories handles GET /api/v1/admin/support/categories.
// @Summary List support categories
// @Tags admin-support
// @Produce json
// @Security BearerAuth
// @Router /admin/support/categories [get]
func (h *SupportHandler) AdminListCategories(c *fiber.Ctx) error {
	rows, err := h.db.Query(c.UserContext(), `SELECT id::text, slug, names, sort_order, is_active FROM support_categories ORDER BY sort_order, slug`)
	if err != nil {
		return supportServerError(c, "admin categories", err)
	}
	defer rows.Close()
	out := make([]supportCategoryOut, 0)
	for rows.Next() {
		var o supportCategoryOut
		if err := rows.Scan(&o.ID, &o.Slug, &o.Names, &o.SortOrder, &o.IsActive); err != nil {
			return supportServerError(c, "admin scan category", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return supportServerError(c, "admin category rows", err)
	}
	return c.JSON(out)
}

// AdminCreateCategory handles POST /api/v1/admin/support/categories.
// @Summary Create a support category
// @Tags admin-support
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/support/categories [post]
func (h *SupportHandler) AdminCreateCategory(c *fiber.Ctx) error {
	var req supportCategoryRequest
	if err := c.BodyParser(&req); err != nil {
		return supportFail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if req.Slug == nil || !supportSlugPattern.MatchString(*req.Slug) {
		return supportFail(c, fiber.StatusBadRequest, "slug must be 1-40 lowercase letters, digits or '-'")
	}
	names, ok := cleanLocalized(req.Names, supportNameMax, true)
	if !ok {
		return supportFail(c, fiber.StatusBadRequest, "give the category a name of up to 80 characters in at least one language")
	}
	sortOrder, active := 0, true
	if req.SortOrder != nil {
		sortOrder = *req.SortOrder
	}
	if req.IsActive != nil {
		active = *req.IsActive
	}
	var o supportCategoryOut
	err := h.db.QueryRow(c.UserContext(),
		`INSERT INTO support_categories (slug, names, sort_order, is_active) VALUES ($1, $2::jsonb, $3, $4)
		 RETURNING id::text, slug, names, sort_order, is_active`, *req.Slug, localizedJSON(names), sortOrder, active,
	).Scan(&o.ID, &o.Slug, &o.Names, &o.SortOrder, &o.IsActive)
	if err != nil {
		if isSupportConstraintError(err) {
			return supportFail(c, fiber.StatusConflict, "a category with this slug already exists")
		}
		return supportServerError(c, "create category", err)
	}
	return c.Status(fiber.StatusCreated).JSON(o)
}

// AdminUpdateCategory handles PUT /api/v1/admin/support/categories/:id.
// @Summary Update a support category
// @Tags admin-support
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/support/categories/{id} [put]
func (h *SupportHandler) AdminUpdateCategory(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "category not found")
	}
	var req supportCategoryRequest
	if err := c.BodyParser(&req); err != nil {
		return supportFail(c, fiber.StatusBadRequest, "invalid request body")
	}
	var namesJSON *string
	if req.Names != nil {
		names, ok := cleanLocalized(req.Names, supportNameMax, true)
		if !ok {
			return supportFail(c, fiber.StatusBadRequest, "give the category a name of up to 80 characters in at least one language")
		}
		j := localizedJSON(names)
		namesJSON = &j
	}
	if req.Slug != nil && !supportSlugPattern.MatchString(*req.Slug) {
		return supportFail(c, fiber.StatusBadRequest, "slug must be 1-40 lowercase letters, digits or '-'")
	}
	tag, err := h.db.Exec(c.UserContext(),
		`UPDATE support_categories SET slug = COALESCE($2, slug), names = COALESCE($3::jsonb, names),
		   sort_order = COALESCE($4, sort_order), is_active = COALESCE($5, is_active) WHERE id = $1`,
		id, req.Slug, namesJSON, req.SortOrder, req.IsActive)
	if err != nil {
		if isSupportConstraintError(err) {
			return supportFail(c, fiber.StatusConflict, "a category with this slug already exists")
		}
		return supportServerError(c, "update category", err)
	}
	if tag.RowsAffected() == 0 {
		return supportFail(c, fiber.StatusNotFound, "category not found")
	}
	return c.JSON(fiber.Map{"message": "category updated"})
}

// AdminDeleteCategory handles DELETE /api/v1/admin/support/categories/:id.
// @Summary Delete a support category and its answers
// @Tags admin-support
// @Produce json
// @Security BearerAuth
// @Router /admin/support/categories/{id} [delete]
func (h *SupportHandler) AdminDeleteCategory(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "category not found")
	}
	tag, err := h.db.Exec(c.UserContext(), `DELETE FROM support_categories WHERE id = $1`, id)
	if err != nil {
		return supportServerError(c, "delete category", err)
	}
	if tag.RowsAffected() == 0 {
		return supportFail(c, fiber.StatusNotFound, "category not found")
	}
	return c.JSON(fiber.Map{"message": "category deleted"})
}

type supportFAQRequest struct {
	CategoryID *string           `json:"category_id"`
	Questions  map[string]string `json:"questions"`
	Answers    map[string]string `json:"answers"`
	SortOrder  *int              `json:"sort_order"`
	IsActive   *bool             `json:"is_active"`
}

// AdminListFAQs handles GET /api/v1/admin/support/faqs.
// @Summary List support answers
// @Tags admin-support
// @Produce json
// @Security BearerAuth
// @Router /admin/support/faqs [get]
func (h *SupportHandler) AdminListFAQs(c *fiber.Ctx) error {
	rows, err := h.db.Query(c.UserContext(),
		`SELECT f.id::text, f.category_id::text, f.questions, f.answers, f.sort_order, f.is_active
		 FROM support_faqs f JOIN support_categories c ON c.id = f.category_id ORDER BY c.sort_order, f.sort_order, f.created_at`)
	if err != nil {
		return supportServerError(c, "admin faqs", err)
	}
	defer rows.Close()
	out := make([]supportFAQOut, 0)
	for rows.Next() {
		var o supportFAQOut
		if err := rows.Scan(&o.ID, &o.CategoryID, &o.Questions, &o.Answers, &o.SortOrder, &o.IsActive); err != nil {
			return supportServerError(c, "admin scan faq", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return supportServerError(c, "admin faq rows", err)
	}
	return c.JSON(out)
}

// AdminCreateFAQ handles POST /api/v1/admin/support/faqs.
// @Summary Create a support answer
// @Tags admin-support
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/support/faqs [post]
func (h *SupportHandler) AdminCreateFAQ(c *fiber.Ctx) error {
	var req supportFAQRequest
	if err := c.BodyParser(&req); err != nil {
		return supportFail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if req.CategoryID == nil || !isUUID(*req.CategoryID) {
		return supportFail(c, fiber.StatusBadRequest, "choose a category")
	}
	questions, ok := cleanLocalized(req.Questions, supportQuestionMax, true)
	if !ok {
		return supportFail(c, fiber.StatusBadRequest, "question must be 1-300 characters in at least one language")
	}
	answers, ok := cleanLocalized(req.Answers, supportAnswerMax, true)
	if !ok {
		return supportFail(c, fiber.StatusBadRequest, "answer must be 1-5000 characters in at least one language")
	}
	sortOrder, active := 0, true
	if req.SortOrder != nil {
		sortOrder = *req.SortOrder
	}
	if req.IsActive != nil {
		active = *req.IsActive
	}
	var o supportFAQOut
	err := h.db.QueryRow(c.UserContext(),
		`INSERT INTO support_faqs (category_id, questions, answers, sort_order, is_active) VALUES ($1, $2::jsonb, $3::jsonb, $4, $5)
		 RETURNING id::text, category_id::text, questions, answers, sort_order, is_active`,
		*req.CategoryID, localizedJSON(questions), localizedJSON(answers), sortOrder, active,
	).Scan(&o.ID, &o.CategoryID, &o.Questions, &o.Answers, &o.SortOrder, &o.IsActive)
	if err != nil {
		if isSupportConstraintError(err) {
			return supportFail(c, fiber.StatusBadRequest, "category not found")
		}
		return supportServerError(c, "create faq", err)
	}
	return c.Status(fiber.StatusCreated).JSON(o)
}

// AdminUpdateFAQ handles PUT /api/v1/admin/support/faqs/:id.
// @Summary Update a support answer
// @Tags admin-support
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/support/faqs/{id} [put]
func (h *SupportHandler) AdminUpdateFAQ(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "answer not found")
	}
	var req supportFAQRequest
	if err := c.BodyParser(&req); err != nil {
		return supportFail(c, fiber.StatusBadRequest, "invalid request body")
	}
	var questionsJSON, answersJSON *string
	if req.Questions != nil {
		q, ok := cleanLocalized(req.Questions, supportQuestionMax, true)
		if !ok {
			return supportFail(c, fiber.StatusBadRequest, "question must be 1-300 characters in at least one language")
		}
		j := localizedJSON(q)
		questionsJSON = &j
	}
	if req.Answers != nil {
		a, ok := cleanLocalized(req.Answers, supportAnswerMax, true)
		if !ok {
			return supportFail(c, fiber.StatusBadRequest, "answer must be 1-5000 characters in at least one language")
		}
		j := localizedJSON(a)
		answersJSON = &j
	}
	if req.CategoryID != nil && !isUUID(*req.CategoryID) {
		return supportFail(c, fiber.StatusBadRequest, "choose a category")
	}
	tag, err := h.db.Exec(c.UserContext(),
		`UPDATE support_faqs SET category_id = COALESCE($2::uuid, category_id), questions = COALESCE($3::jsonb, questions),
		   answers = COALESCE($4::jsonb, answers), sort_order = COALESCE($5, sort_order), is_active = COALESCE($6, is_active),
		   updated_at = NOW() WHERE id = $1`,
		id, req.CategoryID, questionsJSON, answersJSON, req.SortOrder, req.IsActive)
	if err != nil {
		if isSupportConstraintError(err) {
			return supportFail(c, fiber.StatusBadRequest, "category not found")
		}
		return supportServerError(c, "update faq", err)
	}
	if tag.RowsAffected() == 0 {
		return supportFail(c, fiber.StatusNotFound, "answer not found")
	}
	return c.JSON(fiber.Map{"message": "answer updated"})
}

// AdminDeleteFAQ handles DELETE /api/v1/admin/support/faqs/:id.
// @Summary Delete a support answer
// @Tags admin-support
// @Produce json
// @Security BearerAuth
// @Router /admin/support/faqs/{id} [delete]
func (h *SupportHandler) AdminDeleteFAQ(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return supportFail(c, fiber.StatusNotFound, "answer not found")
	}
	tag, err := h.db.Exec(c.UserContext(), `DELETE FROM support_faqs WHERE id = $1`, id)
	if err != nil {
		return supportServerError(c, "delete faq", err)
	}
	if tag.RowsAffected() == 0 {
		return supportFail(c, fiber.StatusNotFound, "answer not found")
	}
	return c.JSON(fiber.Map{"message": "answer deleted"})
}
