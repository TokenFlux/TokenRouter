package text

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/stretchr/testify/require"
)

// countFixture 用真实循环检查无槽尝试、排除与释放数量，端口不提供扣费能力。
type countFixture struct {
	ctx                          context.Context
	outcomes                     []*AttemptFailure
	selected, prepared, released int
	excluded                     []bool
	canceled, exhausted, failed  bool
}

func (p *countFixture) Context() context.Context { return p.ctx }
func (p *countFixture) Select(ids map[int64]struct{}) (Selection, error) {
	p.selected++
	_, excluded := ids[1]
	p.excluded = append(p.excluded, excluded)
	return Selection{Account: account.AccountSnapshot{ID: int64(p.selected), Platform: "anthropic"}}, nil
}
func (p *countFixture) SelectionFailed(error, *AttemptFailure) { p.failed = true }
func (p *countFixture) Prepare(Selection) bool                 { p.prepared++; return true }
func (p *countFixture) Forward(Selection) *AttemptFailure {
	out := p.outcomes[0]
	p.outcomes = p.outcomes[1:]
	return out
}
func (p *countFixture) ForwardFailed(Selection, error)                                     { p.failed = true }
func (p *countFixture) ReleaseSession(Selection)                                           { p.released++ }
func (p *countFixture) Exhausted(Selection, *AttemptFailure)                               { p.exhausted = true }
func (p *countFixture) Canceled()                                                          { p.canceled = true }
func (*countFixture) TempUnscheduleRetryableError(context.Context, int64, *AttemptFailure) {}
func TestCountTokensAttemptBoundaries(t *testing.T) {
	t.Run("失败账号排除且每次重新准备", func(t *testing.T) {
		p := &countFixture{ctx: context.Background(), outcomes: []*AttemptFailure{{Cause: errors.New("retry"), Policy: &failover.FailureInfo{RetryNext: true}}, nil}}
		RunCountTokens(p, 2, nil)
		require.Equal(t, 2, p.selected)
		require.Equal(t, 2, p.prepared)
		require.Equal(t, 1, p.released)
		require.Equal(t, []bool{false, true}, p.excluded)
		require.False(t, p.exhausted)
	})
	t.Run("取消只释放已尝试会话", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p := &countFixture{ctx: ctx, outcomes: []*AttemptFailure{{Cause: context.Canceled, Policy: &failover.FailureInfo{RetryNext: true}}}}
		RunCountTokens(p, 2, nil)
		require.True(t, p.canceled)
		require.Equal(t, 1, p.selected)
		require.Equal(t, 1, p.released)
	})
	t.Run("普通错误不再次选号", func(t *testing.T) {
		p := &countFixture{ctx: context.Background(), outcomes: []*AttemptFailure{{Cause: errors.New("read failed")}}}
		RunCountTokens(p, 2, nil)
		require.True(t, p.failed)
		require.Equal(t, 1, p.selected)
		require.Equal(t, 1, p.released)
	})
}
