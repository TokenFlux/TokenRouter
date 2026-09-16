package text

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

// 同一运行时为每次执行创建独立状态；调用者只提供值和输出。
type fixedMessageRuntimeTest struct {
	inputs   []execution.Request
	sessions []*messageFixture
	outcomes []Outcome
}

func (r *fixedMessageRuntimeTest) Open(_ context.Context, in execution.Request, _ upstream.OutputSink) (MessagePorts, error) {
	r.inputs = append(r.inputs, in)
	p := &messageFixture{outcomes: r.outcomes}
	r.sessions = append(r.sessions, p)
	return p, nil
}
func TestFixedMessagesExecutorReturnsLastAttemptAndOwnsOnlyLoop(t *testing.T) {
	err := errors.New("temporary")
	r := &fixedMessageRuntimeTest{outcomes: []Outcome{{Err: err, Failure: &AttemptFailure{Cause: err, Policy: &failover.FailureInfo{StatusCode: 503, RetryNext: true}}}, {HasResult: true, Attempt: upstream.AttemptResult{Model: "final", HasUsage: true, Usage: upstream.TokenUsage{OutputTokens: 7}}}}}
	executor := NewMessagesExecutor(r, MessageOptions{MaxSwitches: 3, CompletePartialFailure: true}, MessageOptions{MaxSwitches: 1})
	for _, model := range []string{"first", "second"} {
		result, err := executor.Execute(context.Background(), execution.Request{Model: model}, nil)
		require.NoError(t, err)
		require.Equal(t, 2, result.Attempts)
		require.Equal(t, int64(2), result.Account.ID)
		require.Equal(t, "final", result.Attempt.Model)
		require.Equal(t, 7, result.Attempt.Usage.OutputTokens)
	}
	require.Len(t, r.sessions, 2)
	require.NotSame(t, r.sessions[0], r.sessions[1])
	require.Equal(t, "first", r.inputs[0].Model)
	require.Equal(t, "second", r.inputs[1].Model)
	for _, s := range r.sessions {
		require.Equal(t, 2, s.prepares)
		require.Equal(t, 2, s.forwards)
		require.Equal(t, 1, s.completed)
		require.True(t, s.finished)
	}
}
func TestFixedMessagesExecutorKeepsGeminiPartialCompletionDifference(t *testing.T) {
	err := errors.New("partial output")
	for _, kind := range []execution.TextKind{execution.TextMessages, execution.TextGeminiMessages} {
		r := &fixedMessageRuntimeTest{outcomes: []Outcome{{Err: err, HasResult: true, Attempt: upstream.AttemptResult{HasUsage: true, Usage: upstream.TokenUsage{OutputTokens: 2}}}}}
		executor := NewMessagesExecutor(r, MessageOptions{CompletePartialFailure: true}, MessageOptions{})
		result, got := executor.Execute(context.Background(), execution.Request{Text: execution.TextState{Kind: kind}}, nil)
		require.ErrorIs(t, got, err)
		require.Equal(t, 2, result.Attempt.Usage.OutputTokens)
		expected := 0
		if kind == execution.TextMessages {
			expected = 1
		}
		require.Equal(t, expected, r.sessions[0].completed)
	}
}
