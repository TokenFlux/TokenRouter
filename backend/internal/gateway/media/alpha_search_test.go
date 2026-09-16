package media

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/stretchr/testify/require"
)

type alphaPortsStub struct {
	outcomes                                          []AlphaOutcome
	selected, released, reported, completed, switched int
	exclusions                                        []map[int64]struct{}
	cancel                                            context.CancelFunc
}

func (p *alphaPortsStub) SelectAlpha(_ context.Context, excluded map[int64]struct{}) (AlphaSelection, bool, error) {
	p.selected++
	snapshot := make(map[int64]struct{}, len(excluded))
	for id := range excluded {
		snapshot[id] = struct{}{}
	}
	p.exclusions = append(p.exclusions, snapshot)
	return AlphaSelection{Account: account.AccountSnapshot{ID: 1}, RetryLimit: 1}, true, nil
}
func (p *alphaPortsStub) AcquireAlpha(context.Context, AlphaSelection) (func(), bool) {
	return func() { p.released++ }, true
}
func (p *alphaPortsStub) ForwardAlpha(context.Context, AlphaSelection, []byte) AlphaOutcome {
	return p.outcomes[p.selected-1]
}
func (p *alphaPortsStub) ReportAlpha(context.Context, AlphaSelection, *AlphaResult, bool, error) {
	p.reported++
}
func (p *alphaPortsStub) CompleteAlpha(context.Context, AlphaSelection, *AlphaResult) { p.completed++ }
func (p *alphaPortsStub) SwitchAlpha(AlphaSelection)                                  { p.switched++ }
func (p *alphaPortsStub) StopAlpha429(AlphaSelection, int, int) bool                  { return false }
func (p *alphaPortsStub) ObserveAlpha(e AlphaEvent) {
	if e.Kind == "retry" && p.cancel != nil {
		p.cancel()
	}
}
func (p *alphaPortsStub) AlphaClientGone() bool { return false }
func TestAlphaSearchSameAccountBudgetAndCanceledDelay(t *testing.T) {
	retry := AlphaOutcome{Err: errors.New("retry"), Failure: &failover.FailureInfo{RetryableOnSameAccount: true, SameAccountRetryDelay: time.Millisecond}}
	p := &alphaPortsStub{outcomes: []AlphaOutcome{retry, {Result: &AlphaResult{Calls: 1}}}}
	require.Nil(t, RunAlphaSearch(context.Background(), nil, 3, p))
	require.Equal(t, 2, p.selected)
	require.Equal(t, 2, p.released)
	require.Equal(t, 1, p.completed)
	require.Zero(t, p.switched)
	require.Empty(t, p.exclusions[1], "同账号恢复不能排除刚才候选")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p = &alphaPortsStub{outcomes: []AlphaOutcome{retry}, cancel: cancel}
	require.Nil(t, RunAlphaSearch(ctx, nil, 3, p))
	require.Equal(t, 1, p.selected)
	require.Equal(t, 1, p.released)
	require.Zero(t, p.completed)
}
func TestAlphaSearchOutputFailureAndUnbillableResponse(t *testing.T) {
	p := &alphaPortsStub{outcomes: []AlphaOutcome{{Err: errors.New("after output"), Failure: &failover.FailureInfo{}, OutputChanged: true}}}
	failure := RunAlphaSearch(context.Background(), nil, 3, p)
	require.Equal(t, "exhausted", failure.Stage)
	require.Equal(t, 1, p.reported)
	require.Zero(t, p.switched)
	require.Zero(t, p.completed)
	p = &alphaPortsStub{outcomes: []AlphaOutcome{{}}}
	require.Nil(t, RunAlphaSearch(context.Background(), nil, 3, p))
	require.Equal(t, 1, p.reported)
	require.Zero(t, p.completed, "成功透传但无搜索计量不生成完成任务")
}
