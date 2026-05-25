package crypto

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestGenerateTOTPSecret(t *testing.T) {
	secret, url, err := GenerateTOTPSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error: %v", err)
	}
	if len(secret) == 0 {
		t.Error("GenerateTOTPSecret() returned empty secret")
	}
	if len(url) == 0 {
		t.Error("GenerateTOTPSecret() returned empty URL")
	}
}

func TestGenerateTOTPSecret_URLFormat(t *testing.T) {
	_, url, err := GenerateTOTPSecret("user@domain.com")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error: %v", err)
	}
	if len(url) < 10 {
		t.Errorf("GenerateTOTPSecret() URL too short: %q", url)
	}
}

func TestValidateTOTP(t *testing.T) {
	secret, _, err := GenerateTOTPSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error: %v", err)
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("totp.GenerateCode() error: %v", err)
	}

	if !ValidateTOTP(secret, code) {
		t.Error("ValidateTOTP() returned false for valid code")
	}
}

func TestValidateTOTP_InvalidCode(t *testing.T) {
	secret, _, err := GenerateTOTPSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error: %v", err)
	}
	if ValidateTOTP(secret, "000000") {
		t.Error("ValidateTOTP() returned true for invalid code")
	}
}
