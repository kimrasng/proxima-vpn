package handlers

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"testing"
	"time"
)

func TestCertificateStatus_classifiesTransportAndVerificationFailures(t *testing.T) {
	// Given: transport and certificate verification errors are distinct.
	transport := errors.New("connection refused")
	verification := &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}

	// When: each TLS failure is classified.
	transportStatus := certificateStatusForTLSFailure(transport)
	verificationStatus := certificateStatusForTLSFailure(verification)

	// Then: only a verified certificate failure is marked failed.
	if transportStatus != "unknown" || verificationStatus != "failed" {
		t.Fatalf("certificate failure statuses = %q, %q; want unknown, failed", transportStatus, verificationStatus)
	}
}

func TestCertificateStatus_classifiesExpiryOutcomes(t *testing.T) {
	// Given: expiry times on either side of the warning boundary.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// When: certificate expiry is classified.
	expired := certificateStatusAt(now, now.Add(-time.Hour))
	warning := certificateStatusAt(now, now.Add(6*24*time.Hour))
	healthy := certificateStatusAt(now, now.Add(8*24*time.Hour))

	// Then: expired, expiring, and healthy certificates have distinct valid statuses.
	if expired != "failed" || warning != "warning" || healthy != "healthy" {
		t.Fatalf("expiry statuses = %q, %q, %q; want failed, warning, healthy", expired, warning, healthy)
	}
}
