package services

import (
	"encoding/json"
	"testing"
)

func mustConfig(t *testing.T, cfg xrayConfig) []byte {
	t.Helper()
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return out
}

func vlessInbound(t *testing.T, tag string, port int, clients []xrayClient) xrayInbound {
	t.Helper()
	settings, err := json.Marshal(xrayInboundSettings{Clients: clients, Decryption: "none"})
	if err != nil {
		t.Fatalf("marshal inbound settings: %v", err)
	}
	return xrayInbound{
		Port:     port,
		Protocol: "vless",
		Tag:      tag,
		Settings: settings,
	}
}

func TestVlessUsersOfFlattensClientsPerInbound(t *testing.T) {
	inbounds := []xrayInbound{
		{Port: 10085, Protocol: "dokodemo-door", Tag: "api", Settings: json.RawMessage(`{"address":"127.0.0.1"}`)},
		vlessInbound(t, "vless-reality", 443, []xrayClient{
			{ID: "uuid-a", Flow: "xtls-rprx-vision", Email: "uuid-a@proxima"},
		}),
		vlessInbound(t, "vless-reality-limit-10", 20010, []xrayClient{
			{ID: "uuid-b", Flow: "xtls-rprx-vision", Email: "uuid-b@proxima"},
		}),
	}

	users := vlessUsersOf(inbounds)
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2: %+v", len(users), users)
	}

	byEmail := map[string]ConfigUser{}
	for _, u := range users {
		byEmail[u.Email] = u
	}

	a, ok := byEmail["uuid-a@proxima"]
	if !ok {
		t.Fatal("uuid-a missing")
	}
	if a.InboundTag != "vless-reality" || a.UUID != "uuid-a" || a.Flow != "xtls-rprx-vision" {
		t.Errorf("unexpected user a: %+v", a)
	}

	b, ok := byEmail["uuid-b@proxima"]
	if !ok {
		t.Fatal("uuid-b missing")
	}
	if b.InboundTag != "vless-reality-limit-10" {
		t.Errorf("speed-tier user landed on %q", b.InboundTag)
	}
}

// Only VLESS clients can be provisioned incrementally, so non-VLESS inbounds
// must not appear in the digest's user set.
func TestVlessUsersOfIgnoresNonVlessInbounds(t *testing.T) {
	ssSettings, err := json.Marshal(xrayShadowsocksSettings{Method: "aes-128-gcm", Password: "p", Network: "tcp,udp"})
	if err != nil {
		t.Fatalf("marshal ss settings: %v", err)
	}
	trojanSettings, err := json.Marshal(xrayTrojanInboundSettings{
		Clients: []xrayTrojanClient{{Password: "uuid-c", Email: "uuid-c@proxima-trojan"}},
	})
	if err != nil {
		t.Fatalf("marshal trojan settings: %v", err)
	}

	users := vlessUsersOf([]xrayInbound{
		{Port: 8388, Protocol: "shadowsocks", Tag: "ss", Settings: ssSettings},
		{Port: 2083, Protocol: "trojan", Tag: "trojan-tls", Settings: trojanSettings},
	})

	if len(users) != 0 {
		t.Errorf("got %d users from non-VLESS inbounds, want 0: %+v", len(users), users)
	}
}

// The structure hash must be blind to client-list changes; that property is
// what lets the agent apply a users-only change without restarting Xray.
func TestStripClientsIgnoresUserChanges(t *testing.T) {
	base := func(clients []xrayClient) []byte {
		return mustConfig(t, xrayConfig{
			Inbounds: []xrayInbound{vlessInbound(t, "vless-reality", 443, clients)},
		})
	}

	oneUser := base([]xrayClient{{ID: "uuid-a", Email: "uuid-a@proxima"}})
	twoUsers := base([]xrayClient{
		{ID: "uuid-a", Email: "uuid-a@proxima"},
		{ID: "uuid-b", Email: "uuid-b@proxima"},
	})

	strippedOne, err := stripClients(oneUser)
	if err != nil {
		t.Fatalf("stripClients: %v", err)
	}
	strippedTwo, err := stripClients(twoUsers)
	if err != nil {
		t.Fatalf("stripClients: %v", err)
	}

	if string(strippedOne) != string(strippedTwo) {
		t.Errorf("structure changed when only users differ:\n one=%s\n two=%s", strippedOne, strippedTwo)
	}
}

// Conversely a real structural edit must change the stripped form, or the agent
// would skip a restart it needs.
func TestStripClientsReflectsStructuralChanges(t *testing.T) {
	clients := []xrayClient{{ID: "uuid-a", Email: "uuid-a@proxima"}}

	onPort443 := mustConfig(t, xrayConfig{
		Inbounds: []xrayInbound{vlessInbound(t, "vless-reality", 443, clients)},
	})
	onPort8443 := mustConfig(t, xrayConfig{
		Inbounds: []xrayInbound{vlessInbound(t, "vless-reality", 8443, clients)},
	})
	extraInbound := mustConfig(t, xrayConfig{
		Inbounds: []xrayInbound{
			vlessInbound(t, "vless-reality", 443, clients),
			vlessInbound(t, "vless-reality-limit-10", 20010, clients),
		},
	})

	strippedBase, err := stripClients(onPort443)
	if err != nil {
		t.Fatalf("stripClients: %v", err)
	}
	strippedPort, err := stripClients(onPort8443)
	if err != nil {
		t.Fatalf("stripClients: %v", err)
	}
	strippedExtra, err := stripClients(extraInbound)
	if err != nil {
		t.Fatalf("stripClients: %v", err)
	}

	if string(strippedBase) == string(strippedPort) {
		t.Error("a port change did not alter the structure hash input")
	}
	if string(strippedBase) == string(strippedExtra) {
		t.Error("adding an inbound did not alter the structure hash input")
	}
}

func TestStripClientsEmptiesClientListsOnly(t *testing.T) {
	cfg := mustConfig(t, xrayConfig{
		Inbounds: []xrayInbound{vlessInbound(t, "vless-reality", 443, []xrayClient{
			{ID: "uuid-a", Email: "uuid-a@proxima"},
		})},
	})

	stripped, err := stripClients(cfg)
	if err != nil {
		t.Fatalf("stripClients: %v", err)
	}

	var doc struct {
		Inbounds []struct {
			Port     int `json:"port"`
			Settings struct {
				Clients    []xrayClient `json:"clients"`
				Decryption string       `json:"decryption"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(stripped, &doc); err != nil {
		t.Fatalf("unmarshal stripped config: %v", err)
	}
	if len(doc.Inbounds) != 1 {
		t.Fatalf("got %d inbounds, want 1", len(doc.Inbounds))
	}
	if len(doc.Inbounds[0].Settings.Clients) != 0 {
		t.Error("clients were not emptied")
	}
	if doc.Inbounds[0].Settings.Decryption != "none" {
		t.Error("stripClients dropped a non-client setting")
	}
	if doc.Inbounds[0].Port != 443 {
		t.Error("stripClients altered the port")
	}
}

func TestStripClientsRejectsInvalidJSON(t *testing.T) {
	if _, err := stripClients([]byte(`{"inbounds":`)); err == nil {
		t.Error("stripClients() = nil error for malformed JSON")
	}
}
