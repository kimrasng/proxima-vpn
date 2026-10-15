package main

import (
	"sync"
	"time"
)

const (
	closureLedgerTTL = 5 * time.Minute
	closureLedgerMax = 4096
)

// closureLedger remembers which transport-closure commands this agent already
// handled. The panel redelivers a command on every heartbeat until its short
// key expires, so that a lost response cannot lose a close. Without a ledger
// every redelivery after a successful close would hit a socket that is already
// gone and log a spurious failure.
//
// Only a successful close is remembered as done: a failed attempt must be
// retried on the next delivery. A failure is reported once per command, since
// the usual cause (the client already disconnected) repeats identically on
// every redelivery.
type closureLedger struct {
	mu     sync.Mutex
	done   map[string]time.Time
	warned map[string]time.Time
}

func newClosureLedger() *closureLedger {
	return &closureLedger{done: map[string]time.Time{}, warned: map[string]time.Time{}}
}

// shouldRun reports whether the command still needs to be attempted.
func (l *closureLedger) shouldRun(id string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireLocked(now)
	_, finished := l.done[id]
	return !finished
}

// markDone records a successful close.
func (l *closureLedger) markDone(id string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireLocked(now)
	l.done[id] = now
	delete(l.warned, id)
	l.trimLocked(l.done)
}

// shouldWarn is true only the first time a command is reported as failing.
func (l *closureLedger) shouldWarn(id string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireLocked(now)
	if _, seen := l.warned[id]; seen {
		return false
	}
	l.warned[id] = now
	l.trimLocked(l.warned)
	return true
}

func (l *closureLedger) expireLocked(now time.Time) {
	for _, m := range []map[string]time.Time{l.done, l.warned} {
		for id, at := range m {
			if now.Sub(at) > closureLedgerTTL {
				delete(m, id)
			}
		}
	}
}

// trimLocked evicts the oldest entries until m is within closureLedgerMax, so
// memory stays bounded even if the panel sends an unexpected flood of ids. It
// runs after an insert so the cap holds exactly, not one over.
func (l *closureLedger) trimLocked(m map[string]time.Time) {
	for len(m) > closureLedgerMax {
		var oldest string
		var oldestAt time.Time
		for id, at := range m {
			if oldest == "" || at.Before(oldestAt) {
				oldest, oldestAt = id, at
			}
		}
		delete(m, oldest)
	}
}
