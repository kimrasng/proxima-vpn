package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// OAuthHandler serves the OAuth 2.0 provider: admin app management, the
// consent step behind the portal login, the token endpoints, and the
// connected-apps list on the user's account page.
type OAuthHandler struct {
	db       *pgxpool.Pool
	svc      *services.OAuthService
	panelURL string
}

func NewOAuthHandler(db *pgxpool.Pool, panelURL string) *OAuthHandler {
	return &OAuthHandler{db: db, svc: services.NewOAuthService(db), panelURL: strings.TrimRight(panelURL, "/")}
}

func (h *OAuthHandler) Service() *services.OAuthService { return h.svc }

type oauthClientResponse struct {
	*services.OAuthClient
	ClientSecret string `json:"client_secret,omitempty"`
}

func adminClientError(c *fiber.Ctx, err error) error {
	var v services.ValidationError
	switch {
	case errors.As(err, &v):
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": v.Error()})
	case errors.Is(err, services.ErrOAuthClientNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "oauth app not found"})
	}
	log.Printf("[oauth] admin app operation failed: %v", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
}

// ListClients handles GET /api/v1/admin/oauth-apps.
// @Summary List OAuth apps
// @Tags admin-oauth
// @Produce json
// @Success 200 {array} services.OAuthClient
// @Security BearerAuth
// @Router /admin/oauth-apps [get]
func (h *OAuthHandler) ListClients(c *fiber.Ctx) error {
	list, err := h.svc.ListClients(c.Context())
	if err != nil {
		return adminClientError(c, err)
	}
	return c.JSON(list)
}

// Scopes handles GET /api/v1/admin/oauth-apps/scopes.
// @Summary List the scopes an OAuth app may be granted
// @Tags admin-oauth
// @Produce json
// @Success 200 {array} string
// @Security BearerAuth
// @Router /admin/oauth-apps/scopes [get]
func (h *OAuthHandler) Scopes(c *fiber.Ctx) error {
	return c.JSON(services.OAuthScopes)
}

// CreateClient handles POST /api/v1/admin/oauth-apps. The response carries
// client_secret exactly once.
// @Summary Create an OAuth app
// @Tags admin-oauth
// @Accept json
// @Produce json
// @Param body body services.OAuthClientInput true "App"
// @Success 201 {object} oauthClientResponse
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /admin/oauth-apps [post]
func (h *OAuthHandler) CreateClient(c *fiber.Ctx) error {
	var in services.OAuthClientInput
	if err := c.BodyParser(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	client, secret, err := h.svc.CreateClient(c.Context(), in)
	if err != nil {
		return adminClientError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(oauthClientResponse{client, secret})
}

// UpdateClient handles PUT /api/v1/admin/oauth-apps/:id.
// @Summary Update an OAuth app
// @Tags admin-oauth
// @Accept json
// @Produce json
// @Param id path string true "App ID"
// @Param body body services.OAuthClientInput true "App"
// @Success 200 {object} oauthClientResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/oauth-apps/{id} [put]
func (h *OAuthHandler) UpdateClient(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return adminClientError(c, services.ErrOAuthClientNotFound)
	}
	var in services.OAuthClientInput
	if err := c.BodyParser(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	client, secret, err := h.svc.UpdateClient(c.Context(), id, in)
	if err != nil {
		return adminClientError(c, err)
	}
	return c.JSON(oauthClientResponse{client, secret})
}

// RotateSecret handles POST /api/v1/admin/oauth-apps/:id/secret.
// @Summary Issue a new client secret
// @Tags admin-oauth
// @Produce json
// @Param id path string true "App ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/oauth-apps/{id}/secret [post]
func (h *OAuthHandler) RotateSecret(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return adminClientError(c, services.ErrOAuthClientNotFound)
	}
	secret, err := h.svc.RotateSecret(c.Context(), id)
	if err != nil {
		return adminClientError(c, err)
	}
	return c.JSON(fiber.Map{"client_secret": secret})
}

// DeleteClient handles DELETE /api/v1/admin/oauth-apps/:id.
// @Summary Delete an OAuth app and every token it holds
// @Tags admin-oauth
// @Param id path string true "App ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/oauth-apps/{id} [delete]
func (h *OAuthHandler) DeleteClient(c *fiber.Ctx) error {
	id := c.Params("id")
	if !isUUID(id) {
		return adminClientError(c, services.ErrOAuthClientNotFound)
	}
	if err := h.svc.DeleteClient(c.Context(), id); err != nil {
		return adminClientError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type authorizeRequestBody struct {
	ResponseType        string `json:"response_type" query:"response_type"`
	ClientID            string `json:"client_id" query:"client_id"`
	RedirectURI         string `json:"redirect_uri" query:"redirect_uri"`
	Scope               string `json:"scope" query:"scope"`
	State               string `json:"state" query:"state"`
	CodeChallenge       string `json:"code_challenge" query:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method" query:"code_challenge_method"`
	Approve             bool   `json:"approve"`
}

func (b authorizeRequestBody) params() services.AuthorizeParams {
	return services.AuthorizeParams{ResponseType: b.ResponseType, ClientID: b.ClientID, RedirectURI: b.RedirectURI,
		Scope: b.Scope, State: b.State, CodeChallenge: b.CodeChallenge, CodeChallengeMethod: b.CodeChallengeMethod}
}

// callbackURL appends the response parameters to the verified redirect URI,
// keeping any query it already has.
func callbackURL(redirectURI string, values map[string]string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := u.Query()
	for k, v := range values {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func oauthJSONError(c *fiber.Ctx, err error) error {
	var oe *services.OAuthError
	if errors.As(err, &oe) {
		return c.Status(oe.Status).JSON(fiber.Map{"error": oe.Code, "error_description": oe.Description})
	}
	log.Printf("[oauth] request failed: %v", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "server_error"})
}

// AuthorizeInfo handles GET /api/v1/user/oauth/authorize. The consent page
// calls it with the client's query string. On success it describes the app;
// when the redirect URI is verified but the request is otherwise bad it
// returns redirect_to so the page sends the error back to the app.
// @Summary Describe an OAuth authorization request for the consent page
// @Tags user-oauth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /user/oauth/authorize [get]
func (h *OAuthHandler) AuthorizeInfo(c *fiber.Ctx) error {
	var b authorizeRequestBody
	if err := c.QueryParser(&b); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_request"})
	}
	req, err := h.svc.ValidateAuthorize(c.Context(), b.params())
	if err != nil {
		return h.authorizeFailure(c, req, err)
	}
	return c.JSON(fiber.Map{
		"client": fiber.Map{
			"client_id":   req.Client.ClientID,
			"name":        req.Client.Name,
			"description": req.Client.Description,
		},
		"redirect_uri": req.RedirectURI,
		"scopes":       req.Scopes,
	})
}

func (h *OAuthHandler) authorizeFailure(c *fiber.Ctx, req *services.OAuthAuthorizeRequest, err error) error {
	var oe *services.OAuthError
	if !errors.As(err, &oe) {
		return oauthJSONError(c, err)
	}
	body := fiber.Map{"error": oe.Code, "error_description": oe.Description}
	if req != nil {
		body["redirect_to"] = callbackURL(req.RedirectURI, map[string]string{
			"error": oe.Code, "error_description": oe.Description, "state": req.State,
		})
	}
	return c.Status(oe.Status).JSON(body)
}

// AuthorizeDecision handles POST /api/v1/user/oauth/authorize. The request is
// re-validated so a tampered body cannot reach an unregistered redirect URI.
// @Summary Approve or deny an OAuth authorization request
// @Tags user-oauth
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Security BearerAuth
// @Router /user/oauth/authorize [post]
func (h *OAuthHandler) AuthorizeDecision(c *fiber.Ctx) error {
	var b authorizeRequestBody
	if err := c.BodyParser(&b); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_request"})
	}
	req, err := h.svc.ValidateAuthorize(c.Context(), b.params())
	if err != nil {
		return h.authorizeFailure(c, req, err)
	}
	if !b.Approve {
		return c.JSON(fiber.Map{"redirect_to": callbackURL(req.RedirectURI, map[string]string{
			"error": "access_denied", "error_description": "the user denied the request", "state": req.State,
		})})
	}
	code, err := h.svc.Approve(c.Context(), c.Locals("user_id").(string), req)
	if err != nil {
		return oauthJSONError(c, err)
	}
	return c.JSON(fiber.Map{"redirect_to": callbackURL(req.RedirectURI, map[string]string{"code": code, "state": req.State})})
}

type tokenRequest struct {
	GrantType    string `json:"grant_type" form:"grant_type"`
	Code         string `json:"code" form:"code"`
	RedirectURI  string `json:"redirect_uri" form:"redirect_uri"`
	CodeVerifier string `json:"code_verifier" form:"code_verifier"`
	RefreshToken string `json:"refresh_token" form:"refresh_token"`
	Scope        string `json:"scope" form:"scope"`
	Token        string `json:"token" form:"token"`
	ClientID     string `json:"client_id" form:"client_id"`
	ClientSecret string `json:"client_secret" form:"client_secret"`
}

// clientCredentials reads HTTP Basic first (RFC 6749 2.3.1), then the body.
// Sending both is rejected so one request cannot carry two identities.
func clientCredentials(c *fiber.Ctx, b tokenRequest) (string, string, bool) {
	header := c.Get(fiber.HeaderAuthorization)
	if strings.HasPrefix(header, "Basic ") {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
		if err != nil || b.ClientSecret != "" {
			return "", "", false
		}
		id, secret, ok := strings.Cut(string(raw), ":")
		if !ok {
			return "", "", false
		}
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
		if b.ClientID != "" && b.ClientID != id {
			return "", "", false
		}
		return id, secret, true
	}
	return b.ClientID, b.ClientSecret, true
}

func (h *OAuthHandler) authenticatedClient(c *fiber.Ctx) (*services.OAuthClient, tokenRequest, error) {
	var b tokenRequest
	if err := c.BodyParser(&b); err != nil {
		return nil, b, &services.OAuthError{Status: 400, Code: "invalid_request", Description: "unreadable request body"}
	}
	id, secret, ok := clientCredentials(c, b)
	if !ok {
		return nil, b, &services.OAuthError{Status: 401, Code: "invalid_client", Description: "client authentication failed"}
	}
	client, err := h.svc.AuthenticateClient(c.Context(), id, secret)
	return client, b, err
}

// Token handles POST /api/v1/oauth/token (authorization_code, refresh_token).
// @Summary Exchange an authorization code or refresh token for tokens
// @Tags oauth
// @Accept x-www-form-urlencoded
// @Produce json
// @Success 200 {object} services.OAuthTokenSet
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Router /oauth/token [post]
func (h *OAuthHandler) Token(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set("Pragma", "no-cache")
	client, b, err := h.authenticatedClient(c)
	if err != nil {
		if errors.As(err, new(*services.OAuthError)) && strings.HasPrefix(c.Get(fiber.HeaderAuthorization), "Basic ") {
			c.Set(fiber.HeaderWWWAuthenticate, `Basic realm="oauth"`)
		}
		return oauthJSONError(c, err)
	}
	var set *services.OAuthTokenSet
	switch b.GrantType {
	case "authorization_code":
		set, err = h.svc.ExchangeCode(c.Context(), client, b.Code, b.RedirectURI, b.CodeVerifier)
	case "refresh_token":
		set, err = h.svc.Refresh(c.Context(), client, b.RefreshToken, b.Scope)
	case "":
		err = &services.OAuthError{Status: 400, Code: "invalid_request", Description: "grant_type is required"}
	default:
		err = &services.OAuthError{Status: 400, Code: "unsupported_grant_type", Description: "grant_type " + b.GrantType + " is not supported"}
	}
	if err != nil {
		return oauthJSONError(c, err)
	}
	return c.JSON(set)
}

// Revoke handles POST /api/v1/oauth/revoke (RFC 7009).
// @Summary Revoke an access or refresh token
// @Tags oauth
// @Accept x-www-form-urlencoded
// @Success 200
// @Failure 401 {object} map[string]string
// @Router /oauth/revoke [post]
func (h *OAuthHandler) Revoke(c *fiber.Ctx) error {
	client, b, err := h.authenticatedClient(c)
	if err != nil {
		return oauthJSONError(c, err)
	}
	if err := h.svc.RevokeToken(c.Context(), client, b.Token); err != nil {
		return oauthJSONError(c, err)
	}
	return c.SendStatus(fiber.StatusOK)
}

// ListGrants handles GET /api/v1/user/oauth/grants.
// @Summary List the apps connected to my account
// @Tags user-oauth
// @Produce json
// @Success 200 {array} services.OAuthGrantView
// @Security BearerAuth
// @Router /user/oauth/grants [get]
func (h *OAuthHandler) ListGrants(c *fiber.Ctx) error {
	list, err := h.svc.ListGrants(c.Context(), c.Locals("user_id").(string))
	if err != nil {
		return oauthJSONError(c, err)
	}
	return c.JSON(list)
}

// RevokeGrant handles DELETE /api/v1/user/oauth/grants/:client_id.
// @Summary Disconnect an app from my account
// @Tags user-oauth
// @Param client_id path string true "Client ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /user/oauth/grants/{client_id} [delete]
func (h *OAuthHandler) RevokeGrant(c *fiber.Ctx) error {
	ok, err := h.svc.RevokeGrant(c.Context(), c.Locals("user_id").(string), c.Params("client_id"))
	if err != nil {
		return oauthJSONError(c, err)
	}
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "connected app not found"})
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// ClientProfile handles GET /api/v1/client/profile (scope profile).
// @Summary The signed-in user's basic profile
// @Tags client
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Security BearerAuth
// @Router /client/profile [get]
func (h *OAuthHandler) ClientProfile(c *fiber.Ctx) error {
	var name, email, status, language string
	err := h.db.QueryRow(context.Background(),
		`SELECT name, email, status, language FROM users WHERE id = $1`, c.Locals("user_id").(string),
	).Scan(&name, &email, &status, &language)
	if err != nil {
		return oauthJSONError(c, err)
	}
	return c.JSON(fiber.Map{"id": c.Locals("user_id"), "name": name, "email": email, "status": status, "language": language})
}

type clientSubscriptionDomain struct {
	Domain    string `json:"domain"`
	IsDefault bool   `json:"is_default"`
	URL       string `json:"url"`
}

// ClientSubscription handles GET /api/v1/client/subscription (scope
// subscription:read): the account subscription URL on every public domain.
// @Summary The user's subscription URLs
// @Tags client
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Security BearerAuth
// @Router /client/subscription [get]
func (h *OAuthHandler) ClientSubscription(c *fiber.Ctx) error {
	ctx := context.Background()
	var token string
	if err := h.db.QueryRow(ctx, `SELECT COALESCE(sub_token, '') FROM users WHERE id = $1`,
		c.Locals("user_id").(string)).Scan(&token); err != nil {
		return oauthJSONError(c, err)
	}
	if token == "" {
		return c.JSON(fiber.Map{"available": false, "url": nil, "domains": []clientSubscriptionDomain{}})
	}
	rows, err := h.db.Query(ctx, `SELECT domain, is_default FROM subscription_domains
		WHERE is_enabled AND is_public ORDER BY is_default DESC, sort_order, created_at`)
	if err != nil {
		return oauthJSONError(c, err)
	}
	defer rows.Close()
	domains := []clientSubscriptionDomain{}
	for rows.Next() {
		var d clientSubscriptionDomain
		if err := rows.Scan(&d.Domain, &d.IsDefault); err != nil {
			return oauthJSONError(c, err)
		}
		d.URL = "https://" + d.Domain + "/sub/" + url.PathEscape(token)
		domains = append(domains, d)
	}
	primary := h.panelURL + "/sub/" + url.PathEscape(token)
	if len(domains) > 0 {
		primary = domains[0].URL
	}
	return c.JSON(fiber.Map{"available": true, "url": primary, "domains": domains})
}

// ListAppUsers handles GET /api/v1/admin/oauth-apps/:id/users.
// @Summary List users who authorized an OAuth app
// @Tags admin-oauth
// @Param id path string true "App ID"
// @Produce json
// @Success 200 {array} services.OAuthAppUser
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/oauth-apps/{id}/users [get]
func (h *OAuthHandler) ListAppUsers(c *fiber.Ctx) error {
	if !isUUID(c.Params("id")) {
		return adminClientError(c, services.ErrOAuthClientNotFound)
	}
	list, err := h.svc.ListAppUsers(c.Context(), c.Params("id"))
	if err != nil {
		return adminClientError(c, err)
	}
	return c.JSON(list)
}

// RevokeAppUser handles DELETE /api/v1/admin/oauth-apps/:id/users/:user_id.
// @Summary Disconnect one user from an OAuth app
// @Tags admin-oauth
// @Param id path string true "App ID"
// @Param user_id path string true "User ID"
// @Success 200 {object} map[string]int
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/oauth-apps/{id}/users/{user_id} [delete]
func (h *OAuthHandler) RevokeAppUser(c *fiber.Ctx) error {
	if !isUUID(c.Params("id")) {
		return adminClientError(c, services.ErrOAuthClientNotFound)
	}
	if !isUUID(c.Params("user_id")) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "this user has not authorized the app"})
	}
	n, err := h.svc.RevokeAppUsers(c.Context(), c.Params("id"), c.Params("user_id"))
	if err != nil {
		return adminClientError(c, err)
	}
	if n == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "this user has not authorized the app"})
	}
	return c.JSON(fiber.Map{"revoked": n})
}

// RevokeAllAppUsers handles DELETE /api/v1/admin/oauth-apps/:id/users. Every
// user is disconnected and must approve the app again; the app itself stays.
// @Summary Disconnect every user from an OAuth app
// @Tags admin-oauth
// @Param id path string true "App ID"
// @Success 200 {object} map[string]int
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/oauth-apps/{id}/users [delete]
func (h *OAuthHandler) RevokeAllAppUsers(c *fiber.Ctx) error {
	if !isUUID(c.Params("id")) {
		return adminClientError(c, services.ErrOAuthClientNotFound)
	}
	n, err := h.svc.RevokeAppUsers(c.Context(), c.Params("id"), "")
	if err != nil {
		return adminClientError(c, err)
	}
	return c.JSON(fiber.Map{"revoked": n})
}
