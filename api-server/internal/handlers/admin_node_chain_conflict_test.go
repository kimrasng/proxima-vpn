package handlers

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsEntryPortConflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "tcp unique violation",
			err:  &pgconn.PgError{Code: "23505", ConstraintName: "idx_node_chains_entry_port_tcp_claim"},
			want: true,
		},
		{
			name: "udp unique violation",
			err:  &pgconn.PgError{Code: "23505", ConstraintName: "idx_node_chains_entry_port_udp_claim"},
			want: true,
		},
		{
			name: "tcp index with wrong code",
			err:  &pgconn.PgError{Code: "23514", ConstraintName: "idx_node_chains_entry_port_tcp_claim"},
			want: false,
		},
		{
			name: "wrong constraint",
			err:  &pgconn.PgError{Code: "23505", ConstraintName: "other_unique_constraint"},
			want: false,
		},
		{
			name: "non PgError",
			err:  context.Canceled,
			want: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isEntryPortConflict(testCase.err); got != testCase.want {
				t.Fatalf("isEntryPortConflict() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestIsDirectChainConflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "direct chain unique violation",
			err:  &pgconn.PgError{Code: "23505", ConstraintName: "idx_node_chains_one_direct_per_exit_v2"},
			want: true,
		},
		{
			name: "direct chain index with wrong code",
			err:  &pgconn.PgError{Code: "23514", ConstraintName: "idx_node_chains_one_direct_per_exit"},
			want: false,
		},
		{
			name: "entry port index",
			err:  &pgconn.PgError{Code: "23505", ConstraintName: "idx_node_chains_entry_port_tcp_claim"},
			want: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isDirectChainConflict(testCase.err); got != testCase.want {
				t.Fatalf("isDirectChainConflict() = %v, want %v", got, testCase.want)
			}
		})
	}
}
