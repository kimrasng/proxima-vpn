package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func seedRoutePlan(t *testing.T, fixture nodeChainHTTPFixture, groupID string, speed int) string {
	t.Helper()
	var id string
	if err := fixture.pool.QueryRow(t.Context(),
		`INSERT INTO plans (name, duration_days, max_devices, node_group_id, speed_limit)
		 VALUES ($1, 30, 1, $2, NULLIF($3, 0)) RETURNING id::text`, "route-plan-"+crypto.NewUUID(), groupID, speed,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = fixture.pool.Exec(context.Background(), `DELETE FROM plans WHERE id=$1`, id) })
	return id
}

func cleanupRouteGroup(t *testing.T, fixture nodeChainHTTPFixture, groupID, planID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), `DELETE FROM plans WHERE id=$1`, planID)
		_, _ = fixture.pool.Exec(context.Background(), `DELETE FROM node_groups WHERE id=$1`, groupID)
	})
}

func seedRouteDirectChain(t *testing.T, fixture nodeChainHTTPFixture, exitID string) string {
	t.Helper()
	var id string
	if err := fixture.pool.QueryRow(t.Context(),
		`INSERT INTO node_chains (name, exit_node_id, exit_port, transport) VALUES ($1,$2,443,'tcp_udp') RETURNING id::text`,
		"route-direct-"+crypto.NewUUID(), exitID,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPlanRoutesRejectsInvalidSelectionBeforeDatabase(t *testing.T) {
	planID, chainID := crypto.NewUUID(), crypto.NewUUID()
	for _, test := range []struct{ name, path, body string }{
		{"invalid plan", "/plans/bad/routes", `{"chain_ids":[]}`},
		{"missing selection", "/plans/" + planID + "/routes", `{}`},
		{"null selection", "/plans/" + planID + "/routes", `{"chain_ids":null}`},
		{"invalid chain", "/plans/" + planID + "/routes", `{"chain_ids":["invalid"]}`},
		{"duplicate chain", "/plans/" + planID + "/routes", `{"chain_ids":["` + chainID + `","` + strings.ToUpper(chainID) + `"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New()
			app.Put("/plans/:id/routes", NewAdminPlanHandler(nil).SetRoutes)
			request := httptest.NewRequest(fiber.MethodPut, test.path, bytes.NewBufferString(test.body))
			request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != fiber.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
		})
	}
}

func TestPlanRoutesIsolatesSharedAccessAndProvisioningThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	ctx := t.Context()
	groupID := seedChainGroup(t, fixture.pool, "plan-routes-shared-"+crypto.NewUUID())
	oldExit := seedChainNode(t, fixture.pool, "plan-routes-old-"+crypto.NewUUID(), "exit", 443)
	newExit := seedChainNode(t, fixture.pool, "plan-routes-new-"+crypto.NewUUID(), "exit", 443)
	oldChain := seedRouteDirectChain(t, fixture, oldExit)
	selectedChain := seedRouteDirectChain(t, fixture, newExit)
	fixture.addNodeToGroup(groupID, oldExit)
	if _, err := fixture.pool.Exec(ctx, `INSERT INTO node_group_chains (node_group_id, chain_id) VALUES ($1,$2)`, groupID, oldChain); err != nil {
		t.Fatal(err)
	}
	planID := seedRoutePlan(t, fixture, groupID, 100)
	otherPlan := seedRoutePlan(t, fixture, groupID, 0)
	status, body := fixture.request(fiber.MethodGet, "/admin/plans/"+planID+"/routes", "")
	if status != fiber.StatusOK {
		t.Fatalf("get status=%d body=%s", status, body)
	}
	var before planRoutesResponse
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.ChainIDs, []string{oldChain}) || before.SpeedEnforcement != "device_global_v1" || before.SpeedLimit == nil || *before.SpeedLimit != 100 || !strings.Contains(strings.Join(before.Warnings, " "), "device_bandwidth_requires_current_agent_ack") {
		t.Fatalf("initial routes=%+v", before)
	}
	status, body = fixture.request(fiber.MethodPut, "/admin/plans/"+planID+"/routes", `{"chain_ids":["`+selectedChain+`"]}`)
	if status != fiber.StatusOK {
		t.Fatalf("set status=%d body=%s", status, body)
	}
	var updated planRoutesResponse
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.NodeGroupID == groupID || !reflect.DeepEqual(updated.ChainIDs, []string{selectedChain}) {
		t.Fatalf("updated routes=%+v", updated)
	}
	cleanupRouteGroup(t, fixture, updated.NodeGroupID, planID)
	var nodeIDs []string
	if err := fixture.pool.QueryRow(ctx, `SELECT ARRAY(SELECT node_id::text FROM node_group_nodes WHERE node_group_id=$1 ORDER BY node_id)`, updated.NodeGroupID).Scan(&nodeIDs); err != nil || !reflect.DeepEqual(nodeIDs, []string{newExit}) {
		t.Fatalf("selected exit access=%v err=%v", nodeIDs, err)
	}
	status, body = fixture.request(fiber.MethodGet, "/admin/plans/"+otherPlan+"/routes", "")
	if status != fiber.StatusOK {
		t.Fatalf("other get status=%d body=%s", status, body)
	}
	var other planRoutesResponse
	if err := json.Unmarshal(body, &other); err != nil {
		t.Fatal(err)
	}
	if other.NodeGroupID != groupID || !reflect.DeepEqual(other.ChainIDs, []string{oldChain}) || other.SpeedEnforcement != "unlimited" {
		t.Fatalf("other plan changed=%+v", other)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT ARRAY(SELECT node_id::text FROM node_group_nodes WHERE node_group_id=$1 ORDER BY node_id)`, groupID).Scan(&nodeIDs); err != nil || !reflect.DeepEqual(nodeIDs, []string{oldExit}) {
		t.Fatalf("other exit access=%v err=%v", nodeIDs, err)
	}
	status, body = fixture.request(fiber.MethodPut, "/admin/plans/"+planID+"/routes", `{"chain_ids":[]}`)
	if status != fiber.StatusOK {
		t.Fatalf("clear status=%d body=%s", status, body)
	}
	var cleared planRoutesResponse
	if err := json.Unmarshal(body, &cleared); err != nil || cleared.ChainIDs == nil || len(cleared.ChainIDs) != 0 || cleared.NodeGroupID != updated.NodeGroupID {
		t.Fatalf("cleared=%+v err=%v", cleared, err)
	}
	var count int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM node_group_nodes WHERE node_group_id=$1`, cleared.NodeGroupID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cleared exit count=%d err=%v", count, err)
	}
}

// The blocker reproduces SetRoutes' group-before-chain sequence while both
// real HTTP assignment endpoints contend for that group. If SetGroups takes
// the chain first, the blocker's chain read deadlocks/timeouts instead.
func TestPlanRoutesAndSetGroupsUseGroupBeforeChainLockOrderThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	groupID := seedChainGroup(t, fixture.pool, "route-lock-order-"+crypto.NewUUID())
	exitID := seedChainNode(t, fixture.pool, "route-lock-order-exit-"+crypto.NewUUID(), "exit", 443)
	chainID := seedRouteDirectChain(t, fixture, exitID)
	planID := seedRoutePlan(t, fixture, groupID, 0)
	if _, err := fixture.pool.Exec(t.Context(), `INSERT INTO node_group_chains (node_group_id, chain_id) VALUES ($1,$2)`, groupID, chainID); err != nil {
		t.Fatal(err)
	}
	blocker, err := fixture.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	var blockerPID int
	if err := blocker.QueryRow(t.Context(), `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := blocker.QueryRow(t.Context(), `SELECT id::text FROM node_groups WHERE id=$1 FOR UPDATE`, groupID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		status int
		body   []byte
		err    error
	}
	startRequest := func(path, body string) <-chan result {
		results := make(chan result, 1)
		go func() {
			request := httptest.NewRequest(fiber.MethodPut, path, bytes.NewBufferString(body))
			request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := fixture.app.Test(request, 5000)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer response.Body.Close()
			responseBody, err := io.ReadAll(response.Body)
			results <- result{status: response.StatusCode, body: responseBody, err: err}
		}()
		return results
	}
	groupsResult := startRequest("/admin/node-chains/"+chainID+"/groups", `{"group_ids":["`+groupID+`"]}`)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var waiting bool
		if err := fixture.pool.QueryRow(t.Context(),
			`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)) AND query LIKE '%node_groups%')`, blockerPID,
		).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SetGroups did not wait on the blocked route group")
		}
		time.Sleep(10 * time.Millisecond)
	}
	planResult := startRequest("/admin/plans/"+planID+"/routes", `{"chain_ids":["`+chainID+`"]}`)
	lockCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := blocker.QueryRow(lockCtx, `SELECT id::text FROM node_chains WHERE id=$1 FOR KEY SHARE`, chainID).Scan(&lockedID); err != nil {
		t.Fatalf("group-before-chain lock acquisition blocked: %v", err)
	}
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for name, results := range map[string]<-chan result{"SetGroups": groupsResult, "SetRoutes": planResult} {
		select {
		case result := <-results:
			if result.err != nil || result.status != fiber.StatusOK {
				t.Fatalf("%s status=%d body=%s err=%v", name, result.status, result.body, result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not complete after the blocker released its locks", name)
		}
	}
}

func TestPlanRoutesUnknownSelectionPreservesSharedGroupThroughHTTP(t *testing.T) {
	fixture := newNodeChainHTTPFixture(t)
	groupID := seedChainGroup(t, fixture.pool, "plan-routes-unknown-"+crypto.NewUUID())
	planID := seedRoutePlan(t, fixture, groupID, 0)
	seedRoutePlan(t, fixture, groupID, 0)
	status, body := fixture.request(fiber.MethodPut, "/admin/plans/"+planID+"/routes", `{"chain_ids":["`+crypto.NewUUID()+`"]}`)
	if status != fiber.StatusBadRequest {
		t.Fatalf("unknown status=%d body=%s", status, body)
	}
	var actualGroup string
	if err := fixture.pool.QueryRow(t.Context(), `SELECT node_group_id::text FROM plans WHERE id=$1`, planID).Scan(&actualGroup); err != nil || actualGroup != groupID {
		t.Fatalf("unknown selection changed group=%s err=%v", actualGroup, err)
	}
	status, body = fixture.request(fiber.MethodGet, "/admin/plans/"+crypto.NewUUID()+"/routes", "")
	if status != fiber.StatusNotFound {
		t.Fatalf("missing plan status=%d body=%s", status, body)
	}
}
