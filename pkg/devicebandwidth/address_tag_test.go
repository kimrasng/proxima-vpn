package devicebandwidth

import "testing"

func TestAddressOutboundTagStableAndExact(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000001"
	tag, prefix, err := AddressOutboundTag(id, "203.0.113.2")
	if err != nil || prefix != "203.0.113.2/32" || !IsDeviceOutboundTag(tag) {
		t.Fatalf("tag=%s prefix=%s err=%v", tag, prefix, err)
	}
	mapped, _, err := AddressOutboundTag(id, "::ffff:203.0.113.2")
	if err != nil || mapped != tag {
		t.Fatal("mapped address created separate cohort")
	}
	other, _, _ := AddressOutboundTag(id, "203.0.113.3")
	if other == tag {
		t.Fatal("distinct source cohort collision")
	}
	for _, source := range []string{"invalid", "0.0.0.0", "ff02::1", "fe80::1%en0"} {
		if _, _, err := AddressOutboundTag(id, source); err == nil {
			t.Errorf("accepted %q", source)
		}
	}
}
