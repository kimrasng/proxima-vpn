package handlers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func beginDomainMutation(ctx context.Context, pool *pgxpool.Pool, isDefault bool) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if isDefault {
		// The partial unique index guards the invariant; this lock also serializes competing selectors.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(27027)`); err != nil {
			return nil, errors.Join(err, tx.Rollback(ctx))
		}
		if _, err := tx.Exec(ctx, `UPDATE subscription_domains SET is_default = false WHERE is_default`); err != nil {
			return nil, errors.Join(err, tx.Rollback(ctx))
		}
	}
	return tx, nil
}
