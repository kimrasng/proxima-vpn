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
	evictionCooldown         = 10 * time.Minute
)

type ConcurrencyScheduler struct {
	db        *pgxpool.Pool
	tracker   *services.OnlineTracker
	enforce   bool
	grace     int
	threshold int
	strikes   map[string]int
	cancel    context.CancelFunc
}

func NewConcurrencyScheduler(db *pgxpool.Pool, rdb *redis.Client) *ConcurrencyScheduler {
	return &ConcurrencyScheduler{db: db, tracker: services.NewOnlineTracker(rdb), enforce: os.Getenv("ENFORCE_CONCURRENCY") == "1", grace: nonnegativeEnv("CONCURRENCY_GRACE", 0), threshold: positiveEnv("CONCURRENCY_STRIKES", 2), strikes: map[string]int{}}
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
		if r.cap > 0 {
			s.checkUser(ctx, r)
		}
	}
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
