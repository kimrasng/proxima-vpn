package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	passwords      map[string]string
	authorized     map[string]struct{}
	managed        map[string]struct{}
	ready          func() bool
	count          int
}

func newDeviceEgress(setCredentials func([]devicebandwidth.Credential) error) *deviceEgress {
	return &deviceEgress{setCredentials: setCredentials, passwords: make(map[string]string)}
}

type deviceConfig struct {
	root       map[string]json.RawMessage
	outbounds  []map[string]json.RawMessage
	devices    map[string]int
	identities map[string]struct{}
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
	cfg := &deviceConfig{devices: make(map[string]int), identities: make(map[string]struct{})}
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
		id := strings.TrimPrefix(tag, devicebandwidth.OutboundPrefix)
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
		if _, duplicate := cfg.devices[id]; duplicate {
			return nil, fmt.Errorf("duplicate device egress for %q", id)
		}
		cfg.devices[id] = i
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
		Rules []struct {
			Type        string   `json:"type"`
			User        []string `json:"user"`
			InboundTag  []string `json:"inboundTag"`
			OutboundTag string   `json:"outboundTag"`
		} `json:"rules"`
	}
	if raw := cfg.root["routing"]; raw != nil {
		if err := json.Unmarshal(raw, &routing); err != nil {
			return nil, fmt.Errorf("invalid xray routing: %w", err)
		}
	}
	matched := make(map[string]bool)
	for _, inbound := range inbounds {
		_, limited := speedtier.ParseLimitTag(inbound.Tag)
		for _, user := range inbound.Settings.Clients {
			identity := user.ID
			if identity == "" {
				identity = user.Password
			}
			// Include non-VLESS clients in rollback authorization: an older
			// protocol must not resurrect a revoked credential either.
			cfg.identities[inbound.Protocol+"\x00"+inbound.Tag+"\x00"+identity+"\x00"+user.Email] = struct{}{}
			_, managed := cfg.devices[user.ID]
			if (limited || len(cfg.devices) > 0 && inbound.Protocol == "vless") && (inbound.Protocol != "vless" || !managed) {
				return nil, fmt.Errorf("controlled inbound %q has no device egress", inbound.Tag)
			}
			if inbound.Protocol != "vless" || !managed {
				continue
			}
			if user.Email != user.ID+"@proxima" {
				return nil, fmt.Errorf("device %q has invalid authenticated email", user.ID)
			}
			// Xray uses the first matching rule. Reject any earlier broad
			// inbound/user match that could silently route this client direct.
			found := false
			for _, rule := range routing.Rules {
				userMatch := len(rule.User) == 0 || containsString(rule.User, user.Email)
				inboundMatch := len(rule.InboundTag) == 0 || containsString(rule.InboundTag, inbound.Tag)
				if !userMatch || !inboundMatch {
					continue
				}
				if rule.Type == "field" && len(rule.User) == 1 && rule.User[0] == user.Email && rule.OutboundTag == devicebandwidth.OutboundPrefix+user.ID {
					found = true
				}
				break
			}
			if !found {
				return nil, fmt.Errorf("device %q lacks first-match authenticated egress routing", user.ID)
			}
			matched[user.ID] = true
		}
	}
	for id := range cfg.devices {
		if !matched[id] {
			return nil, fmt.Errorf("device egress %q has no authenticated client", id)
		}
	}
	return cfg, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
		_ = e.setCredentials(nil)
		return nil, err
	}
	if len(cfg.devices) > 0 && e.ready != nil && !e.ready() {
		e.authorized = nil
		e.managed = nil
		e.count = 0
		_ = e.setCredentials(nil)
		return nil, fmt.Errorf("local device egress listener is unavailable")
	}
	e.authorized = cfg.identities
	e.managed = make(map[string]struct{}, len(cfg.devices))
	for id := range cfg.devices {
		e.managed[id] = struct{}{}
	}
	for id := range e.passwords {
		if _, keep := cfg.devices[id]; !keep {
			delete(e.passwords, id)
		}
	}
	// Revoke first, before generating/admitting newly authorized credentials.
	kept := make([]devicebandwidth.Credential, 0, len(e.passwords))
	for id, password := range e.passwords {
		kept = append(kept, devicebandwidth.Credential{UUID: id, Password: password})
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
		credentials = append(credentials, devicebandwidth.Credential{UUID: id, Password: password})
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
