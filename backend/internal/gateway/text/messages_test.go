package text

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/stretchr/testify/require"
)

// messageFixture 只控制单次执行结果，验证编排是否额外尝试、重复完成或扩大部分失败资格。
type messageFixture struct {
	outcomes                                          []Outcome
	prepares, selected, forwards, completed, switches int
	finished, served, exhausted, forcedStream         bool
}

func (*messageFixture) Context() context.Context { return context.Background() }
func (*messageFixture) Begin()                   {}
func (p *messageFixture) Finish(served bool)     { p.finished = true; p.served = served }
func (p *messageFixture) PrepareAttempt() bool   { p.prepares++; return true }
func (p *messageFixture) Select(map[int64]struct{}) (Selection, error) {
	p.selected++
	return Selection{Account: account.AccountSnapshot{ID: int64(p.selected), Platform: "anthropic"}}, nil
}
func (*messageFixture) FirstSelectionFailure(error, bool) {}
func (*messageFixture) SingleAccountRetry()               {}
func (*messageFixture) Canceled()                         {}
func (p *messageFixture) Exhausted(_ *AttemptFailure, _ string, stream bool) {
	p.exhausted = true
	p.forcedStream = stream
}
func (*messageFixture) Intercept() bool { return false }
func (*messageFixture) Acquire() bool   { return true }
func (p *messageFixture) Forward(AttemptState) Outcome {
	i := p.forwards
	p.forwards++
	if i >= len(p.outcomes) {
		return Outcome{Stop: true}
	}
	return p.outcomes[i]
}
func (*messageFixture) PolicyFailure(error)                                                  {}
func (*messageFixture) Fallback(error, bool) bool                                            { return false }
func (*messageFixture) OtherFailure(error)                                                   {}
func (p *messageFixture) Complete(AttemptState)                                              { p.completed++ }
func (*messageFixture) Success()                                                             {}
func (p *messageFixture) Switched()                                                          { p.switches++ }
func (*messageFixture) Abandon(int64)                                                        {}
func (*messageFixture) TempUnscheduleRetryableError(context.Context, int64, *AttemptFailure) {}

func TestMessagesKeepPartialCompletionPerEntry(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		p := &messageFixture{outcomes: []Outcome{{Err: errors.New("stream ended"), HasResult: true}}}
		RunMessages(MessageOptions{MaxSwitches: 3, CompletePartialFailure: enabled}, p)
		require.Equal(t, 1, p.forwards)
		require.True(t, p.finished)
		require.Equal(t, enabled, p.served)
		want := 0
		if enabled {
			want = 1
		}
		require.Equal(t, want, p.completed)
	}
}
func TestMessagesRetryRebuildsAndCompletesOnlyFinalAttempt(t *testing.T) {
	err := errors.New("temporary failure")
	p := &messageFixture{outcomes: []Outcome{{Err: err, Failure: &AttemptFailure{Cause: err, Policy: &failover.FailureInfo{StatusCode: 503, RetryNext: true}}}, {HasResult: true}}}
	RunMessages(MessageOptions{MaxSwitches: 3, CompletePartialFailure: true}, p)
	require.Equal(t, 2, p.prepares)
	require.Equal(t, 2, p.forwards)
	require.Equal(t, 1, p.completed)
	require.Equal(t, 1, p.switches)
	require.True(t, p.served)
}
func TestMessagesWrittenFailureCannotSwitchOrComplete(t *testing.T) {
	err := errors.New("failure after output")
	p := &messageFixture{outcomes: []Outcome{{Err: err, HasResult: true, OutputChanged: true, Failure: &AttemptFailure{Cause: err, Policy: &failover.FailureInfo{StatusCode: 503, RetryNext: true}}}}}
	RunMessages(MessageOptions{MaxSwitches: 3, CompletePartialFailure: true}, p)
	require.Equal(t, 1, p.selected)
	require.Zero(t, p.switches)
	require.Zero(t, p.completed)
	require.True(t, p.exhausted)
	require.True(t, p.forcedStream)
	require.True(t, p.finished)
}
