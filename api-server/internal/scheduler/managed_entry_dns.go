package scheduler

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	managedEntryDNSBatchSize = 100
	managedEntryDNSInterval  = 30 * time.Second
)

type managedEntryDNSDueReconciler interface {
	ReconcileDue(context.Context, int) error
}

// ManagedEntryDNSScheduler runs one bounded due batch immediately and on each tick.
type ManagedEntryDNSScheduler struct {
	reconciler managedEntryDNSDueReconciler
	newTicker  func() (<-chan time.Time, func())
	mu         sync.Mutex
	cancel     context.CancelFunc
	done       chan struct{}
	stopped    bool
}

func NewManagedEntryDNSScheduler(reconciler managedEntryDNSDueReconciler) *ManagedEntryDNSScheduler {
	return newManagedEntryDNSScheduler(reconciler, func() (<-chan time.Time, func()) {
		ticker := time.NewTicker(managedEntryDNSInterval)
		return ticker.C, ticker.Stop
	})
}

func newManagedEntryDNSScheduler(reconciler managedEntryDNSDueReconciler, newTicker func() (<-chan time.Time, func())) *ManagedEntryDNSScheduler {
	return &ManagedEntryDNSScheduler{reconciler: reconciler, newTicker: newTicker}
}

// Start launches the loop once and returns; Stop cancels and joins that loop.
func (s *ManagedEntryDNSScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.done != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go s.run(ctx, s.done)
}

// Stop is safe before or during Start and waits for an in-flight cycle to exit.
func (s *ManagedEntryDNSScheduler) Stop() {
	s.mu.Lock()
	s.stopped = true
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *ManagedEntryDNSScheduler) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticks, stopTicker := s.newTicker()
	defer stopTicker()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.reconciler.ReconcileDue(ctx, managedEntryDNSBatchSize); err != nil && ctx.Err() == nil {
			log.Printf("[ManagedEntryDNS] reconciliation cycle failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}
