package handlers

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

const (
	// settingSignupDomainRestriction turns the allow-list on ("true") or off.
	settingSignupDomainRestriction = "signup_email_domain_restriction"
	// settingSignupEmailDomains holds the allowed domains, comma- or
	// whitespace-separated, e.g. "gmail.com, qq.com".
	settingSignupEmailDomains = "signup_email_domains"
)

// settingsQuerier is the slice of *pgxpool.Pool the signup policy needs, so it
// can be tested without a connection pool.
type settingsQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// normalizeEmailDomains splits a stored/entered list into unique, lower-case
// domains. Entries are tolerated in the forms an admin is likely to paste:
// "gmail.com", "@gmail.com", " Gmail.COM ", "user@gmail.com". Anything that is
// not a plausible hostname is dropped rather than stored, so a typo can never
// widen the allow-list into something that matches unexpectedly.
func normalizeEmailDomains(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		d := strings.ToLower(strings.TrimSpace(f))
		if i := strings.LastIndex(d, "@"); i >= 0 {
			d = d[i+1:]
		}
		d = strings.Trim(d, ".")
		if !validEmailDomain(d) {
			continue
		}
		if _, dup := seen[d]; dup {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

// validEmailDomain accepts a dotted hostname made of letters, digits and
// hyphens (internationalised names must be given in their punycode form).
func validEmailDomain(d string) bool {
	if len(d) < 3 || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// emailDomain returns the lower-case part after the last "@", or "" when the
// address has none.
func emailDomain(email string) string {
	i := strings.LastIndex(email, "@")
	if i < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[i+1:]))
}

// signupDomainAllowed reports whether an address may register under the
// current policy. The policy is enforced only when restriction is "true"; an
// enabled restriction with an empty list refuses everyone, because "restrict to
// nothing" is a configuration the admin must notice rather than a silent
// open door. Matching is exact: "mail.com" does not admit "evilmail.com" or
// "sub.mail.com".
func signupDomainAllowed(email string, restrictionOn bool, allowed []string) bool {
	if !restrictionOn {
		return true
	}
	d := emailDomain(email)
	if d == "" {
		return false
	}
	for _, a := range allowed {
		if d == a {
			return true
		}
	}
	return false
}

// loadSignupPolicy reads the policy. A missing or unreadable setting means
// "not restricted": registration must keep working on an install that never
// configured it, and the admin toggle is the only thing that turns it on.
func loadSignupPolicy(ctx context.Context, db settingsQuerier) (restrictionOn bool, allowed []string) {
	var on string
	if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, settingSignupDomainRestriction).Scan(&on); err != nil {
		return false, nil
	}
	if strings.TrimSpace(strings.ToLower(on)) != "true" {
		return false, nil
	}
	var list string
	if err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, settingSignupEmailDomains).Scan(&list); err != nil {
		// Restriction is on but the list cannot be read: fail closed.
		return true, nil
	}
	return true, normalizeEmailDomains(list)
}

// SignupPolicy reports, before anyone fills in the form, whether registration
// is limited to certain email domains and which ones. It is public on purpose:
// the sign-up page shows the list up front instead of rejecting people after
// they have typed everything. It exposes nothing beyond what a refused
// registration already returns.
// @Summary Sign-up email domain policy
// @Tags user-auth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /auth/signup-policy [get]
func (h *UserAuthHandler) SignupPolicy(c *fiber.Ctx) error {
	on, allowed := loadSignupPolicy(c.UserContext(), h.db)
	if allowed == nil {
		allowed = []string{}
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(fiber.Map{
		"restricted":      on,
		"allowed_domains": allowed,
	})
}
