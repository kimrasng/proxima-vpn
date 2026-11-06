package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"testing"
)

func TestConnectionHistoryHTTPLayersSharedIdentityAndSessions(t *testing.T) {
	// Given: a complete proxy flow and an independently observed IP-layer flow.
	f := historyFixture(t)
	f.enable()
	proxy := f.event(1, "flow_end", 1, 131, 271)
	wg := f.event(2, "flow_end", 2, 311, 419)
	wg.Protocol = historyPtr("wireguard")
	wg.Runtime = "wireguard"
	wg.ObserverBootID = historyUUID(t)
	wg.EventID = string(wg.ObserverBootID) + ":2"
	wg.ProducerID = "wireguard:wg0"
	wg.FlowID = historyPtr(string(wg.ObserverBootID) + ":f:1")
	wg.TunnelID = historyPtr(string(wg.ObserverBootID) + ":t:1")
	wg.Auth = nodeprov.HistoryAuth{Kind: "unknown"}
	wg.Quality.Identity = "unknown"
	wg.Bytes.Layer = "inner_ip_observed"
	wg.Lifecycle.Scope = "activity_window"
	wg.Lifecycle.EndReason = historyPtr("idle_timeout")
	wg.Lifecycle.EndSemantics = historyPtr("activity_expired")
	wg.Quality.Start = "observed"
	wg.Quality.End = "observed"
	wg.Destination.RequestedHost = nil
	wg.Destination.RequestedPort = nil
	wg.Destination.ObservedIP = historyPtr("203.0.113.64")
	wg.Destination.ObservedPort = historyPtr(5353)
	session := f.event(3, "flow_end", 1, 0, 0)
	session.Kind = "tunnel_end"
	session.FlowID = nil
	session.Destination = nil
	session.Bytes = nodeprov.HistoryBytes{Layer: "none"}
	session.Lifecycle.Scope = "authenticated_transport"
	session.Quality.Bytes = "unknown"
	session.Quality.Destination = "unknown"
	// When: distinct measurement layers and their real parent are ingested.
	f.ingest(f.batch(proxy, wg, session), 200)
	// Then: totals stay separate, and tunnels derive child totals without bytes.
	body := f.request("GET", "/api/v1/admin/connection-history/aggregate", nil, "", 200)
	var totals []services.ConnectionHistoryLayerTotal
	if err := json.Unmarshal(body, &totals); err != nil {
		t.Fatal(err)
	}
	if len(totals) != 2 || totals[0].Layer != "inner_ip_observed" || totals[0].Upload != "311" || totals[1].Upload != "131" {
		t.Fatalf("mixed layers: %s", body)
	}
	data := f.request("GET", "/api/v1/admin/connection-history/sessions", nil, "", 200)
	var sessions services.ConnectionHistoryPage
	if err := json.Unmarshal(data, &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions.Items) != 1 || len(sessions.Items[0].ChildTotals) != 1 || sessions.Items[0].ChildTotals[0].Upload != "131" || sessions.Items[0].Event.Bytes.Upload != nil {
		t.Fatalf("session totals=%s", data)
	}
	// Shared bindings are opaque and never acquire an account owner.
	shared, err := f.svc.IssueBinding(context.Background(), f.node, 2, nodeprov.HistoryBinding{Runtime: "xray", InboundTag: "ss-shared", LabelKind: "shared_inbound", Label: "ss-shared", Kind: "shared"}, services.ConnectionHistoryBindingOwner{})
	if err != nil {
		t.Fatal(err)
	}
	ss := f.event(4, "flow_end", 3, 23, 47)
	ss.ConfigRevision = 3
	ss.Protocol = historyPtr("shadowsocks")
	ss.InboundTag = historyPtr("ss-shared")
	ss.Auth = nodeprov.HistoryAuth{Kind: "shared", CredentialRef: &shared.CredentialRef, BindingRevision: &shared.BindingRevision}
	ss.Quality.Identity = "unknown"
	f.ingest(f.batch(ss), 200)
	page := f.page("?node_id=" + f.node + "&limit=1")
	if len(page.Items) != 1 || page.Items[0].UserID != nil || page.Items[0].Event.Auth.Kind != "shared" || page.Items[0].Event.Quality.Identity != "unknown" {
		t.Fatalf("shared attribution=%+v", page)
	}
	body = f.request("GET", "/api/v1/admin/nodes/"+f.node+"/connection-logging", nil, "", 200)
	if !bytes.Contains(body, []byte(`"shared_identity":true`)) {
		t.Fatalf("shared degradation missing: %s", body)
	}
}
