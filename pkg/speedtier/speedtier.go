// Package speedtier defines how per-plan speed limits map to dedicated Xray
// VLESS Reality inbound ports and tags. It is the single source of truth shared
// between the API server (which generates node Xray configs and client
// subscriptions) and the node agent (which applies tc bandwidth shaping).
//
// A plan with a speed limit of M Mbps gets its own VLESS Reality inbound on a
// dedicated port (PortBase+M) tagged so the node agent can rate-limit that port
// with tc. Unlimited clients keep using the node's main port. Speed-limited
// clients are only provisioned on their dedicated VLESS inbound (never on the
// shared vmess/trojan/shadowsocks inbounds) so the limit cannot be bypassed.
package speedtier

import (
	"strconv"
	"strings"
)

const (
	// PortBase is the first port used for speed-limited VLESS Reality inbounds.
	PortBase = 20000
	// MaxMbps clamps unreasonable speed values to keep ports in a sane range.
	MaxMbps = 2000
	// TagUnlimited is the tag of the default (unlimited) VLESS Reality inbound.
	TagUnlimited = "vless-reality"

	tagLimitPrefix = "vless-reality-limit-"
)

// clamp bounds mbps to [1, MaxMbps].
func clamp(mbps int) int {
	if mbps < 1 {
		return 1
	}
	if mbps > MaxMbps {
		return MaxMbps
	}
	return mbps
}

// VlessPort returns the VLESS Reality port a client with the given speed limit
// should connect to. Unlimited clients (mbps <= 0) use the node's main port.
func VlessPort(nodePort, mbps int) int {
	if mbps <= 0 {
		return nodePort
	}
	return PortBase + clamp(mbps)
}

// Tag returns the inbound tag for the given speed limit.
func Tag(mbps int) string {
	if mbps <= 0 {
		return TagUnlimited
	}
	return tagLimitPrefix + strconv.Itoa(clamp(mbps))
}

// ParseLimitTag extracts the Mbps limit from a tag produced by Tag. ok is false
// for the unlimited tag or any tag not produced by this package.
func ParseLimitTag(tag string) (mbps int, ok bool) {
	if !strings.HasPrefix(tag, tagLimitPrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(tag, tagLimitPrefix))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
