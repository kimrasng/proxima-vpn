package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

const limitedDeviceConfig = `{"inbounds":[{"protocol":"vless","tag":"vless-in","settings":{"clients":[{"id":"uuid-a","email":"uuid-a@proxima"}]}}],"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"device-egress-uuid-a","protocol":"socks","settings":{"servers":[{"address":"127.0.0.1","port":10086,"users":[{"user":"uuid-a","pass":"materialize-at-node"}]}]}}],"routing":{"rules":[{"type":"field","user":["uuid-a@proxima"],"outboundTag":"device-egress-uuid-a"}]}}`

func TestDeviceEgressMaterializesOnlyRuntimeSecrets(t *testing.T) {
	var credentials []devicebandwidth.Credential
	egress := newDeviceEgress(func(c []devicebandwidth.Credential) error { credentials = c; return nil })
	canonical := []byte(limitedDeviceConfig)
	state := newNodeState(canonical)
	hash := state.ConfigHash()
	runner := xray.NewXrayRunner(filepath.Join(t.TempDir(), "config.json"), "")
	runner.SetConfigTransform(egress.TransformConfig)
	if err := runner.WriteConfig(canonical); err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].UUID != "uuid-a" {
		t.Fatalf("unexpected credentials: %+v", credentials)
	}
	password := credentials[0].Password
	if len(password) < 32 || password == "uuid-a" || strings.Contains(password, "materialize") {
		t.Fatalf("not a random local password: %q", password)
	}
	runtime, err := os.ReadFile(runner.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(runtime, []byte(password)) || bytes.Contains(runtime, []byte("materialize-at-node")) {
		t.Fatalf("runtime configuration was not materialized: %s", runtime)
	}
	if state.ConfigHash() != hash || string(state.Config()) != limitedDeviceConfig || string(canonical) != limitedDeviceConfig {
		t.Fatal("materialization changed the canonical configuration/hash")
	}
	if err := runner.WriteConfig(canonical); err != nil {
		t.Fatal(err)
	}
	if credentials[0].Password != password {
		t.Fatal("password changed for an unchanged device")
	}
	backup, err := os.ReadFile(runner.ConfigPath() + ".prev")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(backup, []byte(password)) || string(backup) != limitedDeviceConfig {
		t.Fatal("backup is not canonical")
	}
	for _, path := range []string{runner.ConfigPath(), runner.ConfigPath() + ".prev"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", path, info.Mode().Perm())
		}
	}
	if _, err := runner.RestoreConfig(); err != nil {
		t.Fatal(err)
	}
	if credentials[0].Password != password {
		t.Fatal("authorized rollback changed password")
	}
}

func TestDeviceEgressRejectsInvalidDeclarationsAndRevokesCredentials(t *testing.T) {
	for name, canonical := range map[string]string{
		"uuid password":               strings.Replace(limitedDeviceConfig, "materialize-at-node", "uuid-a", 1),
		"remote endpoint":             strings.Replace(limitedDeviceConfig, "127.0.0.1", "example.org", 1),
		"wrong port":                  strings.Replace(limitedDeviceConfig, "10086", "1080", 1),
		"wrong username":              strings.Replace(limitedDeviceConfig, `"user":"uuid-a"`, `"user":"uuid-b"`, 1),
		"no authenticated routing":    strings.Replace(limitedDeviceConfig, `"user":["uuid-a@proxima"]`, `"user":["uuid-b@proxima"]`, 1),
		"earlier direct rule":         strings.Replace(limitedDeviceConfig, `"rules":[`, `"rules":[{"type":"field","inboundTag":["vless-in"],"outboundTag":"direct"},`, 1),
		"partial VLESS migration":     strings.Replace(limitedDeviceConfig, `"clients":[`, `"clients":[{"id":"uuid-b","email":"uuid-b@proxima"},`, 1),
		"missing limited declaration": `{"inbounds":[{"protocol":"vless","tag":"vless-reality-limit-10","settings":{"clients":[{"id":"uuid-a","email":"uuid-a@proxima"}]}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var credentials []devicebandwidth.Credential
			egress := newDeviceEgress(func(c []devicebandwidth.Credential) error { credentials = c; return nil })
			if _, err := egress.TransformConfig([]byte(limitedDeviceConfig)); err != nil {
				t.Fatal(err)
			}
			if _, err := egress.TransformConfig([]byte(canonical)); err == nil {
				t.Fatal("invalid declaration accepted")
			}
			if len(credentials) != 0 {
				t.Fatal("old credentials still authorized after invalid generation")
			}
			if err := egress.CanRestore([]byte(limitedDeviceConfig)); err == nil {
				t.Fatal("rollback reauthorized stale generation")
			}
		})
	}
}

func TestDeviceEgressRollbackCannotReviveUnrestrictedOrRevokedClients(t *testing.T) {
	unrestricted := []byte(`{"inbounds":[{"protocol":"vless","tag":"vless-in","settings":{"clients":[{"id":"uuid-a","email":"uuid-a@proxima"}]}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`)
	var credentials []devicebandwidth.Credential
	egress := newDeviceEgress(func(c []devicebandwidth.Credential) error { credentials = c; return nil })
	runner := xray.NewXrayRunner(filepath.Join(t.TempDir(), "config.json"), "")
	runner.SetConfigTransform(egress.TransformConfig)
	runner.SetConfigRestoreGuard(egress.CanRestore)
	if err := runner.WriteConfig(unrestricted); err != nil {
		t.Fatal(err)
	}
	if err := runner.WriteConfig([]byte(limitedDeviceConfig)); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RestoreConfig(); err == nil {
		t.Fatal("rollback revived unrestricted Freedom configuration")
	}
	runtime, err := os.ReadFile(runner.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(runtime, []byte("device-egress-uuid-a")) {
		t.Fatal("failed rollback overwrote limited runtime")
	}
	if err := runner.WriteConfig([]byte(`{"inbounds":[],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`)); err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 0 {
		t.Fatal("removed device credential not revoked")
	}
	if _, err := runner.RestoreConfig(); err == nil {
		t.Fatal("rollback re-admitted revoked device")
	}
}

func TestDeviceEgressCapabilityRequiresLiveListenerAndAppliedMetadata(t *testing.T) {
	egress := newDeviceEgress(func([]devicebandwidth.Credential) error { return nil })
	ready := false
	egress.ready = func() bool { return ready }
	state := newNodeState([]byte(limitedDeviceConfig))
	state.deviceCapability = egress.Capability
	if mode := state.ShapingMode(); mode != "" {
		t.Fatalf("premature capability: %q", mode)
	}
	if _, err := egress.TransformConfig([]byte(limitedDeviceConfig)); err == nil {
		t.Fatal("materialized config without a listener")
	}
	ready = true
	if _, err := egress.TransformConfig([]byte(limitedDeviceConfig)); err != nil {
		t.Fatal(err)
	}
	if mode := state.ShapingMode(); mode != "device_global_v1" {
		t.Fatalf("capability = %q", mode)
	}
	ready = false
	if mode := state.ShapingMode(); mode != "" {
		t.Fatalf("closed listener capability = %q", mode)
	}
	if ok, _, _ := state.Shaping(); ok {
		t.Fatal("closed listener reported healthy")
	}
}

func TestDeviceEgressTopologyIsUserScoped(t *testing.T) {
	// Swapping one device for another changes only per-device outbounds and
	// exact user rules, which the agent applies live over gRPC.
	if !sameLocalStructure([]byte(limitedDeviceConfig), []byte(strings.ReplaceAll(limitedDeviceConfig, "uuid-a", "uuid-b"))) {
		t.Fatal("device swap treated as structural; admissions would restart Xray")
	}
	// A device rule carrying any other condition is no longer user-scoped.
	widened := strings.Replace(limitedDeviceConfig, `"user":["uuid-a@proxima"]`, `"user":["uuid-a@proxima"],"network":"udp"`, 1)
	if sameLocalStructure([]byte(limitedDeviceConfig), []byte(widened)) {
		t.Fatal("device rule with an extra condition considered users-only")
	}
	// Non-device rules stay structural.
	denied := strings.Replace(limitedDeviceConfig, `"rules":[`, `"rules":[{"type":"field","inboundTag":["vless-in"],"outboundTag":"block"},`, 1)
	if sameLocalStructure([]byte(limitedDeviceConfig), []byte(denied)) {
		t.Fatal("new deny rule considered users-only")
	}
	var canonical map[string]json.RawMessage
	if err := json.Unmarshal([]byte(limitedDeviceConfig), &canonical); err != nil {
		t.Fatal(err)
	}
	canonical["inbounds"] = json.RawMessage(`[{"protocol":"vless","tag":"vless-in","settings":{"clients":[{"id":"uuid-a","email":"uuid-a@proxima","flow":"xtls-rprx-vision"}]}}]`)
	changed, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !sameLocalStructure([]byte(limitedDeviceConfig), changed) {
		t.Fatal("pure user change lost legacy incremental compatibility")
	}
}

func TestDeviceEgressRejectedWriteBlocksSupervisorAndUnsafeRollback(t *testing.T) {
	bad := []byte(strings.Replace(limitedDeviceConfig, "materialize-at-node", "uuid-a", 1))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(bad) }))
	defer server.Close()
	api := client.NewAPIClient(&config.AgentConfig{ServerURL: server.URL})
	egress := newDeviceEgress(func([]devicebandwidth.Credential) error { return nil })
	runner := xray.NewXrayRunner(filepath.Join(t.TempDir(), "config.json"), "")
	runner.SetConfigTransform(egress.TransformConfig)
	runner.SetConfigRestoreGuard(egress.CanRestore)
	if err := runner.WriteConfig([]byte(limitedDeviceConfig)); err != nil {
		t.Fatal(err)
	}
	state := newNodeState([]byte(limitedDeviceConfig))
	badState := newNodeState(bad)
	applyConfigDigest(t.Context(), api, runner, nil, state, client.ConfigDigest{Hash: badState.ConfigHash(), StructureHash: "changed", Users: []client.ConfigDigestUser{{InboundTag: "vless-in", UUID: "uuid-a", Email: "uuid-a@proxima"}}})
	if !state.restartBlocked {
		t.Fatal("supervisor can respawn stale disk policy")
	}
	if state.ConfigHash() != newNodeState([]byte(limitedDeviceConfig)).ConfigHash() {
		t.Fatal("rejected generation changed canonical state")
	}
	if ok, _, _ := state.Shaping(); ok {
		t.Fatal("failed apply reported healthy")
	}
}

func TestDeviceEgressFailedCredentialsCannotBeReportedReady(t *testing.T) {
	egress := newDeviceEgress(func([]devicebandwidth.Credential) error { return fmt.Errorf("closed") })
	egress.ready = func() bool { return true }
	if _, err := egress.TransformConfig([]byte(limitedDeviceConfig)); err == nil {
		t.Fatal("credential failure accepted")
	}
	if ok, _ := egress.Capability(); ok {
		t.Fatal("credential failure reported capability")
	}
}

func sourceDeviceConfig(t *testing.T, two bool) []byte {
	t.Helper()
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(limitedDeviceConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	var outbounds []map[string]json.RawMessage
	_ = json.Unmarshal(cfg["outbounds"], &outbounds)
	device := outbounds[1]
	outbounds = outbounds[:1]
	rules := []map[string]any{}
	keys := []string{"0123456789abcdef", "ABCDEF0123456789"}
	sources := []string{"192.0.2.1/32", "2001:db8::1/128"}
	for i, key := range keys {
		if i == 1 && !two {
			break
		}
		tag := "device-egress-uuid-a~" + key
		copy := make(map[string]json.RawMessage)
		for k, v := range device {
			copy[k] = v
		}
		copy["tag"], _ = json.Marshal(tag)
		outbounds = append(outbounds, copy)
		rules = append(rules, map[string]any{"type": "field", "user": []string{"uuid-a@proxima"}, "source": []string{sources[i]}, "outboundTag": tag})
	}
	outbounds = append(outbounds, map[string]json.RawMessage{"tag": json.RawMessage(`"block"`), "protocol": json.RawMessage(`"blackhole"`)})
	rules = append(rules, map[string]any{"type": "field", "user": []string{"uuid-a@proxima"}, "outboundTag": "block"})
	cfg["outbounds"], _ = json.Marshal(outbounds)
	cfg["routing"], _ = json.Marshal(map[string]any{"rules": rules})
	cfg["inbounds"] = json.RawMessage(`[{"protocol":"vless","tag":"vless-reality-limit-10","settings":{"clients":[{"id":"uuid-a","email":"uuid-a@proxima"}]}}]`)
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDeviceEgressSourceCredentialsStableAndIndependentlyRevoked(t *testing.T) {
	var credentials []devicebandwidth.Credential
	var rates map[string]int
	e := newDeviceEgress(func(c []devicebandwidth.Credential) error { credentials = c; return nil })
	e.setRates = func(r map[string]int) { rates = r }
	canonical := sourceDeviceConfig(t, true)
	original := append([]byte(nil), canonical...)
	runtime, err := e.TransformConfig(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 2 || credentials[0].UUID != "uuid-a" || credentials[1].UUID != "uuid-a" || credentials[0].Password == credentials[1].Password {
		t.Fatalf("unexpected credentials: %+v", credentials)
	}
	if len(rates) != 1 || rates["uuid-a"] != 10 {
		t.Fatalf("rates not shared by UUID: %v", rates)
	}
	if !bytes.Equal(canonical, original) || bytes.Contains(runtime, []byte(deviceEgressPlaceholder)) {
		t.Fatal("canonical modified or runtime not materialized")
	}
	passwords := make(map[string]string)
	for tag, pass := range e.passwords {
		passwords[tag] = pass
	}
	if _, err := e.TransformConfig(canonical); err != nil {
		t.Fatal(err)
	}
	for tag, pass := range passwords {
		if e.passwords[tag] != pass {
			t.Fatal("unchanged tag password rotated")
		}
	}
	if _, err := e.TransformConfig(sourceDeviceConfig(t, false)); err != nil {
		t.Fatal(err)
	}
	remaining := "device-egress-uuid-a~0123456789abcdef"
	if len(e.passwords) != 1 || e.passwords[remaining] != passwords[remaining] || len(credentials) != 1 || credentials[0].Password != passwords[remaining] {
		t.Fatal("removing one tag changed or revoked the surviving credential")
	}
	if err := e.CanRestore(canonical); err == nil {
		t.Fatal("rollback restored revoked source tag")
	}
}

func TestDeviceEgressRejectsUnsafeSourceRouting(t *testing.T) {
	canonical := string(sourceDeviceConfig(t, true))
	for name, bad := range map[string]string{
		"odd suffix":          strings.ReplaceAll(canonical, "~0123456789abcdef", "~0123456789abcdef0"),
		"empty suffix":        strings.ReplaceAll(canonical, "~0123456789abcdef", "~"),
		"short suffix":        strings.ReplaceAll(canonical, "~0123456789abcdef", "~0123456789abcde"),
		"long suffix":         strings.ReplaceAll(canonical, "~0123456789abcdef", "~"+strings.Repeat("a", 65)),
		"non hex":             strings.ReplaceAll(canonical, "~0123456789abcdef", "~0123456789abcdeg"),
		"multiple separators": strings.ReplaceAll(canonical, "~0123456789abcdef", "~0123456789abcdef~"),
		"unicode hex":         strings.ReplaceAll(canonical, "~0123456789abcdef", "~０123456789abcdef"),
		"broad IPv4":          strings.ReplaceAll(canonical, "192.0.2.1/32", "192.0.2.0/24"),
		"broad IPv6":          strings.ReplaceAll(canonical, "2001:db8::1/128", "2001:db8::/64"),
		"bare address":        strings.ReplaceAll(canonical, "192.0.2.1/32", "192.0.2.1"),
		"no deny":             strings.ReplaceAll(canonical, `"outboundTag":"block"`, `"outboundTag":"direct"`),
		"conditional deny":    strings.ReplaceAll(canonical, `"outboundTag":"block"`, `"outboundTag":"block","network":"tcp"`),
		"conditional source":  strings.ReplaceAll(canonical, `"source":["192.0.2.1/32"]`, `"source":["192.0.2.1/32"],"network":"tcp"`),
		"shadowed source":     strings.ReplaceAll(canonical, "2001:db8::1/128", "192.0.2.1/32"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDeviceConfig([]byte(bad)); err == nil {
				t.Fatal("unsafe source config accepted")
			}
		})
	}
	// Even a valid legacy declaration cannot act as a shared catchall after
	// source-qualified routes; unknown sources must be denied.
	var mixed map[string]json.RawMessage
	_ = json.Unmarshal([]byte(canonical), &mixed)
	var outbounds []json.RawMessage
	_ = json.Unmarshal(mixed["outbounds"], &outbounds)
	var legacy map[string]json.RawMessage
	_ = json.Unmarshal([]byte(limitedDeviceConfig), &legacy)
	var legacyOutbounds []json.RawMessage
	_ = json.Unmarshal(legacy["outbounds"], &legacyOutbounds)
	outbounds = append(outbounds, legacyOutbounds[1])
	mixed["outbounds"], _ = json.Marshal(outbounds)
	mixed["routing"] = json.RawMessage(strings.ReplaceAll(string(mixed["routing"]), `"outboundTag":"block"`, `"outboundTag":"device-egress-uuid-a"`))
	bad, _ := json.Marshal(mixed)
	if _, err := parseDeviceConfig(bad); err == nil {
		t.Fatal("shared legacy catchall bypass accepted")
	}
	for _, n := range []int{16, 32, 64} {
		if _, _, ok := parseLocalDeviceTag("device-egress-uuid-a~" + strings.Repeat("f", n)); !ok {
			t.Fatalf("valid %d-character ASCII hex key rejected", n)
		}
	}
}
