package server

import (
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/connectionhistory"
	"github.com/stretchr/testify/require"
)

func TestHistoryUDPDoesNotGuessAliasedReplies(t *testing.T) {
	// Given two original destinations that may reach the same full-cone peer.
	o, _ := historyFixture(t)
	tunnel, err := o.NewTunnel(connectionhistory.TunnelInfo{Protocol: "hysteria2", InboundTag: "test", LabelKind: "hysteria_auth_id", Label: "alice"})
	require.NoError(t, err)
	u := newUDPHistory(o, tunnel)
	u.upload("127.0.0.1:1234", 10)
	u.upload("localhost:1234", 20)
	// When a reply arrives, the domain's resolution is not proven by this hook.
	u.download("127.0.0.1:1234", 7)
	u.close("transport_close")
	// Then it belongs to one response-only flow, never either guessed request.
	var requested, response int
	for _, e := range historyEvents(t, o) {
		if e.Kind != "flow_end" {
			continue
		}
		if e.Destination.RequestedHost == nil {
			response++
			require.EqualValues(t, 7, *e.Bytes.Download)
			require.Equal(t, "127.0.0.1", *e.Destination.ObservedIP)
			require.Equal(t, "observed", e.Quality.Destination)
		} else {
			requested++
			require.Zero(t, *e.Bytes.Download)
		}
	}
	require.Equal(t, 2, requested)
	require.Equal(t, 1, response)
}

func TestHistoryUDPRedirectKeepsOriginalTargets(t *testing.T) {
	// Given two requests redirected to one endpoint in the same association.
	o, _ := historyFixture(t)
	tunnel, err := o.NewTunnel(connectionhistory.TunnelInfo{Protocol: "hysteria2", InboundTag: "test", LabelKind: "hysteria_auth_id", Label: "alice"})
	require.NoError(t, err)
	u := newUDPHistory(o, tunnel)
	u.upload("first.test:443", 11)
	u.upload("second.test:443", 13)
	u.effective("first.test:443", "127.0.0.1:1234")
	u.effective("second.test:443", "127.0.0.1:1234")
	// When the common endpoint replies, identity remains exact but target
	// attribution is ambiguous even though both requests' effective IP is known.
	u.download("127.0.0.1:1234", 17)
	u.close("idle_timeout")
	// Then requested hosts remain unchanged and the reply is counted only once.
	var downloaded uint64
	for _, e := range historyEvents(t, o) {
		if e.Kind != "flow_end" {
			continue
		}
		downloaded += uint64(*e.Bytes.Download)
		if e.Destination.RequestedHost != nil {
			require.Contains(t, []string{"first.test", "second.test"}, *e.Destination.RequestedHost)
			require.Equal(t, "127.0.0.1", *e.Destination.EffectiveHost)
			require.Zero(t, *e.Bytes.Download)
		}
	}
	require.Equal(t, uint64(17), downloaded)
}
