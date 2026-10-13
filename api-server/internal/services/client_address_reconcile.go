package services

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ObserveAndReconcileClientAddressSlots captures the admission fence BEFORE
// collecting fleet reports. Callers must not substitute an after-observation
// fence, which could release a connection admitted during collection.
// No background caller enables this automatically yet.
func ObserveAndReconcileClientAddressSlots(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, account string) (ClientAddressObservation, []string, error) {
	if rdb == nil {
		return ClientAddressObservation{}, nil, fmt.Errorf("address reconciliation unavailable")
	}
	clock, err := rdb.Time(ctx).Result()
	if err != nil {
		return ClientAddressObservation{}, nil, err
	}
	fence, err := CaptureAddressReservationFence(ctx, rdb, account)
	if err != nil {
		return ClientAddressObservation{}, nil, err
	}
	observation, observationErr := ObserveClientAddresses(ctx, db, rdb, account)
	if observationErr != nil {
		// Even a failed collection breaks the consecutive absence proof.
		_, clearErr := ReconcileAbsentClientAddresses(ctx, rdb, account, ClientAddressObservation{Complete: false}, clock.UnixMilli(), fence)
		if clearErr != nil {
			return observation, nil, fmt.Errorf("observation failed (%v); absence reset failed: %w", observationErr, clearErr)
		}
		return observation, nil, observationErr
	}
	released, err := ReconcileAbsentClientAddresses(ctx, rdb, account, observation, clock.UnixMilli(), fence)
	return observation, released, err
}
