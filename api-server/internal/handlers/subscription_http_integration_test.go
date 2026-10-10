package handlers

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

type subscriptionHTTPFixture struct {
	app         *fiber.App
	pool        *pgxpool.Pool
	exitID      string
	chainID     string
	planID      string
	relayID     string
	path        string
	entryHost   string
	entryPort   int
	exitAddress string
}

func newSubscriptionHTTPFixture(t *testing.T, pool *pgxpool.Pool) subscriptionHTTPFixture {
	t.Helper()
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitAddress := "203.0.113.81"
	entryHost := "relay-" + suffix + ".example.test"
	entryPort := 30000 + os.Getpid()%20000

	exitID := seedChainNode(t, pool, "subscription-exit-"+suffix, "exit", 443)
	if _, err := pool.Exec(ctx, `INSERT INTO inbounds (node_id, protocol, port, tag, settings) VALUES ($1,'vless_reality',443,'reality-test','{"dest":"reality.example.test:443","server_names":["reality.example.test"]}')`, exitID); err != nil {
		t.Fatalf("seed Reality listener: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE nodes
		 SET status = 'online', ip = $2::inet, publish_direct = false,
		     reality_public_key = 'integration-public-key', reality_short_id = 'abcdef01',
		     reality_client_sni = 'reality.example.test', reality_sni_status = 'valid',
		     reality_sni_source = 'admin', last_seen = NOW(), xray_running = true
		 WHERE id = $1`, exitID, exitAddress,
	); err != nil {
		t.Fatalf("prepare subscription exit: %v", err)
	}

	planGroupID := seedChainGroup(t, pool, "subscription-plan-group-"+suffix)
	if _, err := pool.Exec(ctx, `INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`, planGroupID, exitID); err != nil {
		t.Fatalf("seed exit membership: %v", err)
	}
	relayPoolID := seedChainGroup(t, pool, "subscription-relay-pool-"+suffix)
	relayID := seedChainNode(t, pool, "subscription-relay-"+suffix, "relay", 443)
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`, relayPoolID, relayID,
	); err != nil {
		t.Fatalf("seed relay pool membership: %v", err)
	}

	var planID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plans (name, duration_days, max_devices, node_group_id)
		 VALUES ($1, 30, 1, $2) RETURNING id::text`,
		"subscription-plan-"+suffix, planGroupID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed subscription plan: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, planID); err != nil {
			t.Errorf("clean up subscription plan: %v", err)
		}
	})

	subToken := crypto.NewUUID()
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, name, password_hash, sub_token, plan_id, status, is_active)
		 VALUES ($1, 'subscription test', 'unused', $2, $3, 'active', true)
		 RETURNING id::text`,
		"subscription-"+suffix+"@example.test", subToken, planID,
	).Scan(&userID); err != nil {
		t.Fatalf("seed subscription user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Errorf("clean up subscription user: %v", err)
		}
	})

	deviceUUID := crypto.NewUUID()
	var deviceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, name, xray_uuid)
		 VALUES ($1, 'integration device', $2) RETURNING id::text`,
		userID, deviceUUID,
	).Scan(&deviceID); err != nil {
		t.Fatalf("seed subscription device: %v", err)
	}

	var chainID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_chains
		   (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport, health)
		 VALUES ($1, $2, $3, $4, $5, 443, 'tcp', 'healthy')
		 RETURNING id::text`,
		"subscription-chain-"+suffix, relayPoolID, entryHost, entryPort, exitID,
	).Scan(&chainID); err != nil {
		t.Fatalf("seed relayed chain: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM node_chains WHERE id = $1`, chainID); err != nil {
			t.Errorf("clean up relayed chain: %v", err)
		}
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_chains (node_group_id, chain_id) VALUES ($1, $2)`,
		planGroupID, chainID,
	); err != nil {
		t.Fatalf("attach relayed chain to plan group: %v", err)
	}
	digest, err := services.NewXrayConfigService(pool).GenerateDigest(ctx, exitID)
	if err != nil {
		t.Fatalf("generate applied fixture config: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET config_hash = $2 WHERE id = $1`, exitID, digest.Hash); err != nil {
		t.Fatalf("apply fixture config hash: %v", err)
	}

	handler := NewSubscriptionHandler(pool, 3600)
	app := fiber.New()
	app.Get("/sub/:sub_token/:device_id", handler.GetSubscription)
	t.Cleanup(func() {
		if err := app.Shutdown(); err != nil {
			t.Errorf("shut down Fiber app: %v", err)
		}
	})

	return subscriptionHTTPFixture{
		app:         app,
		pool:        pool,
		exitID:      exitID,
		chainID:     chainID,
		planID:      planID,
		relayID:     relayID,
		path:        fmt.Sprintf("/sub/%s/%s", subToken, deviceID),
		entryHost:   entryHost,
		entryPort:   entryPort,
		exitAddress: exitAddress,
	}
}

func TestGetSubscriptionAdvertisesRelayedChainEntryThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	fixture := newSubscriptionHTTPFixture(t, pool)

	request := httptest.NewRequest(fiber.MethodGet, fixture.path, nil)
	response, err := fixture.app.Test(request)
	if err != nil {
		t.Fatalf("GET subscription: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read subscription response: %v", err)
	}

	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("GET subscription status = %d, want 200; body=%s", response.StatusCode, body)
	}
	if contentType := response.Header.Get(fiber.HeaderContentType); contentType != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", contentType)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		t.Fatalf("decode subscription body: %v; body=%q", err, body)
	}
	wantEntry := fmt.Sprintf("@%s:%d", fixture.entryHost, fixture.entryPort)
	if !strings.Contains(string(decoded), wantEntry) {
		t.Errorf("decoded subscription does not advertise %q: %s", wantEntry, decoded)
	}
	if strings.Contains(string(decoded), fixture.exitAddress) {
		t.Errorf("decoded subscription leaks exit address %q: %s", fixture.exitAddress, decoded)
	}
}

func TestGetSubscriptionDistinguishesMissingSubscriptionFromDatabaseFailureThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	handler := NewSubscriptionHandler(pool, 3600)
	app := fiber.New()
	app.Get("/sub/:sub_token/:device_id", handler.GetSubscription)
	t.Cleanup(func() { _ = app.Shutdown() })

	missingRequest := httptest.NewRequest(fiber.MethodGet, "/sub/missing/"+crypto.NewUUID(), nil)
	missingResponse, err := app.Test(missingRequest)
	if err != nil {
		t.Fatalf("missing subscription request: %v", err)
	}
	if err := missingResponse.Body.Close(); err != nil {
		t.Fatalf("close missing subscription response: %v", err)
	}
	if missingResponse.StatusCode != fiber.StatusNotFound {
		t.Fatalf("missing subscription status = %d, want 404", missingResponse.StatusCode)
	}

	pool.Close()
	failureRequest := httptest.NewRequest(fiber.MethodGet, "/sub/token/device", nil)
	failureResponse, err := app.Test(failureRequest)
	if err != nil {
		t.Fatalf("database failure subscription request: %v", err)
	}
	defer func() { _ = failureResponse.Body.Close() }()
	if failureResponse.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("database failure status = %d, want 500", failureResponse.StatusCode)
	}
}
