package services

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
	"github.com/proximavpn/proxima-vpn/pkg/speedtier"
)

const (
	egressDeviceA   = "11111111-1111-4111-8111-111111111111"
	egressDeviceB   = "22222222-2222-4222-8222-222222222222"
	egressUnlimited = "33333333-3333-4333-8333-333333333333"
)

func egressClient(id string) xrayClient {
	return xrayClient{ID: id, Email: id + "@proxima", Flow: "xtls-rprx-vision"}
}

func egressFixture(t *testing.T, limitedIDs, unlimitedIDs []string) []byte {
	t.Helper()
	limited := make([]xrayClient, 0, len(limitedIDs))
	main := make([]xrayClient, 0, len(limitedIDs)+len(unlimitedIDs))
	for _, id := range limitedIDs {
		limited = append(limited, egressClient(id))
		main = append(main, egressClient(id))
	}
	for _, id := range unlimitedIDs {
		main = append(main, egressClient(id))
	}
	return mustConfig(t, xrayConfig{
		Inbounds: []xrayInbound{
			{Listen: "127.0.0.1", Port: 10085, Tag: "api", Protocol: "dokodemo-door", Settings: json.RawMessage(`{"address":"127.0.0.1"}`)},
			vlessInbound(t, "vless-reality", 443, main),
			vlessInbound(t, speedtier.Tag(25), speedtier.VlessPort(443, 25), limited),
		},
	})
}

func withEgress(t *testing.T, config []byte) ([]byte, xrayConfig) {
	t.Helper()
	out, err := WithDeviceEgressRouting(config)
	if err != nil {
		t.Fatalf("WithDeviceEgressRouting: %v", err)
	}
	var cfg xrayConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return out, cfg
}

func assertDeviceEgressDeclarations(t *testing.T, cfg xrayConfig, ids []string, controlledTags []string) {
	t.Helper()
	if len(cfg.Outbounds) != len(ids)+2 {
		t.Fatalf("got %d outbounds, want %d", len(cfg.Outbounds), len(ids)+2)
	}
	if cfg.Outbounds[0].Tag != "direct" || cfg.Outbounds[0].Protocol != "freedom" {
		t.Fatal("legacy non-VLESS default is not Freedom")
	}
	if cfg.Outbounds[1].Tag != "block" || cfg.Outbounds[1].Protocol != "blackhole" {
		t.Fatal("deny outbound is not blackhole")
	}
	for _, ob := range cfg.Outbounds[:2] {
		if len(ob.Settings) != 0 {
			t.Errorf("%s outbound unexpectedly has settings: %s", ob.Tag, ob.Settings)
		}
	}
	wantRuleCount := 1 + len(ids)
	if len(controlledTags) > 0 {
		wantRuleCount++
	}
	if len(cfg.Routing.Rules) != wantRuleCount {
		t.Fatalf("got %d rules, want %d", len(cfg.Routing.Rules), wantRuleCount)
	}
	wantAPI := xrayRoutingRule{Type: "field", InboundTag: []string{"api"}, OutboundTag: "api"}
	if !reflect.DeepEqual(cfg.Routing.Rules[0], wantAPI) {
		t.Errorf("API rule must remain first: %+v", cfg.Routing.Rules[0])
	}
	for i, id := range ids {
		ob := cfg.Outbounds[i+2]
		tag := devicebandwidth.OutboundPrefix + id
		if ob.Protocol != "socks" || ob.Tag != tag {
			t.Errorf("device %s outbound = %+v", id, ob)
		}
		// Assert the old Xray SOCKS 'servers' shape, not a newer shorthand.
		wantSettings := fmt.Sprintf(`{"servers":[{"address":"127.0.0.1","port":%d,"users":[{"user":%q,"pass":"materialize-at-node"}]}]}`, devicebandwidth.Port, id)
		var compactSettings bytes.Buffer
		if err := json.Compact(&compactSettings, ob.Settings); err != nil {
			t.Fatalf("compact SOCKS settings: %v", err)
		}
		if compactSettings.String() != wantSettings {
			t.Errorf("device %s SOCKS settings = %s, want %s", id, compactSettings.String(), wantSettings)
		}
		var settings xraySOCKSOutboundSettings
		if err := json.Unmarshal(ob.Settings, &settings); err != nil {
			t.Fatalf("decode SOCKS settings: %v", err)
		}
		if settings.Servers[0].Users[0].Pass == id {
			t.Error("SOCKS password must never reuse the device UUID")
		}
		wantRule := xrayRoutingRule{Type: "field", User: []string{id + "@proxima"}, OutboundTag: tag}
		if !reflect.DeepEqual(cfg.Routing.Rules[i+1], wantRule) {
			t.Errorf("device %s exact-user rule = %+v, want %+v", id, cfg.Routing.Rules[i+1], wantRule)
		}
	}
	if len(controlledTags) > 0 {
		fallback := cfg.Routing.Rules[len(cfg.Routing.Rules)-1]
		wantFallback := xrayRoutingRule{Type: "field", InboundTag: controlledTags, OutboundTag: "block"}
		if !reflect.DeepEqual(fallback, wantFallback) {
			t.Errorf("controlled inbound deny fallback = %+v, want %+v", fallback, wantFallback)
		}
	}
}

func TestDeviceEgressRoutesSameTierDevicesIndependently(t *testing.T) {
	in := egressFixture(t, []string{egressDeviceB, egressDeviceA}, []string{egressUnlimited})
	out, cfg := withEgress(t, in)
	assertDeviceEgressDeclarations(t, cfg, []string{egressDeviceA, egressDeviceB, egressUnlimited}, []string{"vless-reality", speedtier.Tag(25)})

	// Unlimited VLESS also uses local egress to make future plan downgrades
	// effective for already-established sessions; its budget remains unlimited.
	if cfg.Inbounds[1].Port != 443 || cfg.Inbounds[2].Port != speedtier.VlessPort(443, 25) {
		t.Error("device routing changed the main or legacy tier endpoint")
	}
	users := vlessUsersOf(cfg.Inbounds)
	for _, id := range []string{egressDeviceA, egressDeviceB} {
		for _, tag := range []string{"vless-reality", speedtier.Tag(25)} {
			found := false
			for _, user := range users {
				found = found || (user.UUID == id && user.InboundTag == tag)
			}
			if !found {
				t.Errorf("limited device %s not admitted on %s", id, tag)
			}
		}
	}

	var raw struct {
		Routing struct {
			Rules []map[string]json.RawMessage `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	for _, rule := range raw.Routing.Rules[1:4] {
		if _, exists := rule["inboundTag"]; exists {
			t.Error("exact-user rule must apply on main and tier ingress, without an inboundTag selector")
		}
	}
	if _, exists := raw.Routing.Rules[0]["user"]; exists {
		t.Error("API rule should omit empty user selector")
	}
}

// Xray routing uses the first matching rule. Exercise both saved-subscription
// tier ingress and fixed-chain main ingress with the same authenticated email.
func TestDeviceEgressLimitedMatchesPrecedeControlledDenyFallback(t *testing.T) {
	_, cfg := withEgress(t, egressFixture(t, []string{egressDeviceA}, []string{egressUnlimited}))
	resolve := func(inboundTag, email string) string {
		for _, rule := range cfg.Routing.Rules {
			inboundMatches := len(rule.InboundTag) == 0
			for _, tag := range rule.InboundTag {
				inboundMatches = inboundMatches || tag == inboundTag
			}
			userMatches := len(rule.User) == 0
			for _, user := range rule.User {
				userMatches = userMatches || user == email
			}
			if inboundMatches && userMatches {
				return rule.OutboundTag
			}
		}
		return cfg.Outbounds[0].Tag
	}
	for _, tc := range []struct{ tag, email, want string }{
		{"api", egressDeviceA + "@proxima", "api"},
		{speedtier.Tag(25), egressDeviceA + "@proxima", devicebandwidth.OutboundPrefix + egressDeviceA},
		{"vless-reality", egressDeviceA + "@proxima", devicebandwidth.OutboundPrefix + egressDeviceA},
		{speedtier.Tag(25), "unknown@proxima", "block"},
		{speedtier.Tag(25), "", "block"},
		{speedtier.Tag(25), "prefix-" + egressDeviceA + "@proxima", "block"},
		{"vless-reality", egressUnlimited + "@proxima", devicebandwidth.OutboundPrefix + egressUnlimited},
		{"vless-reality", "unknown@proxima", "block"},
		{"vmess-in", egressUnlimited + "@proxima-vmess", "direct"},
		{"trojan-in", egressUnlimited + "@proxima-trojan", "direct"},
	} {
		if got := resolve(tc.tag, tc.email); got != tc.want {
			t.Errorf("route(%q, %q) = %q, want %q", tc.tag, tc.email, got, tc.want)
		}
	}
}

func TestDeviceEgressDeclarationsAreSortedAndDeterministic(t *testing.T) {
	input := egressFixture(t, []string{egressDeviceB, egressDeviceA}, nil)
	first, cfg := withEgress(t, input)
	second, _ := withEgress(t, input)
	if !bytes.Equal(first, second) {
		t.Error("repeated generation changed canonical JSON")
	}
	idempotent, _ := withEgress(t, first)
	if !bytes.Equal(first, idempotent) {
		t.Error("reapplying declarations changed canonical JSON")
	}
	_, reversed := withEgress(t, egressFixture(t, []string{egressDeviceA, egressDeviceB}, nil))
	if !reflect.DeepEqual(cfg.Outbounds, reversed.Outbounds) || !reflect.DeepEqual(cfg.Routing, reversed.Routing) {
		t.Error("device input order changed managed outbounds or routing")
	}
}

func TestDeviceEgressDeduplicatesDeviceAcrossCompatibilityInbounds(t *testing.T) {
	var input xrayConfig
	if err := json.Unmarshal(egressFixture(t, []string{egressDeviceA}, nil), &input); err != nil {
		t.Fatal(err)
	}
	input.Inbounds = append(input.Inbounds, vlessInbound(t, speedtier.Tag(10), speedtier.VlessPort(443, 10), []xrayClient{egressClient(egressDeviceA)}))
	_, cfg := withEgress(t, mustConfig(t, input))
	assertDeviceEgressDeclarations(t, cfg, []string{egressDeviceA}, []string{"vless-reality", speedtier.Tag(10), speedtier.Tag(25)})
}

func TestDeviceEgressEmptyControlledInboundRemainsFailClosed(t *testing.T) {
	_, cfg := withEgress(t, egressFixture(t, nil, []string{egressUnlimited}))
	assertDeviceEgressDeclarations(t, cfg, []string{egressUnlimited}, []string{"vless-reality", speedtier.Tag(25)})
}

func TestDeviceEgressUnlimitedVLESSStillUsesLocalEgress(t *testing.T) {
	input := xrayConfig{Inbounds: []xrayInbound{vlessInbound(t, "vless-reality", 443, []xrayClient{egressClient(egressUnlimited)})}}
	_, cfg := withEgress(t, mustConfig(t, input))
	assertDeviceEgressDeclarations(t, cfg, []string{egressUnlimited}, []string{"vless-reality"})
}

func TestDeviceEgressNonVLESSKeepsNormalFreedom(t *testing.T) {
	input := xrayConfig{Inbounds: []xrayInbound{{Tag: "vmess-in", Protocol: "vmess", Settings: json.RawMessage(`{"clients":[]}`)}}}
	_, cfg := withEgress(t, mustConfig(t, input))
	assertDeviceEgressDeclarations(t, cfg, nil, nil)
}

func TestDeviceEgressRejectsMissingControlledIdentity(t *testing.T) {
	for _, tc := range []struct{ name, protocol, settings string }{
		{"invalid settings", "vless", `[]`},
		{"missing client list", "vless", `{}`},
		{"null client list", "vless", `{"clients":null}`},
		{"missing UUID", "vless", `{"clients":[{"email":"@proxima"}]}`},
		{"invalid UUID", "vless", `{"clients":[{"id":"bad","email":"bad@proxima"}]}`},
		{"missing email", "vless", fmt.Sprintf(`{"clients":[{"id":%q}]}`, egressDeviceA)},
		{"wrong email", "vless", fmt.Sprintf(`{"clients":[{"id":%q,"email":"other@proxima"}]}`, egressDeviceA)},
		{"unsupported protocol", "vmess", `{"clients":[]}`},
	} {
		for _, tag := range []string{speedtier.Tag(25), "vless-reality"} {
			t.Run(tc.name+"/"+tag, func(t *testing.T) {
				input := mustConfig(t, xrayConfig{Inbounds: []xrayInbound{{Tag: tag, Protocol: tc.protocol, Settings: json.RawMessage(tc.settings)}}})
				if _, err := WithDeviceEgressRouting(input); err == nil && (tc.protocol == "vless" || tag == speedtier.Tag(25)) {
					t.Error("invalid controlled identity silently generated a config")
				}
			})
		}
	}
	for _, input := range []string{`null`, `{}`, `{"inbounds":`, `{"inbounds":{}}`} {
		if _, err := WithDeviceEgressRouting([]byte(input)); err == nil {
			t.Errorf("accepted invalid config %s", input)
		}
	}
}

func TestDeviceEgressLimitedDeviceChangesRemainStructuralAfterStripClients(t *testing.T) {
	structure := func(limited, unlimited []string) []byte {
		out, _ := withEgress(t, egressFixture(t, limited, unlimited))
		stripped, err := stripClients(out)
		if err != nil {
			t.Fatalf("stripClients: %v", err)
		}
		return stripped
	}
	base := structure([]string{egressDeviceA}, nil)
	addedLimited := structure([]string{egressDeviceA, egressDeviceB}, nil)
	removedLimited := structure(nil, nil)
	replacedLimited := structure([]string{egressDeviceB}, nil)
	addedUnlimited := structure([]string{egressDeviceA}, []string{egressUnlimited})
	for name, config := range map[string][]byte{"add limited": addedLimited, "remove limited": removedLimited, "replace limited": replacedLimited, "add unlimited": addedUnlimited} {
		if sha256.Sum256(base) == sha256.Sum256(config) {
			t.Errorf("%s device did not change structure hash input", name)
		}
	}
	var cfg xrayConfig
	if err := json.Unmarshal(base, &cfg); err != nil {
		t.Fatal(err)
	}
	assertDeviceEgressDeclarations(t, cfg, []string{egressDeviceA}, []string{"vless-reality", speedtier.Tag(25)})
	if len(vlessUsersOf(cfg.Inbounds)) != 0 {
		t.Error("stripClients did not remove inbound clients")
	}
}

func TestDeviceEgressHighSpeedPlansShareOneCompatibilityListener(t *testing.T) {
	tiers := make(map[int][]xrayClient)
	addCompatibilityTierClient(tiers, 2000, egressClient(egressDeviceA))
	addCompatibilityTierClient(tiers, 5000, egressClient(egressDeviceB))
	if len(tiers) != 1 || len(tiers[speedtier.MaxMbps]) != 2 {
		t.Fatalf("high speed plans did not normalize to one tier: %+v", tiers)
	}
	service := NewXrayConfigService(nil)
	inbounds := []xrayInbound{vlessInbound(t, "vless-reality", 443, []xrayClient{egressClient(egressDeviceA), egressClient(egressDeviceB)})}
	for mbps, clients := range tiers {
		inbounds = append(inbounds, service.buildTierVlessInbound(speedtier.VlessPort(443, mbps), speedtier.Tag(mbps), clients, "private", "short", "example.com:443", []string{"example.com"}))
	}
	_, cfg := withEgress(t, mustConfig(t, xrayConfig{Inbounds: inbounds}))
	assertDeviceEgressDeclarations(t, cfg, []string{egressDeviceA, egressDeviceB}, []string{"vless-reality", speedtier.Tag(speedtier.MaxMbps)})
	if len(cfg.Inbounds) != 2 || cfg.Inbounds[1].Port != 22000 {
		t.Errorf("duplicate or wrong compatibility listeners: %+v", cfg.Inbounds)
	}
}

func TestDeviceEgressCompatibilityPortCollisionReusesCompatibleReality(t *testing.T) {
	svc := NewXrayConfigService(nil)
	limited := []xrayClient{egressClient(egressDeviceA)}
	all := append(append([]xrayClient{}, limited...), egressClient(egressUnlimited))
	tier := svc.buildTierVlessInbound(20025, speedtier.Tag(25), limited, "private", "short", "www.cloudflare.com:443", []string{"www.cloudflare.com"})
	for _, mode := range []string{"legacy", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			var inbounds []xrayInbound
			if mode == "legacy" {
				inbounds = svc.buildLegacyInbounds(20025, all, nil, nil, "private", "short", nil, nil, nil)
			} else {
				ib, err := svc.buildVlessReality(inboundRow{Port: 20025, Tag: "main", Settings: json.RawMessage(`{}`)}, all, "private", "short")
				if err != nil {
					t.Fatal(err)
				}
				inbounds = []xrayInbound{*ib}
			}
			got, err := appendCompatibilityTierInbound(inbounds, tier)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, inbounds) {
				t.Error("compatible main listener was duplicated or changed")
			}
			_, cfg := withEgress(t, mustConfig(t, xrayConfig{Inbounds: got}))
			assertDeviceEgressDeclarations(t, cfg, []string{egressDeviceA, egressUnlimited}, []string{inbounds[0].Tag})
		})
	}
}

func TestDeviceEgressCompatibilityPortCollisionFailsClosed(t *testing.T) {
	svc := NewXrayConfigService(nil)
	tier := svc.buildTierVlessInbound(20025, speedtier.Tag(25), []xrayClient{egressClient(egressDeviceA)}, "private", "short", "www.cloudflare.com:443", []string{"www.cloudflare.com"})
	for _, name := range []string{"non-VLESS", "non-Reality", "different-key", "missing-device", "invalid-settings", "different-listen-address", "duplicate-listeners"} {
		t.Run(name, func(t *testing.T) {
			// Marshal round-trip gives each case its own nested stream settings.
			var main xrayInbound
			wire, err := json.Marshal(tier)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(wire, &main); err != nil {
				t.Fatal(err)
			}
			main.Tag = "main"
			switch name {
			case "non-VLESS":
				main.Protocol = "trojan"
			case "non-Reality":
				main.StreamSettings.Security = "tls"
			case "different-key":
				main.StreamSettings.RealitySettings.PrivateKey = "other-key"
			case "missing-device":
				main.Settings = json.RawMessage(`{"clients":[],"decryption":"none"}`)
			case "invalid-settings":
				main.Settings = json.RawMessage(`[]`)
			case "different-listen-address":
				main.Listen = "127.0.0.1"
			}
			inbounds := []xrayInbound{main}
			if name == "duplicate-listeners" {
				inbounds = append(inbounds, main)
			}
			if _, err := appendCompatibilityTierInbound(inbounds, tier); err == nil {
				t.Error("incompatible colliding listener was silently accepted")
			}
		})
	}
}

func TestDeviceEgressCompatibilityPortWithoutCollisionAppendsTier(t *testing.T) {
	svc := NewXrayConfigService(nil)
	clients := []xrayClient{egressClient(egressDeviceA)}
	inbounds := svc.buildLegacyInbounds(443, clients, nil, nil, "private", "short", nil, nil, nil)
	tier := svc.buildTierVlessInbound(20025, speedtier.Tag(25), clients, "private", "short", "www.cloudflare.com:443", []string{"www.cloudflare.com"})
	got, err := appendCompatibilityTierInbound(inbounds, tier)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(inbounds)+1 || !reflect.DeepEqual(got[len(got)-1], tier) {
		t.Error("non-colliding compatibility listener was not preserved")
	}
}

func TestDeviceEgressPreservesNonRoutingConfig(t *testing.T) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(egressFixture(t, []string{egressDeviceA}, nil), &input); err != nil {
		t.Fatal(err)
	}
	input["custom"] = json.RawMessage(`{"keep":"unchanged"}`)
	in, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := withEgress(t, in)
	var output map[string]json.RawMessage
	if err := json.Unmarshal(out, &output); err != nil {
		t.Fatal(err)
	}
	for key, before := range input {
		if key == "outbounds" || key == "routing" {
			continue
		}
		var compactBefore, compactAfter bytes.Buffer
		if err := json.Compact(&compactBefore, before); err != nil {
			t.Fatal(err)
		}
		if err := json.Compact(&compactAfter, output[key]); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(compactBefore.Bytes(), compactAfter.Bytes()) {
			t.Errorf("field %s changed", key)
		}
	}
}
