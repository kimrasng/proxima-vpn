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

	// Xray's counters are read destructively (QueryStats reset=true), so a
	// sample that fails to reach the server cannot be re-read. Carry it and
	// fold it into the next attempt; dropping it under-reports usage for the
	// length of any outage and makes traffic caps under-count.
	pending map[string]client.TrafficStat
}

func NewCollector(statsClient *xray.StatsClient, apiClient *client.APIClient, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Collector{
		statsClient: statsClient,
		apiClient:   apiClient,
		interval:    interval,
		pending:     map[string]client.TrafficStat{},
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

	// Fold the fresh sample into whatever a previous attempt failed to deliver.
	// Done before the online-users query so an error there cannot discard
	// counters Xray has already zeroed.
	c.accumulate(traffic)

	onlineUsers, err := c.statsClient.GetOnlineUsers(ctx)
	if err != nil {
		log.Printf("collect online users: %v", err)
		return
	}

	if err := c.flush(ctx, onlineUsers); err != nil {
		log.Printf("send stats: %v (retrying %d device(s) on the next tick)", err, len(c.pending))
	}
}

// flush posts the undelivered set, clearing it only once the server has it.
func (c *Collector) flush(ctx context.Context, onlineUsers []string) error {
	if len(c.pending) == 0 {
		return nil
	}

	apiTraffic := make([]client.TrafficStat, 0, len(c.pending))
	for _, t := range c.pending {
		apiTraffic = append(apiTraffic, t)
	}

	if err := c.apiClient.SendStats(ctx, apiTraffic, onlineUsers); err != nil {
		return err
	}

	c.pending = map[string]client.TrafficStat{}
	return nil
}

// accumulate merges a freshly-read sample into the undelivered set.
func (c *Collector) accumulate(traffic []xray.TrafficStat) {
	for _, t := range traffic {
		prev := c.pending[t.UUID]
		c.pending[t.UUID] = client.TrafficStat{
			UUID:     t.UUID,
			Upload:   prev.Upload + t.Upload,
			Download: prev.Download + t.Download,
		}
	}
}
