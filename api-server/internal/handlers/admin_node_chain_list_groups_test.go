package handlers

import (
	"encoding/json"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestListNodeChainsIncludesSubscriptionGroupIDs(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	fixture.app.Get("/admin/node-chains", NewAdminNodeChainHandler(fixture.pool).List)
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "chain-list-exit-"+suffix, "exit", 443)
	relayID := seedChainNode(t, fixture.pool, "chain-list-relay-"+suffix, "relay", 443)
	seedChainManagedHostname(t, fixture, relayID, "relay.example.test")
	accessID := seedChainGroup(t, fixture.pool, "chain-list-access-"+suffix)

	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		Name: "chain-list-" + suffix, EntryNodeID: &relayID, EntryHost: "relay.example.test",
		ExitNodeID: exitID, ExitPort: 443, Transport: "tcp",
	}))
	if status != fiber.StatusCreated {
		t.Fatalf("create chain status=%d body=%s", status, body)
	}
	var chain nodeChainResponse
	if err := json.Unmarshal(body, &chain); err != nil {
		t.Fatal(err)
	}
	status, body = fixture.request(fiber.MethodPut, "/admin/node-chains/"+chain.ID+"/groups", `{"group_ids":["`+accessID+`"]}`)
	if status != fiber.StatusOK {
		t.Fatalf("set chain groups status=%d body=%s", status, body)
	}
	status, body = fixture.request(fiber.MethodGet, "/admin/node-chains", "")
	if status != fiber.StatusOK {
		t.Fatalf("list chains status=%d body=%s", status, body)
	}
	var listed []nodeChainResponse
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	for _, item := range listed {
		if item.ID == chain.ID {
			if len(item.GroupIDs) != 1 || item.GroupIDs[0] != accessID {
				t.Fatalf("group_ids=%v, want [%s]", item.GroupIDs, accessID)
			}
			return
		}
	}
	t.Fatalf("created chain %s absent from list", chain.ID)
}
