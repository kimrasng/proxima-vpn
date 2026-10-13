package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
	"github.com/proximavpn/proxima-vpn/pkg/speedtier"
)

const deviceEgressPlaceholder = "materialize-at-node"

// deviceEgress keeps local secrets out of the canonical config and authorizes
// rollback against the latest control-plane generation, never the backup's.
// Its mutex protects both credential updates and heartbeat capability reads.
type deviceEgress struct {
	mu             sync.Mutex
	setCredentials func([]devicebandwidth.Credential) error
	// setRates receives per-device plan speeds for local enforcement; nil
	// means rates are unknown and metered traffic must be denied.
	setRates       func(map[string]int)
	passwords      map[string]string
	passwordTokens map[string]string
	authorized     map[string]struct{}
	managed        map[string]struct{}
	ready          func() bool
	count          int
}

func newDeviceEgress(setCredentials func([]devicebandwidth.Credential) error) *deviceEgress {
	return &deviceEgress{setCredentials: setCredentials, passwords: make(map[string]string), passwordTokens: make(map[string]string)}
}

type deviceConfig struct {
	root                map[string]json.RawMessage
	outbounds           []map[string]json.RawMessage
	devices             map[string]int    // outbound tag -> index
	deviceIDs           map[string]string // outbound tag -> device UUID
	identities          map[string]struct{}
	deviceSources       map[string]string
	deviceSourcePorts   map[string]int // outbound tag -> exact source port (0 = source-only)
	deviceSessionTokens map[string]string
	// rates maps each managed device UUID to its plan speed in Mbps (0 =
	// unlimited), taken from the limit tier of the inbound it belongs to.
	rates map[string]int
}

type deviceSOCKSServer struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Users   []struct {
		User string `json:"user"`
		Pass string `json:"pass"`
	} `json:"users"`
}

// parseDeviceConfig rejects invalid declarations and missing exact routing.
// Legacy unlimited configs without device declarations are left unchanged.
func parseDeviceConfig(data []byte) (*deviceConfig, error) {
	cfg := &deviceConfig{deviceSources: make(map[string]string), deviceSourcePorts: make(map[string]int), deviceSessionTokens: make(map[string]string), devices: make(map[string]int), deviceIDs: make(map[string]string), identities: make(map[string]struct{}), rates: make(map[string]int)}
	if err := json.Unmarshal(data, &cfg.root); err != nil || cfg.root == nil {
		return nil, fmt.Errorf("invalid xray configuration")
	}
	if raw := cfg.root["outbounds"]; raw != nil {
		if err := json.Unmarshal(raw, &cfg.outbounds); err != nil {
			return nil, fmt.Errorf("invalid xray outbounds: %w", err)
		}
	}
	for i, outbound := range cfg.outbounds {
		var tag, protocol string
		_ = json.Unmarshal(outbound["tag"], &tag)
		if !strings.HasPrefix(tag, devicebandwidth.OutboundPrefix) {
			continue
		}
		_ = json.Unmarshal(outbound["protocol"], &protocol)
		id, _, valid := parseLocalDeviceTag(tag)
		if !valid {
			return nil, fmt.Errorf("invalid device egress tag %q", tag)
		}
		var settings struct {
			Servers []deviceSOCKSServer `json:"servers"`
		}
		if err := json.Unmarshal(outbound["settings"], &settings); err != nil {
			return nil, fmt.Errorf("invalid device egress settings for %q", id)
		}
		if id == "" || len(id) > 255 || protocol != "socks" || len(settings.Servers) != 1 {
			return nil, fmt.Errorf("invalid device egress declaration for %q", id)
		}
		server := settings.Servers[0]
		if server.Address != "127.0.0.1" || server.Port != devicebandwidth.Port || len(server.Users) != 1 || server.Users[0].User != id || server.Users[0].Pass != deviceEgressPlaceholder {
			return nil, fmt.Errorf("invalid device egress endpoint or credential for %q", id)
		}
		if _, duplicate := cfg.devices[tag]; duplicate {
			return nil, fmt.Errorf("duplicate device egress for %q", id)
		}
		cfg.devices[tag] = i
		cfg.deviceIDs[tag] = id
	}
	var inbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
		Settings struct {
			Clients []struct {
				ID       string `json:"id"`
				Password string `json:"password"`
				Email    string `json:"email"`
			} `json:"clients"`
		} `json:"settings"`
	}
	if raw := cfg.root["inbounds"]; raw != nil {
		if err := json.Unmarshal(raw, &inbounds); err != nil {
			return nil, fmt.Errorf("invalid xray inbounds: %w", err)
		}
	}
	var routing struct {
		Rules []map[string]json.RawMessage `json:"rules"`
	}
	if raw := cfg.root["routing"]; raw != nil {
		if err := json.Unmarshal(raw, &routing); err != nil {
			return nil, fmt.Errorf("invalid xray routing: %w", err)
		}
	}
	matched := make(map[string]bool)
	managedIDs := make(map[string]bool)
	denyTags := make(map[string]bool)
	for _, id := range cfg.deviceIDs {
		managedIDs[id] = true
	}
	for _, outbound := range cfg.outbounds {
		var tag, protocol string
		_ = json.Unmarshal(outbound["tag"], &tag)
		_ = json.Unmarshal(outbound["protocol"], &protocol)
		if protocol == "blackhole" {
			denyTags[tag] = true
		}
	}
	for _, inbound := range inbounds {
		limitMbps, limited := speedtier.ParseLimitTag(inbound.Tag)
		controlled := limited || len(cfg.devices) > 0
		for _, raw := range routing.Rules {
			var rule struct {
				InboundTag  []string `json:"inboundTag"`
				OutboundTag string   `json:"outboundTag"`
			}
			if err := decodeDeviceRule(raw, &rule); err != nil {
				return nil, err
			}
			if containsString(rule.InboundTag, inbound.Tag) && denyTags[rule.OutboundTag] {
				controlled = true
			}
		}
		for _, user := range inbound.Settings.Clients {
			identity := user.ID
			if identity == "" {
				identity = user.Password
			}
			// Include non-VLESS clients in rollback authorization: an older
			// protocol must not resurrect a revoked credential either.
			cfg.identities[inbound.Protocol+"\x00"+inbound.Tag+"\x00"+identity+"\x00"+user.Email] = struct{}{}
			managed := managedIDs[user.ID]
			if inbound.Protocol == "vless" && !managed {
				denied, err := explicitlyDeniedDevice(routing.Rules, inbound.Tag, user.ID, user.Email, denyTags)
				if err != nil {
					return nil, err
				}
				if denied {
					continue // Authorized inbound identity, but no local credential or rate.
				}
			}
			if (limited || controlled && inbound.Protocol == "vless") && (inbound.Protocol != "vless" || !managed) {
				return nil, fmt.Errorf("controlled inbound %q has no device egress", inbound.Tag)
			}
			if inbound.Protocol != "vless" || !managed {
				continue
			}
			if user.Email != user.ID+"@proxima" {
				return nil, fmt.Errorf("device %q has invalid authenticated email", user.ID)
			}
			// Source-qualified routes must terminate in an unconditional deny,
			// never a shared device or Freedom catchall for unknown sources.
			sourceMode := false
			for tag, id := range cfg.deviceIDs {
				if id == user.ID && strings.Contains(tag, "~") {
					sourceMode = true
				}
			}
			found, denied := false, false
			clientMatched := make(map[string]bool)
			seenSources := make(map[netip.Prefix]map[int]bool)
			for _, rawRule := range routing.Rules {
				var rule struct {
					Type         string   `json:"type"`
					User         []string `json:"user"`
					InboundTag   []string `json:"inboundTag"`
					OutboundTag  string   `json:"outboundTag"`
					Source       []string `json:"source"`
					SourcePort   []string `json:"sourcePort"`
					SessionToken string   `json:"sessionToken"`
				}
				if err := decodeDeviceRule(rawRule, &rule); err != nil {
					return nil, err
				}
				if (len(rule.User) > 0 && !containsString(rule.User, user.Email)) || (len(rule.InboundTag) > 0 && !containsString(rule.InboundTag, inbound.Tag)) {
					continue
				}
				strict := rule.Type == "field"
				for key := range rawRule {
					switch key {
					case "type", "user", "inboundTag", "outboundTag", "ruleTag", "source", "sourcePort", "sessionToken":
					default:
						strict = false
					}
				}
				_, hasSource := rawRule["source"]
				_, hasSourcePort := rawRule["sourcePort"]
				_, hasSessionToken := rawRule["sessionToken"]
				if sourceMode && strict && !hasSource && !hasSourcePort && !hasSessionToken && denyTags[rule.OutboundTag] {
					denied = true
					break
				}
				id, sourceKey, valid := parseLocalDeviceTag(rule.OutboundTag)
				_, declared := cfg.devices[rule.OutboundTag]
				if rule.SessionToken != "" {
					if !sourceMode || !hasSource || !hasSourcePort || len(rule.SessionToken) != 64 {
						return nil, fmt.Errorf("invalid session-qualified route")
					}
					if previous := cfg.deviceSessionTokens[rule.OutboundTag]; previous != "" && previous != rule.SessionToken {
						return nil, fmt.Errorf("conflicting session token")
					}
					cfg.deviceSessionTokens[rule.OutboundTag] = rule.SessionToken
				}
				if !strict || !valid || !declared || id != user.ID || len(rule.User) != 1 || rule.User[0] != user.Email {
					return nil, fmt.Errorf("device %q lacks first-match authenticated egress routing", user.ID)
				}
				if sourceMode {
					if sourceKey == "" || len(rule.Source) != 1 {
						return nil, fmt.Errorf("device %q requires exact source routing", user.ID)
					}
					prefix, err := netip.ParsePrefix(rule.Source[0])
					if err != nil || prefix.Bits() != prefix.Addr().BitLen() || prefix.Addr().Is4In6() {
						return nil, fmt.Errorf("device %q has invalid or shadowed source route", user.ID)
					}
					port := 0
					if hasSourcePort {
						if len(rule.SourcePort) != 1 {
							return nil, fmt.Errorf("device %q requires one exact source port", user.ID)
						}
						port, err = strconv.Atoi(rule.SourcePort[0])
						if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != rule.SourcePort[0] {
							return nil, fmt.Errorf("device %q has invalid source port", user.ID)
						}
					}
					if seenSources[prefix] == nil {
						seenSources[prefix] = make(map[int]bool)
					}
					// A source-only rule overlaps every port for that address,
					// regardless of its position in the first-match rule list.
					if seenSources[prefix][port] || port == 0 && len(seenSources[prefix]) > 0 || port != 0 && seenSources[prefix][0] {
						return nil, fmt.Errorf("device %q has invalid or shadowed source route", user.ID)
					}
					seenSources[prefix][port] = true
					if previous := cfg.deviceSources[rule.OutboundTag]; previous != "" && previous != prefix.Addr().String() {
						return nil, fmt.Errorf("source credential is bound to multiple addresses")
					}
					cfg.deviceSources[rule.OutboundTag] = prefix.Addr().String()
					if previous := cfg.deviceSourcePorts[rule.OutboundTag]; previous != 0 && previous != port {
						return nil, fmt.Errorf("source credential is bound to multiple ports")
					}
					cfg.deviceSourcePorts[rule.OutboundTag] = port
				} else if hasSource || hasSourcePort {
					return nil, fmt.Errorf("device %q requires unconditional legacy routing", user.ID)
				}
				found = true
				matched[rule.OutboundTag] = true
				clientMatched[rule.OutboundTag] = true
				if !sourceMode {
					break
				}
			}
			if !found || sourceMode && !denied {
				return nil, fmt.Errorf("device %q lacks authenticated egress routing or deny fallback", user.ID)
			}
			for tag, id := range cfg.deviceIDs {
				if id == user.ID && !clientMatched[tag] {
					return nil, fmt.Errorf("device egress %q lacks routing on inbound %q", tag, inbound.Tag)
				}
			}
			// A device listed on several inbounds takes the strictest tier.
			if previous, seen := cfg.rates[user.ID]; !seen || (limitMbps > 0 && (previous == 0 || limitMbps < previous)) {
				cfg.rates[user.ID] = limitMbps
			}
		}
	}
	for id := range cfg.devices {
		if !matched[id] {
			return nil, fmt.Errorf("device egress %q has no authenticated client", id)
		}
	}
	return cfg, nil
}

// parseLocalDeviceTag preserves legacy IDs while reserving ~ for a bounded
// ASCII hex source key. The source key is not part of the SOCKS username.
func parseLocalDeviceTag(tag string) (id, sourceKey string, valid bool) {
	if !strings.HasPrefix(tag, devicebandwidth.OutboundPrefix) {
		return "", "", false
	}
	id, sourceKey, qualified := strings.Cut(strings.TrimPrefix(tag, devicebandwidth.OutboundPrefix), "~")
	if id == "" || len(id) > 255 {
		return "", "", false
	}
	if qualified {
		if len(sourceKey) < 16 || len(sourceKey) > 64 || len(sourceKey)%2 != 0 {
			return "", "", false
		}
		for _, c := range sourceKey {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return "", "", false
			}
		}
	}
	return id, sourceKey, true
}

// explicitlyDeniedDevice allows an inbound credential to survive removal of
// its last source. Only a first-match, exact user deny is a declaration; an
// inbound catchall alone must never authorize missing device egress.
func explicitlyDeniedDevice(rules []map[string]json.RawMessage, inbound, id, email string, denyTags map[string]bool) (bool, error) {
	if id == "" || email != id+"@proxima" {
		return false, nil
	}
	for i, raw := range rules {
		var rule struct {
			Type        string   `json:"type"`
			User        []string `json:"user"`
			InboundTag  []string `json:"inboundTag"`
			OutboundTag string   `json:"outboundTag"`
		}
		if err := decodeDeviceRule(raw, &rule); err != nil {
			return false, err
		}
		if len(rule.User) > 0 && !containsString(rule.User, email) || len(rule.InboundTag) > 0 && !containsString(rule.InboundTag, inbound) {
			continue
		}
		if rule.Type != "field" || len(rule.User) != 1 || rule.User[0] != email || !denyTags[rule.OutboundTag] {
			return false, nil
		}
		for key := range raw {
			switch key {
			case "type", "user", "outboundTag", "ruleTag":
			default:
				return false, nil
			}
		}
		// Keep the controlled inbound's unconditional fail-closed fallback.
		for _, fallback := range rules[i+1:] {
			rule.User, rule.InboundTag, rule.Type, rule.OutboundTag = nil, nil, "", ""
			if err := decodeDeviceRule(fallback, &rule); err != nil {
				return false, err
			}
			if len(rule.User) > 0 || !containsString(rule.InboundTag, inbound) {
				continue
			}
			if rule.Type != "field" || len(rule.InboundTag) != 1 || !denyTags[rule.OutboundTag] {
				break
			}
			for key := range fallback {
				switch key {
				case "type", "inboundTag", "outboundTag", "ruleTag":
				default:
					return false, fmt.Errorf("controlled inbound %q lacks unconditional deny fallback", inbound)
				}
			}
			return true, nil
		}
		return false, fmt.Errorf("controlled inbound %q lacks unconditional deny fallback", inbound)
	}
	return false, nil
}

func decodeDeviceRule(raw map[string]json.RawMessage, rule any) error {
	data, err := json.Marshal(raw)
	if err == nil {
		err = json.Unmarshal(data, rule)
	}
	if err != nil {
		return fmt.Errorf("invalid xray routing rule: %w", err)
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (e *deviceEgress) applyRates(rates map[string]int) {
	if e.setRates != nil {
		e.setRates(rates)
	}
}

func (e *deviceEgress) TransformConfig(canonical []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cfg, err := parseDeviceConfig(canonical)
	if err != nil {
		// Do not leave previously authorized local connections serving after
		// a malformed generation arrives. The caller also stops old Xray.
		e.authorized = nil
		e.managed = nil
		e.count = 0
		e.applyRates(nil)
		_ = e.setCredentials(nil)
		return nil, err
	}
	if len(cfg.devices) > 0 && e.ready != nil && !e.ready() {
		e.authorized = nil
		e.managed = nil
		e.count = 0
		e.applyRates(nil)
		_ = e.setCredentials(nil)
		return nil, fmt.Errorf("local device egress listener is unavailable")
	}
	// Rates first: a device whose credential is installed below must already
	// have its speed limit, never a window of unknown (denied) or stale rate.
	e.applyRates(cfg.rates)
	e.authorized = cfg.identities
	e.managed = make(map[string]struct{}, len(cfg.devices))
	for id := range cfg.devices {
		e.managed[id] = struct{}{}
	}
	for id := range e.passwords {
		if _, keep := cfg.devices[id]; !keep {
			delete(e.passwords, id)
			delete(e.passwordTokens, id)
		}
	}
	// Revoke first, before generating/admitting newly authorized credentials.
	kept := make([]devicebandwidth.Credential, 0, len(e.passwords))
	for id, password := range e.passwords {
		kept = append(kept, devicebandwidth.Credential{UUID: cfg.deviceIDs[id], Password: password, SourceAddress: cfg.deviceSources[id], SessionToken: e.passwordTokens[id]})
	}
	if err := e.setCredentials(kept); err != nil {
		e.count = 0
		return nil, err
	}
	credentials := make([]devicebandwidth.Credential, 0, len(cfg.devices))
	for id, index := range cfg.devices {
		password := e.passwords[id]
		if password == "" {
			var secret [32]byte
			if _, err := rand.Read(secret[:]); err != nil {
				e.count = 0
				return nil, fmt.Errorf("generate local egress password: %w", err)
			}
			password = hex.EncodeToString(secret[:])
			e.passwords[id] = password
		}
		if sessionToken := cfg.deviceSessionTokens[id]; sessionToken != e.passwordTokens[id] {
			if sessionToken == "" {
				delete(e.passwordTokens, id)
			} else {
				e.passwordTokens[id] = sessionToken
			}
		}
		credentials = append(credentials, devicebandwidth.Credential{UUID: cfg.deviceIDs[id], Password: password, SourceAddress: cfg.deviceSources[id], SessionToken: e.passwordTokens[id]})
		// Replace only the credential field; retain any unrelated settings.
		outbound := cfg.outbounds[index]
		var settings map[string]json.RawMessage
		_ = json.Unmarshal(outbound["settings"], &settings)
		var servers []map[string]json.RawMessage
		_ = json.Unmarshal(settings["servers"], &servers)
		var users []map[string]json.RawMessage
		_ = json.Unmarshal(servers[0]["users"], &users)
		users[0]["pass"], _ = json.Marshal(password)
		servers[0]["users"], _ = json.Marshal(users)
		settings["servers"], _ = json.Marshal(servers)
		outbound["settings"], _ = json.Marshal(settings)
	}
	if err := e.setCredentials(credentials); err != nil {
		e.count = 0
		return nil, err
	}
	e.count = len(credentials)
	if e.count == 0 {
		return append([]byte(nil), canonical...), nil
	}
	cfg.root["outbounds"], _ = json.Marshal(cfg.outbounds)
	return json.Marshal(cfg.root)
}

func (e *deviceEgress) CanRestore(canonical []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	cfg, err := parseDeviceConfig(canonical)
	if err != nil {
		return err
	}
	if e.authorized == nil {
		return fmt.Errorf("no currently authorized generation")
	}
	for identity := range cfg.identities {
		if _, allowed := e.authorized[identity]; !allowed {
			return fmt.Errorf("backup contains a revoked or changed client")
		}
	}
	for id := range cfg.devices {
		if _, allowed := e.managed[id]; !allowed {
			return fmt.Errorf("backup contains revoked device egress")
		}
	}
	if len(e.managed) > 0 {
		// Conservative: reject a rollback that removes a managed route. It
		// might revive a Freedom connection during an unlimited-to-limited
		// policy change even if the UUID itself is still authorized.
		for id := range e.managed {
			if _, exists := cfg.devices[id]; !exists {
				return fmt.Errorf("backup would bypass device egress")
			}
		}
	}
	return nil
}

func (e *deviceEgress) Capability() (bool, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready != nil && e.ready() && e.count > 0, e.count
}

// sameLocalStructure supplements the server digest before live gRPC edits.
// Even a stale/old digest implementation cannot hide changed egress topology.
func sameLocalStructure(a, b []byte) bool {
	strip := func(data []byte) ([]byte, error) {
		var cfg map[string]json.RawMessage
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		// Device outbounds and their exact user rules are applied live over
		// gRPC (syncDeviceTopology), the same contract the panel's
		// StructureHash uses.
		if err := devicebandwidth.StripDeviceTopology(cfg); err != nil {
			return nil, err
		}
		if raw := cfg["inbounds"]; raw != nil {
			var inbounds []map[string]json.RawMessage
			if err := json.Unmarshal(raw, &inbounds); err != nil {
				return nil, err
			}
			for _, inbound := range inbounds {
				var protocol string
				_ = json.Unmarshal(inbound["protocol"], &protocol)
				if protocol != "vless" {
					continue
				}
				var settings map[string]json.RawMessage
				if err := json.Unmarshal(inbound["settings"], &settings); err != nil {
					return nil, err
				}
				settings["clients"] = json.RawMessage(`[]`)
				inbound["settings"], _ = json.Marshal(settings)
			}
			cfg["inbounds"], _ = json.Marshal(inbounds)
		}
		return json.Marshal(cfg)
	}
	left, err := strip(a)
	if err != nil {
		return false
	}
	right, err := strip(b)
	return err == nil && bytes.Equal(left, right)
}
