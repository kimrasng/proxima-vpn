package handlers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type nodeChainRowsQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func claimedEntryPorts(ctx context.Context, query nodeChainRowsQuerier) ([]nodeprov.PortClaim, error) {
	rows, err := query.Query(ctx, `SELECT entry_port, transport FROM node_chains WHERE entry_port IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var claims []nodeprov.PortClaim
	for rows.Next() {
		var claim nodeprov.PortClaim
		if err := rows.Scan(&claim.Port, &claim.Transport); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func entryPortOverlaps(port int, transport nodeprov.Transport, claims []nodeprov.PortClaim) bool {
	for _, claim := range claims {
		if claim.Port == port && (claim.Transport.Covers(transport) || transport.Covers(claim.Transport)) {
			return true
		}
	}
	return false
}

func isDirectChainConflict(err error) bool {
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return false
	}
	return databaseError.Code == "23505" && databaseError.ConstraintName == "idx_node_chains_one_direct_per_exit_v2"
}

func isEntryPortConflict(err error) bool {
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return false
	}
	if databaseError.Code != "23505" {
		return false
	}
	return databaseError.ConstraintName == "idx_node_chains_entry_port_tcp_claim" ||
		databaseError.ConstraintName == "idx_node_chains_entry_port_udp_claim"
}
