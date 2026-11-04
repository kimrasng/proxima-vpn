package ruleset

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMobileCacheSuccessIsolationExpiryAndBounds(t *testing.T) {
	c := mobileProviderCache{entries: make(map[mobileFetchSource]mobileCacheEntry), flights: make(map[mobileFetchSource]*mobileCacheFlight)}
	calls := 0
	download := func(context.Context, string, string) (Contents, error) {
		calls++
		return Contents{Count: 1, Entries: []string{"example.com"}}, nil
	}
	key := mobileFetchSource{"https://custom.example/a", "text"}
	first, err := c.fetch(context.Background(), key, download)
	if err != nil {
		t.Fatal(err)
	}
	first.Entries[0] = "mutation"
	second, err := c.fetch(context.Background(), key, download)
	if err != nil || calls != 1 || second.Entries[0] != "example.com" {
		t.Fatalf("cache miss or mutation: %+v %v calls=%d", second, err, calls)
	}
	e := c.entries[key]
	e.expires = time.Time{}
	c.entries[key] = e
	if _, err := c.fetch(context.Background(), key, download); err != nil || calls != 2 {
		t.Fatalf("expired entry reused: calls=%d err=%v", calls, err)
	}
	for i := range mobileCacheCapacity + 2 {
		if _, err := c.fetch(context.Background(), mobileFetchSource{fmt.Sprint(i), "text"}, download); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.entries) != mobileCacheCapacity || c.bytes > mobileCacheMaxBytes {
		t.Fatal("cache exceeded bounds")
	}
}

func TestMobileCacheCoalescesConcurrentDownloads(t *testing.T) {
	c := mobileProviderCache{entries: make(map[mobileFetchSource]mobileCacheEntry), flights: make(map[mobileFetchSource]*mobileCacheFlight)}
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	waiting := make(chan struct{}, 19)
	download := func(context.Context, string, string) (Contents, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return Contents{}, errors.New("uncached test result")
	}
	var wg sync.WaitGroup
	wg.Go(func() { _, _ = c.fetch(context.Background(), mobileFetchSource{"url", "text"}, download) })
	<-started
	for range 19 {
		wg.Go(func() {
			ctx := &observedWaitContext{Context: context.Background(), waiting: waiting}
			_, err := c.fetch(ctx, mobileFetchSource{"url", "text"}, download)
			if err == nil {
				t.Error("expected uncached test error")
			}
		})
	}
	for range 19 {
		select {
		case <-waiting:
		case <-time.After(5 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("callers did not join in-flight download")
		}
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate downloads: %d", calls.Load())
	}
}

type observedWaitContext struct {
	context.Context
	waiting chan<- struct{}
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.waiting <- struct{}{}
	return nil
}

func TestMobileCacheDoesNotCacheErrors(t *testing.T) {
	c := mobileProviderCache{entries: make(map[mobileFetchSource]mobileCacheEntry), flights: make(map[mobileFetchSource]*mobileCacheFlight)}
	calls := 0
	want := errors.New("download error")
	for range 2 {
		_, err := c.fetch(context.Background(), mobileFetchSource{"url", "text"}, func(context.Context, string, string) (Contents, error) {
			calls++
			return Contents{}, want
		})
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
	if calls != 2 || len(c.entries) != 0 {
		t.Fatal("error cached")
	}
}
