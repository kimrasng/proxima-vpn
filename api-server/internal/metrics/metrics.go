package metrics

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
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
	stats := services.NewStatsService(db)
	updateGauges(ctx, stats)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateGauges(ctx, stats)
		}
	}
}

// updateGauges delegates to services.StatsService so this gauge's counts
// can't drift from the admin dashboard's or the Telegram bot's - it
// previously counted 'pending' nodes toward NodesTotal while the dashboard
// didn't, which this fixes.
func updateGauges(ctx context.Context, stats *services.StatsService) {
	summary, err := stats.GetSummary(ctx)
	if err != nil {
		log.Printf("metrics: failed to query summary: %v", err)
		return
	}
	NodesTotal.Set(float64(summary.TotalNodes))
	UsersTotal.Set(float64(summary.TotalUsers))
	UsersActive.Set(float64(summary.ActiveUsers))
}
