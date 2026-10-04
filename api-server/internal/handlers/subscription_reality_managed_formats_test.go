package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/yaml.v3"
)

func TestSubscriptionReality_explicitManagedEntryFormatsUseExitSNI(t *testing.T) {
	// Given a live managed Entry name distinct from the saved chain host and Exit SNI.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	managedHost := "live-" + f.relayID + ".example.test"
	const exitSNI = "exit-listener.example.test"
	if _, err := f.pool.Exec(t.Context(), `UPDATE node_chains SET relay_pool_id=NULL, entry_node_id=$2 WHERE id=$1`, f.chainID, f.relayID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO managed_entry_dns (owner_node_id, node_id, hostname, desired_action) VALUES ($1,$1,$2,'present')`, f.relayID, managedHost); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE inbounds SET settings=$2::jsonb WHERE node_id=$1 AND protocol='vless_reality'`, f.exitID, `{"dest":"exit-listener.example.test:443","server_names":["exit-listener.example.test"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET reality_client_sni=$2 WHERE id=$1`, f.exitID, exitSNI); err != nil {
		t.Fatal(err)
	}
	f.applyDigest(t)

	// When each public format is requested, then its VLESS endpoint uses the live
	// Entry name and port but the Exit-owned listener SNI.
	t.Run("raw", func(t *testing.T) {
		links := f.rawLinks(t)
		if len(links) != 1 || links[0].Scheme != "vless" || links[0].Hostname() != managedHost ||
			links[0].Port() != strconv.Itoa(f.entryPort) || links[0].Query().Get("sni") != exitSNI {
			t.Fatalf("raw managed endpoint: %v", links)
		}
	})
	for _, format := range []string{"clash", "singbox"} {
		t.Run(format, func(t *testing.T) {
			response, err := f.app.Test(httptest.NewRequest(fiber.MethodGet, f.path+"?format="+format, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != fiber.StatusOK {
				t.Fatalf("%s status %d: %s", format, response.StatusCode, body)
			}
			switch format {
			case "clash":
				var config struct {
					Proxies []struct {
						Type   string `yaml:"type"`
						Server string `yaml:"server"`
						Port   int    `yaml:"port"`
						SNI    string `yaml:"servername"`
					} `yaml:"proxies"`
				}
				if err := yaml.Unmarshal(body, &config); err != nil {
					t.Fatal(err)
				}
				if len(config.Proxies) == 0 || config.Proxies[0].Type != "vless" || config.Proxies[0].Server != managedHost ||
					config.Proxies[0].Port != f.entryPort || config.Proxies[0].SNI != exitSNI {
					t.Fatalf("clash managed endpoint: %+v", config.Proxies)
				}
			case "singbox":
				var config struct {
					Outbounds []struct {
						Type   string `json:"type"`
						Server string `json:"server"`
						Port   int    `json:"server_port"`
						TLS    struct {
							SNI string `json:"server_name"`
						} `json:"tls"`
					} `json:"outbounds"`
				}
				if err := json.Unmarshal(body, &config); err != nil {
					t.Fatal(err)
				}
				var found bool
				for _, outbound := range config.Outbounds {
					if outbound.Type == "vless" {
						found = true
						if outbound.Server != managedHost || outbound.Port != f.entryPort || outbound.TLS.SNI != exitSNI {
							t.Fatalf("singbox managed endpoint: %+v", outbound)
						}
					}
				}
				if !found {
					t.Fatalf("singbox missing VLESS endpoint: %+v", config.Outbounds)
				}
			}
		})
	}
}
