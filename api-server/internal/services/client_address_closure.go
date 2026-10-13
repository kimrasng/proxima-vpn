package services

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

// ClientAddressClosureTargets resolves the exact Exit transports that must be
// closed so that every denied client address stops being served. It fails
// closed: an incomplete observation, an address the observation does not
// contain, or an address with no unambiguously resolved transport yields an
// error and no targets, never a guess.
//
// Errors deliberately never include client addresses.
func ClientAddressClosureTargets(observation ClientAddressObservation, denied []string) ([]ObservedAddressTransport, error) {
	if !observation.Complete {
		return nil, fmt.Errorf("client address observation is incomplete")
	}
	known := make(map[string]bool, len(observation.Addresses))
	for _, address := range observation.Addresses {
		known[address] = true
	}
	seenAddress := make(map[string]bool, len(denied))
	seenTransport := make(map[string]bool)
	targets := []ObservedAddressTransport{}
	for _, address := range denied {
		if seenAddress[address] {
			continue
		}
		seenAddress[address] = true
		if !known[address] {
			return nil, fmt.Errorf("denied client address is not in the observation")
		}
		transports := observation.Transports[address]
		if len(transports) == 0 {
			return nil, fmt.Errorf("denied client address has no resolved transports")
		}
		for _, transport := range transports {
			if !canonicalUUID(transport.ExitID) {
				return nil, fmt.Errorf("denied client address transport has an invalid Exit identity")
			}
			// The session token covers Exit, credential, both endpoints and
			// AcceptedAt, but not IdleMillis, which varies between reports.
			token, err := nodeprov.TunnelSessionToken(transport.ExitID, transport.Connection)
			if err != nil {
				return nil, fmt.Errorf("denied client address transport is invalid")
			}
			if seenTransport[token] {
				continue
			}
			seenTransport[token] = true
			targets = append(targets, transport)
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		a, b := targets[i], targets[j]
		if a.ExitID != b.ExitID {
			return a.ExitID < b.ExitID
		}
		if a.Connection.PeerIP != b.Connection.PeerIP {
			return a.Connection.PeerIP < b.Connection.PeerIP
		}
		if a.Connection.PeerPort != b.Connection.PeerPort {
			return a.Connection.PeerPort < b.Connection.PeerPort
		}
		if a.Connection.AcceptedAt != b.Connection.AcceptedAt {
			return a.Connection.AcceptedAt < b.Connection.AcceptedAt
		}
		// Remaining tie-breakers keep the order total for distinct tokens.
		if a.Connection.LocalIP != b.Connection.LocalIP {
			return a.Connection.LocalIP < b.Connection.LocalIP
		}
		if a.Connection.LocalPort != b.Connection.LocalPort {
			return a.Connection.LocalPort < b.Connection.LocalPort
		}
		return a.Connection.UUID < b.Connection.UUID
	})
	return targets, nil
}

// QueueClientAddressClosures queues an exact, generation-fenced close for every
// transport of the denied client addresses and returns how many were queued.
// It stops at the first queue failure. It never revokes or bans the shared
// account UUID and never logs client addresses.
func QueueClientAddressClosures(ctx context.Context, rdb *redis.Client, observation ClientAddressObservation, denied []string) (int, error) {
	if rdb == nil {
		return 0, fmt.Errorf("transport closure storage unavailable")
	}
	targets, err := ClientAddressClosureTargets(observation, denied)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, target := range targets {
		if _, err := QueueTransportClosure(ctx, rdb, target.ExitID, target.Connection); err != nil {
			return queued, fmt.Errorf("queue transport closure: %w", err)
		}
		queued++
	}
	return queued, nil
}

// canonicalUUID requires the lowercase canonical form, which is what the node
// registry emits and what the per-node closure queue key is built from.
func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
