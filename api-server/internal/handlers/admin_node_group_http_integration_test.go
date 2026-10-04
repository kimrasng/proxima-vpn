package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestSetNodesPreservesRelayedChainAttachmentThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, pool, "set-nodes-relay-exit-"+suffix, "exit", 443)
	groupID := seedChainGroup(t, pool, "set-nodes-plan-group-"+suffix)
	relayPoolID := seedChainGroup(t, pool, "set-nodes-relay-pool-"+suffix)

	var chainID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO node_chains
		   (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 SELECT $1, $2, $3, candidate, $4, 443, 'tcp'
		 FROM generate_series(30000, 60000) AS candidate
		 WHERE NOT EXISTS (
		   SELECT 1 FROM node_chains
		   WHERE entry_port = candidate AND transport IN ('tcp', 'tcp_udp')
		 )
		 ORDER BY candidate
		 LIMIT 1
		 RETURNING id::text`,
		"set-nodes-relayed-"+suffix, relayPoolID, "relay-"+suffix+".example.test", exitID,
	).Scan(&chainID); err != nil {
		t.Fatalf("seed relayed chain: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_chains (node_group_id, chain_id) VALUES ($1, $2)`,
		groupID, chainID,
	); err != nil {
		t.Fatalf("attach relayed chain: %v", err)
	}

	app := fiber.New()
	app.Put("/admin/node-groups/:id/nodes", NewAdminNodeGroupHandler(pool).SetNodes)
	t.Cleanup(func() {
		if err := app.Shutdown(); err != nil {
			t.Errorf("shut down Fiber app: %v", err)
		}
	})

	request := httptest.NewRequest(
		fiber.MethodPut,
		"/admin/node-groups/"+groupID+"/nodes",
		bytes.NewBufferString(`{"node_ids":[]}`),
	)
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("set nodes request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("set nodes status = %d, want 200", response.StatusCode)
	}

	var attachments int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM node_group_chains WHERE node_group_id = $1 AND chain_id = $2`,
		groupID, chainID,
	).Scan(&attachments); err != nil {
		t.Fatalf("count relayed attachment: %v", err)
	}
	if attachments != 1 {
		t.Errorf("relayed attachment count = %d, want 1", attachments)
	}
}

func TestSetNodesRejectsInvalidMembershipForReferencedRelayPoolThroughHTTP(t *testing.T) {
	testCases := []struct {
		name    string
		newRole string
	}{
		{name: "empty"},
		{name: "non-forwarding member", newRole: "exit"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := chainTestDB(t)
			ctx := context.Background()
			suffix := crypto.NewUUID()
			exitID := seedChainNode(t, pool, "set-nodes-invariant-exit-"+suffix, "exit", 443)
			relayID := seedChainNode(t, pool, "set-nodes-invariant-relay-"+suffix, "relay", 443)
			poolID := seedChainGroup(t, pool, "set-nodes-invariant-pool-"+suffix)
			if _, err := pool.Exec(ctx,
				`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`, poolID, relayID,
			); err != nil {
				t.Fatalf("seed relay membership: %v", err)
			}
			if _, err := pool.Exec(ctx,
				`INSERT INTO node_chains
				   (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport)
				 VALUES ($1, $2, 'relay.example.test', 29993, $3, 443, 'tcp')`,
				"set-nodes-invariant-"+suffix, poolID, exitID,
			); err != nil {
				t.Fatalf("seed referenced chain: %v", err)
			}
			nodeIDs := `[]`
			if testCase.newRole != "" {
				candidateID := seedChainNode(t, pool, "set-nodes-invariant-candidate-"+suffix, testCase.newRole, 443)
				nodeIDs = `["` + candidateID + `"]`
			}

			app := fiber.New()
			app.Put("/admin/node-groups/:id/nodes", NewAdminNodeGroupHandler(pool).SetNodes)
			t.Cleanup(func() { _ = app.Shutdown() })
			request := httptest.NewRequest(fiber.MethodPut, "/admin/node-groups/"+poolID+"/nodes", bytes.NewBufferString(`{"node_ids":`+nodeIDs+`}`))
			request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("set nodes request: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read set nodes response: %v", err)
			}

			if response.StatusCode != fiber.StatusConflict {
				t.Fatalf("invalid referenced pool status = %d, want 409; body=%s", response.StatusCode, body)
			}
			var members int
			if err := pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM node_group_nodes WHERE node_group_id = $1 AND node_id = $2`, poolID, relayID,
			).Scan(&members); err != nil {
				t.Fatalf("count original relay membership: %v", err)
			}
			if members != 1 {
				t.Errorf("original relay membership count = %d, want 1", members)
			}
		})
	}
}

func TestUpdateNodeRejectsDemotingMemberOfReferencedRelayPoolThroughHTTP(t *testing.T) {
	pool := chainTestDB(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, pool, "role-invariant-exit-"+suffix, "exit", 443)
	relayID := seedChainNode(t, pool, "role-invariant-relay-"+suffix, "relay", 443)
	poolID := seedChainGroup(t, pool, "role-invariant-pool-"+suffix)
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`, poolID, relayID,
	); err != nil {
		t.Fatalf("seed relay membership: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO node_chains
		   (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 'relay.example.test', 29995, $3, 443, 'tcp')`,
		"role-invariant-"+suffix, poolID, exitID,
	); err != nil {
		t.Fatalf("seed referenced chain: %v", err)
	}

	app := fiber.New()
	app.Put("/admin/nodes/:id", NewAdminNodeHandler(pool, nil, "").UpdateNode)
	t.Cleanup(func() { _ = app.Shutdown() })
	request := httptest.NewRequest(fiber.MethodPut, "/admin/nodes/"+relayID, bytes.NewBufferString(`{"role":"exit"}`))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("update node role: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read update role response: %v", err)
	}

	if response.StatusCode != fiber.StatusConflict {
		t.Fatalf("relay demotion status = %d, want 409; body=%s", response.StatusCode, body)
	}
	var role string
	if err := pool.QueryRow(ctx, `SELECT role FROM nodes WHERE id = $1`, relayID).Scan(&role); err != nil {
		t.Fatalf("read role after rejected demotion: %v", err)
	}
	if role != "relay" {
		t.Errorf("role after rejected demotion = %q, want relay", role)
	}
}
