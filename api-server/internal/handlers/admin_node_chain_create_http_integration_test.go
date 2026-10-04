package handlers

import (
	"context"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestCreateNodeChainReturnsSafeConflictForExistingDirectChainThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-direct-conflict-"+suffix, "exit", 443)
	if _, err := fixture.pool.Exec(context.Background(),
		`INSERT INTO node_chains (name, exit_node_id, exit_port, transport)
		 VALUES ($1, $2, 443, 'tcp_udp')`,
		"existing-direct-"+suffix, exitID,
	); err != nil {
		t.Fatalf("seed direct chain: %v", err)
	}

	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		Name: "duplicate-direct-" + suffix, ExitNodeID: exitID, ExitPort: 443, Transport: "tcp_udp",
	}))

	if status != fiber.StatusConflict {
		t.Fatalf("duplicate direct chain status = %d, want 409; body=%s", status, body)
	}
	if string(body) != `{"error":"an exit node already has a direct chain"}` {
		t.Errorf("duplicate direct chain body = %s, want stable client-safe error", body)
	}
}
