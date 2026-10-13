package xray

import (
	"encoding/json"
	"fmt"
	"sort"
)

// tunnelPorts extracts only managed VLESS listeners. The initial observation
// contract excludes the local gRPC/SOCKS listeners and other VPN protocols.
func tunnelPorts(config []byte) ([]int, error) {
	var document struct {
		Inbounds []struct {
			Protocol string `json:"protocol"`
			Port     int    `json:"port"`
			Settings struct {
				Clients []struct {
					Email string `json:"email"`
				} `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(config, &document); err != nil {
		return nil, err
	}
	ports := make(map[int]bool)
	for _, inbound := range document.Inbounds {
		if inbound.Protocol != "vless" {
			continue
		}
		if inbound.Port < 1 || inbound.Port > 65535 {
			return nil, fmt.Errorf("invalid VLESS listener port")
		}
		ports[inbound.Port] = true
	}
	out := make([]int, 0, len(ports))
	for port := range ports {
		out = append(out, port)
	}
	sort.Ints(out)
	return out, nil
}
