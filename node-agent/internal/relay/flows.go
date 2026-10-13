package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// listFlows is read-only: observation must never reset or consume flow keys.
var listFlows = func() ([]byte, error) {
	cmd := exec.Command("nft", "-j", "list", "set", "inet", tableName, "client_flows")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read relay flows: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return data, nil
}

// Flows returns the recently observed original-direction TCP flows. A missing
// observer is an error, not an empty report: the server must not enforce from
// incomplete evidence. A successfully installed policy without TCP routes has
// no flows to observe.
func (m *Manager) Flows() ([]nodeprov.RelayFlow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.applied == "" {
		return nil, fmt.Errorf("relay policy not installed")
	}
	if !strings.Contains(m.applied, "set client_flows {") {
		return []nodeprov.RelayFlow{}, nil
	}
	data, err := listFlows()
	if err != nil {
		return nil, err
	}
	return parseFlows(data)
}

func parseFlows(data []byte) ([]nodeprov.RelayFlow, error) {
	var document struct {
		Nftables []struct {
			Set *struct {
				Family   string `json:"family"`
				Table    string `json:"table"`
				Name     string `json:"name"`
				Elements []struct {
					Element struct {
						Value struct {
							Concat []json.RawMessage `json:"concat"`
						} `json:"val"`
					} `json:"elem"`
				} `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode relay flows: %w", err)
	}
	out := make([]nodeprov.RelayFlow, 0)
	found := false
	seen := make(map[nodeprov.RelayFlow]bool)
	for _, object := range document.Nftables {
		set := object.Set
		if set == nil || set.Family != "inet" || set.Table != tableName || set.Name != "client_flows" {
			continue
		}
		found = true
		for _, item := range set.Elements {
			parts := item.Element.Value.Concat
			if len(parts) != 5 {
				return nil, fmt.Errorf("invalid relay flow tuple length %d", len(parts))
			}
			var flow nodeprov.RelayFlow
			targets := []any{&flow.PeerIP, &flow.PeerPort, &flow.ExitIP, &flow.ExitPort, &flow.ClientIP}
			for i, target := range targets {
				if err := json.Unmarshal(parts[i], target); err != nil {
					return nil, fmt.Errorf("decode relay flow field %d: %w", i, err)
				}
			}
			if err := flow.Validate(); err != nil {
				return nil, err
			}
			if !seen[flow] {
				out = append(out, flow)
				seen[flow] = true
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("relay flow set absent from nft response")
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.PeerIP != b.PeerIP {
			return a.PeerIP < b.PeerIP
		}
		if a.PeerPort != b.PeerPort {
			return a.PeerPort < b.PeerPort
		}
		if a.ExitIP != b.ExitIP {
			return a.ExitIP < b.ExitIP
		}
		if a.ExitPort != b.ExitPort {
			return a.ExitPort < b.ExitPort
		}
		return a.ClientIP < b.ClientIP
	})
	return out, nil
}
