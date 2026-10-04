package services

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
	"github.com/proximavpn/proxima-vpn/pkg/speedtier"
)

// deviceEgressPassword is a declaration, not a usable SOCKS credential. The
// node agent must replace it with a random node-local password before starting
// Xray, retaining the API config bytes for canonical config/digest hashes.
const deviceEgressPassword = "materialize-at-node"

type xraySOCKSUser struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type xraySOCKSServer struct {
	Address string          `json:"address"`
	Port    int             `json:"port"`
	Users   []xraySOCKSUser `json:"users"`
}

type xraySOCKSOutboundSettings struct {
	Servers []xraySOCKSServer `json:"servers"`
}

// addCompatibilityTierClient normalizes the legacy endpoint key before grouping.
// Values above MaxMbps share a single compatibility listener; central per-device
// permits, not this clamped endpoint key, enforce the plan's actual Mbps value.
func addCompatibilityTierClient(tiers map[int][]xrayClient, mbps int64, client xrayClient) {
	if mbps <= 0 {
		return
	}
	if mbps > int64(speedtier.MaxMbps) {
		mbps = int64(speedtier.MaxMbps)
	}
	tier, _ := speedtier.ParseLimitTag(speedtier.Tag(int(mbps)))
	tiers[tier] = append(tiers[tier], client)
}

// appendCompatibilityTierInbound reuses a main Reality listener when a saved
// tier endpoint has the same port, but only if its transport/keys and admitted
// credentials are compatible. A collision with anything else fails closed.
func appendCompatibilityTierInbound(inbounds []xrayInbound, tier xrayInbound) ([]xrayInbound, error) {
	var existing *xrayInbound
	for i := range inbounds {
		if inbounds[i].Port != tier.Port {
			continue
		}
		if existing != nil {
			return nil, fmt.Errorf("port %d already has multiple listeners", tier.Port)
		}
		existing = &inbounds[i]
	}
	if existing == nil {
		return append(inbounds, tier), nil
	}
	if existing.Protocol != "vless" || existing.Listen != tier.Listen ||
		existing.StreamSettings == nil || existing.StreamSettings.Security != "reality" ||
		existing.StreamSettings.RealitySettings == nil ||
		!reflect.DeepEqual(existing.StreamSettings, tier.StreamSettings) {
		return nil, fmt.Errorf("port %d conflicts with incompatible inbound %q", tier.Port, existing.Tag)
	}
	var admitted, required xrayInboundSettings
	if err := json.Unmarshal(existing.Settings, &admitted); err != nil {
		return nil, fmt.Errorf("decode conflicting inbound %q: %w", existing.Tag, err)
	}
	if err := json.Unmarshal(tier.Settings, &required); err != nil {
		return nil, fmt.Errorf("decode compatibility inbound %q: %w", tier.Tag, err)
	}
	if admitted.Decryption != "none" || required.Decryption != "none" {
		return nil, fmt.Errorf("port %d inbound %q has incompatible VLESS decryption", tier.Port, existing.Tag)
	}
	clients := make(map[string]xrayClient, len(admitted.Clients))
	for _, client := range admitted.Clients {
		clients[client.ID] = client
	}
	for _, client := range required.Clients {
		if got, ok := clients[client.ID]; !ok || got != client {
			return nil, fmt.Errorf("port %d inbound %q does not admit compatibility device %q", tier.Port, existing.Tag, client.ID)
		}
	}
	return inbounds, nil
}

// WithDeviceEgressRouting installs the API-generated server config's managed
// outbounds and routing. Every VLESS device, even one currently unlimited, uses
// the local SOCKS egress so live sessions cannot keep a Freedom bypass after a
// plan downgrade. Non-VLESS shared inbounds keep the legacy direct default.
// Invalid VLESS identity fails generation instead of silently bypassing egress.
// Other config fields are preserved. Declarations are sorted by device UUID and
// remain outside client lists, making device changes structural in the digest.
func WithDeviceEgressRouting(config []byte) ([]byte, error) {
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("decode xray config: %w", err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("missing xray config")
	}
	var inbounds []xrayInbound
	if err := json.Unmarshal(cfg["inbounds"], &inbounds); err != nil {
		return nil, fmt.Errorf("decode inbounds: %w", err)
	}

	devices := make(map[string]struct{})
	controlled := make(map[string]struct{})
	for _, ib := range inbounds {
		if ib.Protocol != "vless" {
			if _, limited := speedtier.ParseLimitTag(ib.Tag); limited {
				return nil, fmt.Errorf("controlled inbound %q must use VLESS", ib.Tag)
			}
			continue
		}
		if ib.Tag == "" || ib.Tag == "api" {
			return nil, fmt.Errorf("VLESS inbound has invalid controlled tag %q", ib.Tag)
		}
		var settings xrayInboundSettings
		if err := json.Unmarshal(ib.Settings, &settings); err != nil {
			return nil, fmt.Errorf("decode controlled inbound %q: %w", ib.Tag, err)
		}
		if settings.Clients == nil {
			return nil, fmt.Errorf("controlled inbound %q has no client list", ib.Tag)
		}
		controlled[ib.Tag] = struct{}{}
		for _, client := range settings.Clients {
			id, err := uuid.Parse(client.ID)
			if err != nil || id.String() != client.ID || id == uuid.Nil {
				return nil, fmt.Errorf("controlled inbound %q has invalid device UUID %q", ib.Tag, client.ID)
			}
			if client.Email != client.ID+"@proxima" {
				return nil, fmt.Errorf("controlled inbound %q device %q has no exact authenticated email", ib.Tag, client.ID)
			}
			devices[client.ID] = struct{}{}
		}
	}

	deviceUUIDs := make([]string, 0, len(devices))
	for id := range devices {
		deviceUUIDs = append(deviceUUIDs, id)
	}
	sort.Strings(deviceUUIDs)
	controlledTags := make([]string, 0, len(controlled))
	for tag := range controlled {
		controlledTags = append(controlledTags, tag)
	}
	sort.Strings(controlledTags)

	// Freedom remains the default for legacy shared non-VLESS inbounds. Every
	// VLESS inbound has an explicit deny fallback after exact user matches.
	outbounds := []xrayOutbound{
		{Protocol: "freedom", Tag: "direct"},
		{Protocol: "blackhole", Tag: "block"},
	}
	rules := []xrayRoutingRule{
		{Type: "field", InboundTag: []string{"api"}, OutboundTag: "api"},
	}
	for _, id := range deviceUUIDs {
		settings, err := json.Marshal(xraySOCKSOutboundSettings{
			Servers: []xraySOCKSServer{{
				Address: "127.0.0.1",
				Port:    devicebandwidth.Port,
				Users:   []xraySOCKSUser{{User: id, Pass: deviceEgressPassword}},
			}},
		})
		if err != nil {
			return nil, fmt.Errorf("marshal device %q SOCKS settings: %w", id, err)
		}
		tag := devicebandwidth.OutboundPrefix + id
		outbounds = append(outbounds, xrayOutbound{Protocol: "socks", Tag: tag, Settings: settings})
		rules = append(rules, xrayRoutingRule{Type: "field", User: []string{id + "@proxima"}, OutboundTag: tag})
	}
	if len(controlledTags) > 0 {
		rules = append(rules, xrayRoutingRule{Type: "field", InboundTag: controlledTags, OutboundTag: "block"})
	}

	var err error
	cfg["outbounds"], err = json.Marshal(outbounds)
	if err != nil {
		return nil, fmt.Errorf("marshal outbounds: %w", err)
	}
	cfg["routing"], err = json.Marshal(xrayRouting{Rules: rules})
	if err != nil {
		return nil, fmt.Errorf("marshal routing: %w", err)
	}
	return json.MarshalIndent(cfg, "", "  ")
}
