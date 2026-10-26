package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/middleware"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

const adminTestSecret = "admin-accounts-test-secret"

type adminFixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	app  *fiber.App
	auth *handlers.AdminAuthHandler
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed admin account tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	// Every test owns the whole admins table: the last-owner guard counts it.
	if _, err := pool.Exec(context.Background(), `DELETE FROM admins`); err != nil {
		t.Fatalf("reset admins: %v", err)
	}
	auth := handlers.NewAdminAuthHandler(pool, adminTestSecret, time.Hour)
	accounts := handlers.NewAdminAccountsHandler(pool)
	support := handlers.NewSupportHandler(pool, nil, "https://panel.test")
	app := fiber.New()
	app.Post("/admin/auth/login", auth.Login)
	admin := app.Group("/admin", middleware.AdminJWTMiddleware(adminTestSecret, pool))
	admin.Get("/me", accounts.Me)
	admin.Patch("/me", accounts.UpdateMe)
	admin.Post("/me/password", accounts.ChangeMyPassword(auth.IssueToken))
	admin.Get("/admins/directory", accounts.Directory)
	owners := admin.Group("/admins", middleware.RequireAdminOwner())
	owners.Get("", accounts.List)
	owners.Post("", accounts.Create)
	owners.Patch("/:id", accounts.Update)
	owners.Post("/:id/password", accounts.ResetPassword)
	owners.Delete("/:id", accounts.Delete)
	admin.Get("/support/tickets", support.AdminListTickets)
	admin.Get("/support/tickets/:id", support.AdminGetTicket)
	admin.Patch("/support/tickets/:id", support.AdminUpdateTicket)
	admin.Get("/support/summary", support.AdminSummary)
	return &adminFixture{t: t, pool: pool, app: app, auth: auth}
}

func (f *adminFixture) seedAdmin(email, password, role string) string {
	f.t.Helper()
	hash, err := crypto.HashPassword(password)
	if err != nil {
		f.t.Fatal(err)
	}
	var id string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO admins (email, password_hash, role) VALUES ($1, $2, $3) RETURNING id::text`,
		email, hash, role).Scan(&id); err != nil {
		f.t.Fatalf("seed admin: %v", err)
	}
	return id
}

func (f *adminFixture) do(method, path, token string, body any) (int, map[string]any) {
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
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := f.app.Test(req, 10000)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (f *adminFixture) login(email, password string) string {
	f.t.Helper()
	status, body := f.do("POST", "/admin/auth/login", "", map[string]string{"email": email, "password": password})
	if status != fiber.StatusOK {
		f.t.Fatalf("login %s: %d %v", email, status, body)
	}
	return body["token"].(string)
}

func expectStatus(t *testing.T, label string, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d, want %d (%v)", label, got, want, body)
	}
}

func TestAdminAccountsOwnerGuards(t *testing.T) {
	f := newAdminFixture(t)
	ownerID := f.seedAdmin("owner@admin.test", "owner-password-1", "owner")
	owner := f.login("OWNER@admin.test", "owner-password-1")

	status, body := f.do("POST", "/admin/admins", owner, map[string]string{"email": "staff@admin.test", "name": "Staff", "role": "admin"})
	expectStatus(t, "create staff", status, fiber.StatusCreated, body)
	tempPassword, _ := body["temporary_password"].(string)
	if len(tempPassword) < 10 {
		t.Fatalf("expected a generated temporary password, got %q", tempPassword)
	}
	staffID := body["admin"].(map[string]any)["id"].(string)

	status, body = f.do("POST", "/admin/admins", owner, map[string]string{"email": "Staff@admin.test"})
	expectStatus(t, "duplicate email (case-insensitive)", status, fiber.StatusConflict, body)

	staff := f.login("staff@admin.test", tempPassword)
	status, body = f.do("GET", "/admin/admins", staff, nil)
	expectStatus(t, "non-owner list", status, fiber.StatusForbidden, body)
	status, body = f.do("GET", "/admin/admins/directory", staff, nil)
	expectStatus(t, "non-owner directory", status, fiber.StatusOK, body)

	status, body = f.do("PATCH", "/admin/admins/"+ownerID, owner, map[string]string{"role": "admin"})
	expectStatus(t, "self demote", status, fiber.StatusConflict, body)
	status, body = f.do("PATCH", "/admin/admins/"+ownerID, owner, map[string]any{"is_active": false})
	expectStatus(t, "self disable", status, fiber.StatusConflict, body)
	status, body = f.do("DELETE", "/admin/admins/"+ownerID, owner, nil)
	expectStatus(t, "self delete", status, fiber.StatusConflict, body)

	status, body = f.do("PATCH", "/admin/admins/"+staffID, owner, map[string]string{"role": "owner"})
	expectStatus(t, "promote staff", status, fiber.StatusOK, body)
	status, body = f.do("PATCH", "/admin/admins/"+ownerID, staff, map[string]string{"role": "admin"})
	expectStatus(t, "demote original owner", status, fiber.StatusOK, body)

	status, body = f.do("GET", "/admin/admins", owner, nil)
	expectStatus(t, "demoted owner list", status, fiber.StatusForbidden, body)

	status, body = f.do("DELETE", "/admin/admins/"+staffID, staff, nil)
	expectStatus(t, "sole owner deletes self", status, fiber.StatusConflict, body)

	status, body = f.do("PATCH", "/admin/admins/"+ownerID, staff, map[string]any{"is_active": false})
	expectStatus(t, "disable admin", status, fiber.StatusOK, body)
	status, body = f.do("GET", "/admin/me", owner, nil)
	expectStatus(t, "disabled admin token", status, fiber.StatusUnauthorized, body)
	status, body = f.do("POST", "/admin/auth/login", "", map[string]string{"email": "owner@admin.test", "password": "owner-password-1"})
	expectStatus(t, "disabled admin login", status, fiber.StatusForbidden, body)

	status, body = f.do("DELETE", "/admin/admins/"+ownerID, staff, nil)
	expectStatus(t, "delete admin", status, fiber.StatusOK, body)
	status, body = f.do("GET", "/admin/me", owner, nil)
	expectStatus(t, "deleted admin token", status, fiber.StatusUnauthorized, body)

	var activeOwners int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM admins WHERE role = 'owner' AND is_active`).Scan(&activeOwners); err != nil {
		t.Fatal(err)
	}
	if activeOwners != 1 {
		t.Fatalf("expected exactly one active owner left, got %d", activeOwners)
	}
}

func TestAdminPasswordChangeRevokesOtherSessions(t *testing.T) {
	f := newAdminFixture(t)
	ownerID := f.seedAdmin("pw-owner@admin.test", "owner-password-1", "owner")
	staffID := f.seedAdmin("pw-staff@admin.test", "staff-password-1", "admin")
	owner := f.login("pw-owner@admin.test", "owner-password-1")
	staffA := f.login("pw-staff@admin.test", "staff-password-1")
	staffB := f.login("pw-staff@admin.test", "staff-password-1")

	status, body := f.do("POST", "/admin/me/password", staffA, map[string]string{"current_password": "wrong", "new_password": "staff-password-2"})
	expectStatus(t, "wrong current password", status, fiber.StatusUnauthorized, body)
	status, body = f.do("POST", "/admin/me/password", staffA, map[string]string{"current_password": "staff-password-1", "new_password": "short"})
	expectStatus(t, "weak password", status, fiber.StatusBadRequest, body)
	status, body = f.do("POST", "/admin/me/password", staffA, map[string]string{"current_password": "staff-password-1", "new_password": "staff-password-2"})
	expectStatus(t, "change password", status, fiber.StatusOK, body)
	fresh := body["token"].(string)

	status, body = f.do("GET", "/admin/me", staffA, nil)
	expectStatus(t, "old token after change", status, fiber.StatusUnauthorized, body)
	status, body = f.do("GET", "/admin/me", staffB, nil)
	expectStatus(t, "other session after change", status, fiber.StatusUnauthorized, body)
	status, body = f.do("GET", "/admin/me", fresh, nil)
	expectStatus(t, "fresh token after change", status, fiber.StatusOK, body)

	status, body = f.do("POST", "/admin/admins/"+staffID+"/password", owner, nil)
	expectStatus(t, "owner resets staff password", status, fiber.StatusOK, body)
	reset := body["temporary_password"].(string)
	status, body = f.do("GET", "/admin/me", fresh, nil)
	expectStatus(t, "token after reset", status, fiber.StatusUnauthorized, body)
	f.login("pw-staff@admin.test", reset)

	status, body = f.do("POST", "/admin/admins/"+ownerID+"/password", owner, nil)
	expectStatus(t, "owner resets own password via admin API", status, fiber.StatusConflict, body)

	// A legacy token minted before the migration has no pwc claim; it stays
	// valid only while the password has never been changed.
	legacy := jwt.NewWithClaims(jwt.SigningMethodHS256, handlers.AdminClaims{
		AdminID: ownerID,
		Email:   "pw-owner@admin.test",
		Role:    "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	legacyToken, err := legacy.SignedString([]byte(adminTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	status, body = f.do("GET", "/admin/admins", legacyToken, nil)
	expectStatus(t, "legacy token uses role from DB", status, fiber.StatusOK, body)
}

func TestSupportTicketAssignment(t *testing.T) {
	f := newAdminFixture(t)
	f.seedAdmin("as-owner@admin.test", "owner-password-1", "owner")
	staffID := f.seedAdmin("as-staff@admin.test", "staff-password-1", "admin")
	disabledID := f.seedAdmin("as-off@admin.test", "off-password-1", "admin")
	if _, err := f.pool.Exec(context.Background(), `UPDATE admins SET is_active = false WHERE id = $1`, disabledID); err != nil {
		t.Fatal(err)
	}
	owner := f.login("as-owner@admin.test", "owner-password-1")
	staff := f.login("as-staff@admin.test", "staff-password-1")

	ctx := context.Background()
	var userID, ticketID string
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, sub_token, status, is_active)
		 VALUES (gen_random_uuid()::text || '@assign.test', 'x', 'ASSIGNTEST' || upper(substr(md5(random()::text), 1, 20)), 'active', true)
		 RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO support_tickets (user_id, subject) VALUES ($1, 'assignment test ticket') RETURNING id::text`, userID).Scan(&ticketID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) })

	status, body := f.do("PATCH", "/admin/support/tickets/"+ticketID, owner, map[string]string{"assigned_admin_id": disabledID})
	expectStatus(t, "assign disabled admin", status, fiber.StatusBadRequest, body)
	status, body = f.do("PATCH", "/admin/support/tickets/"+ticketID, owner, map[string]string{"assigned_admin_id": "not-a-uuid"})
	expectStatus(t, "assign invalid id", status, fiber.StatusBadRequest, body)
	status, body = f.do("PATCH", "/admin/support/tickets/"+ticketID, owner, map[string]string{"assigned_admin_id": staffID})
	expectStatus(t, "assign staff", status, fiber.StatusOK, body)

	status, body = f.do("GET", "/admin/support/tickets/"+ticketID, owner, nil)
	expectStatus(t, "get ticket", status, fiber.StatusOK, body)
	tk := body["ticket"].(map[string]any)
	if tk["assigned_admin_id"] != staffID || tk["assigned_admin_email"] != "as-staff@admin.test" {
		t.Fatalf("assignee not returned: %v", tk)
	}

	listIDs := func(token, assignee string) []string {
		status, body := f.do("GET", "/admin/support/tickets?status=&limit=100&assignee="+assignee, token, nil)
		expectStatus(t, "list "+assignee, status, fiber.StatusOK, body)
		ids := []string{}
		for _, raw := range body["tickets"].([]any) {
			ids = append(ids, raw.(map[string]any)["id"].(string))
		}
		return ids
	}
	contains := func(ids []string) bool {
		for _, id := range ids {
			if id == ticketID {
				return true
			}
		}
		return false
	}
	if !contains(listIDs(staff, "mine")) {
		t.Fatal("assigned ticket missing from staff's mine filter")
	}
	if contains(listIDs(owner, "mine")) {
		t.Fatal("ticket assigned to staff appeared in owner's mine filter")
	}
	if contains(listIDs(owner, "unassigned")) {
		t.Fatal("assigned ticket appeared in unassigned filter")
	}
	status, body = f.do("GET", "/admin/support/summary", staff, nil)
	expectStatus(t, "summary", status, fiber.StatusOK, body)
	if body["mine"].(float64) < 1 {
		t.Fatalf("summary mine count: %v", body)
	}
	status, body = f.do("GET", "/admin/support/tickets?assignee=someone", owner, nil)
	expectStatus(t, "bad assignee filter", status, fiber.StatusBadRequest, body)

	status, body = f.do("PATCH", "/admin/support/tickets/"+ticketID, staff, map[string]string{"assigned_admin_id": ""})
	expectStatus(t, "unassign", status, fiber.StatusOK, body)
	if !contains(listIDs(owner, "unassigned")) {
		t.Fatal("unassigned ticket missing from unassigned filter")
	}

	// Deleting an admin unassigns their tickets instead of failing.
	if _, err := f.pool.Exec(ctx, `UPDATE support_tickets SET assigned_admin_id = $2 WHERE id = $1`, ticketID, staffID); err != nil {
		t.Fatal(err)
	}
	status, body = f.do("DELETE", "/admin/admins/"+staffID, owner, nil)
	expectStatus(t, "delete assigned staff", status, fiber.StatusOK, body)
	var assignee *string
	if err := f.pool.QueryRow(ctx, `SELECT assigned_admin_id::text FROM support_tickets WHERE id = $1`, ticketID).Scan(&assignee); err != nil {
		t.Fatal(err)
	}
	if assignee != nil {
		t.Fatalf("ticket still assigned to deleted admin: %s", *assignee)
	}
	if !strings.Contains(strings.Join(listIDs(owner, "all"), ","), ticketID) {
		t.Fatal("ticket missing from all filter")
	}
}
