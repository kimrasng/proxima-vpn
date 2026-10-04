package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/redis/go-redis/v9"
)

// UUIDSlotReconciler returns slots only after fenced complete reports prove
// that every serving Exit has closed its association. Unknown nodes retain
// members, denying newcomers rather than displacing incumbents.
type UUIDSlotReconciler struct {
	db    *pgxpool.Pool
	redis *redis.Client
}

func NewUUIDSlotReconciler(db *pgxpool.Pool, rdb *redis.Client) *UUIDSlotReconciler {
	return &UUIDSlotReconciler{db: db, redis: rdb}
}
func (s *UUIDSlotReconciler) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, err := s.db.Query(ctx, `SELECT DISTINCT u.id::text FROM users u JOIN plans p ON p.id=u.plan_id WHERE COALESCE(p.max_concurrent,p.max_devices)>0`)
			if err != nil {
				log.Printf("UUID slot reconciliation: %v", err)
				continue
			}
			users := []string{}
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					users = append(users, id)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				log.Printf("UUID slot reconciliation: %v", err)
				continue
			}
			for _, user := range users {
				released, err := services.ReconcileAccountUUIDSlots(ctx, s.db, s.redis, user)
				if err != nil {
					log.Printf("UUID slot reconciliation unavailable user=%s: %v", user, err)
				} else if len(released) > 0 {
					log.Printf("UUID slots released user=%s count=%d", user, len(released))
				}
			}
		}
	}
}
