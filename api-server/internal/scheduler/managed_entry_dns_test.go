package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type dnsReconcileFunc func(context.Context, int) error

func (f dnsReconcileFunc) ReconcileDue(ctx context.Context, limit int) error {
	return f(ctx, limit)
}

func Test_ManagedEntryDNSScheduler_runs_immediately_when_started(t *testing.T) {
	// Given: an idle clock and a reconciler that reports each invocation.
	called := make(chan int, 1)
	ticks := make(chan time.Time)
	s := newManagedEntryDNSScheduler(dnsReconcileFunc(func(_ context.Context, limit int) error {
		called <- limit
		return nil
	}), func() (<-chan time.Time, func()) { return ticks, func() {} })

	// When: the scheduler starts, with no tick delivered.
	s.Start(context.Background())
	defer s.Stop()

	// Then: one bounded reconciliation starts immediately.
	select {
	case limit := <-called:
		if limit != managedEntryDNSBatchSize {
			t.Fatalf("limit = %d, want %d", limit, managedEntryDNSBatchSize)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("immediate reconciliation did not run")
	}
}

func Test_ManagedEntryDNSScheduler_continues_after_cycle_error_on_tick(t *testing.T) {
	// Given: the first reconciliation fails and subsequent calls succeed.
	called := make(chan int)
	ticks := make(chan time.Time)
	count := 0
	s := newManagedEntryDNSScheduler(dnsReconcileFunc(func(_ context.Context, limit int) error {
		count++
		called <- limit
		if count == 1 {
			return errors.New("transient cycle failure")
		}
		return nil
	}), func() (<-chan time.Time, func()) { return ticks, func() {} })
	s.Start(context.Background())
	defer s.Stop()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("initial cycle did not run")
	}

	// When: another clock tick arrives after the failed cycle.
	select {
	case ticks <- time.Time{}:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not accept tick")
	}

	// Then: the following reconciliation still runs with the same bound.
	select {
	case limit := <-called:
		if limit != managedEntryDNSBatchSize {
			t.Fatalf("limit = %d, want %d", limit, managedEntryDNSBatchSize)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconciliation did not continue after error")
	}
}

func Test_ManagedEntryDNSScheduler_stop_joins_cancelled_cycle(t *testing.T) {
	// Given: the immediate cycle is blocked until its context is cancelled.
	entered := make(chan struct{})
	exited := make(chan struct{})
	tickerStopped := make(chan struct{})
	s := newManagedEntryDNSScheduler(dnsReconcileFunc(func(ctx context.Context, _ int) error {
		close(entered)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	}), func() (<-chan time.Time, func()) {
		return make(chan time.Time), func() { close(tickerStopped) }
	})
	s.Start(context.Background())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("cycle did not start")
	}

	// When: Stop is called concurrently by multiple callers.
	var callers sync.WaitGroup
	for range 2 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			s.Stop()
		}()
	}
	joined := make(chan struct{})
	go func() { callers.Wait(); close(joined) }()

	// Then: both calls return only after the cycle and ticker have exited.
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not join the cycle")
	}
	select {
	case <-exited:
	default:
		t.Fatal("Stop returned before reconciliation exited")
	}
	select {
	case <-tickerStopped:
	default:
		t.Fatal("Stop returned before ticker stopped")
	}
}

func Test_ManagedEntryDNSScheduler_stop_before_start_prevents_work(t *testing.T) {
	// Given: an unstarted scheduler.
	called := make(chan struct{}, 1)
	s := newManagedEntryDNSScheduler(dnsReconcileFunc(func(context.Context, int) error {
		called <- struct{}{}
		return nil
	}), func() (<-chan time.Time, func()) { return nil, func() {} })

	// When: Stop wins before Start.
	s.Stop()
	s.Start(context.Background())

	// Then: no cycle can start, and repeated Stop is safe.
	s.Stop()
	select {
	case <-called:
		t.Fatal("cycle started after Stop")
	default:
	}
}

func Test_ManagedEntryDNSScheduler_exits_on_parent_cancellation(t *testing.T) {
	// Given: a running scheduler whose first cycle has completed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{})
	tickerStopped := make(chan struct{})
	s := newManagedEntryDNSScheduler(dnsReconcileFunc(func(context.Context, int) error {
		close(called)
		return nil
	}), func() (<-chan time.Time, func()) {
		return make(chan time.Time), func() { close(tickerStopped) }
	})
	s.Start(ctx)
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("initial cycle did not run")
	}

	// When: the parent context is cancelled without calling Stop first.
	cancel()
	select {
	case <-tickerStopped:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not exit on cancellation")
	}

	// Then: Stop joins the already-exited loop without blocking.
	s.Stop()
}

func Test_ManagedEntryDNSScheduler_stop_races_start_without_leaking(t *testing.T) {
	// Given: fresh schedulers with cancellable reconciliation.
	for range 100 {
		s := newManagedEntryDNSScheduler(dnsReconcileFunc(func(ctx context.Context, _ int) error {
			<-ctx.Done()
			return ctx.Err()
		}), func() (<-chan time.Time, func()) { return nil, func() {} })
		start := make(chan struct{})
		var callers sync.WaitGroup
		callers.Add(2)

		// When: Start and Stop contend on the same instance.
		go func() { defer callers.Done(); <-start; s.Start(context.Background()) }()
		go func() { defer callers.Done(); <-start; s.Stop() }()
		close(start)
		joined := make(chan struct{})
		go func() { callers.Wait(); close(joined) }()

		// Then: both callers finish; Stop cannot leave a running loop behind.
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent Start/Stop did not finish")
		}
		s.Stop()
	}
}
