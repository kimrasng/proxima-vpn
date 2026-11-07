package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
)

const (
	serverReleaseTimeout = 5 * time.Second
	serverReleaseMaxBody = 1 << 20
)

var (
	serverReleaseOwner = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	serverReleaseRepo  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
)

// ServerReleaseHandler discovers public releases only. Mount Latest behind
// AdminJWTMiddleware and RequireAdminOwner; it also checks the loaded owner role.
type ServerReleaseHandler struct {
	client http.Client
}

// NewServerReleaseHandler accepts a trusted client for transport injection.
// It copies the client and enforces its own timeout and redirect policy.
// A nil client uses the standard HTTP transport without credentials or cookies.
func NewServerReleaseHandler(client *http.Client) *ServerReleaseHandler {
	h := &ServerReleaseHandler{}
	if client != nil {
		h.client = *client
	}
	if h.client.Timeout <= 0 || h.client.Timeout > serverReleaseTimeout {
		h.client.Timeout = serverReleaseTimeout
	}
	h.client.Jar = nil
	h.client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return h
}

// Latest handles GET requests with owner and repo query parameters. The response
// contains only tag, name, published_at (RFC3339), and html_url; it never installs
// or downloads a release and never forwards GitHub response bodies on errors.
func (h *ServerReleaseHandler) Latest(c *fiber.Ctx) error {
	if role, _ := c.Locals("admin_role").(string); role != "owner" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "owner_required"})
	}
	if c.Method() != http.MethodGet {
		c.Set("Allow", http.MethodGet)
		return c.Status(fiber.StatusMethodNotAllowed).JSON(fiber.Map{"error": "method_not_allowed"})
	}
	owner, repo := c.Query("owner"), c.Query("repo")
	if !serverReleaseOwner.MatchString(owner) || strings.Contains(owner, "--") ||
		!serverReleaseRepo.MatchString(repo) || repo == "." || repo == ".." ||
		len(c.Context().QueryArgs().PeekMulti("owner")) != 1 ||
		len(c.Context().QueryArgs().PeekMulti("repo")) != 1 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_repository"})
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), serverReleaseTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+owner+"/"+repo+"/releases/latest", nil)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "release_unavailable"})
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Proxima-Release-Discovery")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := h.client.Do(req)
	if err != nil {
		status := fiber.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = fiber.StatusGatewayTimeout
		}
		return c.Status(status).JSON(fiber.Map{"error": "release_unavailable"})
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "release_not_found"})
	}
	if resp.StatusCode != http.StatusOK {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "release_unavailable"})
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, serverReleaseMaxBody+1))
	if err != nil || len(body) > serverReleaseMaxBody || !utf8.Valid(body) {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "invalid_release_response"})
	}
	var release struct {
		Tag         string `json:"tag_name"`
		Name        string `json:"name"`
		PublishedAt string `json:"published_at"`
		HTMLURL     string `json:"html_url"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "invalid_release_response"})
	}
	published, dateErr := time.Parse(time.RFC3339, release.PublishedAt)
	link, urlErr := url.Parse(release.HTMLURL)
	if !serverReleaseText(release.Tag, 256) || len(release.Name) > 1024 ||
		(release.Name != "" && !serverReleaseText(release.Name, 1024)) ||
		dateErr != nil || published.IsZero() || release.Draft || release.Prerelease ||
		urlErr != nil || link.Scheme != "https" || link.Host != "github.com" ||
		link.User != nil || link.RawQuery != "" || link.ForceQuery || link.Fragment != "" ||
		!strings.EqualFold(strings.TrimSuffix(link.Path, "/releases/tag/"+release.Tag), "/"+owner+"/"+repo) ||
		!strings.HasSuffix(link.Path, "/releases/tag/"+release.Tag) {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "invalid_release_response"})
	}
	return c.JSON(fiber.Map{
		"tag": release.Tag, "name": release.Name,
		"published_at": release.PublishedAt, "html_url": release.HTMLURL,
	})
}

func serverReleaseText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max &&
		!strings.ContainsFunc(value, unicode.IsControl)
}
