package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

// groupAvailabilityProbeRunnerRepoStub 记录领取参数，并可阻塞首轮领取以验证 runner 非重入。
type groupAvailabilityProbeRunnerRepoStub struct {
	mu           sync.Mutex
	claimCalls   int
	claimLimit   int
	claimStarted chan struct{}
	releaseClaim chan struct{}
	startedOnce  sync.Once
}

func (r *groupAvailabilityProbeRunnerRepoStub) ClaimDue(ctx context.Context, _ time.Time, _ time.Time, _ string, limit int) ([]GroupAvailabilityProbeDueGroup, error) {
	r.mu.Lock()
	r.claimCalls++
	r.claimLimit = limit
	r.mu.Unlock()

	if r.claimStarted != nil {
		r.startedOnce.Do(func() { close(r.claimStarted) })
	}
	if r.releaseClaim != nil {
		select {
		case <-r.releaseClaim:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, nil
}

func (r *groupAvailabilityProbeRunnerRepoStub) SaveResultAndScheduleNext(context.Context, *GroupAvailabilityProbeResult, time.Time) error {
	return nil
}

func (r *groupAvailabilityProbeRunnerRepoStub) GetSummaryByGroupIDs(context.Context, []int64, int, int, string, time.Time) (map[int64]*GroupAvailabilitySummary, error) {
	return nil, nil
}

func (r *groupAvailabilityProbeRunnerRepoStub) CleanupOldResults(context.Context, time.Time) error {
	return nil
}

func (r *groupAvailabilityProbeRunnerRepoStub) claimSnapshot() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.claimCalls, r.claimLimit
}

func TestGroupAvailabilityProbeRunDueClaimsOnlyRunnableBatch(t *testing.T) {
	repo := &groupAvailabilityProbeRunnerRepoStub{}
	runner := &GroupAvailabilityProbeRunnerService{repo: repo, instanceID: "test-instance"}

	runner.runDue()

	calls, limit := repo.claimSnapshot()
	if calls != 1 {
		t.Fatalf("ClaimDue() calls = %d, want 1", calls)
	}
	if limit != groupAvailabilityProbeDefaultMaxWorkers {
		t.Fatalf("ClaimDue() limit = %d, want %d", limit, groupAvailabilityProbeDefaultMaxWorkers)
	}
}

func TestGroupAvailabilityProbeRunDueSkipsOverlappingRun(t *testing.T) {
	claimStarted := make(chan struct{})
	releaseClaim := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseClaim)
		}
	}()

	repo := &groupAvailabilityProbeRunnerRepoStub{claimStarted: claimStarted, releaseClaim: releaseClaim}
	runner := &GroupAvailabilityProbeRunnerService{repo: repo, instanceID: "test-instance"}
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		runner.runDue()
	}()

	select {
	case <-claimStarted:
	case <-time.After(time.Second):
		t.Fatal("first run did not reach ClaimDue")
	}

	// 第二轮必须立即跳过，不能再次领取并绕过实例级 worker 上限。
	runner.runDue()
	if calls, _ := repo.claimSnapshot(); calls != 1 {
		t.Fatalf("ClaimDue() calls during overlap = %d, want 1", calls)
	}

	close(releaseClaim)
	released = true
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first run did not finish after releasing ClaimDue")
	}
}

func TestRunGroupAvailabilityProbeAttemptsStopsAfterSuccess(t *testing.T) {
	attempts := 0
	result := runGroupAvailabilityProbeAttempts(context.Background(), 3, time.Second, func(ctx context.Context) *GroupAvailabilityProbeResult {
		attempts++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("each probe attempt must have its own deadline")
		}
		if attempts < 3 {
			return &GroupAvailabilityProbeResult{Status: GroupAvailabilityProbeStatusFailed, Success: false}
		}
		return &GroupAvailabilityProbeResult{Status: GroupAvailabilityProbeStatusSuccess, Success: true}
	})

	if result == nil || !result.Success {
		t.Fatalf("runGroupAvailabilityProbeAttempts() result = %+v, want success", result)
	}
	if attempts != 3 {
		t.Fatalf("runGroupAvailabilityProbeAttempts() attempts = %d, want 3", attempts)
	}
}

func TestRunGroupAvailabilityProbeAttemptsUsesRetriesAfterInitialAttempt(t *testing.T) {
	attempts := 0
	result := runGroupAvailabilityProbeAttempts(context.Background(), 3, 0, func(context.Context) *GroupAvailabilityProbeResult {
		attempts++
		return &GroupAvailabilityProbeResult{
			Status:       GroupAvailabilityProbeStatusFailed,
			Success:      false,
			ErrorMessage: "temporary failure",
		}
	})

	if result == nil || result.Success {
		t.Fatalf("runGroupAvailabilityProbeAttempts() result = %+v, want failure", result)
	}
	if attempts != 4 {
		t.Fatalf("runGroupAvailabilityProbeAttempts() attempts = %d, want 4", attempts)
	}
}

func TestRunGroupAvailabilityProbeAttemptsRetriesAfterAttemptTimeout(t *testing.T) {
	attempts := 0
	result := runGroupAvailabilityProbeAttempts(context.Background(), 1, 5*time.Millisecond, func(ctx context.Context) *GroupAvailabilityProbeResult {
		attempts++
		if attempts == 1 {
			<-ctx.Done()
			return &GroupAvailabilityProbeResult{
				Status:       GroupAvailabilityProbeStatusFailed,
				Success:      false,
				ErrorMessage: ctx.Err().Error(),
			}
		}
		if ctx.Err() != nil {
			t.Fatalf("retry context error = %v, want active context", ctx.Err())
		}
		return &GroupAvailabilityProbeResult{Status: GroupAvailabilityProbeStatusSuccess, Success: true}
	})

	if result == nil || !result.Success {
		t.Fatalf("runGroupAvailabilityProbeAttempts() result = %+v, want success", result)
	}
	if attempts != 2 {
		t.Fatalf("runGroupAvailabilityProbeAttempts() attempts = %d, want 2", attempts)
	}
}

func TestRunGroupAvailabilityProbeAttemptsStopsAfterParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	runGroupAvailabilityProbeAttempts(ctx, 3, 0, func(context.Context) *GroupAvailabilityProbeResult {
		attempts++
		cancel()
		return &GroupAvailabilityProbeResult{Status: GroupAvailabilityProbeStatusFailed, Success: false}
	})

	if attempts != 1 {
		t.Fatalf("runGroupAvailabilityProbeAttempts() attempts = %d, want 1", attempts)
	}
}
