package crypto

import (
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// GenerateTOTPSecret generates a new TOTP secret and otpauth URL for the given email.
func GenerateTOTPSecret(email string) (secret string, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "ProximaVPN",
		AccountName: email,
		Algorithm:   otp.AlgorithmSHA1,
		Digits:      otp.DigitsSix,
		Period:      30,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTP validates a 6-digit TOTP code against the given secret.
func ValidateTOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}
