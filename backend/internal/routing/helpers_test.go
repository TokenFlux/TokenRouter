package routing

import (
	"context"
	"sync"
	"time"
)

// A probe round has to reach its repository claim before overlap or shutdown
// can be exercised deterministically. The fixture records the claim count and
// worker limit under a mutex, then optionally announces entry and waits for a
// test-controlled release.
//
// The start signal closes once across repeated claims, while the release
// channel can hold each claim until the channel closes or its context is
// canceled. Recording happens before that wait, allowing lifecycle and runner
// tests to inspect an admitted round while it is still blocked. Other
// repository operations return empty successful results so these tests remain
// focused on round admission and completion.

// groupAvailabilityProbeRunnerRepoStub coordinates observable claim entry
// with runner tests and records each requested batch limit. Configure its
// channels before launching a round; inspect counters through claimSnapshot
// while that round is active.
type groupAvailabilityProbeRunnerRepoStub struct {
	// mu protects claimCalls and claimLimit while a runner goroutine records
	// requests and the test goroutine inspects them through claimSnapshot.
	mu         sync.Mutex // claim counter access mutex
	claimCalls int        // recorded claim request count
	claimLimit int        // latest requested worker limit
	// claimStarted and releaseClaim coordinate ClaimDue with the test goroutine.
	// A supplied start channel closes once, while a supplied release channel
	// holds each claim until release or context cancellation.
	claimStarted chan struct{} // optional claim entry signal
	releaseClaim chan struct{} // optional claim release gate
	// startedOnce closes claimStarted at most once, so later claims can reuse
	// the same observation channel safely.
	startedOnce sync.Once // claim entry closure guard
}

// ClaimDue records the requested limit and announces entry before waiting for
// the optional release gate. Tests configure the channels before calling the
// runner; the method returns an empty due-group batch after release and
// returns the context error when cancellation wins.
//
// Counter writes are protected by mu, and startedOnce prevents repeated entry
// signals from closing the same channel twice. The wait happens after
// releasing mu, allowing claimSnapshot to observe the blocked request.
func (r *groupAvailabilityProbeRunnerRepoStub) ClaimDue(ctx context.Context, _ time.Time, _ time.Time, _ string, limit int) ([]GroupAvailabilityProbeDueGroup, error) {
	// Record both claim observations before signaling entry, so a test awakened
	// by claimStarted can inspect the complete request through claimSnapshot.
	r.mu.Lock()
	r.claimCalls++
	r.claimLimit = limit
	r.mu.Unlock()

	// Repeated claims share one start signal. Close it through startedOnce so
	// the first admitted round wakes the test without a later double close.
	if r.claimStarted != nil {
		r.startedOnce.Do(func() { close(r.claimStarted) })
	}
	// Hold the repository call after recording admission so tests can exercise a
	// concurrent tick or shutdown; cancellation lets a cooperative blocked claim
	// exit.
	if r.releaseClaim != nil {
		select {
		case <-r.releaseClaim:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, nil
}

// SaveResultAndScheduleNext accepts result writes as successful empty fixture
// operations.
func (r *groupAvailabilityProbeRunnerRepoStub) SaveResultAndScheduleNext(context.Context, *GroupAvailabilityProbeResult, time.Time) error {
	return nil
}

// GetSummaryByGroupIDs returns no historical summaries for these runner
// fixtures.
func (r *groupAvailabilityProbeRunnerRepoStub) GetSummaryByGroupIDs(context.Context, []int64, int, int, string, time.Time) (map[int64]*GroupAvailabilitySummary, error) {
	return nil, nil
}

// CleanupOldResults accepts retention maintenance so a round can reach
// claiming.
func (r *groupAvailabilityProbeRunnerRepoStub) CleanupOldResults(context.Context, time.Time) error {
	return nil
}

// claimSnapshot returns the recorded claim count and latest worker limit
// together. It takes the fixture mutex so tests can inspect those values
// while a ClaimDue call is blocked.
func (r *groupAvailabilityProbeRunnerRepoStub) claimSnapshot() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.claimCalls, r.claimLimit
}

// testPtrFloat64 allocates a supplied floating-point amount for nullable test
// prices.
func testPtrFloat64(value float64) *float64 { return &value }

// testPtrInt allocates a supplied integer value for nullable token ceilings.
func testPtrInt(value int) *int { return &value }
