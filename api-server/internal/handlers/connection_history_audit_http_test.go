package handlers_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func historyClearAudit(t *testing.T, f *historyHTTPFixture) {
	t.Helper()
	if _, err := f.db.Exec(context.Background(), `TRUNCATE activity_logs`); err != nil {
		t.Fatal(err)
	}
}

func historyAuditRows(t *testing.T, f *historyHTTPFixture, count int) []services.Entry {
	t.Helper()
	rows, err := services.NewActivityService(f.db).List(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != count {
		t.Fatalf("activity rows=%d want=%d", len(rows), count)
	}
	var actor string
	if err := f.db.QueryRow(context.Background(), `SELECT id::text FROM admins WHERE email='history-admin@example.test'`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ActorType != "admin" || row.ActorID != actor || row.ActorLabel != "history-admin@example.test" {
			t.Fatalf("wrong audit actor: %+v", row)
		}
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{
			"Target-1.example.test", "198.51.100.3", "private-configured-label",
			"history-key-0", "unused-token", "history-test-secret", f.token,
			"private-query-secret", "password_hash", "credential_ref", "bindings",
		} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("private data copied into activity row: %q", secret)
			}
		}
		if f.binding.CredentialRef != "" && strings.Contains(string(raw), string(f.binding.CredentialRef)) {
			t.Fatal("private credential reference copied into audit")
		}
		t.Logf("AUDIT ROW %s", raw)
	}
	return rows
}

func historyAssertAudit(t *testing.T, row services.Entry, event, targetType, targetID string, detail map[string]any) {
	t.Helper()
	if row.EventType != event || row.TargetType == nil || *row.TargetType != targetType || row.TargetID == nil || *row.TargetID != targetID {
		t.Fatalf("audit event/target=%+v want=%s %s/%s", row, event, targetType, targetID)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(row.Detail, want) {
		t.Fatalf("audit detail=%+v want=%+v", row.Detail, want)
	}
}

func TestConnectionHistoryHTTPAuditBulkControl(t *testing.T) {
	for _, action := range []string{"enable", "disable", "no_op", "conflict", "invalid", "unauthorized"} {
		t.Run(action, func(t *testing.T) {
			// Given: two supported nodes and a real administrator.
			f := historyFixture(t)
			f.enable()
			f.status(f.other)
			_, err := f.svc.Config(context.Background(), f.other)
			if err != nil {
				t.Fatal(err)
			}
			cmd := services.ConnectionHistoryBulkCommand{Enabled: action == "enable" || action == "no_op", Nodes: []services.ConnectionHistoryNodeCAS{
				{ID: nodeprov.HistoryUUID(f.node), ExpectedRevision: 2},
				{ID: nodeprov.HistoryUUID(f.other), ExpectedRevision: 0},
			}}
			want := 200
			switch action {
			case "no_op":
				cmd.Nodes = cmd.Nodes[:1]
			case "conflict":
				cmd.Nodes[1].ExpectedRevision = 99
				want = 409
			case "invalid":
				cmd.Nodes[1].ID = "not-a-uuid"
				want = 400
			case "unauthorized":
				f.token = "invalid-admin-token"
				want = 401
			}
			historyClearAudit(t, f)
			// When: the actual authenticated HTTP bulk operation completes.
			raw := f.request("POST", "/api/v1/admin/nodes/connection-logging", cmd, "", want)
			// Then: only successful commands audit every selected node, not manifests.
			if want != 200 {
				historyAuditRows(t, f, 0)
				return
			}
			var configs map[string]struct {
				Enabled  bool                  `json:"enabled"`
				Revision nodeprov.HistoryU64   `json:"revision"`
				Epoch    *nodeprov.HistoryUUID `json:"collection_epoch"`
			}
			if err := json.Unmarshal(raw, &configs); err != nil {
				t.Fatal(err)
			}
			rows := historyAuditRows(t, f, len(cmd.Nodes))
			seen := map[string]bool{}
			for _, row := range rows {
				if row.TargetID == nil {
					t.Fatal("missing node audit target")
				}
				cfg, ok := configs[*row.TargetID]
				if !ok || seen[*row.TargetID] || row.Severity != "success" {
					t.Fatalf("unexpected node audit: %+v", row)
				}
				seen[*row.TargetID] = true
				historyAssertAudit(t, row, "node.connection_logging_updated", "node", *row.TargetID, map[string]any{
					"enabled": cfg.Enabled, "revision": cfg.Revision, "collection_epoch": cfg.Epoch,
				})
			}
		})
	}
}

func TestConnectionHistoryHTTPAuditReads(t *testing.T) {
	for _, resource := range []string{"flows", "sessions", "aggregate"} {
		t.Run(resource, func(t *testing.T) {
			// Given: real retained destination data and a private binding.
			f := historyFixture(t)
			f.enable()
			flow := f.event(1, "flow_end", 1, 123, 456)
			tunnel := f.event(2, "flow_end", 1, 0, 0)
			tunnel.Kind = "tunnel_end"
			tunnel.FlowID, tunnel.Destination = nil, nil
			tunnel.Bytes = nodeprov.HistoryBytes{Layer: "none"}
			tunnel.Lifecycle.Scope = "authenticated_transport"
			tunnel.Quality.Bytes, tunnel.Quality.Destination = "unknown", "unknown"
			f.ingest(f.batch(flow, tunnel), 200)
			for _, scope := range []string{"all", "node", "user", "exact", "prefix", "opaque", "invalid", "unauthorized", "detail", "missing"} {
				if (scope == "detail" || scope == "missing") && resource == "aggregate" {
					continue
				}
				t.Run(scope, func(t *testing.T) {
					token := f.token
					defer func() { f.token = token }()
					q := url.Values{}
					filters := map[string]any{"destination_match": "none", "tunnel_filtered": false, "entity_filtered": false}
					operation, targetType, targetID, want := "list", "connection_history", resource, 200
					if resource == "aggregate" {
						operation = "aggregate"
					}
					path := "/api/v1/admin/connection-history/" + resource
					switch scope {
					case "node", "exact", "prefix":
						q.Set("node_id", f.node)
						q.Set("user_id", f.user)
						q.Set("collection_epoch", string(*f.config.CollectionEpoch))
						q.Set("protocol", "vless")
						q.Set("status", "ended")
						q.Set("from", f.now.Add(-time.Hour).Format(time.RFC3339Nano))
						q.Set("to", f.now.Add(time.Hour).Format(time.RFC3339Nano))
						for key, values := range q {
							filters[key] = values[0]
						}
						targetType, targetID = "node", f.node
						if scope == "exact" {
							q.Set("destination", "Target-1.example.test")
							filters["destination_match"] = "exact"
						}
						if scope == "prefix" {
							q.Set("destination_prefix", "private-query-secret")
							filters["destination_match"] = "prefix"
						}
					case "user":
						q.Set("user_id", f.user)
						filters["user_id"] = f.user
						targetType, targetID = "user", f.user
					case "opaque":
						q.Set("entity_id", "private-query-secret")
						q.Set("tunnel_id", "private-query-secret")
						cursor := fmt.Sprintf(`{"First":"2099-01-01T00:00:00Z","Node":%q,"Epoch":%q,"Entity":"private-query-secret"}`, f.node, *f.config.CollectionEpoch)
						q.Set("cursor", base64.RawURLEncoding.EncodeToString([]byte(cursor)))
						filters["entity_filtered"], filters["tunnel_filtered"] = true, true
					case "invalid":
						q.Set("protocol", "private-query-secret")
						want = 400
					case "unauthorized":
						f.token = "invalid-admin-token"
						want = 401
					case "detail", "missing":
						entity := *flow.FlowID
						if resource == "sessions" {
							entity = *tunnel.TunnelID
						}
						operation, targetType, targetID = "detail", "node", f.node
						filters["node_id"], filters["collection_epoch"], filters["entity_id"] = f.node, string(*f.config.CollectionEpoch), entity
						filters["entity_filtered"] = true
						if scope == "missing" {
							entity = "missing-private-query-secret"
							want = 404
						}
						path += "/" + f.node + "/" + string(*f.config.CollectionEpoch) + "/" + strings.ReplaceAll(entity, ":", "%3A")
					}
					q.Set("password", "private-query-secret")
					q.Set("limit", "7")
					historyClearAudit(t, f)
					// When: list, detail or aggregate is read over authenticated HTTP.
					f.request("GET", path+"?"+q.Encode(), nil, "", want)
					// Then: the DB contains only actor, target and allowlisted scope.
					if want != 200 {
						historyAuditRows(t, f, 0)
						return
					}
					detail := map[string]any{"operation": operation, "resource": resource, "filters": filters}
					if operation == "list" {
						detail["limit"], detail["cursor_present"] = 7, q.Get("cursor") != ""
					}
					row := historyAuditRows(t, f, 1)[0]
					if row.Severity != "info" {
						t.Fatalf("read severity=%s", row.Severity)
					}
					historyAssertAudit(t, row, "connection_history.read", targetType, targetID, detail)
				})
			}
		})
	}
}
