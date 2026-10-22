package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestSignupEmailCodeSecurity(t *testing.T) {
	pool := signupTestDB(t)
	ctx := context.Background()
	setSignupSettings(t, pool, "false", "")
	mailer := &signupCodeMailer{}
	h := NewUserAuthHandler(pool, "signup-security-secret", time.Hour, nil, mailer, "")
	app := fiber.New()
	app.Post("/request", h.RequestSignupEmailCode)
	app.Post("/verify", h.VerifySignupEmailCode)
	app.Post("/legacy", h.Register)
	request := func(path string, body any) signupHTTPResponse {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		out, _ := io.ReadAll(res.Body)
		return signupHTTPResponse{res.StatusCode, string(out)}
	}
	payload := func(email string) map[string]string {
		return map[string]string{"email": email, "password": "SignupPass!42", "name": "Signup test"}
	}
	address := func(t *testing.T) string {
		email := fmt.Sprintf("security-%d@example.test", time.Now().UnixNano())
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM signup_email_verifications WHERE lower(email)=lower($1)`, email)
			_, _ = pool.Exec(ctx, `DELETE FROM users WHERE lower(email)=lower($1)`, email)
		})
		return email
	}
	tokens := map[string]string{}
	withToken := func(email string) map[string]string {
		body := payload(email)
		body["signup_token"] = tokens[strings.ToLower(strings.TrimSpace(email))]
		return body
	}
	verifyBody := func(email, code string) map[string]string {
		return map[string]string{"email": email, "code": code, "signup_token": tokens[strings.ToLower(strings.TrimSpace(email))]}
	}
	challenge := func(t *testing.T, email string) string {
		t.Helper()
		got := request("/request", payload(email))
		if got.code != 200 {
			t.Fatalf("request: %+v", got)
		}
		var issued struct {
			SignupToken string `json:"signup_token"`
		}
		if err := json.Unmarshal([]byte(got.body), &issued); err != nil || issued.SignupToken == "" {
			t.Fatalf("request returned no signup_token: %+v err=%v", got, err)
		}
		tokens[strings.ToLower(strings.TrimSpace(email))] = issued.SignupToken
		mailer.mu.Lock()
		defer mailer.mu.Unlock()
		return regexp.MustCompile(`\b[0-9]{6}\b`).FindString(mailer.messages[len(mailer.messages)-1].Body)
	}
	assertNoAccount := func(t *testing.T, email string) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email)=lower($1)`, email).Scan(&count); err != nil || count != 0 {
			t.Fatalf("users=%d error=%v", count, err)
		}
	}
	t.Run("invalid input stops before sending or storing", func(t *testing.T) {
		for _, edit := range []map[string]string{{"email": ""}, {"email": "invalid"}, {"password": ""}, {"name": " "}, {"password": "short"}, {"password": strings.Repeat("a", 73)}, {"name": strings.Repeat("a", 129)}} {
			email := address(t)
			body := payload(email)
			for k, v := range edit {
				body[k] = v
			}
			before := len(mailer.messages)
			if got := request("/request", body); got.code != 400 {
				t.Fatalf("input %v: %+v", edit, got)
			}
			if len(mailer.messages) != before {
				t.Fatal("invalid input sent mail")
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM signup_email_verifications WHERE email=$1`, email).Scan(&count); err != nil || count != 0 {
				t.Fatalf("challenge count=%d error=%v", count, err)
			}
		}
	})
	t.Run("normalization HMAC cooldown and one-time legacy verification", func(t *testing.T) {
		email := address(t)
		code := challenge(t, "  "+strings.ToUpper(email)+"  ")
		var storedEmail, storedHash string
		if err := pool.QueryRow(ctx, `SELECT email,code_hash FROM signup_email_verifications WHERE email=$1`, email).Scan(&storedEmail, &storedHash); err != nil {
			t.Fatal(err)
		}
		mac := hmac.New(sha256.New, []byte("signup-security-secret"))
		_, _ = mac.Write([]byte(email + ":" + code))
		if storedEmail != email || storedHash != hex.EncodeToString(mac.Sum(nil)) || storedHash == code {
			t.Fatal("challenge not normalized/HMAC protected")
		}
		if got := request("/request", withToken(strings.ToUpper(email))); got.code != 429 {
			t.Fatalf("cooldown: %+v", got)
		}
		assertNoAccount(t, email)
		if got := request("/legacy", verifyBody(strings.ToUpper(email), code)); got.code != 201 {
			t.Fatalf("verify: %+v", got)
		}
		if got := request("/verify", verifyBody(email, code)); got.code != 400 {
			t.Fatalf("replay: %+v", got)
		}
	})
	t.Run("concurrent first send and resend cooldown", func(t *testing.T) {
		email := address(t)
		for round := 0; round < 2; round++ {
			wantLoser := 409
			if round == 1 {
				wantLoser = 429
			}
			before := len(mailer.messages)
			var wg sync.WaitGroup
			results := make(chan signupHTTPResponse, 4)
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					e := email
					if i%2 == 0 {
						e = strings.ToUpper(e)
					}
					body := payload(e)
					if round == 1 {
						body = withToken(e)
					}
					results <- request("/request", body)
				}(i)
			}
			wg.Wait()
			close(results)
			successes := 0
			for res := range results {
				if res.code == 200 {
					successes++
					if round == 0 {
						var issued struct {
							SignupToken string `json:"signup_token"`
						}
						_ = json.Unmarshal([]byte(res.body), &issued)
						tokens[email] = issued.SignupToken
					}
				} else if res.code != wantLoser {
					t.Fatalf("round %d: unexpected status %d, want %d", round, res.code, wantLoser)
				}
			}
			if successes != 1 || len(mailer.messages)-before != 1 {
				t.Fatalf("sent=%d successes=%d", len(mailer.messages)-before, successes)
			}
			if _, err := pool.Exec(ctx, `UPDATE signup_email_verifications SET sent_at=NOW()-INTERVAL '61 seconds' WHERE email=$1`, email); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("live challenge cannot be overwritten or completed by someone without its token", func(t *testing.T) {
		email := address(t)
		code := challenge(t, email)
		owner := tokens[email]
		before := len(mailer.messages)
		attacker := payload(email)
		attacker["password"] = "AttackerPass!99"
		if got := request("/request", attacker); got.code != 409 {
			t.Fatalf("overwrite without token: %+v", got)
		}
		if _, err := pool.Exec(ctx, `UPDATE signup_email_verifications SET sent_at=NOW()-INTERVAL '61 seconds' WHERE email=$1`, email); err != nil {
			t.Fatal(err)
		}
		attacker["signup_token"] = strings.Repeat("a", 64)
		if got := request("/request", attacker); got.code != 429 {
			t.Fatalf("overwrite with foreign token after cooldown: %+v", got)
		}
		if len(mailer.messages) != before {
			t.Fatal("rejected overwrite sent mail")
		}
		for _, token := range []string{"", strings.Repeat("a", 64)} {
			if got := request("/verify", map[string]string{"email": email, "code": code, "signup_token": token}); got.code != 400 {
				t.Fatalf("verify with token %q: %+v", token, got)
			}
		}
		var attempts int
		if err := pool.QueryRow(ctx, `SELECT attempts FROM signup_email_verifications WHERE email=$1`, email).Scan(&attempts); err != nil || attempts != 0 {
			t.Fatalf("foreign verify burned attempts=%d err=%v", attempts, err)
		}
		tokens[email] = owner
		if got := request("/verify", verifyBody(email, code)); got.code != 201 {
			t.Fatalf("owner verify: %+v", got)
		}
		var hash string
		if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email=$1`, email).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if !crypto.CheckPassword(hash, "SignupPass!42") {
			t.Fatal("account does not carry the owner's password")
		}
	})
	t.Run("expired challenge can be replaced by a new signup", func(t *testing.T) {
		email := address(t)
		challenge(t, email)
		if _, err := pool.Exec(ctx, `UPDATE signup_email_verifications SET expires_at=NOW()-INTERVAL '1 second' WHERE email=$1`, email); err != nil {
			t.Fatal(err)
		}
		code := challenge(t, email)
		if got := request("/verify", verifyBody(email, code)); got.code != 201 {
			t.Fatalf("verify after replacement: %+v", got)
		}
	})
	t.Run("malformed signup_token is rejected before storing", func(t *testing.T) {
		email := address(t)
		body := payload(email)
		body["signup_token"] = "short"
		if got := request("/request", body); got.code != 400 {
			t.Fatalf("malformed token: %+v", got)
		}
	})
	t.Run("expired code is consumed without account creation", func(t *testing.T) {
		email := address(t)
		code := challenge(t, email)
		if _, err := pool.Exec(ctx, `UPDATE signup_email_verifications SET expires_at=NOW()-INTERVAL '1 second' WHERE email=$1`, email); err != nil {
			t.Fatal(err)
		}
		if got := request("/verify", verifyBody(email, code)); got.code != 400 {
			t.Fatalf("expired: %+v", got)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM signup_email_verifications WHERE email=$1`, email).Scan(&count); err != nil || count != 0 {
			t.Fatalf("expired challenge count=%d error=%v", count, err)
		}
		assertNoAccount(t, email)
	})
	t.Run("maximum attempts rejects even correct code", func(t *testing.T) {
		email := address(t)
		code := challenge(t, email)
		wrong := "000000"
		if code == wrong {
			wrong = "000001"
		}
		for i := 0; i < signupCodeMaxAttempts; i++ {
			if got := request("/verify", verifyBody(email, wrong)); got.code != 400 {
				t.Fatalf("attempt %d: %+v", i, got)
			}
		}
		if got := request("/verify", verifyBody(email, code)); got.code != 429 {
			t.Fatalf("locked: %+v", got)
		}
		assertNoAccount(t, email)
	})
	t.Run("mailer failure rolls back challenge and cooldown", func(t *testing.T) {
		email := address(t)
		mailer.err = errors.New("test send failure")
		got := request("/request", payload(email))
		mailer.err = nil
		if got.code != 502 {
			t.Fatalf("send failure: %+v", got)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM signup_email_verifications WHERE email=$1`, email).Scan(&count); err != nil || count != 0 {
			t.Fatalf("challenge count=%d error=%v", count, err)
		}
		challenge(t, email)
		assertNoAccount(t, email)
	})
	t.Run("legacy credentials alone never register", func(t *testing.T) {
		email := address(t)
		if got := request("/legacy", payload(email)); got.code != 400 {
			t.Fatalf("legacy bypass: %+v", got)
		}
		assertNoAccount(t, email)
	})
	t.Run("unavailable mail and existing accounts stop request", func(t *testing.T) {
		email := address(t)
		h.mailer = nil
		got := request("/request", payload(email))
		h.mailer = mailer
		if got.code != 503 {
			t.Fatalf("nil mailer: %+v", got)
		}
		code := challenge(t, email)
		if got := request("/verify", verifyBody(email, code)); got.code != 201 {
			t.Fatalf("verify: %+v", got)
		}
		if got := request("/request", payload(strings.ToUpper(email))); got.code != 409 {
			t.Fatalf("duplicate: %+v", got)
		}
	})
	for _, policy := range []struct {
		name, restriction, domains string
		disabled                   bool
	}{
		{"changed domain", "true", "gmail.com", false},
		{"empty domain policy", "true", "", false},
		{"invalid domain policy", "true", "*.com,localhost", false},
		{"registration disabled", "false", "", true},
	} {
		t.Run(policy.name, func(t *testing.T) {
			setSignupSettings(t, pool, "false", "")
			email := address(t)
			code := challenge(t, email)
			setSignupSettings(t, pool, policy.restriction, policy.domains)
			if policy.disabled {
				if _, err := pool.Exec(ctx, `UPDATE settings SET value='false' WHERE key='self_registration'`); err != nil {
					t.Fatal(err)
				}
			}
			before := len(mailer.messages)
			if got := request("/verify", verifyBody(email, code)); got.code != 403 {
				t.Fatalf("policy recheck: %+v", got)
			}
			if got := request("/request", payload(address(t))); got.code != 403 {
				t.Fatalf("policy request: %+v", got)
			}
			if len(mailer.messages) != before {
				t.Fatal("policy rejection sent mail")
			}
			assertNoAccount(t, email)
		})
	}
}
