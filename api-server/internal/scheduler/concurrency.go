package scheduler

import (
	"context"
	"log"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/redis/go-redis/v9"
)

const (
	concurrencyCheckInterval = 10 * time.Second
	sessionReconcileInterval = 30 * time.Second
	evictionCooldown         = 10 * time.Minute
	// addressClosureCooldown bounds how often transport closures are queued
	// for one account by the client address cap enforcement.
	addressClosureCooldown = 90 * time.Second
)

type ConcurrencyScheduler struct {
	db                       *pgxpool.Pool
	tracker                  *services.OnlineTracker
	enforce                  bool
	grace                    int
	threshold                int
	strikes                  map[string]int
	sessionStrikes           map[string]int
	sessionReconcileAt       map[string]time.Time
	reconcileTunnelSessions  func(context.Context, string) (services.TunnelSessionObservation, []string, error)
	addressStrikes           map[string]int
	addressReconcileAt       map[string]time.Time
	reconcileClientAddresses func(context.Context, string) (services.ClientAddressObservation, []string, error)
	// enforceAddresses closes the transports of over-cap client addresses. It is
	// ON by default; ENFORCE_CLIENT_ADDRESS_LIMIT=0 (or false/off/no) is the
	// kill switch. It is independent of enforce and never leads to UUID
	// revocation.
	enforceAddresses     bool
	addressClosureAt     map[string]time.Time
	queueAddressClosures func(context.Context, services.ClientAddressObservation, []string) (int, error)
	cancel               context.CancelFunc
}

func NewConcurrencyScheduler(db *pgxpool.Pool, rdb *redis.Client) *ConcurrencyScheduler {
	return &ConcurrencyScheduler{
		db: db, tracker: services.NewOnlineTracker(rdb),
		enforce: services.ConcurrencyEnforcementEnabled(),
		grace:   nonnegativeEnv("CONCURRENCY_GRACE", 0), threshold: positiveEnv("CONCURRENCY_STRIKES", 2),
		strikes: map[string]int{}, sessionStrikes: map[string]int{}, sessionReconcileAt: map[string]time.Time{},
		reconcileTunnelSessions: func(ctx context.Context, account string) (services.TunnelSessionObservation, []string, error) {
			return services.ObserveAndReconcileTunnelSessions(ctx, db, rdb, account)
		},
		addressStrikes: map[string]int{}, addressReconcileAt: map[string]time.Time{},
		reconcileClientAddresses: func(ctx context.Context, account string) (services.ClientAddressObservation, []string, error) {
			return services.ObserveAndReconcileClientAddressSlots(ctx, db, rdb, account)
		},
		enforceAddresses: services.ClientAddressEnforcementEnabled(),
		addressClosureAt: map[string]time.Time{},
		queueAddressClosures: func(ctx context.Context, observation services.ClientAddressObservation, denied []string) (int, error) {
			return services.QueueClientAddressClosures(ctx, rdb, observation, denied)
		},
	}
}

func nonnegativeEnv(name string, fallback int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n >= 0 {
		return n
	}
	return fallback
}
func positiveEnv(name string, fallback int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return fallback
}
func (s *ConcurrencyScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(concurrencyCheckInterval)
	defer ticker.Stop()
	if s.enforce {
		log.Printf("concurrency enforcement requested: all Exit epochs and revocation acknowledgments required")
	}
	log.Printf("concurrency scheduler started (enforce=%t)", s.enforce)
	log.Printf("client address cap enforcement (enabled=%t): transport closure only, no UUID revocation", s.enforceAddresses)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}
func (s *ConcurrencyScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

type capRow struct {
	userID, email string
	cap           int
}

func (s *ConcurrencyScheduler) sweep(ctx context.Context) {
	rows, err := s.db.Query(ctx, `SELECT u.id::text,u.email,COALESCE(p.max_concurrent,p.max_devices) FROM users u JOIN plans p ON u.plan_id=p.id WHERE u.status='active' AND u.is_active=true`)
	if err != nil {
		log.Printf("concurrency sweep: %v", err)
		return
	}
	var caps []capRow
	for rows.Next() {
		var r capRow
		if err := rows.Scan(&r.userID, &r.email, &r.cap); err == nil {
			caps = append(caps, r)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		log.Printf("concurrency sweep: %v", err)
		return
	}
	for _, r := range caps {
		s.checkTunnelSessions(ctx, r)
		s.checkClientAddresses(ctx, r)
		if r.cap > 0 {
			s.checkUser(ctx, r)
		}
	}
}
func (s *ConcurrencyScheduler) checkTunnelSessions(ctx context.Context, r capRow) {
	now := time.Now()
	if s.sessionReconcileAt == nil {
		s.sessionReconcileAt = make(map[string]time.Time)
	}
	if next := s.sessionReconcileAt[r.userID]; now.Before(next) {
		return
	}
	s.sessionReconcileAt[r.userID] = now.Add(sessionReconcileInterval)
	if s.reconcileTunnelSessions == nil {
		delete(s.sessionStrikes, r.userID)
		return
	}
	observation, released, err := s.reconcileTunnelSessions(ctx, r.userID)
	if err != nil || !observation.Complete {
		delete(s.sessionStrikes, r.userID)
		if err != nil {
			log.Printf("tunnel session reconciliation: user=%s err=%v", r.userID, err)
		}
		return
	}
	if len(released) > 0 {
		log.Printf("tunnel session reservations released: user=%s count=%d", r.userID, len(released))
	}
	if r.cap <= 0 {
		delete(s.sessionStrikes, r.userID)
		return
	}
	count, err := services.CountTunnelSessions(observation)
	if err != nil {
		delete(s.sessionStrikes, r.userID)
		return
	}
	if count <= r.cap {
		delete(s.sessionStrikes, r.userID)
		return
	}
	targets, err := services.NewestTunnelSessions(observation, r.cap)
	if err != nil || len(targets) == 0 {
		return
	}
	s.sessionStrikes[r.userID]++
	if s.sessionStrikes[r.userID] < s.threshold {
		return
	}
	// Reporting only. Per-session local credential materialization and a
	// durable, multi-exit admission policy are not enabled yet; never fall
	// through to UUID-wide revocation for a session-count excess.
	log.Printf("tunnel session cap exceeded, no session-specific enforcement: user=%s sessions=%d cap=%d candidate_exit=%s peer=%s:%d", r.userID, count, r.cap, targets[0].ExitID, targets[0].Connection.PeerIP, targets[0].Connection.PeerPort)
	s.sessionStrikes[r.userID] = 0
}

// checkClientAddresses frees stale client source address slots and reports a
// sustained excess. Only when enforceAddresses is set does it also queue exact
// transport closures for the newest over-cap addresses. It never revokes or
// evicts a UUID. Raw addresses are never logged.
func (s *ConcurrencyScheduler) checkClientAddresses(ctx context.Context, r capRow) {
	now := time.Now()
	if s.addressReconcileAt == nil {
		s.addressReconcileAt = make(map[string]time.Time)
	}
	if s.addressStrikes == nil {
		s.addressStrikes = make(map[string]int)
	}
	if next := s.addressReconcileAt[r.userID]; now.Before(next) {
		return
	}
	s.addressReconcileAt[r.userID] = now.Add(sessionReconcileInterval)
	if s.reconcileClientAddresses == nil {
		delete(s.addressStrikes, r.userID)
		return
	}
	observation, released, err := s.reconcileClientAddresses(ctx, r.userID)
	if err != nil || !observation.Complete {
		delete(s.addressStrikes, r.userID)
		if err != nil {
			log.Printf("client address reconciliation: user=%s err=%v", r.userID, err)
		}
		return
	}
	if len(released) > 0 {
		log.Printf("client address slots released: user=%s count=%d", r.userID, len(released))
	}
	if r.cap <= 0 || len(observation.Addresses) <= r.cap {
		delete(s.addressStrikes, r.userID)
		return
	}
	s.addressStrikes[r.userID]++
	if s.addressStrikes[r.userID] < s.threshold {
		return
	}
	if s.enforceAddresses {
		s.closeExcessClientAddresses(ctx, r, observation, now)
	} else {
		// Reporting only: an address count is not a device count, so an excess
		// is never grounds for UUID revocation.
		log.Printf("client address cap exceeded, observation only: user=%s addresses=%d cap=%d", r.userID, len(observation.Addresses), r.cap)
	}
	s.addressStrikes[r.userID] = 0
}

// closeExcessClientAddresses queues exact transport closures for the newest
// client addresses beyond the cap. It acts only on a complete observation with
// a known, unambiguous policy, and at most once per addressClosureCooldown per
// account. It never touches UUID eviction or revocation, and logs only counts
// and fixed reason tokens, never client addresses.
func (s *ConcurrencyScheduler) closeExcessClientAddresses(ctx context.Context, r capRow, observation services.ClientAddressObservation, now time.Time) {
	if !observation.Complete || r.cap <= 0 || s.queueAddressClosures == nil {
		return
	}
	skip := func(reason string) {
		log.Printf("client address cap exceeded, no transport closure: user=%s addresses=%d cap=%d reason=%s", r.userID, len(observation.Addresses), r.cap, reason)
	}
	policy, err := services.ProposeClientAddressPolicy(observation, r.cap)
	switch {
	case err != nil:
		skip("policy_error")
		return
	case !policy.Known:
		reason := policy.Reason
		if reason == "" {
			reason = "unknown"
		}
		skip(reason)
		return
	case len(policy.Denied) == 0:
		skip("no_denied_addresses")
		return
	}
	if s.addressClosureAt == nil {
		s.addressClosureAt = make(map[string]time.Time)
	}
	if next := s.addressClosureAt[r.userID]; now.Before(next) {
		skip("cooldown")
		return
	}
	queued, err := s.queueAddressClosures(ctx, observation, policy.Denied)
	if err != nil {
		s.addressClosureAt[r.userID] = now.Add(addressClosureCooldown)
		log.Printf("client address cap exceeded, transport closure failed: user=%s addresses=%d cap=%d denied=%d queued=%d err=%v", r.userID, len(observation.Addresses), r.cap, len(policy.Denied), queued, err)
		return
	}
	if queued >= 1 {
		s.addressClosureAt[r.userID] = now.Add(addressClosureCooldown)
	}
	log.Printf("client address cap exceeded, transport closure queued: user=%s addresses=%d cap=%d denied=%d queued=%d", r.userID, len(observation.Addresses), r.cap, len(policy.Denied), queued)
}

func (s *ConcurrencyScheduler) checkUser(ctx context.Context, r capRow) {
	obs, err := s.tracker.ObserveAccount(ctx, s.db, r.userID)
	if err != nil {
		log.Printf("concurrency epoch: %v", err)
		return
	}
	if !obs.Complete {
		log.Printf("concurrency observation incomplete: user=%s unknown_exits=%v", r.userID, obs.UnknownExitIDs)
		return
	}
	pending, err := completeUUIDEvictions(ctx, s.db, r.userID)
	if err != nil {
		log.Printf("concurrency pending query: %v", err)
		return
	}
	if pending {
		log.Printf("concurrency eviction pending confirmation: user=%s", r.userID)
		return
	}
	online := obs.OnlineUUIDs
	if len(online) <= r.cap+s.grace {
		delete(s.strikes, r.userID)
		return
	}
	target := obs.NewestUUID
	if obs.Ambiguous || target == "" {
		log.Printf("concurrency ambiguous newest UUID: user=%s candidates=%v", r.userID, obs.AmbiguousUUIDs)
		return
	}
	s.strikes[r.userID]++
	log.Printf("concurrency observation: user=%s online_uuids=%d cap=%d grace=%d strikes=%d candidate=%s enforce=%t", r.userID, len(online), r.cap, s.grace, s.strikes[r.userID], target, s.enforce)
	if s.strikes[r.userID] < s.threshold || !s.enforce {
		return
	}
	exits, err := eligibleEvictionExits(ctx, s.db, obs)
	if err != nil {
		log.Printf("concurrency fleet not ready: %v", err)
		return
	}
	epoch, err := beginUUIDEviction(ctx, s.db, r.userID, target, exits)
	if err != nil {
		log.Printf("concurrency eviction request failed: %v", err)
		return
	}
	delete(s.strikes, r.userID)
	log.Printf("concurrency eviction pending: user=%s uuid=%s epoch=%s required_exits=%d", r.userID, target, epoch, len(exits))
}

// pickEvictionTarget chooses the newest confirmed account-wide online transition.
// Missing start times are not evidence of a recent transition.
func pickEvictionTarget(starts map[string]int64) string {
	uuids := make([]string, 0, len(starts))
	for uuid := range starts {
		uuids = append(uuids, uuid)
	}
	sort.Slice(uuids, func(i, j int) bool {
		if starts[uuids[i]] != starts[uuids[j]] {
			return starts[uuids[i]] > starts[uuids[j]]
		}
		return uuids[i] < uuids[j]
	})
	if len(uuids) == 0 {
		return ""
	}
	if len(uuids) > 1 && starts[uuids[0]] == starts[uuids[1]] {
		return ""
	}
	return uuids[0]
}
