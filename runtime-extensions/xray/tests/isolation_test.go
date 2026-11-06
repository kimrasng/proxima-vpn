package historytests

import (
	"encoding/json"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestXUDPGlobalIDDoesNotCrossAuthenticatedAccounts(t *testing.T) {
	// Given two credentials from the same source IP using the same global ID.
	target, ingress := echoUDP(t), freePort(t)
	secondID := "00000000-0000-4000-8000-000000000002"
	secondRef := "55555555-5555-4555-8555-555555555555"
	var cfg map[string]any
	if err := json.Unmarshal([]byte(config("vless", ingress, 0, false)), &cfg); err != nil {
		t.Fatal(err)
	}
	settings := cfg["inbounds"].([]any)[0].(map[string]any)["settings"].(map[string]any)
	settings["clients"] = append(settings["clients"].([]any), map[string]any{"id": secondID, "email": "fixture-b@history"})
	wire, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	socket := socketPath(t)
	startRuntime(t, string(wire), socket)
	c := connectCollector(t, socket)
	bindings := []any{}
	for label, ref := range map[string]string{"fixture@history": credential, "fixture-b@history": secondRef} {
		bindings = append(bindings, map[string]any{
			"runtime": "xray", "inbound_tag": "proof", "label_kind": "xray_email",
			"label": label, "credential_ref": ref, "kind": "device", "binding_revision": "1",
		})
	}
	c.send(t, map[string]any{"op": "apply", "config_revision": "1", "collection_epoch": epoch, "enabled": true, "bindings": bindings})
	for {
		var frame struct {
			Op string `json:"op"`
		}
		if err := json.Unmarshal(c.next(t), &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Op == "applied" {
			break
		}
		if frame.Op != "" {
			t.Fatalf("unexpected control %s", frame.Op)
		}
	}
	a := rawMuxUser(t, ingress, identity)
	b := rawMuxUser(t, ingress, secondID)
	// When both carriers stay live and alternate packets on identical IDs.
	muxDatagram(t, a, 1, target, 11, true)
	muxDatagram(t, b, 1, target, 7, true)
	muxDatagram(t, a, 2, target, 5, false)
	c.send(t, map[string]any{"op": "apply", "config_revision": "2", "collection_epoch": epoch, "enabled": false, "bindings": bindings})
	// Then no ownership migration occurred and each credential has its own bytes.
	totals := make(map[string]uint64)
	for {
		line := c.next(t)
		var frame struct {
			Op string `json:"op"`
		}
		if err := json.Unmarshal(line, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Op == "applied" {
			break
		}
		e, err := nodeprov.DecodeHistoryEvent(line)
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == "flow_end" {
			t.Fatal("another authenticated account detached the live association")
		}
		if e.Kind == "flow_update" {
			if e.Auth.CredentialRef == nil || e.Bytes.Upload == nil || e.Bytes.Download == nil || *e.Bytes.Upload != *e.Bytes.Download {
				t.Fatal("account ownership or counters unknown")
			}
			totals[string(*e.Auth.CredentialRef)] += uint64(*e.Bytes.Upload)
		}
	}
	if totals[credential] != 16 || totals[secondRef] != 7 || len(totals) != 2 {
		t.Fatalf("cross-account byte attribution: %v", totals)
	}
}
