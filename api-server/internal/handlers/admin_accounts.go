package handlers

import (
	"context"
	"errors"
	"log"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

const (
	adminRoleOwner = "owner"
	adminRoleAdmin = "admin"

	adminMinPasswordLength = 10
	adminMaxNameLength     = 60
)

// AdminAccountsHandler manages the admin team: owners list, invite, edit and
// remove admins, and every admin manages their own profile.
type AdminAccountsHandler struct {
	db       *pgxpool.Pool
	activity *services.ActivityService
}

func NewAdminAccountsHandler(db *pgxpool.Pool) *AdminAccountsHandler {
	return &AdminAccountsHandler{db: db, activity: services.NewActivityService(db)}
}

type adminAccountOut struct {
	ID                string     `json:"id"`
	Email             string     `json:"email"`
	Name              string     `json:"name"`
	Role              string     `json:"role"`
	IsActive          bool       `json:"is_active"`
	TOTPEnabled       bool       `json:"totp_enabled"`
	LastLoginAt       *time.Time `json:"last_login_at"`
	PasswordChangedAt *time.Time `json:"password_changed_at"`
	CreatedAt         time.Time  `json:"created_at"`
	OpenTickets       int        `json:"open_tickets"`
}

const adminAccountColumns = `a.id::text, a.email, a.name, a.role, a.is_active, a.totp_enabled, a.last_login_at,
	NULLIF(a.password_changed_at, to_timestamp(0)), a.created_at,
	(SELECT count(*) FROM support_tickets t WHERE t.assigned_admin_id = a.id AND t.status <> 'closed')`

func scanAdminAccount(row pgx.Row) (adminAccountOut, error) {
	var a adminAccountOut
	err := row.Scan(&a.ID, &a.Email, &a.Name, &a.Role, &a.IsActive, &a.TOTPEnabled, &a.LastLoginAt, &a.PasswordChangedAt, &a.CreatedAt, &a.OpenTickets)
	return a, err
}

func adminFail(c *fiber.Ctx, status int, code, msg string) error {
	return c.Status(status).JSON(fiber.Map{"error": msg, "code": code})
}

func adminServerError(c *fiber.Ctx, what string, err error) error {
	log.Printf("[admin-accounts] %s: %v", what, err)
	return adminFail(c, fiber.StatusInternalServerError, "internal", "internal server error")
}

func validAdminRole(r string) bool { return r == adminRoleOwner || r == adminRoleAdmin }

func normalizeAdminName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	return name, utf8.RuneCountInString(name) <= adminMaxNameLength && !strings.ContainsAny(name, "\r\n")
}

func validAdminPassword(p string) bool {
	return utf8.RuneCountInString(p) >= adminMinPasswordLength && len(p) <= 72
}

// List handles GET /api/v1/admin/admins.
// @Summary List admin accounts
// @Tags admin-accounts
// @Produce json
// @Security BearerAuth
// @Router /admin/admins [get]
func (h *AdminAccountsHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(c.UserContext(),
		`SELECT `+adminAccountColumns+` FROM admins a ORDER BY (a.role = 'owner') DESC, a.is_active DESC, a.created_at`)
	if err != nil {
		return adminServerError(c, "list", err)
	}
	defer rows.Close()
	out := make([]adminAccountOut, 0)
	for rows.Next() {
		a, err := scanAdminAccount(rows)
		if err != nil {
			return adminServerError(c, "scan", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return adminServerError(c, "rows", err)
	}
	return c.JSON(fiber.Map{"admins": out})
}

// Directory handles GET /api/v1/admin/admins/directory: the active admins any
// admin may assign a ticket to. It exposes no security details.
// @Summary List assignable admins
// @Tags admin-accounts
// @Produce json
// @Security BearerAuth
// @Router /admin/admins/directory [get]
func (h *AdminAccountsHandler) Directory(c *fiber.Ctx) error {
	rows, err := h.db.Query(c.UserContext(),
		`SELECT id::text, email, name FROM admins WHERE is_active ORDER BY lower(COALESCE(NULLIF(name, ''), email))`)
	if err != nil {
		return adminServerError(c, "directory", err)
	}
	defer rows.Close()
	type entry struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	out := make([]entry, 0)
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.Email, &e.Name); err != nil {
			return adminServerError(c, "directory scan", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return adminServerError(c, "directory rows", err)
	}
	return c.JSON(fiber.Map{"admins": out, "me": adminIDOf(c)})
}

type createAdminRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

// Create handles POST /api/v1/admin/admins. With an empty password a random
// one is generated and returned once.
// @Summary Create an admin account
// @Tags admin-accounts
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/admins [post]
func (h *AdminAccountsHandler) Create(c *fiber.Ctx) error {
	var req createAdminRequest
	if err := c.BodyParser(&req); err != nil {
		return adminFail(c, fiber.StatusBadRequest, "invalid_body", "invalid request body")
	}
	email := strings.TrimSpace(req.Email)
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email || len(email) > 254 {
		return adminFail(c, fiber.StatusBadRequest, "invalid_email", "enter a valid email address")
	}
	name, ok := normalizeAdminName(req.Name)
	if !ok {
		return adminFail(c, fiber.StatusBadRequest, "invalid_name", "name is too long")
	}
	role := req.Role
	if role == "" {
		role = adminRoleAdmin
	}
	if !validAdminRole(role) {
		return adminFail(c, fiber.StatusBadRequest, "invalid_role", "role must be owner or admin")
	}
	password := req.Password
	generated := password == ""
	if generated {
		password = crypto.GenerateRandomPassword()
	} else if !validAdminPassword(password) {
		return adminFail(c, fiber.StatusBadRequest, "weak_password", "password must be 10 to 72 characters")
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return adminServerError(c, "hash", err)
	}
	ctx := c.UserContext()
	var id string
	err = h.db.QueryRow(ctx,
		`INSERT INTO admins (email, password_hash, name, role, password_changed_at) VALUES ($1, $2, $3, $4, NOW()) RETURNING id::text`,
		email, hash, name, role).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return adminFail(c, fiber.StatusConflict, "email_taken", "an admin with this email already exists")
		}
		return adminServerError(c, "create", err)
	}
	h.log(ctx, c, services.EventAdminAccountCreated, map[string]any{"email": email, "role": role})
	account, err := scanAdminAccount(h.db.QueryRow(ctx, `SELECT `+adminAccountColumns+` FROM admins a WHERE a.id = $1`, id))
	if err != nil {
		return adminServerError(c, "reload", err)
	}
	resp := fiber.Map{"admin": account}
	if generated {
		resp["temporary_password"] = password
	}
	return c.Status(fiber.StatusCreated).JSON(resp)
}

type updateAdminRequest struct {
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

// lockAdmins serialises owner-count checks so two concurrent demotions cannot
// both pass the "another active owner remains" test.
func lockAdmins(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('proxima-admin-accounts'))`)
	return err
}

// otherActiveOwners counts active owners other than id inside tx.
func otherActiveOwners(ctx context.Context, tx pgx.Tx, id string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM admins WHERE role = 'owner' AND is_active AND id <> $1`, id).Scan(&n)
	return n, err
}

// Update handles PATCH /api/v1/admin/admins/:id.
// @Summary Change an admin's name, role or status
// @Tags admin-accounts
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/admins/{id} [patch]
func (h *AdminAccountsHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return adminFail(c, fiber.StatusNotFound, "not_found", "admin not found")
	}
	var req updateAdminRequest
	if err := c.BodyParser(&req); err != nil {
		return adminFail(c, fiber.StatusBadRequest, "invalid_body", "invalid request body")
	}
	if req.Name == nil && req.Role == nil && req.IsActive == nil {
		return adminFail(c, fiber.StatusBadRequest, "nothing_to_update", "nothing to update")
	}
	var name *string
	if req.Name != nil {
		n, ok := normalizeAdminName(*req.Name)
		if !ok {
			return adminFail(c, fiber.StatusBadRequest, "invalid_name", "name is too long")
		}
		name = &n
	}
	if req.Role != nil && !validAdminRole(*req.Role) {
		return adminFail(c, fiber.StatusBadRequest, "invalid_role", "role must be owner or admin")
	}
	self := id == adminIDOf(c)
	if self && req.Role != nil && *req.Role != adminRoleOwner {
		return adminFail(c, fiber.StatusConflict, "self_demote", "you cannot remove your own owner role")
	}
	if self && req.IsActive != nil && !*req.IsActive {
		return adminFail(c, fiber.StatusConflict, "self_disable", "you cannot disable your own account")
	}

	ctx := c.UserContext()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return adminServerError(c, "begin", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAdmins(ctx, tx); err != nil {
		return adminServerError(c, "lock", err)
	}
	var curRole string
	var curActive bool
	if err := tx.QueryRow(ctx, `SELECT role, is_active FROM admins WHERE id = $1 FOR UPDATE`, id).Scan(&curRole, &curActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return adminFail(c, fiber.StatusNotFound, "not_found", "admin not found")
		}
		return adminServerError(c, "load", err)
	}
	losesOwner := curRole == adminRoleOwner && curActive &&
		((req.Role != nil && *req.Role != adminRoleOwner) || (req.IsActive != nil && !*req.IsActive))
	if losesOwner {
		n, err := otherActiveOwners(ctx, tx, id)
		if err != nil {
			return adminServerError(c, "count owners", err)
		}
		if n == 0 {
			return adminFail(c, fiber.StatusConflict, "last_owner", "at least one active owner must remain")
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE admins SET name = COALESCE($2, name), role = COALESCE($3, role), is_active = COALESCE($4, is_active), updated_at = NOW() WHERE id = $1`,
		id, name, req.Role, req.IsActive); err != nil {
		return adminServerError(c, "update", err)
	}
	account, err := scanAdminAccount(tx.QueryRow(ctx, `SELECT `+adminAccountColumns+` FROM admins a WHERE a.id = $1`, id))
	if err != nil {
		return adminServerError(c, "reload", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return adminServerError(c, "commit", err)
	}
	h.log(ctx, c, services.EventAdminAccountUpdated, map[string]any{"email": account.Email, "role": account.Role, "active": account.IsActive})
	return c.JSON(fiber.Map{"admin": account})
}

// ResetPassword handles POST /api/v1/admin/admins/:id/password. It returns a
// new random password once and signs the admin out everywhere.
// @Summary Reset another admin's password
// @Tags admin-accounts
// @Produce json
// @Security BearerAuth
// @Router /admin/admins/{id}/password [post]
func (h *AdminAccountsHandler) ResetPassword(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return adminFail(c, fiber.StatusNotFound, "not_found", "admin not found")
	}
	if id == adminIDOf(c) {
		return adminFail(c, fiber.StatusConflict, "use_profile", "change your own password from your profile")
	}
	password := crypto.GenerateRandomPassword()
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return adminServerError(c, "hash", err)
	}
	ctx := c.UserContext()
	var email string
	err = h.db.QueryRow(ctx,
		`UPDATE admins SET password_hash = $2, password_changed_at = clock_timestamp(), updated_at = NOW() WHERE id = $1 RETURNING email`,
		id, hash).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return adminFail(c, fiber.StatusNotFound, "not_found", "admin not found")
	}
	if err != nil {
		return adminServerError(c, "reset", err)
	}
	h.log(ctx, c, services.EventAdminPasswordReset, map[string]any{"email": email})
	return c.JSON(fiber.Map{"temporary_password": password})
}

// Delete handles DELETE /api/v1/admin/admins/:id. Tickets assigned to the
// removed admin become unassigned.
// @Summary Delete an admin account
// @Tags admin-accounts
// @Produce json
// @Security BearerAuth
// @Router /admin/admins/{id} [delete]
func (h *AdminAccountsHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return adminFail(c, fiber.StatusNotFound, "not_found", "admin not found")
	}
	if id == adminIDOf(c) {
		return adminFail(c, fiber.StatusConflict, "self_delete", "you cannot delete your own account")
	}
	ctx := c.UserContext()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return adminServerError(c, "begin", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAdmins(ctx, tx); err != nil {
		return adminServerError(c, "lock", err)
	}
	var role, email string
	var active bool
	if err := tx.QueryRow(ctx, `SELECT role, is_active, email FROM admins WHERE id = $1 FOR UPDATE`, id).Scan(&role, &active, &email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return adminFail(c, fiber.StatusNotFound, "not_found", "admin not found")
		}
		return adminServerError(c, "load", err)
	}
	if role == adminRoleOwner && active {
		n, err := otherActiveOwners(ctx, tx, id)
		if err != nil {
			return adminServerError(c, "count owners", err)
		}
		if n == 0 {
			return adminFail(c, fiber.StatusConflict, "last_owner", "at least one active owner must remain")
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM admins WHERE id = $1`, id); err != nil {
		return adminServerError(c, "delete", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return adminServerError(c, "commit", err)
	}
	h.log(ctx, c, services.EventAdminAccountDeleted, map[string]any{"email": email})
	return c.JSON(fiber.Map{"message": "admin deleted"})
}

// Me handles GET /api/v1/admin/me.
// @Summary Current admin profile
// @Tags admin-accounts
// @Produce json
// @Security BearerAuth
// @Router /admin/me [get]
func (h *AdminAccountsHandler) Me(c *fiber.Ctx) error {
	account, err := scanAdminAccount(h.db.QueryRow(c.UserContext(), `SELECT `+adminAccountColumns+` FROM admins a WHERE a.id = $1`, adminIDOf(c)))
	if err != nil {
		return adminServerError(c, "me", err)
	}
	return c.JSON(fiber.Map{"admin": account})
}

type updateMeRequest struct {
	Name string `json:"name"`
}

// UpdateMe handles PATCH /api/v1/admin/me.
// @Summary Change your own display name
// @Tags admin-accounts
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/me [patch]
func (h *AdminAccountsHandler) UpdateMe(c *fiber.Ctx) error {
	var req updateMeRequest
	if err := c.BodyParser(&req); err != nil {
		return adminFail(c, fiber.StatusBadRequest, "invalid_body", "invalid request body")
	}
	name, ok := normalizeAdminName(req.Name)
	if !ok {
		return adminFail(c, fiber.StatusBadRequest, "invalid_name", "name is too long")
	}
	ctx := c.UserContext()
	if _, err := h.db.Exec(ctx, `UPDATE admins SET name = $2, updated_at = NOW() WHERE id = $1`, adminIDOf(c), name); err != nil {
		return adminServerError(c, "update me", err)
	}
	return h.Me(c)
}

type changeMyPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangeMyPassword handles POST /api/v1/admin/me/password. Other sessions are
// signed out; the response carries a fresh token for this one.
// @Summary Change your own password
// @Tags admin-accounts
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/me/password [post]
func (h *AdminAccountsHandler) ChangeMyPassword(issue func(c *fiber.Ctx, adminID string) (string, error)) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var req changeMyPasswordRequest
		if err := c.BodyParser(&req); err != nil {
			return adminFail(c, fiber.StatusBadRequest, "invalid_body", "invalid request body")
		}
		if !validAdminPassword(req.NewPassword) {
			return adminFail(c, fiber.StatusBadRequest, "weak_password", "password must be 10 to 72 characters")
		}
		ctx := c.UserContext()
		id := adminIDOf(c)
		var hash string
		if err := h.db.QueryRow(ctx, `SELECT password_hash FROM admins WHERE id = $1`, id).Scan(&hash); err != nil {
			return adminServerError(c, "load password", err)
		}
		if !crypto.CheckPassword(hash, req.CurrentPassword) {
			return adminFail(c, fiber.StatusUnauthorized, "wrong_password", "current password is incorrect")
		}
		if req.CurrentPassword == req.NewPassword {
			return adminFail(c, fiber.StatusBadRequest, "same_password", "choose a password you have not used here")
		}
		newHash, err := crypto.HashPassword(req.NewPassword)
		if err != nil {
			return adminServerError(c, "hash", err)
		}
		if _, err := h.db.Exec(ctx,
			`UPDATE admins SET password_hash = $2, password_changed_at = clock_timestamp(), updated_at = NOW() WHERE id = $1`,
			id, newHash); err != nil {
			return adminServerError(c, "change password", err)
		}
		token, err := issue(c, id)
		if err != nil {
			return adminServerError(c, "issue token", err)
		}
		h.log(ctx, c, services.EventAdminPasswordChanged, nil)
		return c.JSON(fiber.Map{"token": token})
	}
}

func (h *AdminAccountsHandler) log(ctx context.Context, c *fiber.Ctx, event string, detail map[string]any) {
	h.activity.Log(ctx, services.Record{
		EventType:  event,
		Severity:   services.SeverityInfo,
		ActorType:  "admin",
		ActorID:    adminIDOf(c),
		ActorLabel: adminEmail(c),
		Detail:     detail,
	})
}
