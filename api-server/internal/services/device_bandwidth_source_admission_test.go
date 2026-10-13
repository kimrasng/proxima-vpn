package services

import (
	"context"
	"errors"
	"testing"
)

func TestSourceAdmissionRejectsInvalidRequestsBeforeDB(t *testing.T) {
	for _, source := range []string{"", "hostname", "192.0.2.1:443", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "fe80::1%eth0", "255.255.255.255", "::ffff:0.0.0.0", "::ffff:224.0.0.1"} {
		allowed, err := NewDeviceBandwidthService(nil, nil).AdmitSourceDevice(context.Background(), bandwidthTestNode, bandwidthTestDevice, source)
		if allowed || !errors.Is(err, ErrInvalidBandwidthRequest) {
			t.Errorf("source %q: allowed=%v err=%v", source, allowed, err)
		}
	}
	for _, ids := range [][2]string{{"invalid", bandwidthTestDevice}, {bandwidthTestNode, "invalid"}} {
		if _, err := NewDeviceBandwidthService(nil, nil).AdmitSourceDevice(context.Background(), ids[0], ids[1], "192.0.2.1"); !errors.Is(err, ErrInvalidBandwidthRequest) {
			t.Fatal(err)
		}
	}
}
