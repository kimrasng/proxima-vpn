package services

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestAdmitDeviceWithoutConsumingBandwidth(t *testing.T) {
	db := &bandwidthAuthorizationDB{}
	if _, err := NewDeviceBandwidthService(db, nil).AdmitDevice(context.Background(), bandwidthTestNode, bandwidthTestDevice); err == nil {
		t.Fatal("authorized admission must fail closed without Redis")
	}
	if db.calls != 4 {
		t.Fatalf("authorization calls = %d, want 4", db.calls)
	}
	deniedDB := &bandwidthAuthorizationDB{err: pgx.ErrNoRows}
	admitted, err := NewDeviceBandwidthService(deniedDB, nil).AdmitDevice(context.Background(), bandwidthTestNode, bandwidthTestDevice)
	if err != nil || admitted {
		t.Fatalf("ineligible admission = %v, %v", admitted, err)
	}
	if _, err := NewDeviceBandwidthService(db, nil).AdmitDevice(context.Background(), "bad", bandwidthTestDevice); !errors.Is(err, ErrInvalidBandwidthRequest) {
		t.Fatalf("invalid admission error = %v", err)
	}
}
