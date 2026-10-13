package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func portDeviceConfig(t *testing.T) string {
	t.Helper()
	cfg := string(sourceDeviceConfig(t, true))
	cfg = strings.Replace(cfg, `"source":["192.0.2.1/32"]`, `"source":["192.0.2.1/32"],"sourcePort":["41001"]`, 1)
	cfg = strings.Replace(cfg, `"source":["2001:db8::1/128"]`, `"source":["192.0.2.1/32"],"sourcePort":["41002"]`, 1)
	return cfg
}

func TestDeviceEgressSameSourceDistinctPorts(t *testing.T) {
	var credentials []devicebandwidth.Credential
	e := newDeviceEgress(func(c []devicebandwidth.Credential) error { credentials = c; return nil })
	canonical := []byte(portDeviceConfig(t))
	cfg, err := parseDeviceConfig(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.deviceSourcePorts) != 2 {
		t.Fatalf("source port bindings: %v", cfg.deviceSourcePorts)
	}
	runtime, err := e.TransformConfig(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 2 || credentials[0].UUID != "uuid-a" || credentials[1].UUID != "uuid-a" || credentials[0].Password == credentials[1].Password {
		t.Fatalf("unexpected credentials: %+v", credentials)
	}
	for tag, port := range cfg.deviceSourcePorts {
		if port != 41001 && port != 41002 || cfg.deviceSources[tag] != "192.0.2.1" {
			t.Fatalf("invalid binding %s: %s:%d", tag, cfg.deviceSources[tag], port)
		}
		if e.passwords[tag] == "" {
			t.Fatalf("missing password for %s", tag)
		}
	}
	var materialized struct {
		Outbounds []struct {
			Tag      string `json:"tag"`
			Settings struct {
				Servers []struct {
					Users []struct {
						Pass string `json:"pass"`
					} `json:"users"`
				} `json:"servers"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(runtime, &materialized); err != nil {
		t.Fatal(err)
	}
	for _, outbound := range materialized.Outbounds {
		if pass := e.passwords[outbound.Tag]; pass != "" && outbound.Settings.Servers[0].Users[0].Pass != pass {
			t.Fatalf("outbound %s password not materialized", outbound.Tag)
		}
	}
	if _, err := e.TransformConfig(canonical); err != nil {
		t.Fatal(err)
	}
	for _, credential := range credentials {
		if credential.SourceAddress != "192.0.2.1" {
			t.Fatalf("unexpected source admission: %+v", credential)
		}
	}
}

func TestDeviceEgressSessionTokenBindsCredentialAndExactRoute(t *testing.T) {
	token := strings.Repeat("a", 64)
	canonical := portDeviceConfig(t)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(canonical), &cfg); err != nil {
		t.Fatal(err)
	}
	outbounds := cfg["outbounds"].([]any)
	routing := cfg["routing"].(map[string]any)
	rules := routing["rules"].([]any)
	oldTag := "device-egress-uuid-a~0123456789abcdef"
	newTag := "device-egress-uuid-a~" + token
	for _, item := range outbounds {
		entry := item.(map[string]any)
		if entry["tag"] == oldTag {
			entry["tag"] = newTag
		}
	}
	for _, item := range rules {
		rule := item.(map[string]any)
		if rule["outboundTag"] == oldTag {
			rule["outboundTag"] = newTag
			rule["sessionToken"] = token
			rule["ruleTag"] = "session-" + token
		}
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	canonical = string(encoded)
	var credentials []devicebandwidth.Credential
	e := newDeviceEgress(func(got []devicebandwidth.Credential) error {
		credentials = append([]devicebandwidth.Credential(nil), got...)
		return nil
	})
	if _, err := e.TransformConfig([]byte(canonical)); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, credential := range credentials {
		if credential.UUID == "uuid-a" && credential.SessionToken == token {
			found = true
		}
	}
	if !found {
		t.Fatalf("session token not bound to materialized egress credentials: %+v", credentials)
	}
	for name, bad := range map[string]string{
		"tag mismatch":      strings.Replace(canonical, "session-"+token, "wrong", 1),
		"outbound mismatch": strings.Replace(canonical, "device-egress-uuid-a~"+token, "device-egress-uuid-a~"+strings.Repeat("b", 64), 1),
		"token malformed":   strings.Replace(canonical, token, strings.Repeat("z", 64), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDeviceConfig([]byte(bad)); err == nil {
				t.Fatal("invalid session-qualified configuration accepted")
			}
		})
	}
}

func TestDeviceEgressRejectsInvalidSourcePorts(t *testing.T) {
	canonical := portDeviceConfig(t)
	for name, bad := range map[string]string{
		"zero":                    strings.Replace(canonical, `"sourcePort":["41001"]`, `"sourcePort":["0"]`, 1),
		"range":                   strings.Replace(canonical, `"sourcePort":["41001"]`, `"sourcePort":["41001-41002"]`, 1),
		"multiple":                strings.Replace(canonical, `"sourcePort":["41001"]`, `"sourcePort":["41001","41002"]`, 1),
		"empty":                   strings.Replace(canonical, `"sourcePort":["41001"]`, `"sourcePort":[]`, 1),
		"missing source":          strings.Replace(canonical, `"source":["192.0.2.1/32"],"sourcePort":["41001"]`, `"sourcePort":["41001"]`, 1),
		"shadowed port":           strings.Replace(canonical, `"sourcePort":["41002"]`, `"sourcePort":["41001"]`, 1),
		"source-only after port":  strings.Replace(canonical, `,"sourcePort":["41002"]`, ``, 1),
		"source-only before port": strings.Replace(canonical, `,"sourcePort":["41001"]`, ``, 1),
		"port on deny":            strings.Replace(canonical, `"outboundTag":"block"`, `"outboundTag":"block","sourcePort":["41003"]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDeviceConfig([]byte(bad)); err == nil {
				t.Fatal("unsafe source port config accepted")
			}
		})
	}
}
