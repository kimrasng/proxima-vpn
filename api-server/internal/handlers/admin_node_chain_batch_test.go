package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestBatchNodeChainsRejectsInvalidRequestBeforeDatabase(t *testing.T) {
	entryID, exitID := crypto.NewUUID(), crypto.NewUUID()
	validRoute := `{"exit_node_id":"` + exitID + `"}`
	for _, test := range []struct{ name, body string }{
		{"invalid JSON", `{`},
		{"missing Entry", `{"routes":[` + validRoute + `]}`},
		{"no routes", `{"entry_node_id":"` + entryID + `","routes":[]}`},
		{"invalid exit", `{"entry_node_id":"` + entryID + `","routes":[{"exit_node_id":"no"}]}`},
		{"same nodes", `{"entry_node_id":"` + entryID + `","routes":[{"exit_node_id":"` + strings.ToUpper(entryID) + `"}]}`},
		{"invalid transport", `{"entry_node_id":"` + entryID + `","routes":[{"exit_node_id":"` + exitID + `","transport":"sctp"}]}`},
		{"invalid port", `{"entry_node_id":"` + entryID + `","routes":[{"exit_node_id":"` + exitID + `","entry_port":70000}]}`},
		{"duplicate groups", `{"entry_node_id":"` + entryID + `","routes":[` + validRoute + `],"group_ids":["` + exitID + `","` + strings.ToUpper(exitID) + `"]}`},
		{"invalid plans", `{"entry_node_id":"` + entryID + `","routes":[` + validRoute + `],"plan_ids":["invalid"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New()
			app.Post("/batch", NewAdminNodeChainHandler(nil).Batch)
			request := httptest.NewRequest(fiber.MethodPost, "/batch", bytes.NewBufferString(test.body))
			request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != fiber.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
		})
	}
}

func TestBatchNodeChainsValidatesDefaultsAndKeepsOrder(t *testing.T) {
	entryID, firstID, secondID := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	req := batchNodeChainsRequest{EntryNodeID: entryID, Routes: []batchNodeChainRoute{
		{ExitNodeID: firstID}, {ExitNodeID: secondID, Transport: "udp", ExitPort: 8443},
	}}
	requests, transports, err := validateBatchNodeChains(&req)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0].ExitNodeID != firstID || requests[1].ExitNodeID != secondID || transports[0] != "tcp" || transports[1] != "udp" {
		t.Fatalf("requests=%+v transports=%v", requests, transports)
	}
}

func TestBatchNodeChainsPreviewAndCreateAtomicallyThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	ctx := t.Context()
	suffix := crypto.NewUUID()
	entryID := seedChainNode(t, fixture.pool, "batch-entry-"+suffix, "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "batch-exit-"+suffix, "exit", 8443)
	oldExitID := seedChainNode(t, fixture.pool, "batch-old-exit-"+suffix, "exit", 443)
	seedChainManagedHostname(t, fixture, entryID, "batch.example.test")
	groupID := seedChainGroup(t, fixture.pool, "batch-shared-"+suffix)
	firstPlan := seedRoutePlan(t, fixture, groupID, 0)
	otherPlan := seedRoutePlan(t, fixture, groupID, 0)
	oldChain := seedRouteDirectChain(t, fixture, oldExitID)
	if _, err := fixture.pool.Exec(ctx, `INSERT INTO node_group_chains (node_group_id, chain_id) VALUES ($1,$2)`, groupID, oldChain); err != nil {
		t.Fatal(err)
	}
	fixture.addNodeToGroup(groupID, oldExitID)
	req := batchNodeChainsRequest{EntryNodeID: entryID, PlanIDs: []string{firstPlan}, Preview: true, Routes: []batchNodeChainRoute{
		{Name: "first", ExitNodeID: exitID}, {Name: "second", ExitNodeID: exitID, Transport: "tcp_udp"},
	}}
	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains/batch", routeRequestBody(t, req))
	if status != fiber.StatusOK {
		t.Fatalf("preview status=%d body=%s", status, body)
	}
	var preview batchNodeChainsResponse
	if err := json.Unmarshal(body, &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Preview || len(preview.Routes) != 2 || preview.Routes[0].Name != "first" || preview.Routes[1].Name != "second" || *preview.Routes[0].EntryPort == *preview.Routes[1].EntryPort || preview.Routes[0].EntryHost != "batch.example.test" || preview.Routes[0].ExitPort != 8443 {
		t.Fatalf("preview=%+v", preview)
	}
	var count int
	var originalGroup string
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM node_chains WHERE entry_node_id=$1`, entryID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("preview chain count=%d err=%v", count, err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT node_group_id::text FROM plans WHERE id=$1`, firstPlan).Scan(&originalGroup); err != nil || originalGroup != groupID {
		t.Fatalf("preview plan group=%s err=%v", originalGroup, err)
	}
	req.Preview = false
	for i := range req.Routes {
		req.Routes[i].EntryPort = *preview.Routes[i].EntryPort
	}
	status, body = fixture.request(fiber.MethodPost, "/admin/node-chains/batch", routeRequestBody(t, req))
	if status != fiber.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, body)
	}
	var created batchNodeChainsResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.Preview || len(created.Routes) != 2 || created.Routes[0].Name != "first" || created.Routes[1].Name != "second" {
		t.Fatalf("created=%+v", created)
	}
	var isolatedGroup string
	if err := fixture.pool.QueryRow(ctx, `SELECT node_group_id::text FROM plans WHERE id=$1`, firstPlan).Scan(&isolatedGroup); err != nil || isolatedGroup == groupID {
		t.Fatalf("isolated group=%s err=%v", isolatedGroup, err)
	}
	cleanupRouteGroup(t, fixture, isolatedGroup, firstPlan)
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM node_group_chains WHERE node_group_id=$1`, isolatedGroup).Scan(&count); err != nil || count != 3 {
		t.Fatalf("selected plan chain count=%d err=%v", count, err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM node_group_nodes WHERE node_group_id=$1`, isolatedGroup).Scan(&count); err != nil || count != 2 {
		t.Fatalf("selected plan node count=%d err=%v", count, err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT node_group_id::text FROM plans WHERE id=$1`, otherPlan).Scan(&originalGroup); err != nil || originalGroup != groupID {
		t.Fatalf("other plan group=%s err=%v", originalGroup, err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM node_group_chains WHERE node_group_id=$1`, groupID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("other plan chain count=%d err=%v", count, err)
	}
}

func TestBatchNodeChainsRollsBackWholeBatchOnLaterConflictThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	entryID := seedChainNode(t, fixture.pool, "batch-conflict-entry-"+crypto.NewUUID(), "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "batch-conflict-exit-"+crypto.NewUUID(), "exit", 443)
	seedChainManagedHostname(t, fixture, entryID, "batch-conflict.example.test")
	groupID := seedChainGroup(t, fixture.pool, "batch-conflict-group-"+crypto.NewUUID())
	planID := seedRoutePlan(t, fixture, groupID, 0)
	seedRoutePlan(t, fixture, groupID, 0) // Force isolation so rollback must undo it.
	req := batchNodeChainsRequest{EntryNodeID: entryID, PlanIDs: []string{planID}, Routes: []batchNodeChainRoute{
		{ExitNodeID: exitID, EntryPort: 29987, Transport: "tcp"},
		{ExitNodeID: exitID, EntryPort: 29987, Transport: "tcp_udp"},
	}}
	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains/batch", routeRequestBody(t, req))
	if status != fiber.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", status, body)
	}
	var count int
	if err := fixture.pool.QueryRow(t.Context(), `SELECT count(*) FROM node_chains WHERE entry_node_id=$1`, entryID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("chain count=%d err=%v", count, err)
	}
	var actualGroup string
	if err := fixture.pool.QueryRow(t.Context(), `SELECT node_group_id::text FROM plans WHERE id=$1`, planID).Scan(&actualGroup); err != nil || actualGroup != groupID {
		t.Fatalf("plan group=%s err=%v", actualGroup, err)
	}
}

func TestBatchNodeChainsRollsBackEarlierWritesWhenLaterExitIsInvalidThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	entryID := seedChainNode(t, fixture.pool, "batch-rollback-entry-"+crypto.NewUUID(), "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "batch-rollback-exit-"+crypto.NewUUID(), "exit", 443)
	invalidExitID := seedChainNode(t, fixture.pool, "batch-rollback-invalid-"+crypto.NewUUID(), "relay", 443)
	seedChainManagedHostname(t, fixture, entryID, "batch-rollback.example.test")
	groupID := seedChainGroup(t, fixture.pool, "batch-rollback-group-"+crypto.NewUUID())
	planID := seedRoutePlan(t, fixture, groupID, 0)
	seedRoutePlan(t, fixture, groupID, 0)
	req := batchNodeChainsRequest{EntryNodeID: entryID, PlanIDs: []string{planID}, Routes: []batchNodeChainRoute{
		{ExitNodeID: exitID}, {ExitNodeID: invalidExitID},
	}}
	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains/batch", routeRequestBody(t, req))
	if status != fiber.StatusBadRequest {
		t.Fatalf("later invalid exit status=%d body=%s", status, body)
	}
	var count int
	if err := fixture.pool.QueryRow(t.Context(), `SELECT count(*) FROM node_chains WHERE entry_node_id=$1`, entryID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back chain count=%d err=%v", count, err)
	}
	if err := fixture.pool.QueryRow(t.Context(), `SELECT count(*) FROM node_group_nodes WHERE node_group_id=$1`, groupID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back node count=%d err=%v", count, err)
	}
	var actualGroup string
	if err := fixture.pool.QueryRow(t.Context(), `SELECT node_group_id::text FROM plans WHERE id=$1`, planID).Scan(&actualGroup); err != nil || actualGroup != groupID {
		t.Fatalf("rolled back plan group=%s err=%v", actualGroup, err)
	}
}

func TestBatchNodeChainsAllowsDisjointTransportClaimsThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	entryID := seedChainNode(t, fixture.pool, "batch-transport-entry-"+crypto.NewUUID(), "relay", 443)
	exitID := seedChainNode(t, fixture.pool, "batch-transport-exit-"+crypto.NewUUID(), "exit", 443)
	seedChainManagedHostname(t, fixture, entryID, "batch-transport.example.test")
	groupID := seedChainGroup(t, fixture.pool, "batch-transport-group-"+crypto.NewUUID())
	req := batchNodeChainsRequest{EntryNodeID: entryID, GroupIDs: []string{groupID}, Routes: []batchNodeChainRoute{
		{ExitNodeID: exitID, EntryPort: 29986, Transport: "tcp"},
		{ExitNodeID: exitID, EntryPort: 29986, Transport: "udp"},
	}}
	status, body := fixture.request(fiber.MethodPost, "/admin/node-chains/batch", routeRequestBody(t, req))
	if status != fiber.StatusCreated {
		t.Fatalf("disjoint transports status=%d body=%s", status, body)
	}
	var count int
	if err := fixture.pool.QueryRow(t.Context(), `SELECT count(*) FROM node_group_chains WHERE node_group_id=$1`, groupID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("group route count=%d err=%v", count, err)
	}
	if err := fixture.pool.QueryRow(t.Context(), `SELECT count(*) FROM node_group_nodes WHERE node_group_id=$1 AND node_id=$2`, groupID, exitID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("group exit count=%d err=%v", count, err)
	}
}

func TestBatchNodeChainsSerializesAutomaticAllocationAcrossEntriesThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	exitID := seedChainNode(t, fixture.pool, "batch-race-exit-"+crypto.NewUUID(), "exit", 443)
	requests := make([]batchNodeChainsRequest, 2)
	for i := range requests {
		entryID := seedChainNode(t, fixture.pool, "batch-race-entry-"+crypto.NewUUID(), "relay", 443)
		seedChainManagedHostname(t, fixture, entryID, "race-"+entryID+".example.test")
		requests[i] = batchNodeChainsRequest{EntryNodeID: entryID, Routes: []batchNodeChainRoute{{ExitNodeID: exitID, Transport: "tcp_udp"}}}
	}
	type result struct {
		status int
		body   []byte
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, request := range requests {
		payload := routeRequestBody(t, request)
		go func() {
			<-start
			status, body := fixture.request(fiber.MethodPost, "/admin/node-chains/batch", payload)
			results <- result{status: status, body: body}
		}()
	}
	close(start)
	ports := make(map[int]bool)
	for range requests {
		result := <-results
		if result.status != fiber.StatusCreated {
			t.Fatalf("concurrent automatic allocation status=%d body=%s", result.status, result.body)
		}
		var created batchNodeChainsResponse
		if err := json.Unmarshal(result.body, &created); err != nil || len(created.Routes) != 1 || created.Routes[0].EntryPort == nil {
			t.Fatalf("concurrent response=%+v err=%v", created, err)
		}
		port := *created.Routes[0].EntryPort
		if ports[port] {
			t.Fatalf("both Entries claimed %d/tcp_udp", port)
		}
		ports[port] = true
	}
}

func routeRequestBody(t *testing.T, request any) string {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
