package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

const (
	serverUpdateSocket    = "/run/proxima-updater/control.sock"
	serverUpdateTimeout   = 5 * time.Second
	serverUpdateMaxInput  = 4096
	serverUpdateMaxOutput = 1 << 20
)

var (
	serverUpdateID         = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	serverUpdateImage      = regexp.MustCompile(`^[a-z0-9][a-z0-9./_-]+@sha256:[a-f0-9]{64}$`)
	serverUpdateOTP        = regexp.MustCompile(`^[0-9]{6}$`)
	serverUpdateTag        = regexp.MustCompile(`^v[0-9][A-Za-z0-9._-]{0,126}$`)
	serverUpdateHash       = regexp.MustCompile(`^[a-f0-9]{64}$`)
	errServerUpdateInvalid = errors.New("invalid updater protocol")
)

type serverUpdateAdmin struct {
	id, email, role, passwordHash, totpSecret string
	active, totpEnabled                       bool
}

// ServerUpdateControlHandler bridges the v1 host operations. Register all routes
// AFTER AdminJWTMiddleware and RequireAdminOwner. Prepare and Apply also need
// the deployment's authentication rate limiter and fresh password/TOTP.
// A 202 is acceptance (possibly an idempotent terminal snapshot), not success.
// On an uncertain apply outcome, read Status; never automatically repeat Apply.
type ServerUpdateControlHandler struct {
	client    *http.Client
	loadAdmin func(context.Context, string) (serverUpdateAdmin, error)
	audit     func(context.Context, services.Record)
	now       func() time.Time
}

// NewServerUpdateControlHandler captures PROXIMA_UPDATER_SOCKET once. An absent
// socket, denied access, invalid configuration or missing DB fails closed. No
// caller-controlled address, image, command, environment or credential reaches
// the host. The socket mount and its parent directories must be host-controlled.
func NewServerUpdateControlHandler(db *pgxpool.Pool, activity *services.ActivityService) *ServerUpdateControlHandler {
	path := os.Getenv("PROXIMA_UPDATER_SOCKET")
	if path == "" {
		path = serverUpdateSocket
	}
	transport := &http.Transport{
		DisableKeepAlives: true, DisableCompression: true,
		MaxResponseHeaderBytes: 4096, ResponseHeaderTimeout: serverUpdateTimeout,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if !filepath.IsAbs(path) || filepath.Clean(path) != path {
				return nil, errServerUpdateInvalid
			}
			st, err := os.Lstat(path)
			if err != nil || st.Mode()&os.ModeSocket == 0 {
				return nil, errServerUpdateInvalid
			}
			return (&net.Dialer{Timeout: serverUpdateTimeout}).DialContext(ctx, "unix", path)
		},
	}
	return &ServerUpdateControlHandler{
		client: &http.Client{Transport: transport, Timeout: serverUpdateTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		now: time.Now, audit: activity.Log,
		loadAdmin: func(ctx context.Context, id string) (serverUpdateAdmin, error) {
			var a serverUpdateAdmin
			if db == nil {
				return a, errServerUpdateInvalid
			}
			err := db.QueryRow(ctx, `SELECT id::text, email, role, is_active, password_hash,
				COALESCE(totp_secret, ''), totp_enabled FROM admins WHERE id = $1`, id).
				Scan(&a.id, &a.email, &a.role, &a.active, &a.passwordHash, &a.totpSecret, &a.totpEnabled)
			return a, err
		},
	}
}

func (h *ServerUpdateControlHandler) Status(c *fiber.Ctx) error {
	return h.serve(c, "/v1/status", http.MethodGet)
}

func (h *ServerUpdateControlHandler) Preflight(c *fiber.Ctx) error {
	return h.serve(c, "/v1/preflight", http.MethodGet)
}

func (h *ServerUpdateControlHandler) Candidate(c *fiber.Ctx) error {
	return h.serve(c, "/v1/candidate", http.MethodGet)
}

func (h *ServerUpdateControlHandler) Apply(c *fiber.Ctx) error {
	return h.serve(c, "/v1/apply", http.MethodPost)
}

func (h *ServerUpdateControlHandler) Prepare(c *fiber.Ctx) error {
	return h.serve(c, "/v1/prepare", http.MethodPost)
}

func (h *ServerUpdateControlHandler) PreparationStatus(c *fiber.Ctx) error {
	return h.serve(c, "/v1/prepare", http.MethodGet)
}

func (h *ServerUpdateControlHandler) serve(c *fiber.Ctx, path, method string) error {
	c.Set("Cache-Control", "no-store")
	id, _ := c.Locals("admin_id").(string)
	role, _ := c.Locals("admin_role").(string)
	if id == "" {
		return c.Status(401).JSON(fiber.Map{"error": "admin_required"})
	}
	if role != "owner" {
		return c.Status(403).JSON(fiber.Map{"error": "owner_required"})
	}
	if c.Method() != method {
		return c.Status(405).JSON(fiber.Map{"error": "method_not_allowed"})
	}
	if len(c.Context().QueryArgs().QueryString()) != 0 || len(c.Body()) > serverUpdateMaxInput ||
		(method == http.MethodGet && len(c.Body()) != 0) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), serverUpdateTimeout)
	defer cancel()
	a, err := h.loadAdmin(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(401).JSON(fiber.Map{"error": "admin_required"})
	}
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"error": "admin_verification_unavailable"})
	}
	if a.id != id || !a.active || a.role != "owner" {
		return c.Status(403).JSON(fiber.Map{"error": "owner_required"})
	}
	var payload []byte
	var releaseID string
	if method == http.MethodPost {
		var input struct {
			ReleaseID     string `json:"release_id"`
			Maintenance   bool   `json:"maintenance"`
			Password      string `json:"password"`
			TOTPCode      string `json:"totp_code"`
			Tag           string `json:"tag"`
			PayloadSHA256 string `json:"payload_sha256"`
		}
		keys := []string{"release_id", "maintenance", "password", "totp_code"}
		if path == "/v1/prepare" {
			keys = []string{"tag", "password", "totp_code"}
		} else {
			var fields map[string]json.RawMessage
			if json.Unmarshal(c.Body(), &fields) == nil {
				if _, ok := fields["payload_sha256"]; ok {
					keys = append(keys, "payload_sha256")
				}
			}
		}
		if c.Get("Content-Type") != "application/json" ||
			serverUpdateObject(c.Body(), &input, keys...) != nil ||
			(path == "/v1/prepare" && !serverUpdateTag.MatchString(input.Tag)) ||
			(path == "/v1/apply" && (!serverUpdateID.MatchString(input.ReleaseID) || !input.Maintenance)) ||
			(slices.Contains(keys, "payload_sha256") && !serverUpdateHash.MatchString(input.PayloadSHA256)) ||
			len(input.Password) == 0 || len(input.Password) > 72 || !serverUpdateOTP.MatchString(input.TOTPCode) {
			return c.Status(400).JSON(fiber.Map{"error": "invalid_request"})
		}
		// Same SHA1/six-digit/30-second/skew-one policy as crypto.ValidateTOTP,
		// with an explicit clock so tests cannot pass or fail at a time boundary.
		validOTP, otpErr := totp.ValidateCustom(input.TOTPCode, a.totpSecret, h.now(), totp.ValidateOpts{
			Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if !a.totpEnabled || a.totpSecret == "" || otpErr != nil || !validOTP || !crypto.CheckPassword(a.passwordHash, input.Password) {
			return c.Status(403).JSON(fiber.Map{"error": "reauthentication_failed"})
		}
		releaseID = input.ReleaseID
		// The validated ID alphabet contains no JSON escapes. Do not serialize
		// input: it contains the reauthentication credentials.
		payload = []byte(`{"release_id":"` + releaseID + `","maintenance":true}`)
		if input.PayloadSHA256 != "" {
			payload = []byte(`{"release_id":"` + releaseID + `","maintenance":true,"payload_sha256":"` + input.PayloadSHA256 + `"}`)
		}
		event, detail := "server.update_requested", map[string]any{"release_id": releaseID, "maintenance": true}
		if path == "/v1/prepare" {
			payload = []byte(`{"tag":"` + input.Tag + `"}`)
			event, detail = "server.update_prepare_requested", map[string]any{"tag": input.Tag}
		}
		h.audit(ctx, services.Record{EventType: event, Severity: services.SeverityWarning,
			ActorType: "admin", ActorID: a.id, ActorLabel: a.email,
			Detail: detail})
	}
	// DisableKeepAlives plus no idempotency header prevents transport replay.
	req, err := http.NewRequestWithContext(ctx, method, "http://updater"+path, bytes.NewReader(payload))
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"error": "updater_unavailable"})
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	status, result := h.exchange(req, releaseID)
	if method == http.MethodPost {
		event := "server.update_response"
		if path == "/v1/prepare" {
			event = "server.update_prepare_response"
		}
		h.audit(ctx, services.Record{EventType: event, Severity: services.SeverityInfo,
			ActorType: "admin", ActorID: a.id, ActorLabel: a.email,
			Detail: map[string]any{"release_id": releaseID, "http_status": status}})
	}
	return c.Status(status).JSON(result)
}

func (h *ServerUpdateControlHandler) exchange(req *http.Request, releaseID string) (int, any) {
	failure := fiber.Map{"error": "updater_unavailable"}
	if req.Method == http.MethodPost {
		failure["outcome"] = "unknown"
		failure["check_status"] = true
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return 503, failure
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, serverUpdateMaxOutput+1))
	if err != nil || len(data) > serverUpdateMaxOutput {
		return 502, failure
	}
	if req.URL.Path == "/v1/prepare" {
		return h.preparationResponse(req, resp.StatusCode, data, failure)
	}
	if releaseID != "" && resp.StatusCode != http.StatusAccepted {
		var hostError struct {
			Error string `json:"error"`
		}
		if serverUpdateObject(data, &hostError, "error") == nil &&
			map[int]string{400: "invalid_request", 403: "not_approved", 409: "blocked", 500: "persistence_failure"}[resp.StatusCode] == hostError.Error && hostError.Error != "" {
			return resp.StatusCode, hostError
		}
		return 502, failure
	}
	if releaseID == "" && resp.StatusCode != http.StatusOK {
		return 503, failure
	}
	if req.URL.Path == "/v1/candidate" {
		var candidate struct {
			Status             string          `json:"status"`
			ReleaseID          string          `json:"release_id"`
			Sequence           uint64          `json:"sequence"`
			From               json.RawMessage `json:"from"`
			To                 json.RawMessage `json:"to"`
			RollbackCompatible bool            `json:"rollback_compatible"`
			ExpiresAt          int64           `json:"expires_at"`
			PayloadSHA256      string          `json:"payload_sha256,omitempty"`
		}
		now := h.now().Unix()
		keys := []string{"status", "release_id", "sequence", "from", "to", "rollback_compatible", "expires_at"}
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) == nil {
			if _, ok := fields["payload_sha256"]; ok {
				keys = append(keys, "payload_sha256")
			}
		}
		if serverUpdateObject(data, &candidate, keys...) != nil ||
			(slices.Contains(keys, "payload_sha256") && !serverUpdateHash.MatchString(candidate.PayloadSHA256)) ||
			candidate.Status != "prepared" || !serverUpdateID.MatchString(candidate.ReleaseID) ||
			candidate.Sequence == 0 || candidate.Sequence > 1<<53-1 || candidate.ExpiresAt <= now || candidate.ExpiresAt > now+7*24*60*60 {
			return 502, failure
		}
		type images struct {
			API string `json:"api"`
			Web string `json:"web"`
		}
		var pairs [2]images
		for i, raw := range []json.RawMessage{candidate.From, candidate.To} {
			if serverUpdateObject(raw, &pairs[i], "api", "web") != nil || !serverUpdateImage.MatchString(pairs[i].API) || !serverUpdateImage.MatchString(pairs[i].Web) {
				return 502, failure
			}
		}
		if pairs[0] == pairs[1] {
			return 502, failure
		}
		return http.StatusOK, candidate
	}
	if req.URL.Path == "/v1/preflight" {
		var report struct {
			OK     bool   `json:"ok"`
			Reason string `json:"reason"`
		}
		if serverUpdateObject(data, &report, "ok", "reason") != nil ||
			(report.OK && report.Reason != "") || (!report.OK && !slices.Contains([]string{"busy", "manual_recovery", "not_approved", "topology_mismatch", "unavailable"}, report.Reason)) {
			return 502, failure
		}
		return 200, report
	}
	// This is the HTTP v1 DTO, not the host's signed journal (currently v2).
	// Journal fields must never broaden the response allowlist implicitly.
	var state struct {
		Version   int             `json:"version"`
		Consumed  []string        `json:"consumed"`
		Operation json.RawMessage `json:"operation"`
	}
	if serverUpdateObject(data, &state, "version", "consumed", "operation") != nil || state.Version != 1 || state.Consumed == nil || len(state.Consumed) > 10000 {
		return 502, failure
	}
	for _, id := range state.Consumed {
		if !serverUpdateID.MatchString(id) {
			return 502, failure
		}
	}
	if bytes.Equal(state.Operation, []byte("null")) {
		if releaseID != "" {
			return 502, failure
		}
		return 200, state
	}
	var op struct {
		Approval           json.RawMessage `json:"approval"`
		Phase              string          `json:"phase"`
		Maintenance        bool            `json:"maintenance"`
		MigrationUncertain bool            `json:"migration_uncertain"`
		Error              string          `json:"error"`
	}
	// error is the sole optional v1 field (omitempty on the host).
	var fields map[string]json.RawMessage
	if json.Unmarshal(state.Operation, &fields) != nil {
		return 502, failure
	}
	keys := []string{"approval", "phase", "maintenance", "migration_uncertain"}
	if _, ok := fields["error"]; ok {
		keys = append(keys, "error")
	}
	if serverUpdateObject(state.Operation, &op, keys...) != nil ||
		!slices.Contains([]string{"accepted", "stopping", "applying", "succeeded", "rejected", "rolling_back", "rolled_back", "manual_recovery"}, op.Phase) ||
		!slices.Contains([]string{"", "preflight_failed", "stop_failed", "stop_unverified", "apply_failed", "rollback_stop_failed", "rollback_stop_unverified", "rollback_failed", "persistence_failure", "interrupted_operation", "recovery_stop_unverified"}, op.Error) {
		return 502, failure
	}
	var approval struct {
		ID                 string          `json:"id"`
		From               json.RawMessage `json:"from"`
		To                 json.RawMessage `json:"to"`
		RollbackCompatible bool            `json:"rollback_compatible"`
	}
	if serverUpdateObject(op.Approval, &approval, "id", "from", "to", "rollback_compatible") != nil || !serverUpdateID.MatchString(approval.ID) ||
		!slices.Contains(state.Consumed, approval.ID) || (releaseID != "" && approval.ID != releaseID) {
		return 502, failure
	}
	for _, raw := range []json.RawMessage{approval.From, approval.To} {
		var images struct {
			API string `json:"api"`
			Web string `json:"web"`
		}
		if serverUpdateObject(raw, &images, "api", "web") != nil || !serverUpdateImage.MatchString(images.API) || !serverUpdateImage.MatchString(images.Web) {
			return 502, failure
		}
	}
	return resp.StatusCode, state
}

// All fields are required and exact-case. Raw values allow nested protocol
// objects to be checked separately without accepting Go's case-folded fields.
// Null is rejected except for the explicitly nullable state.operation field.
func serverUpdateObject(data []byte, dst any, keys ...string) error {
	if !utf8.Valid(data) {
		return errServerUpdateInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errServerUpdateInvalid
	}
	seen := make(map[string]bool, len(keys))
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || !slices.Contains(keys, key) {
			return errServerUpdateInvalid
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || (bytes.Equal(value, []byte("null")) && key != "operation") {
			return errServerUpdateInvalid
		}
	}
	if _, err := d.Token(); err != nil {
		return errServerUpdateInvalid
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) || len(seen) != len(keys) {
		return errServerUpdateInvalid
	}
	return json.Unmarshal(data, dst)
}
