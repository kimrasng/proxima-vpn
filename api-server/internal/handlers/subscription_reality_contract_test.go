package handlers

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"gopkg.in/yaml.v3"
)

func TestRealityProjection_directMultipleListenersAndSecondRelayPort(t *testing.T) {
	// Given two enabled listener ports independent of the legacy node port.
	direct := exitNode()
	direct.RealityPorts = []int{2443, 3443}
	relay := relayedTo("entry.example.test", 7000, 3443)
	relay.RealityPorts = direct.RealityPorts
	// When direct and relayed profiles are projected.
	infos := buildNodeInfoList([]subscriptionNode{direct, relay}, "uuid-1", 0, nil, nil)
	// Then direct publishes both ports in order and the relay uses its chain port.
	if len(infos) < 3 || infos[0].Port != 2443 || infos[1].Port != 3443 || infos[0].Name == infos[1].Name || infos[len(infos)-1].Port != 7000 || infos[len(infos)-1].ServerName != direct.RealitySNI {
		t.Fatalf("multiple listener projection: %+v", infos)
	}
}

func TestRealityProjection_speedTierUsesGeneratedPort(t *testing.T) {
	// Given a speed-limited direct node with a shared listener on a different port,
	// whose agent has acknowledged per-device egress (required for limited plans).
	node := exitNode()
	node.DeviceBandwidthReady = true
	node.RealityPorts = []int{3443}
	node.GeneratedRealityPorts = []int{3443, 20050}
	// When the limited plan is projected.
	infos := buildNodeInfoList([]subscriptionNode{node}, "uuid-1", 50, nil, nil)
	// Then only its dedicated tier port is advertised.
	if len(infos) != 1 || infos[0].Port != 20050 {
		t.Fatalf("speed tier projection: %+v", infos)
	}
}

func (f subscriptionHTTPFixture) rawLinks(t *testing.T) []*url.URL {
	t.Helper()
	response, err := f.app.Test(httptest.NewRequest(fiber.MethodGet, f.path, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("raw status: %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) == 0 {
		return nil
	}
	var links []*url.URL
	for _, line := range strings.Split(string(decoded), "\n") {
		link, err := url.Parse(line)
		if err != nil {
			t.Fatal(err)
		}
		links = append(links, link)
	}
	return links
}

func (f subscriptionHTTPFixture) applyDigest(t *testing.T) {
	t.Helper()
	digest, err := services.NewXrayConfigService(f.pool).GenerateDigest(t.Context(), f.exitID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET config_hash=$2 WHERE id=$1`, f.exitID, digest.Hash); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionReality_withholdsValidButUnappliedConfig(t *testing.T) {
	// Given a valid SNI but an agent reporting a different applied hash.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET config_hash='old-config' WHERE id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	// When the subscription is requested.
	links := f.rawLinks(t)
	// Then no unsafe Reality profile is advertised.
	if len(links) != 0 {
		t.Fatalf("unapplied Reality links: %v", links)
	}
}

func TestSubscriptionReality_usesActualListenerPortInsteadOfStaleNodePort(t *testing.T) {
	// Given an enabled listener while nodes.port remains stale.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE inbounds SET port=3443 WHERE node_id=$1 AND protocol='vless_reality'`, f.exitID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE node_chains SET exit_port=3443 WHERE id=$1`, f.chainID); err != nil {
		t.Fatal(err)
	}
	f.applyDigest(t)
	// When the relay subscription is requested.
	links := f.rawLinks(t)
	// Then the actual listener is selected and the entry port is dialed.
	if len(links) != 1 || links[0].Hostname() != f.entryHost || links[0].Port() != strconv.Itoa(f.entryPort) || links[0].Query().Get("sni") != "reality.example.test" {
		t.Fatalf("second Reality listener projection: %v", links)
	}
}

func TestSubscriptionReality_explicitEntryUsesManagedHostname(t *testing.T) {
	// Given an explicit Entry whose managed hostname differs from the saved chain host.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE node_chains SET relay_pool_id=NULL, entry_node_id=$2 WHERE id=$1`, f.chainID, f.relayID); err != nil {
		t.Fatal(err)
	}
	managed := "live-entry.example.test"
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO managed_entry_dns (owner_node_id, node_id, hostname, desired_action) VALUES ($1,$1,$2,'present')`, f.relayID, managed); err != nil {
		t.Fatal(err)
	}
	// When a raw profile is requested.
	links := f.rawLinks(t)
	// Then it dials the live managed name, never the chain's saved name.
	if len(links) != 1 || links[0].Hostname() != managed || links[0].Port() != strconv.Itoa(f.entryPort) {
		t.Fatalf("explicit Entry links: %v", links)
	}
}

func TestSubscriptionReality_explicitEntryWithoutManagedHostnameIsOmitted(t *testing.T) {
	// Given an explicit Entry without a managed hostname, with a saved chain host.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE node_chains SET relay_pool_id=NULL, entry_node_id=$2 WHERE id=$1`, f.chainID, f.relayID); err != nil {
		t.Fatal(err)
	}
	// When the raw subscription is requested.
	links := f.rawLinks(t)
	// Then no fallback host is advertised.
	if len(links) != 0 {
		t.Fatalf("missing managed hostname links: %v", links)
	}
}

func TestSubscriptionReality_withholdsInvalidAgentOrSNIState(t *testing.T) {
	for _, testCase := range []struct{ name, sql string }{
		{"not running", `UPDATE nodes SET xray_running=false WHERE id=$1`},
		{"stale heartbeat", `UPDATE nodes SET last_seen=NOW()-INTERVAL '41 seconds' WHERE id=$1`},
		{"offline", `UPDATE nodes SET status='offline' WHERE id=$1`},
		{"conflicting SNI", `UPDATE nodes SET reality_sni_status='conflict', reality_sni_error_code='listener_mismatch' WHERE id=$1`},
		{"empty SNI", `UPDATE nodes SET reality_client_sni=NULL, reality_sni_status=NULL, reality_sni_source=NULL WHERE id=$1`},
		{"invalid SNI", `UPDATE nodes SET reality_client_sni='192.0.2.1' WHERE id=$1`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Given an otherwise applied Exit with one invalid publication signal.
			f := newSubscriptionHTTPFixture(t, chainTestDB(t))
			if _, err := f.pool.Exec(t.Context(), testCase.sql, f.exitID); err != nil {
				t.Fatal(err)
			}
			// When the subscription is requested.
			links := f.rawLinks(t)
			// Then Reality is withheld.
			if len(links) != 0 {
				t.Fatalf("unsafe Reality links: %v", links)
			}
		})
	}
}

func TestSubscriptionReality_omitsNonRealityTargetAndPreservesOtherProtocol(t *testing.T) {
	// Given an enabled non-Reality target and a stale Reality address.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE inbounds SET protocol='shadowsocks', port=8388, settings='{"method":"2022-blake3-aes-128-gcm","password":"secret"}' WHERE node_id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE node_chains SET exit_port=8388 WHERE id=$1`, f.chainID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET config_hash='old-config' WHERE id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	// When the subscription is requested.
	links := f.rawLinks(t)
	// Then the unrelated protocol still publishes.
	if len(links) != 1 || links[0].Scheme != "ss" || links[0].Hostname() != f.entryHost {
		t.Fatalf("other protocol links: %v", links)
	}
}

func TestSubscriptionReality_formatsShareCanonicalSNI(t *testing.T) {
	// Given a valid canonical SNI on an applied legacy listener.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	// When each public format is requested.
	raw := f.rawLinks(t)
	if len(raw) != 1 {
		t.Fatalf("raw links: %v", raw)
	}
	get := func(format string) []byte {
		t.Helper()
		response, err := f.app.Test(httptest.NewRequest(fiber.MethodGet, f.path+"?format="+format, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != fiber.StatusOK {
			t.Fatalf("%s status %d: %s", format, response.StatusCode, body)
		}
		return body
	}
	var clash struct {
		Proxies []struct {
			Server string `yaml:"server"`
			Port   int    `yaml:"port"`
			SNI    string `yaml:"servername"`
		} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(get("clash"), &clash); err != nil {
		t.Fatal(err)
	}
	var singbox struct {
		Outbounds []struct {
			Type   string `json:"type"`
			Server string `json:"server"`
			Port   int    `json:"server_port"`
			TLS    struct {
				SNI string `json:"server_name"`
			} `json:"tls"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(get("singbox"), &singbox); err != nil {
		t.Fatal(err)
	}
	// Then all three carry the same Exit SNI, Entry hostname and port.
	if raw[0].Query().Get("sni") != "reality.example.test" || len(clash.Proxies) < 1 || clash.Proxies[0].SNI != "reality.example.test" || clash.Proxies[0].Server != f.entryHost || clash.Proxies[0].Port != f.entryPort {
		t.Fatalf("raw/clash parity: %v / %+v", raw, clash.Proxies)
	}
	var vless *struct {
		Type   string `json:"type"`
		Server string `json:"server"`
		Port   int    `json:"server_port"`
		TLS    struct {
			SNI string `json:"server_name"`
		} `json:"tls"`
	}
	for i := range singbox.Outbounds {
		if singbox.Outbounds[i].Type == "vless" {
			vless = &singbox.Outbounds[i]
		}
	}
	if vless == nil || vless.TLS.SNI != "reality.example.test" || vless.Server != f.entryHost || vless.Port != f.entryPort {
		t.Fatalf("singbox parity: %+v", singbox.Outbounds)
	}
}
