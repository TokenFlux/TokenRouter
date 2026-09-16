package account

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/stretchr/testify/require"
)

type quotaActionsSource struct {
	query func(context.Context) (*wire.OpenAIQuotaUsage, error)
	reset func(context.Context) (*wire.OpenAIQuotaResetResult, error)
}

func (s quotaActionsSource) QueryUsage(ctx context.Context, _ int64) (*wire.OpenAIQuotaUsage, error) {
	return s.query(ctx)
}
func (s quotaActionsSource) ResetCredit(ctx context.Context, _ int64) (*wire.OpenAIQuotaResetResult, error) {
	return s.reset(ctx)
}
func (quotaActionsSource) CacheResetCreditsSnapshot(context.Context, int64, *wire.OpenAIRateLimitResetCredits) error {
	return nil
}
func (quotaActionsSource) CachePostResetSnapshot(context.Context, int64, *wire.OpenAIQuotaUsage) error {
	return nil
}

type quotaActionsRecovery func(context.Context) (*SuccessfulTestRecovery, error)

func (f quotaActionsRecovery) RecoverAccountState(ctx context.Context, _ int64, _ AccountRecoveryOptions) (*SuccessfulTestRecovery, error) {
	return f(ctx)
}

// 客户端断开不取消已消费后的恢复；应用停止仍能取消并等待整个工作流。
func TestOpenAIQuotaActionsOwnPostConsumptionLifecycle(t *testing.T) {
	recoveryEntered := make(chan context.Context, 1)
	var consumed atomic.Int32
	source := quotaActionsSource{reset: func(context.Context) (*wire.OpenAIQuotaResetResult, error) {
		consumed.Add(1)
		return &wire.OpenAIQuotaResetResult{Code: "fixture-consumed"}, nil
	}}
	recovery := quotaActionsRecovery(func(ctx context.Context) (*SuccessfulTestRecovery, error) {
		recoveryEntered <- ctx
		<-ctx.Done()
		return nil, ctx.Err()
	})
	actions := NewOpenAIQuotaActions(source, recovery, nil, func(string, ...any) {})
	clientCtx, cancelClient := context.WithCancel(context.Background())
	defer cancelClient()
	outcome := make(chan *OpenAIQuotaResetOutcome, 1)
	failure := make(chan error, 1)
	go func() { value, err := actions.Reset(clientCtx, 1); outcome <- value; failure <- err }()
	postCtx := <-recoveryEntered
	cancelClient()
	require.NoError(t, postCtx.Err())
	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	require.NoError(t, actions.StopContext(stopCtx))
	require.NoError(t, <-failure)
	result := <-outcome
	require.Equal(t, OpenAIQuotaResetWarningAccountRecoveryFailed, result.WarningCode)
	require.False(t, result.AccountStateRecovered)
	require.EqualValues(t, 1, consumed.Load())
	_, err := actions.Reset(context.Background(), 1)
	require.ErrorIs(t, err, ErrOpenAIQuotaStopped)
	require.EqualValues(t, 1, consumed.Load())
}

// 刷新流程在关闭时主动取消支持 context 的查询，不能只等待上游自身超时。
func TestOpenAIQuotaActionsStopCancelsRefresh(t *testing.T) {
	entered := make(chan struct{})
	source := quotaActionsSource{query: func(ctx context.Context) (*wire.OpenAIQuotaUsage, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	actions := NewOpenAIQuotaActions(source, nil, nil, func(string, ...any) {})
	finished := make(chan error, 1)
	go func() { _, err := actions.Refresh(context.Background(), 1); finished <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, actions.StopContext(ctx))
	require.ErrorIs(t, <-finished, context.Canceled)
}
