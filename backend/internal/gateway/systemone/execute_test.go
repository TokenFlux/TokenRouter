package systemone

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type runProbe struct {
	outcomes                                              []Outcome
	calls, completed, warnings, released, slots, switches int
}

func (p *runProbe) Select(context.Context, map[int64]struct{}) (provider.ProviderSnapshot, error) {
	return provider.ProviderSnapshot{ID: int64(p.calls + 1)}, nil
}

func (p *runProbe) Acquire(context.Context) (func(), bool) {
	p.slots++
	return func() { p.slots--; p.released++ }, true
}

func (p *runProbe) Forward(context.Context) Outcome {
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
	failure := Outcome{Err: errors.New("busy"), Retry: true}
	for _, tc := range []struct {
		name                       string
		outcomes                   []Outcome
		calls, completed, warnings int
	}{
		{"switch", []Outcome{failure, {Result: upstream.AttemptResult{Served: true, HasUsage: true}}}, 2, 1, 0},
		{"missing usage", []Outcome{{Result: upstream.AttemptResult{Served: true}, Err: errors.New("write"), Retry: true}}, 1, 0, 1},
		{"write failed", []Outcome{{Result: upstream.AttemptResult{Served: true, HasUsage: true}, Err: errors.New("write"), Retry: true}}, 1, 1, 0},
		{"invalid response", []Outcome{{Err: errors.New("invalid")}}, 1, 0, 0},
		{"exhausted", []Outcome{failure, failure, failure}, 3, 0, 0},
		{"committed", []Outcome{{Result: upstream.AttemptResult{HTTPCommitted: true}, Err: errors.New("write"), Retry: true}}, 1, 0, 0},
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
