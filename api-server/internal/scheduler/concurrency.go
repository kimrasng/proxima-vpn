package scheduler

import (
	"context"
	"log"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/redis/go-redis/v9"
)

const (
	concurrencyCheckInterval = 60 * time.Second

	// Xray reports a source address per connection, and one device legitimately
	// presents several: dual-stack clients appear as both IPv4 and IPv6, and
	// mobile carriers rotate addresses mid-session. Acting on the exact cap
	// would disconnect an ordinary phone, so the cap is only breached once
	// exceeded by this margin.
	concurrencyGrace = 1

	// An over-cap reading must repeat this many consecutive sweeps before it
	// costs anyone their connection; a single sweep landing during a reconnect
	// sees the old and new address at once.
	overageStrikes = 3

	evictionCooldown = 10 * time.Minute
)

// ConcurrencyScheduler enforces each plan's cap on how many distinct source
// addresses may be live across a user's devices, pool-wide.
//
// Enforcement is opt-in via ENFORCE_CONCURRENCY=1. Off, it records what it
// would have done: an operator needs the real distribution of their users'
// addresses before a cap starts denying paid-for traffic, and CGNAT alone can
// make a legitimate household look like sharing.
type ConcurrencyScheduler struct {
	db      *pgxpool.Pool
	tracker *services.OnlineTracker
	enforce bool
	strikes map[string]int
	cancel  context.CancelFunc
}

func NewConcurrencyScheduler(db *pgxpool.Pool, rdb *redis.Client) *ConcurrencyScheduler {
	return &ConcurrencyScheduler{
		db:      db,
		tracker: services.NewOnlineTracker(rdb),
		enforce: os.Getenv("ENFORCE_CONCURRENCY") == "1",
		strikes: map[string]int{},
	}
}

func (s *ConcurrencyScheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(concurrencyCheckInterval)
	defer ticker.Stop()

	mode := "observe-only"
	if s.enforce {
		mode = "enforcing"
	}
	log.Printf("concurrency scheduler started (%s)", mode)

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
	userID string
	email  string
	cap    int
}

func (s *ConcurrencyScheduler) sweep(ctx context.Context) {
	rows, err := s.db.Query(ctx, `
		SELECT u.id::text, u.email, COALESCE(p.max_concurrent, p.max_devices)
		FROM users u
		JOIN plans p ON u.plan_id = p.id
		WHERE u.status = 'active' AND u.is_active = true
	`)
	if err != nil {
		log.Printf("concurrency sweep: %v", err)
		return
	}

	var caps []capRow
	for rows.Next() {
		var r capRow
		if err := rows.Scan(&r.userID, &r.email, &r.cap); err != nil {
			continue
		}
		caps = append(caps, r)
	}
	rows.Close()

	for _, r := range caps {
		if r.cap <= 0 {
			continue
		}
		s.checkUser(ctx, r)
	}
}

func (s *ConcurrencyScheduler) checkUser(ctx context.Context, r capRow) {
	total, perDevice, err := s.tracker.CountDistinctIPsForUser(ctx, s.db, r.userID)
	if err != nil {
		return
	}

	if total <= r.cap+concurrencyGrace {
		delete(s.strikes, r.userID)
		return
	}

	s.strikes[r.userID]++
	if s.strikes[r.userID] < overageStrikes {
		return
	}
	delete(s.strikes, r.userID)

	target := pickEvictionTarget(perDevice)
	if target == "" {
		return
	}

	if !s.enforce {
		log.Printf("concurrency: user %s would be capped (%d IPs over cap %d), device %s",
			r.email, total, r.cap, target)
		return
	}

	_, err = s.db.Exec(ctx,
		`UPDATE devices SET evicted_until = NOW() + $1::interval WHERE xray_uuid = $2`,
		evictionCooldown.String(), target)
	if err != nil {
		log.Printf("concurrency: evicting %s: %v", target, err)
		return
	}
	log.Printf("concurrency: evicted device %s of user %s for %s (%d IPs over cap %d)",
		target, r.email, evictionCooldown, total, r.cap)
}

// pickEvictionTarget chooses the device spread across the most addresses, which
// is the shared credential rather than a victim of it. Ties go to whichever was
// seen most recently, matching the convention that an established session
// outranks the one that just showed up.
func pickEvictionTarget(perDevice map[string]map[string]int64) string {
	type candidate struct {
		uuid     string
		ips      int
		lastSeen int64
	}

	var candidates []candidate
	for uuid, ips := range perDevice {
		var newest int64
		for _, ts := range ips {
			if ts > newest {
				newest = ts
			}
		}
		candidates = append(candidates, candidate{uuid: uuid, ips: len(ips), lastSeen: newest})
	}
	if len(candidates) == 0 {
		return ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ips != candidates[j].ips {
			return candidates[i].ips > candidates[j].ips
		}
		if candidates[i].lastSeen != candidates[j].lastSeen {
			return candidates[i].lastSeen > candidates[j].lastSeen
		}
		return candidates[i].uuid < candidates[j].uuid
	})
	return candidates[0].uuid
}
