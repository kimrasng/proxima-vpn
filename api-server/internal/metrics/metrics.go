package metrics

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	NodesTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxima_nodes_total",
		Help: "Total number of registered nodes",
	})

	UsersTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxima_users_total",
		Help: "Total number of registered users",
	})

	UsersActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxima_users_active",
		Help: "Number of currently active users",
	})

	NodeCPUUsage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxima_node_cpu_usage",
		Help: "CPU usage percentage per node",
	}, []string{"node_id"})

	NodeMemoryUsage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxima_node_memory_usage",
		Help: "Memory usage percentage per node",
	}, []string{"node_id"})

	TrafficBytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "proxima_traffic_bytes_total",
		Help: "Total traffic bytes transferred",
	}, []string{"direction"})
)

// StartGaugeUpdater runs a background goroutine that periodically queries the
// database and updates the node/user count gauges. It stops when ctx is cancelled.
func StartGaugeUpdater(ctx context.Context, db *pgxpool.Pool) {
	updateGauges(ctx, db)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateGauges(ctx, db)
		}
	}
}

func updateGauges(ctx context.Context, db *pgxpool.Pool) {
	var count int64

	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&count); err == nil {
		NodesTotal.Set(float64(count))
	} else {
		log.Printf("metrics: failed to query nodes count: %v", err)
	}

	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err == nil {
		UsersTotal.Set(float64(count))
	} else {
		log.Printf("metrics: failed to query users count: %v", err)
	}

	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE status = 'active' AND is_active = true`).Scan(&count); err == nil {
		UsersActive.Set(float64(count))
	} else {
		log.Printf("metrics: failed to query active users count: %v", err)
	}
}
