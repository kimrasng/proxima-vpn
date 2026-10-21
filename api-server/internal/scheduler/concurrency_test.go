package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func TestNewestSessionWinsRegardlessOfAddressActivity(t *testing.T) {
	starts := map[string]int64{"established": 100, "newest": 200, "other": 150}
	if got := pickEvictionTarget(starts); got != "newest" {
		t.Fatalf("target=%s", got)
	}
}
func TestUnknownStartCannotBeSelected(t *testing.T) {
	if got := pickEvictionTarget(nil); got != "" {
		t.Fatalf("target=%s", got)
	}
}
func TestTieIsStable(t *testing.T) {
	if got := pickEvictionTarget(map[string]int64{"bbb": 10, "aaa": 10}); got != "" {
		t.Fatalf("ambiguous same-second arrival must not select a victim: %s", got)
	}
}
func TestConcurrencyEnforcementIsOnByDefaultWithKillSwitch(t *testing.T) {
	for _, on := range []string{"", "1", "true", "flase"} {
		t.Setenv("ENFORCE_CONCURRENCY", on)
		if !NewConcurrencyScheduler(nil, nil).enforce {
			t.Fatalf("ENFORCE_CONCURRENCY=%q must keep guarded enforcement on", on)
		}
	}
	for _, off := range []string{"0", "false", "OFF", " no "} {
		t.Setenv("ENFORCE_CONCURRENCY", off)
		if NewConcurrencyScheduler(nil, nil).enforce {
			t.Fatalf("ENFORCE_CONCURRENCY=%q must fall back to observation", off)
		}
	}
}

func TestTunnelReconciliationRunsForUnlimitedAccounts(t *testing.T) {
	t.Setenv("ENFORCE_CONCURRENCY", "")
	s := NewConcurrencyScheduler(nil, nil)
	calls := 0
	s.reconcileTunnelSessions = func(_ context.Context, account string) (services.TunnelSessionObservation, []string, error) {
		calls++
		if account != "account-unlimited" {
			t.Fatalf("unexpected account %q", account)
		}
		return services.TunnelSessionObservation{Complete: true, Sessions: []services.ObservedTunnelSession{}}, nil, nil
	}
	s.checkTunnelSessions(context.Background(), capRow{userID: "account-unlimited", cap: 0})
	if calls != 1 {
		t.Fatalf("reconciliation calls=%d, want 1", calls)
	}
}

func TestTunnelReconciliationThrottlesPerAccount(t *testing.T) {
	s := NewConcurrencyScheduler(nil, nil)
	calls := 0
	s.reconcileTunnelSessions = func(context.Context, string) (services.TunnelSessionObservation, []string, error) {
		calls++
		return services.TunnelSessionObservation{Complete: true, Sessions: []services.ObservedTunnelSession{}}, nil, nil
	}
	s.checkTunnelSessions(context.Background(), capRow{userID: "account", cap: 0})
	s.checkTunnelSessions(context.Background(), capRow{userID: "account", cap: 0})
	if calls != 1 {
		t.Fatalf("calls=%d, want one within throttle window", calls)
	}
	s.sessionReconcileAt["account"] = time.Now().Add(-time.Second)
	s.checkTunnelSessions(context.Background(), capRow{userID: "account", cap: 0})
	if calls != 2 {
		t.Fatalf("calls=%d, want retry after throttle window", calls)
	}
}
func TestTunnelReconciliationFailureResetsEnforcementStrikes(t *testing.T) {
	s := NewConcurrencyScheduler(nil, nil)
	s.sessionStrikes["account"] = 1
	s.reconcileTunnelSessions = func(context.Context, string) (services.TunnelSessionObservation, []string, error) {
		return services.TunnelSessionObservation{}, nil, errors.New("report receipts changed")
	}
	s.checkTunnelSessions(context.Background(), capRow{userID: "account", cap: 1})
	if _, ok := s.sessionStrikes["account"]; ok {
		t.Fatal("incomplete reconciliation retained an enforcement strike")
	}
}

const clientAddressReportLine = "client address cap exceeded, observation only"

// captureLog redirects the standard logger for the duration of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

func addressObservation(complete bool, addresses ...string) services.ClientAddressObservation {
	return services.ClientAddressObservation{Complete: complete, Addresses: addresses}
}

func TestClientAddressReconciliationRunsForUnlimitedAccounts(t *testing.T) {
	t.Setenv("ENFORCE_CONCURRENCY", "")
	logs := captureLog(t)
	s := NewConcurrencyScheduler(nil, nil)
	calls := 0
	s.reconcileClientAddresses = func(_ context.Context, account string) (services.ClientAddressObservation, []string, error) {
		calls++
		if account != "account-unlimited" {
			t.Fatalf("unexpected account %q", account)
		}
		return addressObservation(true, "198.51.100.1", "198.51.100.2", "198.51.100.3"), []string{"stale"}, nil
	}
	s.checkClientAddresses(context.Background(), capRow{userID: "account-unlimited", cap: 0})
	if calls != 1 {
		t.Fatalf("reconciliation calls=%d, want 1", calls)
	}
	if !strings.Contains(logs.String(), "client address slots released: user=account-unlimited count=1") {
		t.Fatalf("released slots not logged: %q", logs.String())
	}
	if strings.Contains(logs.String(), clientAddressReportLine) {
		t.Fatalf("unlimited account reported over-limit: %q", logs.String())
	}
	if len(s.addressStrikes) != 0 {
		t.Fatalf("unlimited account accumulated strikes: %v", s.addressStrikes)
	}
}

func TestClientAddressReconciliationThrottlesPerAccount(t *testing.T) {
	s := NewConcurrencyScheduler(nil, nil)
	calls := map[string]int{}
	s.reconcileClientAddresses = func(_ context.Context, account string) (services.ClientAddressObservation, []string, error) {
		calls[account]++
		return addressObservation(true), nil, nil
	}
	s.checkClientAddresses(context.Background(), capRow{userID: "account", cap: 0})
	s.checkClientAddresses(context.Background(), capRow{userID: "account", cap: 0})
	if calls["account"] != 1 {
		t.Fatalf("calls=%d, want one within throttle window", calls["account"])
	}
	s.checkClientAddresses(context.Background(), capRow{userID: "other", cap: 0})
	if calls["other"] != 1 {
		t.Fatalf("other account calls=%d, want independent throttle", calls["other"])
	}
	if until := time.Until(s.addressReconcileAt["account"]); until <= 0 || until > sessionReconcileInterval {
		t.Fatalf("throttle window=%v, want within (0, %v]", until, sessionReconcileInterval)
	}
	s.addressReconcileAt["account"] = time.Now().Add(-time.Second)
	s.checkClientAddresses(context.Background(), capRow{userID: "account", cap: 0})
	if calls["account"] != 2 {
		t.Fatalf("calls=%d, want retry after throttle window", calls["account"])
	}
}

func TestClientAddressReconciliationFailureResetsStrikes(t *testing.T) {
	logs := captureLog(t)
	s := NewConcurrencyScheduler(nil, nil)
	s.addressStrikes["account"] = 1
	s.reconcileClientAddresses = func(context.Context, string) (services.ClientAddressObservation, []string, error) {
		return services.ClientAddressObservation{}, nil, errors.New("report receipts changed")
	}
	s.checkClientAddresses(context.Background(), capRow{userID: "account", cap: 1})
	if _, ok := s.addressStrikes["account"]; ok {
		t.Fatal("failed reconciliation retained an address strike")
	}
	if !strings.Contains(logs.String(), "client address reconciliation: user=account err=report receipts changed") {
		t.Fatalf("error not logged: %q", logs.String())
	}
}

func TestClientAddressIncompleteObservationResetsStrikes(t *testing.T) {
	logs := captureLog(t)
	s := NewConcurrencyScheduler(nil, nil)
	s.addressStrikes["account"] = 1
	s.reconcileClientAddresses = func(context.Context, string) (services.ClientAddressObservation, []string, error) {
		return addressObservation(false, "198.51.100.1", "198.51.100.2"), nil, nil
	}
	s.checkClientAddresses(context.Background(), capRow{userID: "account", cap: 1})
	if _, ok := s.addressStrikes["account"]; ok {
		t.Fatal("incomplete observation retained an address strike")
	}
	if logs.Len() != 0 {
		t.Fatalf("incomplete observation without error must not log: %q", logs.String())
	}
}

func TestClientAddressOverCapNeedsConsecutiveCompleteSweeps(t *testing.T) {
	// Strike counting is the subject here; the observation-only report line is
	// only emitted when the kill switch has turned closing off.
	t.Setenv("ENFORCE_CLIENT_ADDRESS_LIMIT", "0")
	logs := captureLog(t)
	s := NewConcurrencyScheduler(nil, nil)
	s.threshold = 3
	s.reconcileClientAddresses = func(context.Context, string) (services.ClientAddressObservation, []string, error) {
		return addressObservation(true, "198.51.100.1", "198.51.100.2", "198.51.100.3"), nil, nil
	}
	sweep := func() {
		s.addressReconcileAt["account"] = time.Time{}
		s.checkClientAddresses(context.Background(), capRow{userID: "account", cap: 2})
	}
	for i := 1; i < s.threshold; i++ {
		sweep()
		if strings.Contains(logs.String(), clientAddressReportLine) {
			t.Fatalf("reported after %d sweeps, want %d", i, s.threshold)
		}
		if got := s.addressStrikes["account"]; got != i {
			t.Fatalf("strikes=%d after %d sweeps", got, i)
		}
	}
	sweep()
	want := "client address cap exceeded, observation only: user=account addresses=3 cap=2"
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("missing report line %q in %q", want, logs.String())
	}
	if strings.Contains(logs.String(), "198.51.100.") {
		t.Fatalf("raw client address leaked into logs: %q", logs.String())
	}
	if got := s.addressStrikes["account"]; got != 0 {
		t.Fatalf("strikes=%d after report, want reset to 0", got)
	}
	// A new excess run must again take a full threshold of sweeps.
	logs.Reset()
	sweep()
	if strings.Contains(logs.String(), clientAddressReportLine) {
		t.Fatal("reported again before accumulating a new threshold of strikes")
	}
}

func TestClientAddressAtOrBelowCapClearsStrikes(t *testing.T) {
	logs := captureLog(t)
	s := NewConcurrencyScheduler(nil, nil)
	s.threshold = 2
	observation := addressObservation(true, "198.51.100.1", "198.51.100.2")
	s.reconcileClientAddresses = func(context.Context, string) (services.ClientAddressObservation, []string, error) {
		return observation, nil, nil
	}
	for _, cap := range []int{2, 3} {
		s.addressStrikes["account"] = 1
		s.addressReconcileAt["account"] = time.Time{}
		s.checkClientAddresses(context.Background(), capRow{userID: "account", cap: cap})
		if _, ok := s.addressStrikes["account"]; ok {
			t.Fatalf("cap=%d with 2 addresses retained an address strike", cap)
		}
	}
	if strings.Contains(logs.String(), clientAddressReportLine) {
		t.Fatalf("reported at or below cap: %q", logs.String())
	}
}

func TestConcurrencyDefaultsAndOverrides(t *testing.T) {
	t.Setenv("CONCURRENCY_GRACE", "")
	t.Setenv("CONCURRENCY_STRIKES", "")
	if nonnegativeEnv("CONCURRENCY_GRACE", 0) != 0 || positiveEnv("CONCURRENCY_STRIKES", 2) != 2 {
		t.Fatal("defaults")
	}
	t.Setenv("CONCURRENCY_GRACE", "1")
	t.Setenv("CONCURRENCY_STRIKES", "3")
	if nonnegativeEnv("CONCURRENCY_GRACE", 0) != 1 || positiveEnv("CONCURRENCY_STRIKES", 2) != 3 {
		t.Fatal("overrides")
	}
}

// Fixture client addresses. They must never appear in any log line.
const (
	addrOldest = "198.51.100.1"
	addrMiddle = "198.51.100.2"
	addrNewest = "198.51.100.3"
)

var fixtureAddresses = []string{addrOldest, addrMiddle, addrNewest}

// startedObservation builds an observation whose addresses carry start times.
func startedObservation(complete bool, starts map[string]int64) services.ClientAddressObservation {
	addresses := make([]string, 0, len(starts))
	for address := range starts {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	return services.ClientAddressObservation{Complete: complete, Addresses: addresses, Starts: starts}
}

func distinctStarts(complete bool) services.ClientAddressObservation {
	return startedObservation(complete, map[string]int64{addrOldest: 100, addrMiddle: 200, addrNewest: 300})
}

type closureCall struct {
	observation services.ClientAddressObservation
	denied      []string
}

// closureRecorder is an injected queueAddressClosures that never touches Redis.
type closureRecorder struct {
	calls  []closureCall
	queued int
	err    error
}

func (r *closureRecorder) queue(_ context.Context, observation services.ClientAddressObservation, denied []string) (int, error) {
	r.calls = append(r.calls, closureCall{observation: observation, denied: append([]string(nil), denied...)})
	return r.queued, r.err
}

// newAddressScheduler returns a scheduler with threshold 2 whose address
// reconciliation always returns observation and whose closures are recorded.
func newAddressScheduler(t *testing.T, enabled bool, observation services.ClientAddressObservation) (*ConcurrencyScheduler, *closureRecorder) {
	t.Helper()
	flag := "0" // the kill switch; enforcement is otherwise on by default
	if enabled {
		flag = ""
	}
	t.Setenv("ENFORCE_CLIENT_ADDRESS_LIMIT", flag)
	s := NewConcurrencyScheduler(nil, nil)
	s.threshold = 2
	s.reconcileClientAddresses = func(context.Context, string) (services.ClientAddressObservation, []string, error) {
		return observation, nil, nil
	}
	recorder := &closureRecorder{queued: 1}
	s.queueAddressClosures = recorder.queue
	return s, recorder
}

// sweepAddresses runs n unthrottled address checks for one account.
func sweepAddresses(s *ConcurrencyScheduler, account string, cap, n int) {
	for i := 0; i < n; i++ {
		s.addressReconcileAt[account] = time.Time{}
		s.checkClientAddresses(context.Background(), capRow{userID: account, cap: cap})
	}
}

func assertNoFixtureAddresses(t *testing.T, logs *bytes.Buffer) {
	t.Helper()
	for _, line := range strings.Split(logs.String(), "\n") {
		for _, address := range fixtureAddresses {
			if strings.Contains(line, address) {
				t.Fatalf("raw client address leaked into log line %q", line)
			}
		}
	}
	if strings.Contains(logs.String(), "198.51.100.") {
		t.Fatalf("client address prefix leaked into logs: %q", logs.String())
	}
}

func TestClientAddressEnforcementIsOnByDefaultWithKillSwitch(t *testing.T) {
	t.Setenv("ENFORCE_CLIENT_ADDRESS_LIMIT", "")
	t.Setenv("ENFORCE_CONCURRENCY", "0")
	s := NewConcurrencyScheduler(nil, nil)
	if !s.enforceAddresses {
		t.Fatal("address enforcement must default on")
	}
	if s.enforce {
		t.Fatal("address enforcement must not enable UUID enforcement")
	}
	if s.queueAddressClosures == nil {
		t.Fatal("default closure queue func must be wired")
	}
	for _, off := range []string{"0", "false", "off", "no"} {
		t.Setenv("ENFORCE_CLIENT_ADDRESS_LIMIT", off)
		if NewConcurrencyScheduler(nil, nil).enforceAddresses {
			t.Fatalf("%q must turn address enforcement off", off)
		}
	}
	// A typo must never silently disable the limit.
	t.Setenv("ENFORCE_CLIENT_ADDRESS_LIMIT", "flase")
	if !NewConcurrencyScheduler(nil, nil).enforceAddresses {
		t.Fatal("an unrecognised value must leave enforcement on")
	}
	// Independent of UUID enforcement in both directions.
	t.Setenv("ENFORCE_CLIENT_ADDRESS_LIMIT", "0")
	t.Setenv("ENFORCE_CONCURRENCY", "1")
	s = NewConcurrencyScheduler(nil, nil)
	if s.enforceAddresses || !s.enforce {
		t.Fatalf("flags must be independent: addresses=%v uuid=%v", s.enforceAddresses, s.enforce)
	}
}

func TestStartLogsAddressEnforcementState(t *testing.T) {
	logs := captureLog(t)
	s, _ := newAddressScheduler(t, true, distinctStarts(true))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Start(ctx)
	if !strings.Contains(logs.String(), "client address cap enforcement (enabled=true)") {
		t.Fatalf("address enforcement state not logged: %q", logs.String())
	}
}

func TestClientAddressEnforcementOffNeverQueuesClosures(t *testing.T) {
	logs := captureLog(t)
	s, recorder := newAddressScheduler(t, false, distinctStarts(true))
	sweepAddresses(s, "account", 2, 3*s.threshold)
	if len(recorder.calls) != 0 {
		t.Fatalf("closures queued with enforcement off: %d", len(recorder.calls))
	}
	if !strings.Contains(logs.String(), "client address cap exceeded, observation only: user=account addresses=3 cap=2") {
		t.Fatalf("report-only line missing: %q", logs.String())
	}
	if strings.Contains(logs.String(), "transport closure") {
		t.Fatalf("closure activity logged with enforcement off: %q", logs.String())
	}
	assertNoFixtureAddresses(t, logs)
}

func TestClientAddressEnforcementQueuesNewestAddressesAfterThreshold(t *testing.T) {
	logs := captureLog(t)
	s, recorder := newAddressScheduler(t, true, distinctStarts(true))
	sweepAddresses(s, "account", 2, s.threshold-1)
	if len(recorder.calls) != 0 {
		t.Fatalf("queued before %d consecutive sweeps", s.threshold)
	}
	sweepAddresses(s, "account", 2, 1)
	if len(recorder.calls) != 1 {
		t.Fatalf("queue calls=%d, want exactly one after %d sweeps", len(recorder.calls), s.threshold)
	}
	call := recorder.calls[0]
	if got := strings.Join(call.denied, ","); got != addrNewest {
		t.Fatalf("denied=%q, want only the newest address", got)
	}
	if !call.observation.Complete || len(call.observation.Addresses) != 3 {
		t.Fatalf("queue func got unexpected observation: %+v", call.observation)
	}
	want := "client address cap exceeded, transport closure queued: user=account addresses=3 cap=2 denied=1 queued=1"
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("missing log line %q in %q", want, logs.String())
	}
	if strings.Contains(logs.String(), clientAddressReportLine) {
		t.Fatalf("report-only line logged with enforcement on: %q", logs.String())
	}
	if got := s.addressStrikes["account"]; got != 0 {
		t.Fatalf("strikes=%d after attempt, want reset to 0", got)
	}
	if until := time.Until(s.addressClosureAt["account"]); until <= 0 || until > addressClosureCooldown {
		t.Fatalf("cooldown=%v, want within (0, %v]", until, addressClosureCooldown)
	}
	assertNoFixtureAddresses(t, logs)
}

func TestClientAddressEnforcementDeniesEveryAddressBeyondCap(t *testing.T) {
	captureLog(t)
	s, recorder := newAddressScheduler(t, true, distinctStarts(true))
	sweepAddresses(s, "account", 1, s.threshold)
	if len(recorder.calls) != 1 {
		t.Fatalf("queue calls=%d, want 1", len(recorder.calls))
	}
	if got := strings.Join(recorder.calls[0].denied, ","); got != addrMiddle+","+addrNewest {
		t.Fatalf("denied=%q, want the two newest addresses", got)
	}
}

func TestClientAddressEnforcementAmbiguousCutoffQueuesNothing(t *testing.T) {
	logs := captureLog(t)
	// The cap-th and (cap+1)-th addresses started in the same second.
	observation := startedObservation(true, map[string]int64{addrOldest: 100, addrMiddle: 200, addrNewest: 200})
	s, recorder := newAddressScheduler(t, true, observation)
	sweepAddresses(s, "account", 2, s.threshold)
	if len(recorder.calls) != 0 {
		t.Fatalf("queued despite an ambiguous cutoff: %+v", recorder.calls)
	}
	want := "client address cap exceeded, no transport closure: user=account addresses=3 cap=2 reason=ambiguous_cutoff"
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("missing log line %q in %q", want, logs.String())
	}
	if got := s.addressStrikes["account"]; got != 0 {
		t.Fatalf("strikes=%d after attempt, want reset to 0", got)
	}
	if _, ok := s.addressClosureAt["account"]; ok {
		t.Fatal("cooldown set although nothing was attempted")
	}
	assertNoFixtureAddresses(t, logs)
}

func TestClientAddressEnforcementUnknownStartQueuesNothing(t *testing.T) {
	logs := captureLog(t)
	observation := startedObservation(true, map[string]int64{addrOldest: 100, addrMiddle: 200, addrNewest: 0})
	s, recorder := newAddressScheduler(t, true, observation)
	sweepAddresses(s, "account", 2, s.threshold)
	if len(recorder.calls) != 0 {
		t.Fatalf("queued despite an unknown start: %+v", recorder.calls)
	}
	if !strings.Contains(logs.String(), "reason=unknown_start") {
		t.Fatalf("reason not logged: %q", logs.String())
	}
	assertNoFixtureAddresses(t, logs)
}

func TestClientAddressEnforcementCooldownIsPerAccount(t *testing.T) {
	logs := captureLog(t)
	s, recorder := newAddressScheduler(t, true, distinctStarts(true))
	sweepAddresses(s, "account", 2, s.threshold)
	if len(recorder.calls) != 1 {
		t.Fatalf("queue calls=%d, want 1", len(recorder.calls))
	}
	logs.Reset()
	sweepAddresses(s, "account", 2, 2*s.threshold)
	if len(recorder.calls) != 1 {
		t.Fatalf("queue calls=%d during cooldown, want still 1", len(recorder.calls))
	}
	if !strings.Contains(logs.String(), "no transport closure: user=account addresses=3 cap=2 reason=cooldown") {
		t.Fatalf("cooldown not logged: %q", logs.String())
	}
	sweepAddresses(s, "other", 2, s.threshold)
	if len(recorder.calls) != 2 {
		t.Fatalf("queue calls=%d, want the other account to be independent", len(recorder.calls))
	}
	s.addressClosureAt["account"] = time.Now().Add(-time.Second)
	sweepAddresses(s, "account", 2, s.threshold)
	if len(recorder.calls) != 3 {
		t.Fatalf("queue calls=%d, want a retry after the cooldown expired", len(recorder.calls))
	}
	assertNoFixtureAddresses(t, logs)
}

func TestClientAddressEnforcementQueueErrorIsLoggedAndSetsCooldown(t *testing.T) {
	logs := captureLog(t)
	s, recorder := newAddressScheduler(t, true, distinctStarts(true))
	recorder.queued, recorder.err = 0, errors.New("transport closure storage unavailable")
	sweepAddresses(s, "account", 2, s.threshold)
	if len(recorder.calls) != 1 {
		t.Fatalf("queue calls=%d, want 1", len(recorder.calls))
	}
	want := "transport closure failed: user=account addresses=3 cap=2 denied=1 queued=0 err=transport closure storage unavailable"
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("missing log line %q in %q", want, logs.String())
	}
	if strings.Contains(logs.String(), "transport closure queued") {
		t.Fatalf("failure logged as success: %q", logs.String())
	}
	if until := time.Until(s.addressClosureAt["account"]); until <= 0 || until > addressClosureCooldown {
		t.Fatalf("cooldown=%v after an error, want within (0, %v]", until, addressClosureCooldown)
	}
	if got := s.addressStrikes["account"]; got != 0 {
		t.Fatalf("strikes=%d after attempt, want reset to 0", got)
	}
	sweepAddresses(s, "account", 2, s.threshold)
	if len(recorder.calls) != 1 {
		t.Fatalf("queue calls=%d, want the cooldown to suppress the retry", len(recorder.calls))
	}
	assertNoFixtureAddresses(t, logs)
}

func TestClientAddressEnforcementNothingQueuedDoesNotSetCooldown(t *testing.T) {
	captureLog(t)
	s, recorder := newAddressScheduler(t, true, distinctStarts(true))
	recorder.queued = 0
	sweepAddresses(s, "account", 2, s.threshold)
	if len(recorder.calls) != 1 {
		t.Fatalf("queue calls=%d, want 1", len(recorder.calls))
	}
	if _, ok := s.addressClosureAt["account"]; ok {
		t.Fatal("cooldown set although no closure was queued and no error occurred")
	}
}

func TestClientAddressEnforcementNeverActsOnIncompleteOrUncappedAccounts(t *testing.T) {
	logs := captureLog(t)
	s, recorder := newAddressScheduler(t, true, distinctStarts(false))
	sweepAddresses(s, "incomplete", 2, 3*s.threshold)
	if len(recorder.calls) != 0 {
		t.Fatalf("queued for an incomplete observation: %d", len(recorder.calls))
	}
	if len(s.addressStrikes) != 0 {
		t.Fatalf("incomplete observation accumulated strikes: %v", s.addressStrikes)
	}

	s, recorder = newAddressScheduler(t, true, distinctStarts(true))
	for _, cap := range []int{0, -1} {
		sweepAddresses(s, "uncapped", cap, 3*s.threshold)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("queued for an account with cap <= 0: %d", len(recorder.calls))
	}
	if len(s.addressStrikes) != 0 {
		t.Fatalf("uncapped account accumulated strikes: %v", s.addressStrikes)
	}

	// Direct call: the guards hold even if a caller bypasses the strike logic.
	s.closeExcessClientAddresses(context.Background(), capRow{userID: "direct", cap: 0}, distinctStarts(true), time.Now())
	s.closeExcessClientAddresses(context.Background(), capRow{userID: "direct", cap: 2}, distinctStarts(false), time.Now())
	if len(recorder.calls) != 0 {
		t.Fatalf("direct call bypassed the guards: %d", len(recorder.calls))
	}
	assertNoFixtureAddresses(t, logs)
}

func TestClientAddressEnforcementNilQueueFuncIsNoOp(t *testing.T) {
	logs := captureLog(t)
	s, _ := newAddressScheduler(t, true, distinctStarts(true))
	s.queueAddressClosures = nil
	s.addressClosureAt = nil
	sweepAddresses(s, "account", 2, 2*s.threshold)
	if got := s.addressStrikes["account"]; got != 0 {
		t.Fatalf("strikes=%d, want reset after the attempt", got)
	}
	assertNoFixtureAddresses(t, logs)
}

func TestClientAddressEnforcementNeverLogsRawAddresses(t *testing.T) {
	logs := captureLog(t)
	scenarios := []struct {
		name        string
		observation services.ClientAddressObservation
		queued      int
		err         error
	}{
		{"queued", distinctStarts(true), 1, nil},
		{"error", distinctStarts(true), 0, errors.New("transport closure storage unavailable")},
		{"ambiguous", startedObservation(true, map[string]int64{addrOldest: 100, addrMiddle: 200, addrNewest: 200}), 1, nil},
		{"unknown start", startedObservation(true, map[string]int64{addrOldest: 100, addrMiddle: 200, addrNewest: 0}), 1, nil},
		{"incomplete", distinctStarts(false), 1, nil},
	}
	for _, sc := range scenarios {
		s, recorder := newAddressScheduler(t, true, sc.observation)
		recorder.queued, recorder.err = sc.queued, sc.err
		// Twice through the threshold so the cooldown line is exercised too.
		sweepAddresses(s, "account", 2, 2*s.threshold)
		sweepAddresses(s, "other", 1, 2*s.threshold)
	}
	if logs.Len() == 0 {
		t.Fatal("scenarios produced no log output to check")
	}
	assertNoFixtureAddresses(t, logs)
}
