package handlers

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	"github.com/gofiber/fiber/v2"
)

type domainHealthResponse struct {
	DomainID           string     `json:"domain_id"`
	DNSStatus          string     `json:"dns_status"`
	TLSStatus          string     `json:"tls_status"`
	CertificateStatus  string     `json:"certificate_status"`
	CertificateExpires *time.Time `json:"certificate_expires_at"`
	CheckedAt          *time.Time `json:"checked_at"`
	Error              string     `json:"error"`
}

func certificateStatusForTLSFailure(err error) string {
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return "failed"
	}
	return "unknown"
}

func certificateStatusAt(now, expires time.Time) string {
	if !expires.After(now) {
		return "failed"
	}
	if expires.Before(now.Add(7 * 24 * time.Hour)) {
		return "warning"
	}
	return "healthy"
}

// Health probes DNS resolution, TLS reachability, and certificate expiry for
// every domain in the pool, persists the results, and returns them.
// @Summary Check subscription domain health
// @Tags admin/subscription-domains
// @Produce json
// @Security BearerAuth
// @Success 200 {array} domainHealthResponse
// @Router /api/v1/admin/subscription-domains/health [get]
func (h *AdminSubscriptionDomainHandler) Health(c *fiber.Ctx) error {
	ctx := context.Background()
	rows, err := h.db.Query(ctx,
		`SELECT id, domain FROM subscription_domains ORDER BY sort_order, created_at`)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list domains"})
	}
	defer rows.Close()

	type target struct {
		id     string
		domain string
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.domain); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to scan domain"})
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list domains"})
	}

	now := time.Now()
	out := make([]domainHealthResponse, 0, len(targets))
	for _, t := range targets {
		hr := domainHealthResponse{DomainID: t.id, CheckedAt: &now, TLSStatus: "unknown", CertificateStatus: "unknown"}
		dnsResolved := false
		dnsAddrs := []string{}
		var dnsErr string
		addrs, err := net.LookupHost(t.domain)
		if err != nil {
			dnsErr = err.Error()
			hr.DNSStatus = "failed"
			hr.Error = dnsErr
		} else {
			dnsResolved = true
			dnsAddrs = addrs
			hr.DNSStatus = "healthy"
		}

		tlsReachable := false
		var tlsErr string
		var certExpires *time.Time
		if dnsResolved {
			conn, err := tls.DialWithDialer(
				&net.Dialer{Timeout: 5 * time.Second},
				"tcp", t.domain+":443",
				&tls.Config{ServerName: t.domain},
			)
			if err != nil {
				tlsErr = err.Error()
				hr.TLSStatus = "failed"
				hr.CertificateStatus = certificateStatusForTLSFailure(err)
				hr.Error = tlsErr
			} else {
				tlsReachable = true
				hr.TLSStatus = "healthy"
				state := conn.ConnectionState()
				if len(state.PeerCertificates) > 0 {
					exp := state.PeerCertificates[0].NotAfter
					certExpires = &exp
					hr.CertificateExpires = &exp
					hr.CertificateStatus = certificateStatusAt(now, exp)
				}
				if err := conn.Close(); err != nil {
					hr.Error = err.Error()
				}
			}
		}

		result, err := h.db.Exec(ctx,
			`UPDATE subscription_domains
			 SET dns_resolved = $1, dns_addresses = $2, dns_error = $3,
			     tls_reachable = $4, tls_error = $5, cert_expires_at = $6,
			     last_health_check_at = $7, updated_at = NOW()
			 WHERE id = $8`,
			dnsResolved, dnsAddrs, dnsErr,
			tlsReachable, tlsErr, certExpires,
			now, t.id,
		)
		if err != nil || result.RowsAffected() != 1 {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to persist domain health"})
		}
		out = append(out, hr)
	}

	return c.JSON(out)
}
