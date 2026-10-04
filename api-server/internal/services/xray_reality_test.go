package services

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
)

func TestPHS028XrayRealityBaseline_whenRegularLegacyAndTierBuilt(t *testing.T) {
	// Given: pre-refactor settings, including order and case that must remain on wire.
	svc := &XrayConfigService{}
	clients := []xrayClient{{ID: "uuid", Flow: "xtls-rprx-vision", Email: "uuid@proxima"}}
	row := inboundRow{Protocol: "vless_reality", Port: 443, Tag: "primary", Settings: json.RawMessage(`{"dest":"example.org:8443","server_names":["B.Example.org","a.example.org","B.Example.org"]}`)}

	// When: each pre-existing builder emits its listener.
	regular, err := svc.buildVlessReality(row, clients, "key", "short")
	if err != nil {
		t.Fatal(err)
	}
	legacy := svc.buildLegacyInbounds(443, clients, nil, nil, "key", "short", nil, nil, nil)
	tier := svc.buildTierVlessInbound(20010, "tier-10", clients, "key", "short", "example.org:8443", []string{"B.Example.org", "a.example.org"})

	// Then: compare full emitted listener JSON against pre-refactor fixtures.
	assertInboundJSON := func(name string, got xrayInbound, want string) {
		t.Helper()
		wire, marshalErr := json.Marshal(got)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if string(wire) != want {
			t.Errorf("%s wire mismatch:\ngot  %s\nwant %s", name, wire, want)
		}
	}
	assertInboundJSON("regular", *regular, `{"port":443,"protocol":"vless","tag":"primary","settings":{"clients":[{"id":"uuid","flow":"xtls-rprx-vision","email":"uuid@proxima","level":0}],"decryption":"none"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"dest":"example.org:8443","serverNames":["B.Example.org","a.example.org","B.Example.org"],"privateKey":"key","shortIds":["short"]}},"sniffing":{"enabled":true,"destOverride":["http","tls"]}}`)
	if len(legacy) < 1 {
		t.Fatal("legacy Reality listener missing")
	}
	assertInboundJSON("legacy", legacy[0], `{"port":443,"protocol":"vless","tag":"vless-reality","settings":{"clients":[{"id":"uuid","flow":"xtls-rprx-vision","email":"uuid@proxima","level":0}],"decryption":"none"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"dest":"www.cloudflare.com:443","serverNames":["www.cloudflare.com"],"privateKey":"key","shortIds":["short"]}},"sniffing":{"enabled":true,"destOverride":["http","tls"]}}`)
	assertInboundJSON("tier", tier, `{"port":20010,"protocol":"vless","tag":"tier-10","settings":{"clients":[{"id":"uuid","flow":"xtls-rprx-vision","email":"uuid@proxima","level":0}],"decryption":"none"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"dest":"example.org:8443","serverNames":["B.Example.org","a.example.org"],"privateKey":"key","shortIds":["short"]}},"sniffing":{"enabled":true,"destOverride":["http","tls"]}}`)
}

func TestPHS028XrayRealityIntersection_whenMixedGeneratedListeners(t *testing.T) {
	// Given: a regular listener, an inherited tier, and non-Reality listeners.
	svc := &XrayConfigService{}
	primary, err := svc.buildVlessReality(inboundRow{Protocol: "vless_reality", Tag: "primary", Settings: json.RawMessage(`{"server_names":["Z.example","a.EXAMPLE"]}`)}, nil, "key", "short")
	if err != nil {
		t.Fatal(err)
	}
	tier := svc.buildTierVlessInbound(20010, "tier", nil, "key", "short", "fallback:443", []string{"a.example", "z.example"})
	inbounds := []xrayInbound{{Protocol: "dokodemo-door"}, *primary, {Protocol: "vless", StreamSettings: &xrayStreamSettings{Security: "tls"}}, tier}
	// When
	result, err := realityResultFromInbounds(inbounds)
	// Then
	if err != nil || result.Status != reality.Valid || result.Proposed.String() != "a.example" {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestPHS028XrayRealityIntersection_whenInvalidEmittedName(t *testing.T) {
	// Given
	inbounds := []xrayInbound{{Tag: "broken", Protocol: "vless", StreamSettings: &xrayStreamSettings{Security: "reality", RealitySettings: &xrayRealitySettings{ServerNames: []string{"invalid/name"}}}}}
	// When
	_, err := realityResultFromInbounds(inbounds)
	// Then
	var diagnostic *reality.InvalidListenerNameError
	if !errors.As(err, &diagnostic) || diagnostic.Tag != "broken" {
		t.Fatalf("diagnostic = %v", err)
	}
}

func TestPHS028XrayRealityParameters_whenFirstEffectiveRowSelected(t *testing.T) {
	// Given
	rows := []inboundRow{
		{Protocol: "vmess_ws", Enabled: true, Settings: json.RawMessage(`{"server_names":["ignored.example"]}`)},
		{Protocol: "vless_reality", Enabled: true, Settings: json.RawMessage(`{"dest":"first.example:443","server_names":["B.example","A.example"]}`)},
		{Protocol: "vless_reality", Enabled: true, Settings: json.RawMessage(`{"dest":"second.example:443","server_names":["second.example"]}`)},
	}
	// When
	dest, names := tierRealityParameters(rows)
	// Then
	if dest != "first.example:443" || !slices.Equal(names, []string{"B.example", "A.example"}) {
		t.Fatalf("tier parameters = %q, %q", dest, names)
	}
}

func TestPHS028XrayRealityParameters_whenNoConfiguredReality(t *testing.T) {
	// Given / When
	dest, names := tierRealityParameters([]inboundRow{{Protocol: "vmess_ws", Enabled: true}})
	// Then
	if dest != "www.cloudflare.com:443" || !slices.Equal(names, []string{"www.cloudflare.com"}) {
		t.Fatalf("tier defaults = %q, %q", dest, names)
	}
	result, err := realityResultFromInbounds(nil)
	if err != nil || result.Status != reality.NotApplicable {
		t.Fatalf("no emitted listener = %+v, %v", result, err)
	}
}

func TestPHS028XrayRealityParameters_whenMalformedOrPartialSettings(t *testing.T) {
	// Given / When / Then
	for _, raw := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`{}`), json.RawMessage(`{"server_names":[]}`)} {
		dest, names := realityParameters(raw)
		if dest != "www.cloudflare.com:443" || !slices.Equal(names, []string{"www.cloudflare.com"}) {
			t.Fatalf("settings %s = %q, %q", raw, dest, names)
		}
	}
}

func TestPHS028XrayRealityIntersection_whenLegacyFallbackEmitted(t *testing.T) {
	// Given: no DB inbounds, so the generator emits its legacy listener.
	inbounds := (&XrayConfigService{}).buildLegacyInbounds(443, nil, nil, nil, "key", "short", nil, nil, nil)
	// When
	result, err := realityResultFromInbounds(inbounds)
	// Then
	if err != nil || result.Status != reality.Valid || result.Proposed.String() != "www.cloudflare.com" {
		t.Fatalf("legacy result = %+v, %v", result, err)
	}
}

func TestPHS028XrayRealityIntersection_whenTierAndRegularConflict(t *testing.T) {
	// Given: independently emitted listeners with disjoint names.
	svc := &XrayConfigService{}
	regular, err := svc.buildVlessReality(inboundRow{Tag: "primary", Settings: json.RawMessage(`{"server_names":["primary.example"]}`)}, nil, "key", "short")
	if err != nil {
		t.Fatal(err)
	}
	tier := svc.buildTierVlessInbound(20010, "tier", nil, "key", "short", "example:443", []string{"tier.example"})
	// When
	result, err := realityResultFromInbounds([]xrayInbound{*regular, tier})
	// Then
	if err != nil || result.Status != reality.Conflict || result.Reason != reality.NoCommonName || result.Proposed.String() != "" {
		t.Fatalf("conflict result = %+v, %v", result, err)
	}
}

func TestPHS028XrayRealityBaseline_whenMixedWithNonReality(t *testing.T) {
	// Given: a VMess/TLS listener alongside Reality, with no disabled row emitted.
	cert, key := "/cert.pem", "/key.pem"
	svc := &XrayConfigService{}
	vmess, err := svc.buildVmessWS(inboundRow{Tag: "vmess", Port: 8443, Settings: json.RawMessage(`{"ws_path":"/custom"}`)}, []xrayClient{}, &cert, &key)
	if err != nil {
		t.Fatal(err)
	}
	regular, err := svc.buildVlessReality(inboundRow{Tag: "primary", Port: 443, Settings: json.RawMessage(`{"server_names":["EXAMPLE.org"]}`)}, nil, "key", "short")
	if err != nil {
		t.Fatal(err)
	}
	// When
	wire, err := json.Marshal(vmess)
	if err != nil {
		t.Fatal(err)
	}
	result, err := realityResultFromInbounds([]xrayInbound{*vmess, *regular})
	// Then: the VMess wire fixture is pre-refactor output; it cannot constrain SNI.
	want := `{"port":8443,"protocol":"vmess","tag":"vmess","settings":{"clients":[]},"streamSettings":{"network":"ws","security":"tls","tlsSettings":{"certificates":[{"certificateFile":"/cert.pem","keyFile":"/key.pem"}]},"wsSettings":{"path":"/custom"}},"sniffing":{"enabled":true,"destOverride":["http","tls"]}}`
	if string(wire) != want || err != nil || result.Status != reality.Valid || result.Proposed.String() != "example.org" {
		t.Fatalf("mixed wire = %s; result = %+v, %v", wire, result, err)
	}
}
