package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// TunnelSessionKey identifies one authenticated Exit transport generation.
// Neither source IP alone nor UUID alone identifies a session. AcceptedAt
// prevents a later connection reusing the same peer tuple from sharing a slot.
func TunnelSessionKey(session ObservedTunnelSession) (string, error) {
	id, err := uuid.Parse(session.ExitID)
	if err != nil {
		return "", fmt.Errorf("invalid session Exit identity")
	}
	c := session.Connection
	if err := c.Validate(); err != nil {
		return "", err
	}
	local := netip.AddrPortFrom(netip.MustParseAddr(c.LocalIP).Unmap(), uint16(c.LocalPort))
	peer := netip.AddrPortFrom(netip.MustParseAddr(c.PeerIP).Unmap(), uint16(c.PeerPort))
	credential, _ := uuid.Parse(c.UUID)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d", id.String(), credential.String(), local, peer, c.AcceptedAt)))
	return hex.EncodeToString(sum[:]), nil
}

// ReserveTunnelSession atomically reserves a transport slot across Exits.
// Retries of this exact session are idempotent, but another transport on the
// same UUID/IP consumes another slot. No TTL silently frees live sessions.
// This primitive is not automatically enabled on legacy UUID admission.
func ReserveTunnelSession(ctx context.Context, rdb *redis.Client, account string, session ObservedTunnelSession, limit int) (bool, error) {
	if rdb == nil {
		return false, fmt.Errorf("session admission unavailable")
	}
	user, err := uuid.Parse(account)
	if err != nil || limit < 0 {
		return false, fmt.Errorf("invalid session admission")
	}
	key, err := TunnelSessionKey(session)
	if err != nil {
		return false, err
	}
	const script = `
if redis.call('SISMEMBER',KEYS[1],ARGV[1])==1 then return 1 end
local cap=tonumber(ARGV[2])
if cap>0 and redis.call('SCARD',KEYS[1])>=cap then return 0 end
redis.call('SADD',KEYS[1],ARGV[1])
return 1`
	result, err := redis.NewScript(script).Run(ctx, rdb, []string{"account:" + user.String() + ":tunnel_session_slots"}, key, limit).Int()
	return result == 1, err
}
