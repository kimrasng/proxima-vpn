package handlers

import (
	"strconv"
	"testing"
)

func TestSubscriptionReality_zeroEnabledRowsUseLegacyNodePort(t *testing.T) {
	// Given zero enabled inbounds and an explicitly valid canonical legacy SNI.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE inbounds SET enabled=false WHERE node_id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET reality_client_sni='www.cloudflare.com' WHERE id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	f.applyDigest(t)
	// When a chain forwarding to nodes.port is projected.
	links := f.rawLinks(t)
	// Then the legacy listener is available without inferring a different SNI.
	if len(links) != 1 || links[0].Port() != strconv.Itoa(f.entryPort) || links[0].Query().Get("sni") != "www.cloudflare.com" {
		t.Fatalf("legacy listener links: %v", links)
	}
}

func TestSubscriptionReality_mismatchedSNIWithValidStatusIsOmitted(t *testing.T) {
	// Given a valid-shaped SNI marked valid but absent from the applied listener.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET reality_client_sni='different.example.test' WHERE id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	// When the profile is requested.
	links := f.rawLinks(t)
	// Then validation status alone cannot publish an incompatible name.
	if len(links) != 0 {
		t.Fatalf("incompatible SNI links: %v", links)
	}
}

func TestSubscriptionReality_normalizesCanonicalNameWithoutFallback(t *testing.T) {
	// Given a canonical SNI using an upper-case spelling and terminal root dot.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET reality_client_sni='REALITY.EXAMPLE.TEST.' WHERE id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	// When the profile is requested.
	links := f.rawLinks(t)
	// Then its SNI is the normalized Exit name.
	if len(links) != 1 || links[0].Query().Get("sni") != "reality.example.test" {
		t.Fatalf("normalized SNI links: %v", links)
	}
}

func TestSubscriptionReality_directSpeedTierUsesAppliedGeneratedPort(t *testing.T) {
	// Given a direct chain on a speed-limited plan with a freshly applied tier config.
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE node_chains SET relay_pool_id=NULL, entry_port=NULL WHERE id=$1`, f.chainID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET publish_direct=true, shaping_mode='device_global_v1', shaping_ok=true WHERE id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE plans SET speed_limit=50 WHERE id=$1`, f.planID); err != nil {
		t.Fatal(err)
	}
	f.applyDigest(t)
	// When the direct raw subscription is requested.
	links := f.rawLinks(t)
	// Then it dials only the generated controlled listener after device-budget acknowledgment.
	if len(links) != 1 || links[0].Port() != "20050" || links[0].Hostname() != f.exitAddress || links[0].Query().Get("sni") != "reality.example.test" {
		t.Fatalf("direct tier links: %v", links)
	}
}
