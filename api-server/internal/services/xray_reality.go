package services

import (
	"encoding/json"

	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
)

const defaultRealityDest = "www.cloudflare.com:443"

func realityParameters(settings json.RawMessage) (string, []string) {
	var configured struct {
		Dest        string   `json:"dest"`
		ServerNames []string `json:"server_names"`
	}
	if err := json.Unmarshal(settings, &configured); err != nil {
		return defaultRealityDest, []string{"www.cloudflare.com"}
	}
	if configured.Dest == "" {
		configured.Dest = defaultRealityDest
	}
	if len(configured.ServerNames) == 0 {
		configured.ServerNames = []string{"www.cloudflare.com"}
	}
	return configured.Dest, configured.ServerNames
}

func tierRealityParameters(rows []inboundRow) (string, []string) {
	for _, row := range rows {
		if row.Protocol == "vless_reality" {
			return realityParameters(row.Settings)
		}
	}
	return defaultRealityDest, []string{"www.cloudflare.com"}
}

type realityKeys struct {
	privateKey string
	shortID    string
}

func newXrayRealitySettings(dest string, names []string, keys realityKeys) *xrayRealitySettings {
	return &xrayRealitySettings{
		Dest: dest, ServerNames: names, PrivateKey: keys.privateKey, ShortIds: []string{keys.shortID},
	}
}

// realityResultFromInbounds extracts the listeners actually emitted by the
// generator, rather than inferring them from raw rows or independent defaults.
func realityResultFromInbounds(inbounds []xrayInbound) (reality.Result, error) {
	listeners := make([]reality.Listener, 0, len(inbounds))
	for _, inbound := range inbounds {
		if inbound.Protocol != "vless" || inbound.StreamSettings == nil || inbound.StreamSettings.Security != "reality" {
			continue
		}
		var names []string
		if inbound.StreamSettings.RealitySettings != nil {
			names = inbound.StreamSettings.RealitySettings.ServerNames
		}
		listeners = append(listeners, reality.Listener{
			Tag: inbound.Tag, Enabled: true, Protocol: inbound.Protocol,
			Security: inbound.StreamSettings.Security, ServerNames: names,
		})
	}
	return reality.Intersect(listeners)
}
