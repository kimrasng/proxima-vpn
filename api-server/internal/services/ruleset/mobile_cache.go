package ruleset

import (
	"context"
	"slices"
	"sync"
	"time"
)

const mobileCacheTTL = 5 * time.Minute
const mobileCacheCapacity = 16
const mobileCacheMaxBytes = 16 << 20

type mobileCacheEntry struct {
	contents Contents
	expires  time.Time
	bytes    int
}

type mobileCacheFlight struct {
	done     chan struct{}
	contents Contents
	err      error
}

type mobileProviderCache struct {
	mu      sync.Mutex
	entries map[mobileFetchSource]mobileCacheEntry
	flights map[mobileFetchSource]*mobileCacheFlight
	bytes   int
}

var mobileDownloads = mobileProviderCache{
	entries: make(map[mobileFetchSource]mobileCacheEntry),
	flights: make(map[mobileFetchSource]*mobileCacheFlight),
}

func cloneMobileContents(c Contents) Contents {
	c.Entries = slices.Clone(c.Entries)
	return c
}

func (c *mobileProviderCache) fetch(ctx context.Context, source mobileFetchSource, download func(context.Context, string, string) (Contents, error)) (Contents, error) {
	if err := ctx.Err(); err != nil {
		return Contents{}, err
	}
	c.mu.Lock()
	now := time.Now()
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, key)
			c.bytes -= entry.bytes
		}
	}
	if entry, ok := c.entries[source]; ok {
		c.mu.Unlock()
		return cloneMobileContents(entry.contents), nil
	}
	if flight, ok := c.flights[source]; ok {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return Contents{}, ctx.Err()
		case <-flight.done:
			return cloneMobileContents(flight.contents), flight.err
		}
	}
	// Bound in-flight bookkeeping as well. At capacity, fetch independently
	// rather than retaining arbitrarily many attacker-controlled URL keys.
	if len(c.flights) >= mobileCacheCapacity {
		c.mu.Unlock()
		return download(ctx, source.url, source.format)
	}
	flight := &mobileCacheFlight{done: make(chan struct{})}
	c.flights[source] = flight
	c.mu.Unlock()
	contents, err := download(ctx, source.url, source.format)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil && !contents.Truncated && contents.Count == len(contents.Entries) && len(contents.Entries) > 0 {
		size := 0
		for _, entry := range contents.Entries {
			size += len(entry) + 16
		}
		if size <= mobileCacheMaxBytes {
			for len(c.entries) >= mobileCacheCapacity || c.bytes+size > mobileCacheMaxBytes {
				var oldest mobileFetchSource
				var expiry time.Time
				for key, entry := range c.entries {
					if expiry.IsZero() || entry.expires.Before(expiry) {
						oldest, expiry = key, entry.expires
					}
				}
				c.bytes -= c.entries[oldest].bytes
				delete(c.entries, oldest)
			}
			c.entries[source] = mobileCacheEntry{cloneMobileContents(contents), time.Now().Add(mobileCacheTTL), size}
			c.bytes += size
		}
	}
	flight.contents, flight.err = contents, err
	delete(c.flights, source)
	close(flight.done)
	return cloneMobileContents(contents), err
}

func cachedMobileProvider(ctx context.Context, rawURL, format string) (Contents, error) {
	return mobileDownloads.fetch(ctx, mobileFetchSource{rawURL, format}, fetchMobileProvider)
}
