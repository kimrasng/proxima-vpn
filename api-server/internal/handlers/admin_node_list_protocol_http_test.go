package handlers

import (
	"context"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// The node list names the one protocol each node serves, so an operator can
// tell what a node hosts without opening its inbounds page.
func TestListNodesReportsInboundProtocol(t *testing.T) {
	// Given an exit with a Reality inbound and a relay with none
	fixture := endpointHTTPFixture(t)
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "proto-exit-"+suffix, "exit", 8443)
	relayID := seedChainNode(t, fixture.pool, "proto-relay-"+suffix, "relay", 443)
	if _, err := fixture.pool.Exec(context.Background(),
		`INSERT INTO inbounds (node_id, protocol, port, tag) VALUES ($1, 'vless_reality', 8443, 'vless-in')`,
		exitID,
	); err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	// When the node list is requested
	status, body := fixture.request(fiber.MethodGet, "/admin/nodes", "")
	if status != fiber.StatusOK {
		t.Fatalf("list nodes status=%d body=%s", status, body)
	}

	// Then the exit carries its protocol and port, and the relay carries neither
	exit := endpointListItem(t, body, exitID)
	if exit["inbound_protocol"] != "vless_reality" || exit["inbound_port"] != float64(8443) || exit["inbound_enabled"] != true {
		t.Fatalf("exit inbound = %v/%v/%v, want vless_reality/8443/true",
			exit["inbound_protocol"], exit["inbound_port"], exit["inbound_enabled"])
	}
	relay := endpointListItem(t, body, relayID)
	for _, key := range []string{"inbound_protocol", "inbound_port", "inbound_enabled"} {
		if _, present := relay[key]; present {
			t.Fatalf("relay without inbound has %s=%v", key, relay[key])
		}
	}
}
