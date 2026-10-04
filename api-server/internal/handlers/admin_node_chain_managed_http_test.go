package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func seedChainManagedHostname(t *testing.T, fixture nodeChainHTTPFixture, nodeID, hostname string) {
	t.Helper()
	if _, err := fixture.pool.Exec(context.Background(),
		`INSERT INTO managed_entry_dns (owner_node_id, node_id, hostname) VALUES ($1, $1, NULLIF($2, ''))`, nodeID, hostname,
	); err != nil {
		t.Fatalf("seed managed Entry hostname: %v", err)
	}
	t.Cleanup(func() {
		if _, err := fixture.pool.Exec(context.Background(), `DELETE FROM managed_entry_dns WHERE owner_node_id = $1`, nodeID); err != nil {
			t.Errorf("clean managed Entry hostname: %v", err)
		}
	})
}

func TestCreateNodeChainDerivesManagedHostWhenOmittedOrAssertedThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	entryID := seedChainNode(t, fixture.pool, "derived-entry-"+suffix, "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "derived-exit-"+suffix, "exit", 443)
	seedChainManagedHostname(t, fixture, entryID, "managed.example.test")

	for _, supplied := range []string{"", " managed.example.test "} {
		// Given a live managed hostname and an optional deprecated assertion.
		// When a new explicit Entry chain is created.
		status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
			EntryNodeID: &entryID, EntryHost: supplied, ExitNodeID: exitID, Transport: "tcp",
		}))
		// Then the stored endpoint is the managed hostname, even on a second port.
		if status != fiber.StatusCreated {
			t.Fatalf("create status = %d, body=%s", status, body)
		}
		var chain nodeChainResponse
		if err := json.Unmarshal(body, &chain); err != nil {
			t.Fatal(err)
		}
		if chain.EntryHost != "managed.example.test" || chain.EntryPort == nil {
			t.Fatalf("created endpoint = %+v", chain)
		}
	}
}

func TestCreateNodeChainRejectsManagedHostMismatchWithoutWritingThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	entryID := seedChainNode(t, fixture.pool, "mismatch-entry-"+suffix, "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "mismatch-exit-"+suffix, "exit", 443)
	seedChainManagedHostname(t, fixture, entryID, "managed.example.test")

	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		EntryNodeID: &entryID, EntryHost: "other.example.test", ExitNodeID: exitID,
	}))
	if status != fiber.StatusBadRequest || string(body) != `{"error":"entry_host does not match managed Entry hostname"}` {
		t.Fatalf("mismatch status=%d body=%s", status, body)
	}
	var count int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT count(*) FROM node_chains WHERE entry_node_id=$1`, entryID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected chain count=%d err=%v", count, err)
	}
}

func TestCreateNodeChainRejectsMissingOrNullManagedHostWithoutIPFallbackThroughHTTP(t *testing.T) {
	for _, withRow := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "null"}[withRow], func(t *testing.T) {
			fixture := newNodeChainHTTPFixture(t)
			suffix := crypto.NewUUID()
			entryID := seedChainNode(t, fixture.pool, "unavailable-entry-"+suffix, "relay", 443)
			exitID := seedChainNode(t, fixture.pool, "unavailable-exit-"+suffix, "exit", 443)
			if withRow {
				seedChainManagedHostname(t, fixture, entryID, "")
			}
			status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
				EntryNodeID: &entryID, EntryHost: "203.0.113.50", ExitNodeID: exitID,
			}))
			if status != fiber.StatusConflict || string(body) != `{"error":"managed Entry hostname unavailable"}` {
				t.Fatalf("unavailable status=%d body=%s", status, body)
			}
		})
	}
}

func TestUpdateNodeChainPreservesLegacyExplicitHostWhenManagedHostChangesThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	entryID := seedChainNode(t, fixture.pool, "legacy-entry-"+suffix, "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "legacy-exit-"+suffix, "exit", 443)
	seedChainManagedHostname(t, fixture, entryID, "new.example.test")
	var id string
	if err := fixture.pool.QueryRow(context.Background(),
		`INSERT INTO node_chains (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1,$2,'legacy.example.test',29980,$3,443,'tcp') RETURNING id::text`, "legacy-"+suffix, entryID, exitID,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"name":"renamed"}`, `{"entry_host":" new.example.test ","priority":8}`} {
		status, body := fixture.request(fiber.MethodPatch, "/admin/node-chains/"+id, payload)
		if status != fiber.StatusOK {
			t.Fatalf("update status=%d body=%s", status, body)
		}
		var chain nodeChainResponse
		if err := json.Unmarshal(body, &chain); err != nil || chain.EntryHost != "legacy.example.test" {
			t.Fatalf("legacy endpoint=%+v err=%v", chain, err)
		}
	}
	status, body := fixture.request(fiber.MethodPatch, "/admin/node-chains/"+id, `{"entry_host":"legacy.example.test","priority":9}`)
	if status != fiber.StatusBadRequest || string(body) != `{"error":"entry_host does not match managed Entry hostname"}` {
		t.Fatalf("assertion status=%d body=%s", status, body)
	}
	var priority int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT priority FROM node_chains WHERE id=$1`, id).Scan(&priority); err != nil || priority != 8 {
		t.Fatalf("priority after rejected assertion=%d err=%v", priority, err)
	}
}

func TestUpdateNodeChainRejectsUnavailableManagedHostWithoutWritingThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	entryID := seedChainNode(t, fixture.pool, "missing-update-entry-"+suffix, "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "missing-update-exit-"+suffix, "exit", 443)
	var id string
	if err := fixture.pool.QueryRow(context.Background(),
		`INSERT INTO node_chains (name, entry_node_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1,$2,'legacy.example.test',29981,$3,443,'tcp') RETURNING id::text`, "missing-update-"+suffix, entryID, exitID,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	status, body := fixture.request(fiber.MethodPatch, "/admin/node-chains/"+id, `{"entry_host":"legacy.example.test","priority":9}`)
	if status != fiber.StatusConflict || string(body) != `{"error":"managed Entry hostname unavailable"}` {
		t.Fatalf("unavailable status=%d body=%s", status, body)
	}
	var priority int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT priority FROM node_chains WHERE id=$1`, id).Scan(&priority); err != nil || priority != 0 {
		t.Fatalf("priority after rejected assertion=%d err=%v", priority, err)
	}
	status, body = fixture.request(fiber.MethodPatch, "/admin/node-chains/"+id, `{"enabled":false}`)
	if status != fiber.StatusOK {
		t.Fatalf("presentation-only update status=%d body=%s", status, body)
	}
	var chain nodeChainResponse
	if err := json.Unmarshal(body, &chain); err != nil || chain.EntryHost != "legacy.example.test" {
		t.Fatalf("legacy endpoint=%+v err=%v", chain, err)
	}
}

func TestUpdateNodeChainRetainsLegacyPoolEndpointThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	entryID := seedChainNode(t, fixture.pool, "pool-update-entry-"+suffix, "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "pool-update-exit-"+suffix, "exit", 443)
	poolID := seedChainGroup(t, fixture.pool, "pool-update-"+suffix)
	fixture.addNodeToGroup(poolID, entryID)
	var id string
	if err := fixture.pool.QueryRow(context.Background(),
		`INSERT INTO node_chains (name, relay_pool_id, entry_host, entry_port, exit_node_id, exit_port, transport)
		 VALUES ($1,$2,'legacy.example.test',29982,$3,443,'tcp') RETURNING id::text`, "pool-update-"+suffix, poolID, exitID,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	status, body := fixture.request(fiber.MethodPatch, "/admin/node-chains/"+id, `{"entry_host":" pool.example.test "}`)
	if status != fiber.StatusOK {
		t.Fatalf("legacy pool update status=%d body=%s", status, body)
	}
	var chain nodeChainResponse
	if err := json.Unmarshal(body, &chain); err != nil || chain.EntryHost != "pool.example.test" || chain.RelayPoolID == nil || *chain.RelayPoolID != poolID {
		t.Fatalf("pool endpoint=%+v err=%v", chain, err)
	}
}

func TestCreateNodeChainRejectsDirectEndpointAndMissingEntryThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	suffix := crypto.NewUUID()
	exitID := seedChainNode(t, fixture.pool, "direct-validation-exit-"+suffix, "exit", 443)
	for _, request := range []createNodeChainRequest{
		{ExitNodeID: exitID, EntryHost: "host.example.test"},
		{ExitNodeID: exitID, EntryPort: 29983},
	} {
		status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, request))
		if status != fiber.StatusBadRequest {
			t.Fatalf("direct endpoint status=%d body=%s", status, body)
		}
	}
	missingID := crypto.NewUUID()
	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains", createChainBody(t, createNodeChainRequest{
		ExitNodeID: exitID, EntryNodeID: &missingID,
	}))
	if status != fiber.StatusNotFound || string(body) != `{"error":"entry node not found"}` {
		t.Fatalf("missing entry status=%d body=%s", status, body)
	}
}

func TestCreateNodeChainReadsCommittedManagedHostAfterNodeLockThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	ctx := context.Background()
	suffix := crypto.NewUUID()
	entryID := seedChainNode(t, fixture.pool, "concurrent-host-entry-"+suffix, "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "concurrent-host-exit-"+suffix, "exit", 443)
	seedChainManagedHostname(t, fixture, entryID, "old.example.test")
	blocker, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Rollback(ctx) })
	var locked string
	if err := blocker.QueryRow(ctx, `SELECT id::text FROM nodes WHERE id=$1 FOR UPDATE`, entryID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	type result struct {
		status int
		body   []byte
		err    error
	}
	results := make(chan result, 1)
	payload := createChainBody(t, createNodeChainRequest{EntryNodeID: &entryID, ExitNodeID: exitID, EntryHost: "new.example.test"})
	go func() {
		request := httptest.NewRequest(fiber.MethodPost, "/admin/node-chains", bytes.NewBufferString(payload))
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		response, err := fixture.app.Test(request)
		if err != nil {
			results <- result{err: err}
			return
		}
		body, err := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err == nil {
			err = closeErr
		}
		results <- result{status: response.StatusCode, body: body, err: err}
	}()
	waitForBlockedQuery(t, fixture.pool, "FROM nodes", "FOR UPDATE")
	if _, err := fixture.pool.Exec(ctx, `UPDATE managed_entry_dns SET hostname='new.example.test' WHERE node_id=$1`, entryID); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	created := <-results
	if created.err != nil || created.status != fiber.StatusCreated {
		t.Fatalf("concurrent hostname status=%d err=%v body=%s", created.status, created.err, created.body)
	}
	var chain nodeChainResponse
	if err := json.Unmarshal(created.body, &chain); err != nil || chain.EntryHost != "new.example.test" {
		t.Fatalf("committed endpoint=%+v err=%v", chain, err)
	}
}
