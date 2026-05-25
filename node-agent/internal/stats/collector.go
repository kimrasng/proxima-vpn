package stats

import (
	"context"
	"log"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

const DefaultInterval = 30 * time.Second

type Collector struct {
	statsClient *xray.StatsClient
	apiClient   *client.APIClient
	interval    time.Duration
	cancel      context.CancelFunc
}

func NewCollector(statsClient *xray.StatsClient, apiClient *client.APIClient, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Collector{
		statsClient: statsClient,
		apiClient:   apiClient,
		interval:    interval,
	}
}

func (c *Collector) Start(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	go c.loop(ctx)
}

func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Collector) loop(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect(ctx)
		}
	}
}

func (c *Collector) collect(ctx context.Context) {
	traffic, err := c.statsClient.GetUserTraffic(ctx)
	if err != nil {
		log.Printf("collect traffic: %v", err)
		return
	}

	onlineUsers, err := c.statsClient.GetOnlineUsers(ctx)
	if err != nil {
		log.Printf("collect online users: %v", err)
		return
	}

	apiTraffic := make([]client.TrafficStat, len(traffic))
	for i, t := range traffic {
		apiTraffic[i] = client.TrafficStat{
			UUID:     t.UUID,
			Upload:   t.Upload,
			Download: t.Download,
		}
	}

	if err := c.apiClient.SendStats(ctx, apiTraffic, onlineUsers); err != nil {
		log.Printf("send stats: %v", err)
	}
}
