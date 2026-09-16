package text

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// qoderCompatibleFixture 检查不同输出边界下的尝试数与完成资格，不改变平台错误分类。
type qoderCompatibleFixture struct {
	results                                                               []QoderCompatibleOutcome
	refresh                                                               QoderRefreshResult
	selects, forwards, refreshes, switched, partials, successes, acquires int
	knownError                                                            bool
	pending, failed                                                       bool
}

func (*qoderCompatibleFixture) Context() context.Context { return context.Background() }
func (p *qoderCompatibleFixture) Select(map[int64]struct{}) (Selection, error) {
	p.selects++
	return Selection{Account: account.AccountSnapshot{ID: int64(p.selects)}}, nil
}
func (p *qoderCompatibleFixture) SelectionFailed(error, bool, bool, error) { p.failed = true }
func (p *qoderCompatibleFixture) Acquire(bool) bool                        { p.acquires++; return true }
func (p *qoderCompatibleFixture) Forward() QoderCompatibleOutcome {
	p.forwards++
	out := p.results[0]
	p.results = p.results[1:]
	return out
}
func (p *qoderCompatibleFixture) Refresh() QoderRefreshResult    { p.refreshes++; return p.refresh }
func (p *qoderCompatibleFixture) RefreshPending(bool)            { p.pending = true }
func (p *qoderCompatibleFixture) Partial(QoderCompatibleOutcome) { p.partials++ }
func (p *qoderCompatibleFixture) Canceled(bool, error)           {}
func (p *qoderCompatibleFixture) Failure(out QoderCompatibleOutcome) bool {
	return p.knownError || out.OutputChanged
}
func (p *qoderCompatibleFixture) Exhausted(error) { p.failed = true }
func (p *qoderCompatibleFixture) Success()        { p.successes++ }
func (p *qoderCompatibleFixture) Switched()       { p.switched++ }
func TestQoderCompatibleAttemptBoundaries(t *testing.T) {
	failure := errors.New("upstream failed")
	t.Run("已观测部分服务只完成一次且不绑定成功", func(t *testing.T) {
		p := &qoderCompatibleFixture{results: []QoderCompatibleOutcome{{Err: failure, Partial: true, CanRefresh: true, CanFailover: true}}}
		RunQoderCompatible(p, 3)
		require.Equal(t, 1, p.partials)
		require.Zero(t, p.successes)
		require.Zero(t, p.refreshes)
		require.Equal(t, 1, p.forwards)
	})
	t.Run("任何已写字节禁止换号和刷新", func(t *testing.T) {
		p := &qoderCompatibleFixture{results: []QoderCompatibleOutcome{{Err: failure, OutputChanged: true, CanRefresh: true, CanFailover: true}}}
		RunQoderCompatible(p, 3)
		require.Equal(t, 1, p.selects)
		require.Zero(t, p.refreshes)
		require.Zero(t, p.switched)
	})
	t.Run("未分类无输出错误保持旧换号资格", func(t *testing.T) {
		p := &qoderCompatibleFixture{results: []QoderCompatibleOutcome{{Err: failure}, {}}}
		RunQoderCompatible(p, 3)
		require.Equal(t, 2, p.selects)
		require.Equal(t, 1, p.successes)
	})
	t.Run("同账号刷新仍重新获取尝试资源", func(t *testing.T) {
		p := &qoderCompatibleFixture{results: []QoderCompatibleOutcome{{Err: failure, CanRefresh: true}, {}}, refresh: QoderRefreshResult{Ready: true}}
		RunQoderCompatible(p, 3)
		require.Equal(t, 1, p.selects)
		require.Equal(t, 2, p.acquires)
		require.Equal(t, 1, p.successes)
	})
	t.Run("刷新进行中到达账号上限不再选择", func(t *testing.T) {
		p := &qoderCompatibleFixture{results: []QoderCompatibleOutcome{{Err: failure, CanRefresh: true}}, refresh: QoderRefreshResult{Pending: true}}
		RunQoderCompatible(p, 1)
		require.True(t, p.pending)
		require.Equal(t, 1, p.selects)
		require.Zero(t, p.successes)
	})
}
