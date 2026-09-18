// Package xrayver expresses the minimum Xray-core the panel can drive, and
// compares reported versions against it.
//
// The panel reads per-user online IPs through the stats RPC GetStatsOnlineIpList
// and enables policy.statsUserOnline to populate it. Both are recent additions
// upstream: on an older core the concurrency figures are simply absent, and
// nothing else about the node looks wrong. install.sh takes releases/latest, so
// which version a node ends up on depends on the day it was installed.
package xrayver

import (
	"fmt"
	"strconv"
	"strings"
)

// Minimum is the oldest Xray-core known to serve GetStatsOnlineIpList.
const Minimum = "v25.1.1"

// Compare orders two Xray-core versions, returning -1, 0 or 1. Versions are
// dot-separated numbers with an optional leading "v" (upstream tags v26.3.27).
// A part that is not a number sorts as 0 rather than failing: refusing to
// compare would be read as "too old" and lock out a node over a suffix.
func Compare(a, b string) int {
	pa, pb := parts(a), parts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// AtLeastMinimum reports whether version satisfies Minimum. An empty version -
// a node that has not reported yet, or one whose xray binary could not be
// queried - is accepted: treating unknown as too old would reject nodes for
// saying nothing.
func AtLeastMinimum(version string) bool {
	if strings.TrimSpace(version) == "" {
		return true
	}
	return Compare(version, Minimum) >= 0
}

// Explain describes why a version is unacceptable, for an operator who has to
// act on it.
func Explain(version string) string {
	return fmt.Sprintf(
		"xray-core %s is older than the required %s; per-user connection counting needs GetStatsOnlineIpList",
		version, Minimum,
	)
}

func parts(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil
	}
	// Upstream tags are plain numbers, but a build string can trail the version
	// ("26.3.27-1"); everything from the first dash on is not part of ordering.
	if dash := strings.IndexByte(v, '-'); dash >= 0 {
		v = v[:dash]
	}

	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}
