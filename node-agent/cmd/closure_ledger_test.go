package main

import (
	"fmt"
	"testing"
	"time"
)

func TestClosureLedgerRunsUntilSuccessThenSkips(t *testing.T) {
	l := newClosureLedger()
	now := time.Unix(1_000, 0)
	if !l.shouldRun("a", now) {
		t.Fatal("a new command must run")
	}
	// A failed attempt is not recorded as done, so redelivery retries it.
	if !l.shouldRun("a", now.Add(10*time.Second)) {
		t.Fatal("a command that has not succeeded must be retried")
	}
	l.markDone("a", now.Add(10*time.Second))
	if l.shouldRun("a", now.Add(20*time.Second)) {
		t.Fatal("a command that already succeeded must not run again")
	}
	if !l.shouldRun("b", now.Add(20*time.Second)) {
		t.Fatal("success of one command must not affect another")
	}
}

func TestClosureLedgerWarnsOncePerCommand(t *testing.T) {
	l := newClosureLedger()
	now := time.Unix(1_000, 0)
	if !l.shouldWarn("a", now) {
		t.Fatal("first failure must be reported")
	}
	for i := 1; i <= 5; i++ {
		if l.shouldWarn("a", now.Add(time.Duration(i)*10*time.Second)) {
			t.Fatalf("redelivery %d of the same failing command must stay quiet", i)
		}
	}
	if !l.shouldWarn("b", now) {
		t.Fatal("a different command must still be reported")
	}
}

func TestClosureLedgerSuccessClearsEarlierWarning(t *testing.T) {
	l := newClosureLedger()
	now := time.Unix(1_000, 0)
	l.shouldWarn("a", now)
	l.markDone("a", now.Add(time.Second))
	if l.shouldRun("a", now.Add(2*time.Second)) {
		t.Fatal("done command must be skipped")
	}
}

func TestClosureLedgerForgetsAfterTTL(t *testing.T) {
	l := newClosureLedger()
	now := time.Unix(1_000, 0)
	l.markDone("a", now)
	l.shouldWarn("b", now)
	later := now.Add(closureLedgerTTL + time.Second)
	if !l.shouldRun("a", later) {
		t.Fatal("entries must expire so the ledger stays bounded in time")
	}
	if !l.shouldWarn("b", later) {
		t.Fatal("warnings must expire too")
	}
}

func TestClosureLedgerIsBounded(t *testing.T) {
	l := newClosureLedger()
	now := time.Unix(1_000, 0)
	for i := 0; i < closureLedgerMax+500; i++ {
		l.markDone(fmt.Sprintf("id-%d", i), now.Add(time.Duration(i)*time.Millisecond))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.done); n > closureLedgerMax {
		t.Fatalf("ledger holds %d entries, cap is %d", n, closureLedgerMax)
	}
	if _, ok := l.done["id-0"]; ok {
		t.Fatal("the oldest entry should have been evicted first")
	}
	if _, ok := l.done[fmt.Sprintf("id-%d", closureLedgerMax+499)]; !ok {
		t.Fatal("the newest entry must be kept")
	}
}
