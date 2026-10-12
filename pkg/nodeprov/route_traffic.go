package nodeprov

import "fmt"

// MaxRouteTrafficEntries bounds one report. An Entry has at most one counter
// pair per claimed entry port and protocol, so this is far above any real
// policy while still keeping a malformed report from growing without limit.
const MaxRouteTrafficEntries = 4096

// RouteTraffic is the forwarded byte count an Entry measured for one relay
// rule and protocol since its previous report. Up is the client-to-Exit
// direction, Down the reply direction. Bytes are what the Entry forwarded on
// the wire (IP and transport headers included), not tunnel payload.
//
// The route is identified by its entry port and a single protocol: entry port
// claims are unique per protocol across the fleet, so the pair names exactly
// one chain without the agent knowing chain IDs.
type RouteTraffic struct {
	EntryPort int       `json:"entry_port"`
	Transport Transport `json:"transport"`
	UpBytes   int64     `json:"up_bytes"`
	DownBytes int64     `json:"down_bytes"`
}

func (r RouteTraffic) Validate() error {
	if err := ValidatePort(r.EntryPort); err != nil {
		return fmt.Errorf("entry port: %w", err)
	}
	if r.Transport != TransportTCP && r.Transport != TransportUDP {
		return fmt.Errorf("route traffic transport must be tcp or udp, got %q", r.Transport)
	}
	if r.UpBytes < 0 || r.DownBytes < 0 {
		return fmt.Errorf("route traffic bytes must not be negative")
	}
	return nil
}
