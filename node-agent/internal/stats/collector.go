package stats

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/xray"
)

const DefaultInterval = 10 * time.Second

type Collector struct {
	statsClient *xray.StatsClient
	apiClient   *client.APIClient
	interval    time.Duration
	cancel      context.CancelFunc
	outbox      *outbox

	// A sample already reset in Xray but not yet written to disk (e.g. disk
	// full). Do not poll Xray again until it has been safely queued.
	pending           map[string]client.TrafficStat
	provisionedEmails func() []string
}

// NewCollector opens the persistent outbox before the destructive Xray query
// can start. The directory must live on persistent storage, scoped to this node.
func NewCollector(
	statsClient *xray.StatsClient,
	apiClient *client.APIClient,
	interval time.Duration,
	provisionedEmails func() []string,
	outboxDir string,
) (*Collector, error) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	o, err := openOutbox(outboxDir)
	if err != nil {
		return nil, err
	}
	return &Collector{
		statsClient: statsClient, apiClient: apiClient, interval: interval,
		pending: map[string]client.TrafficStat{}, outbox: o,
		provisionedEmails: provisionedEmails,
	}, nil
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
	// Replay the outbox immediately after restart; fresh online state is
	// reported only after a successful new activity query.
	c.collect(ctx)
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
	// Queue a previously-reset sample before any further destructive poll.
	if err := c.queuePending(); err != nil {
		log.Printf("queue stats: %v", err)
	}
	if err := c.drain(ctx); err != nil {
		log.Printf("deliver stats: %v", err)
	}

	if c.statsClient != nil && len(c.pending) == 0 && c.hasCapacity() {
		traffic, err := c.statsClient.GetUserTraffic(ctx)
		if err != nil {
			log.Printf("collect traffic: %v", err)
		} else {
			c.accumulate(traffic)
			if err := c.queuePending(); err != nil {
				log.Printf("queue stats: %v", err)
			} else if err := c.drain(ctx); err != nil {
				log.Printf("deliver stats: %v", err)
			}
		}
	}

	// Online activity is not cumulative. It is never stored or replayed with
	// traffic, so an outage cannot resurrect an old positive report.
	if c.statsClient == nil {
		return
	}
	onlineIPs, err := c.collectOnlineIPs(ctx)
	if err != nil {
		log.Printf("collect online ips: %v", err)
		return
	}
	onlineUsers := make([]string, 0, len(onlineIPs))
	for uuid := range onlineIPs {
		onlineUsers = append(onlineUsers, uuid)
	}
	sort.Strings(onlineUsers)
	if err := c.apiClient.SendStats(ctx, "", nil, onlineUsers, onlineIPs); err != nil {
		log.Printf("send online stats: %v", err)
	}
}

// hasCapacity reserves space for one maximum-sized batch before resetting
// Xray, rather than discovering a full outbox after the destructive read.
func (c *Collector) hasCapacity() bool {
	return len(c.outbox.files) < maxBatches && c.outbox.bytes+maxBatchBytes <= maxOutboxBytes
}

func (c *Collector) queuePending() error {
	if len(c.pending) == 0 {
		return nil
	}
	traffic := make([]client.TrafficStat, 0, len(c.pending))
	for _, t := range c.pending {
		traffic = append(traffic, t)
	}
	sort.Slice(traffic, func(i, j int) bool { return traffic[i].UUID < traffic[j].UUID })
	b, err := newBatch(traffic)
	if err != nil {
		return err
	}
	before := len(c.outbox.files)
	err = c.outbox.append(b)
	if len(c.outbox.files) > before {
		// Even if the directory fsync failed, the file is now the owner of
		// this sample. drain will not send until the directory is synced.
		c.pending = map[string]client.TrafficStat{}
	}
	return err
}

func (c *Collector) drain(ctx context.Context) error {
	if len(c.outbox.files) == 0 {
		return nil
	}
	if err := syncDir(c.outbox.dir); err != nil {
		return fmt.Errorf("sync outbox: %w", err)
	}
	for len(c.outbox.files) > 0 {
		b, err := c.outbox.read(c.outbox.files[0])
		if err != nil {
			return err
		}
		if err := c.apiClient.SendStats(ctx, b.ID, b.Traffic, nil, nil); err != nil {
			return err
		}
		if err := c.outbox.removeFirst(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collector) collectOnlineIPs(ctx context.Context) (map[string][]client.OnlineIP, error) {
	if c.provisionedEmails == nil {
		return map[string][]client.OnlineIP{}, nil
	}
	emails := c.provisionedEmails()
	if len(emails) == 0 {
		return map[string][]client.OnlineIP{}, nil
	}
	byUUID, err := c.statsClient.GetOnlineIPs(ctx, emails)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]client.OnlineIP, len(byUUID))
	now := time.Now().Unix()
	for uuid, ips := range byUUID {
		for ip, lastSeen := range ips {
			if lastSeen >= now-20 && lastSeen <= now {
				out[uuid] = append(out[uuid], client.OnlineIP{IP: ip, LastSeen: lastSeen})
			}
		}
	}
	return out, nil
}

func (c *Collector) accumulate(traffic []xray.TrafficStat) {
	for _, t := range traffic {
		prev := c.pending[t.UUID]
		c.pending[t.UUID] = client.TrafficStat{UUID: t.UUID, Upload: prev.Upload + t.Upload, Download: prev.Download + t.Download}
	}
}

// OutboxPath keeps each node's queue beside its config, independent of the
// current working directory and API connectivity.
func OutboxPath(configPath, nodeID string) string {
	return filepath.Join(filepath.Dir(configPath), "stats-outbox", nodeID)
}
