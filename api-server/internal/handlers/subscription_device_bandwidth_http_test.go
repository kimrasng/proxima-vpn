package handlers

import "testing"

func TestSubscriptionReality_limitedRelayRequiresAcknowledgedDeviceEgress(t *testing.T) {
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	if _, err := f.pool.Exec(t.Context(), `UPDATE plans SET speed_limit=50 WHERE id=$1`, f.planID); err != nil {
		t.Fatal(err)
	}
	f.applyDigest(t)
	if got := f.rawLinks(t); len(got) != 0 {
		t.Fatal("legacy shared-tier acknowledgment published controlled relay")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET shaping_mode='device_global_v1',shaping_ok=true WHERE id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	got := f.rawLinks(t)
	if len(got) != 1 || got[0].Hostname() != f.entryHost {
		t.Fatalf("device-limited relay withheld: %+v", got)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE nodes SET shaping_ok=false WHERE id=$1`, f.exitID); err != nil {
		t.Fatal(err)
	}
	if got := f.rawLinks(t); len(got) != 0 {
		t.Fatal("faileddeviceegress stilladvertised")
	}
}
