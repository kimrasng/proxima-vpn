package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/server"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func TestNodeBandwidthPermitRouteRequiresNodeAPIKey(t *testing.T) {
	t.Setenv("SWAGGER_ENABLED", "false")
	app := server.NewServer(&config.Config{}, nil, nil, nil, nil).App()
	t.Cleanup(func() { _ = app.Shutdown() })
	request := httptest.NewRequest(fiber.MethodPost, "/api/v1/nodes/"+crypto.NewUUID()+"/bandwidth/permit", strings.NewReader(`{}`))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("unauthenticated permit status = %d, want 401", response.StatusCode)
	}
}

// Uses the established disposable TEST_DATABASE_URL fixture. Redis is deliberately
// absent: unlimited eligible devices can pass, but positive limits must fail closed.
func TestNodeBandwidthPermitAuthenticatedHTTPAuthorization(t *testing.T) {
	fixture := newExitRulesHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := fixture.seedNode("bandwidth-exit-"+suffix, "exit", "203.0.113.50")
	otherExit := fixture.seedNode("bandwidth-other-exit-"+suffix, "both", "203.0.113.51")
	relayID := fixture.seedNode("bandwidth-relay-"+suffix, "relay", "203.0.113.52")
	groupID := fixture.seedGroup("bandwidth-group-" + suffix)
	fixture.addPoolMember(groupID, exitID)
	fixture.addPoolMember(groupID, relayID)
	if _, err := fixture.pool.Exec(ctx, `UPDATE nodes SET api_key = $2 WHERE id = $1`, otherExit, "other-key"); err != nil {
		t.Fatal(err)
	}

	var planID, userID, deviceID string
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO plans (name, duration_days, max_devices, node_group_id)
		 VALUES ($1, 30, 2, $2) RETURNING id::text`, "bandwidth-plan-"+suffix, groupID,
	).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = fixture.pool.Exec(ctx, `DELETE FROM plans WHERE id=$1`, planID) })
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, sub_token, plan_id, status, is_active, plan_expires_at)
		 VALUES ($1, 'unused', $2, $3, 'active', true, NOW()+INTERVAL '1 day') RETURNING id::text`,
		"bandwidth-"+suffix+"@example.test", crypto.NewUUID(), planID,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = fixture.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) })
	deviceUUID := crypto.NewUUID()
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, name, xray_uuid) VALUES ($1, 'bandwidth-test', $2) RETURNING id::text`, userID, deviceUUID,
	).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}

	requestPermit := func(nodeID, key, body string, status int, allowed bool) {
		t.Helper()
		request := httptest.NewRequest(fiber.MethodPost, "/api/v1/nodes/"+nodeID+"/bandwidth/permit", strings.NewReader(body))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		request.Header.Set("X-Node-Key", key)
		response, err := fixture.app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != status {
			t.Fatalf("permit status = %d, want %d; body=%s", response.StatusCode, status, data)
		}
		if status != fiber.StatusOK {
			return
		}
		var permit devicebandwidth.PermitResponse
		if err := json.Unmarshal(data, &permit); err != nil {
			t.Fatal(err)
		}
		if permit.Allowed != allowed || permit.RetryAfterMS != 0 {
			t.Fatalf("permit = %+v, want allowed=%v, retry_after_ms=0", permit, allowed)
		}
	}
	body := `{"device_uuid":"` + deviceUUID + `","direction":"upload","bytes":65535}`
	requestPermit(exitID, "test", body, fiber.StatusOK, true)
	requestPermit(exitID, "wrong", body, fiber.StatusUnauthorized, false)
	requestPermit(otherExit, "test", body, fiber.StatusUnauthorized, false) // A different node's key cannot spoof path identity.
	requestPermit(otherExit, "other-key", body, fiber.StatusOK, false)      // Exit outside the plan group.
	requestPermit(relayID, "test", body, fiber.StatusOK, false)
	requestPermit(exitID, "test", `{"device_uuid":"`+crypto.NewUUID()+`","direction":"upload","bytes":1}`, fiber.StatusOK, false)

	for _, tc := range []struct {
		name, denySQL, restoreSQL string
		id                        string
	}{
		{"inactive user", `UPDATE users SET is_active=false WHERE id=$1`, `UPDATE users SET is_active=true WHERE id=$1`, userID},
		{"inactive user status", `UPDATE users SET status='pending' WHERE id=$1`, `UPDATE users SET status='active' WHERE id=$1`, userID},
		{"expired plan", `UPDATE users SET plan_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, `UPDATE users SET plan_expires_at=NOW()+INTERVAL '1 day' WHERE id=$1`, userID},
		{"inactive plan", `UPDATE plans SET is_active=false WHERE id=$1`, `UPDATE plans SET is_active=true WHERE id=$1`, planID},
		{"exhausted quota", `UPDATE plans SET traffic_limit=0 WHERE id=$1`, `UPDATE plans SET traffic_limit=NULL WHERE id=$1`, planID},
		{"evicted device", `UPDATE devices SET evicted_until=NOW()+INTERVAL '1 hour' WHERE id=$1`, `UPDATE devices SET evicted_until=NOW()-INTERVAL '1 second' WHERE id=$1`, deviceID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fixture.pool.Exec(ctx, tc.denySQL, tc.id); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := fixture.pool.Exec(ctx, tc.restoreSQL, tc.id); err != nil {
					t.Error(err)
				}
			}()
			requestPermit(exitID, "test", body, fiber.StatusOK, false)
		})
	}
	requestPermit(exitID, "test", body, fiber.StatusOK, true)
	if _, err := fixture.pool.Exec(ctx, `UPDATE plans SET speed_limit=1 WHERE id=$1`, planID); err != nil {
		t.Fatal(err)
	}
	requestPermit(exitID, "test", body, fiber.StatusServiceUnavailable, false)
	requestPermit(exitID, "test", `{"device_uuid":"`+deviceUUID+`","direction":"upload","bytes":1,"speed_limit":0}`, fiber.StatusServiceUnavailable, false)
}
