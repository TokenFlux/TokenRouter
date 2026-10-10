package systemone

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type runProbe struct {
	outcomes                                              []Outcome
	selected, forwarded                                   []int64
	cancel                                                context.CancelFunc
	calls, completed, warnings, released, slots, switches int
}

func (p *runProbe) Select(context.Context, map[int64]struct{}) (provider.ProviderSnapshot, error) {
	id := int64(p.calls + 1)
	p.selected = append(p.selected, id)
	return provider.ProviderSnapshot{ID: id}, nil
}

func (p *runProbe) Acquire(context.Context) (func(), bool) {
	p.slots++
	return func() { p.slots--; p.released++ }, true
}

func (p *runProbe) Forward(context.Context) Outcome {
	p.forwarded = append(p.forwarded, p.selected[len(p.selected)-1])
	if p.cancel != nil {
		go func() {
			time.Sleep(100 * time.Millisecond)
			p.cancel()
		}()
	}
	out := p.outcomes[p.calls]
	p.calls++
	return out
}
func (p *runProbe) Report(context.Context, Outcome)                      {}
func (p *runProbe) Complete(context.Context, upstream.AttemptResult)     { p.completed++ }
func (p *runProbe) MissingUsage(context.Context, upstream.AttemptResult) { p.warnings++ }
func (p *runProbe) Switch()                                              { p.switches++ }

// TestRunCompletesOnceAndReleasesSlots 覆盖换号、写入失败、缺失用量和重试次数上限。
func TestRunCompletesOnceAndReleasesSlots(t *testing.T) {
	failure := Outcome{Err: errors.New("busy"), Failure: &failover.FailureInfo{RetryNext: true}}
	for _, tc := range []struct {
		name                       string
		outcomes                   []Outcome
		calls, completed, warnings int
	}{
		{"switch", []Outcome{failure, {Result: upstream.AttemptResult{Served: true, HasUsage: true}}}, 2, 1, 0},
		{"missing usage", []Outcome{{Result: upstream.AttemptResult{Served: true}, Err: errors.New("write"), Failure: &failover.FailureInfo{RetryNext: true}}}, 1, 0, 1},
		{"write failed", []Outcome{{Result: upstream.AttemptResult{Served: true, HasUsage: true}, Err: errors.New("write"), Failure: &failover.FailureInfo{RetryNext: true}}}, 1, 1, 0},
		{"invalid response", []Outcome{{Err: errors.New("invalid")}}, 1, 0, 0},
		{"exhausted", []Outcome{failure, failure, failure}, 3, 0, 0},
		{"committed", []Outcome{{Result: upstream.AttemptResult{HTTPCommitted: true}, Err: errors.New("write"), Failure: &failover.FailureInfo{RetryNext: true}}}, 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &runProbe{outcomes: tc.outcomes}
			_ = Run(context.Background(), 2, p)
			require.Equal(t, tc.calls, p.calls)
			require.Equal(t, tc.completed, p.completed)
			require.Equal(t, tc.warnings, p.warnings)
			require.Equal(t, tc.calls, p.released)
			require.Zero(t, p.slots)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &runProbe{}
	require.ErrorIs(t, Run(ctx, 2, p), context.Canceled)
	require.Zero(t, p.calls)
}

// TestRunPoolRetries 检查同提供商预算、换号预算和租约释放分别计算。
func TestRunPoolRetries(t *testing.T) {
	failure := Outcome{Err: errors.New("busy"), Failure: &failover.FailureInfo{RetryNext: true, RetryableOnSameProvider: true, SameProviderRetryDelay: 7 * time.Second}, RetryLimit: 2}
	success := Outcome{Result: upstream.AttemptResult{Served: true, HasUsage: true}}
	zero := failure
	zero.RetryLimit = 0
	committed := failure
	committed.Result.RetryCommitted = true
	served := failure
	served.Result = upstream.AttemptResult{Served: true, HasUsage: true}
	for _, tc := range []struct {
		name                string
		outcomes            []Outcome
		maxSwitches         int
		wantProviders       []int64
		completed, switches int
		delay               time.Duration
	}{
		{name: "retry succeeds with no switches", outcomes: []Outcome{failure, success}, wantProviders: []int64{1, 1}, completed: 1, delay: 7 * time.Second},
		{name: "exhausted", outcomes: []Outcome{failure, failure, failure}, wantProviders: []int64{1, 1, 1}, delay: 14 * time.Second},
		{name: "switch after retries", outcomes: []Outcome{failure, failure, failure, success}, maxSwitches: 1, wantProviders: []int64{1, 1, 1, 4}, completed: 1, switches: 1, delay: 14 * time.Second},
		{name: "zero retries", outcomes: []Outcome{zero}, wantProviders: []int64{1}},
		{name: "committed", outcomes: []Outcome{committed}, wantProviders: []int64{1}},
		{name: "served", outcomes: []Outcome{served}, wantProviders: []int64{1}, completed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := &runProbe{outcomes: tc.outcomes}
				started := time.Now()
				_ = Run(context.Background(), tc.maxSwitches, p)
				require.Equal(t, tc.wantProviders, p.forwarded)
				require.Equal(t, tc.completed, p.completed)
				require.Equal(t, tc.switches, p.switches)
				require.Equal(t, tc.switches+1, p.released)
				require.Equal(t, tc.delay, time.Since(started))
				require.Zero(t, p.slots)
			})
		})
	}
}

// TestRunPoolRetryCancellation 检查退避期间取消后释放租约且不再请求上游。
func TestRunPoolRetryCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p := &runProbe{cancel: cancel, outcomes: []Outcome{{Err: errors.New("busy"), Failure: &failover.FailureInfo{RetryNext: true, RetryableOnSameProvider: true}, RetryLimit: 2}}}
		require.ErrorIs(t, Run(ctx, 3, p), context.Canceled)
		require.Equal(t, 1, p.calls)
		require.Equal(t, 1, p.released)
		require.Zero(t, p.completed)
		require.Zero(t, p.slots)
	})
}
