package text

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

type fixedResponseRuntime struct {
	sessions []*plannedResponseSession
	plan     *routing.CandidatePlan
	outcome  ResponseOutcome
}
type plannedResponseSession struct {
	*responseFixture
	plan *routing.CandidatePlan
}

func (p *plannedResponseSession) Select(excluded map[int64]struct{}) (ResponseSelection, error) {
	s, e := p.responseFixture.Select(excluded)
	if p.plan != nil {
		s.Plan = *p.plan
		s.PlanProvided = true
	}
	return s, e
}
func (p *plannedResponseSession) Forward() ResponseOutcome {
	if p.plan != nil {
		p.plan.GroupID = 999
	}
	return p.responseFixture.Forward()
}
func (r *fixedResponseRuntime) Open(context.Context, execution.Request, upstream.OutputSink) (ResponsePorts, error) {
	p := &plannedResponseSession{responseFixture: &responseFixture{outcomes: []ResponseOutcome{r.outcome}}, plan: r.plan}
	r.sessions = append(r.sessions, p)
	return p, nil
}
func TestFixedResponsesCapturesOnlyProvidedCandidate(t *testing.T) {
	for _, provided := range []bool{false, true} {
		runtime := &fixedResponseRuntime{outcome: ResponseOutcome{Outcome: Outcome{HasResult: true, Attempt: upstream.AttemptResult{Model: "observed"}}}}
		if provided {
			runtime.plan = &routing.CandidatePlan{AccountID: 1, GroupID: 7}
		}
		executor := NewResponsesExecutor(runtime, ResponseOptions{}, ResponseOptions{})
		result, err := executor.Execute(context.Background(), execution.Request{}, nil)
		require.NoError(t, err)
		require.Equal(t, 1, result.Attempts)
		require.Equal(t, provided, result.PlanProvided)
		if provided {
			require.Equal(t, int64(7), result.Plan.GroupID)
		} else {
			require.Equal(t, routing.CandidatePlan{}, result.Plan)
		}
		require.Equal(t, 1, runtime.sessions[0].selected)
		require.Equal(t, 1, runtime.sessions[0].completed)
	}
}
func TestFixedResponsesRetainsPartialFailureAcrossIndependentRequests(t *testing.T) {
	cause := errors.New("partial image")
	runtime := &fixedResponseRuntime{outcome: ResponseOutcome{Images: true, Outcome: Outcome{Err: cause, HasResult: true, Attempt: upstream.AttemptResult{ObservedImages: 1}}}}
	executor := NewResponsesExecutor(runtime, ResponseOptions{}, ResponseOptions{MaxSwitches: 3, FirstOutputBudget: true})
	for i := 0; i < 2; i++ {
		result, err := executor.Execute(context.Background(), execution.Request{Text: execution.TextState{Kind: execution.TextOpenAIResponses}}, nil)
		require.ErrorIs(t, err, cause)
		require.Equal(t, 1, result.Attempt.ObservedImages)
		require.Equal(t, 1, result.Attempts)
	}
	require.NotSame(t, runtime.sessions[0], runtime.sessions[1])
	for _, p := range runtime.sessions {
		require.Equal(t, 1, p.partial)
		require.Equal(t, 1, p.completed)
		require.Zero(t, p.switched)
	}
}
