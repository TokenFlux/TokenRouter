package text

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/stretchr/testify/require"
)

// 预检 fixture 只暴露选择与计数，不可能通过测试路径调用资金完成器。
type inputTokenFixture struct {
	ctx                 context.Context
	results             []*AttemptFailure
	selected, forwarded int
	excluded            []bool
	failed, exhausted   bool
}

func (p *inputTokenFixture) Context() context.Context { return p.ctx }
func (p *inputTokenFixture) Select(ids map[int64]struct{}) (Selection, bool, error) {
	p.selected++
	_, yes := ids[1]
	p.excluded = append(p.excluded, yes)
	return Selection{Account: account.AccountSnapshot{ID: 1}, RetryLimit: 1}, true, nil
}
func (p *inputTokenFixture) SelectionFailed(error, *AttemptFailure, bool) { p.failed = true }
func (p *inputTokenFixture) Forward(Selection) *AttemptFailure {
	p.forwarded++
	value := p.results[0]
	p.results = p.results[1:]
	return value
}
func (p *inputTokenFixture) ForwardFailed(Selection, error) { p.failed = true }
func (p *inputTokenFixture) Exhausted(*AttemptFailure)      { p.exhausted = true }
func TestInputTokensRetryBoundaries(t *testing.T) {
	failure := errors.New("failed")
	t.Run("不可重试错误立即展示", func(t *testing.T) {
		p := &inputTokenFixture{ctx: context.Background(), results: []*AttemptFailure{{Cause: failure, Policy: &failover.FailureInfo{}}}}
		RunInputTokens(p, 3)
		require.True(t, p.exhausted)
		require.Equal(t, 1, p.forwarded)
	})
	t.Run("换号预算零保持一次尝试", func(t *testing.T) {
		p := &inputTokenFixture{ctx: context.Background(), results: []*AttemptFailure{{Cause: failure, Policy: &failover.FailureInfo{RetryNext: true}}}}
		RunInputTokens(p, 0)
		require.True(t, p.exhausted)
		require.Equal(t, 1, p.selected)
	})
	t.Run("同账号等待取消不追加执行", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p := &inputTokenFixture{ctx: ctx, results: []*AttemptFailure{{Cause: failure, Policy: &failover.FailureInfo{RetryNext: true, RetryableOnSameAccount: true}}}}
		RunInputTokens(p, 3)
		require.Equal(t, 1, p.selected)
		require.Equal(t, 1, p.forwarded)
		require.False(t, p.exhausted)
	})
	t.Run("切换时排除失败账号", func(t *testing.T) {
		p := &inputTokenFixture{ctx: context.Background(), results: []*AttemptFailure{{Cause: failure, Policy: &failover.FailureInfo{RetryNext: true}}, nil}}
		RunInputTokens(p, 1)
		require.Equal(t, []bool{false, true}, p.excluded)
		require.Equal(t, 2, p.forwarded)
	})
}
