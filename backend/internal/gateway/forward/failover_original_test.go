package forward

import (
	"context"
	"testing"
	"time"

	failover "github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock
// ---------------------------------------------------------------------------

// mockTempUnscheduler 记录 TempUnscheduleRetryableError 的调用信息。
type mockTempUnscheduler struct {
	calls []tempUnscheduleCall
}

type tempUnscheduleCall struct {
	accountID   int64
	failoverErr *UpstreamFailoverError
}

func (m *mockTempUnscheduler) TempUnscheduleRetryableError(_ context.Context, accountID int64, failoverErr *UpstreamFailoverError) {
	m.calls = append(m.calls, tempUnscheduleCall{accountID: accountID, failoverErr: failoverErr})
}

// TestSameAccountRetryDelayFor 验证容量型瞬时错误指数退避且不改变其它错误的固定等待。
func TestSameAccountRetryDelayFor(t *testing.T) {
	capacityErr := &UpstreamFailoverError{RequestScopedTransient: true}

	for _, tt := range []struct {
		name       string
		retryCount int
		want       time.Duration
	}{
		{name: "first retry", retryCount: 1, want: 500 * time.Millisecond},
		{name: "second retry", retryCount: 2, want: time.Second},
		{name: "third retry", retryCount: 3, want: 2 * time.Second},
		{name: "fourth retry", retryCount: 4, want: 4 * time.Second},
		{name: "fifth retry", retryCount: 5, want: 8 * time.Second},
		{name: "capped retry", retryCount: 10, want: 8 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, failover.SameAccountRetryDelayFor((capacityErr).RetryFailure(), tt.retryCount))
		})
	}

	t.Run("ordinary error", func(t *testing.T) {
		require.Equal(t, failover.SameAccountRetryDelay, failover.SameAccountRetryDelayFor((&UpstreamFailoverError{}).RetryFailure(), 10))
	})
	t.Run("nil error", func(t *testing.T) {
		require.Equal(t, failover.SameAccountRetryDelay, failover.SameAccountRetryDelayFor((*UpstreamFailoverError)(nil).RetryFailure(), 10))
	})

	t.Run("explicit oauth delay wins", func(t *testing.T) {
		err := &UpstreamFailoverError{SameAccountRetryDelay: 3 * time.Second}
		require.Equal(t, 3*time.Second, failover.SameAccountRetryDelayFor((err).RetryFailure(), 1))
	})
}

func TestSameAccountRetryAllowedUsesDeadlineInsteadOfPoolCount(t *testing.T) {
	err := &UpstreamFailoverError{
		RetryableOnSameAccount:   true,
		SameAccountRetryDeadline: time.Now().Add(time.Minute),
	}
	require.True(t, failover.SameAccountRetryAllowed((err).RetryFailure(), 100, 0))
	require.True(t, failover.SameAccountRetryAllowed((err).RetryFailure(), 100, failover.MaxSameAccountRetries))
	err.SameAccountRetryDeadline = time.Now().Add(-time.Second)
	require.False(t, failover.SameAccountRetryAllowed((err).RetryFailure(), 0, 100))
}

func TestSameAccountRetryAllowedRequiresOptInAndDefaultsToCountLimit(t *testing.T) {
	err := &UpstreamFailoverError{SameAccountRetryDeadline: time.Now().Add(time.Minute)}
	require.False(t, failover.SameAccountRetryAllowed((err).RetryFailure(), 0, failover.MaxSameAccountRetries))

	err.RetryableOnSameAccount = true
	err.SameAccountRetryDeadline = time.Time{}
	require.True(t, failover.SameAccountRetryAllowed((err).RetryFailure(), failover.MaxSameAccountRetries-1, failover.MaxSameAccountRetries))
	require.False(t, failover.SameAccountRetryAllowed((err).RetryFailure(), failover.MaxSameAccountRetries, failover.MaxSameAccountRetries))
}

func TestSameAccountRetryAllowedHonorsErrorMaxBeforeDeadline(t *testing.T) {
	err := &UpstreamFailoverError{
		RetryableOnSameAccount:   true,
		SameAccountRetryDeadline: time.Now().Add(time.Minute),
		SameAccountRetryMax:      1,
	}
	require.True(t, failover.SameAccountRetryAllowed((err).RetryFailure(), 0, failover.MaxSameAccountRetries))
	require.False(t, failover.SameAccountRetryAllowed((err).RetryFailure(), 1, failover.MaxSameAccountRetries))
	require.False(t, failover.SameAccountRetryAllowed((err).RetryFailure(), 0, 0), "an explicit zero retry budget remains disabled")
}

func TestSameAccountRetryDeadlineAllows(t *testing.T) {
	require.True(t, failover.SameAccountRetryDeadlineAllows((&UpstreamFailoverError{}).RetryFailure()))
	require.True(t, failover.SameAccountRetryDeadlineAllows((&UpstreamFailoverError{
		SameAccountRetryDeadline: time.Now().Add(time.Second),
	}).RetryFailure()))
	require.False(t, failover.SameAccountRetryDeadlineAllows((&UpstreamFailoverError{
		SameAccountRetryDeadline: time.Now().Add(-time.Second),
	}).RetryFailure()))
}

func TestEffectiveSameAccountRetryLimitHonorsErrorCapAndDisabledAccount(t *testing.T) {
	account := &accountcore.Record{Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true, "pool_mode_retry_count": float64(3)}}
	require.Equal(t, 1, failover.EffectiveSameAccountRetryLimit((&UpstreamFailoverError{SameAccountRetryMax: 1}).RetryFailure(), account.GetPoolModeRetryCount()))
	account.Credentials["pool_mode_retry_count"] = float64(0)
	require.Equal(t, 0, failover.EffectiveSameAccountRetryLimit((&UpstreamFailoverError{SameAccountRetryMax: 1}).RetryFailure(), account.GetPoolModeRetryCount(

	// ---------------------------------------------------------------------------
	// Helper
	// ---------------------------------------------------------------------------
	)))
}

func newTestFailoverErr(statusCode int, retryable, forceBilling bool) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:             statusCode,
		RetryableOnSameAccount: retryable,
		ForceCacheBilling:      forceBilling,
	}
}

// ---------------------------------------------------------------------------
// NewFailoverState 测试
// ---------------------------------------------------------------------------

func TestNewFailoverState(t *testing.T) {
	t.Run("初始化字段正确", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](5, true, gatewaytelemetry.Failover)
		require.Equal(t, 5, fs.MaxSwitches)
		require.Equal(t, 0, fs.SwitchCount)
		require.NotNil(t, fs.FailedAccountIDs)
		require.Empty(t, fs.FailedAccountIDs)
		require.NotNil(t, fs.SameAccountRetryCount)
		require.Empty(t, fs.SameAccountRetryCount)
		require.Nil(t, fs.LastFailoverErr)
		require.False(t, fs.ForceCacheBilling)
		require.True(t, fs.HasBoundSession)
	})

	t.Run("无绑定会话", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		require.Equal(t, 3, fs.MaxSwitches)
		require.False(t, fs.HasBoundSession)
	})

	t.Run("零最大切换次数", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](0, false, gatewaytelemetry.Failover)
		require.Equal(t, 0, fs.MaxSwitches)
	})
}

// ---------------------------------------------------------------------------
// sleepWithContext 测试
// ---------------------------------------------------------------------------

func TestSleepWithContext(t *testing.T) {
	t.Run("零时长立即返回true", func(t *testing.T) {
		start := time.Now()
		ok := failover.SleepWithContext(context.Background(), 0)
		require.True(t, ok)
		require.Less(t, time.Since(start), 50*time.Millisecond)
	})

	t.Run("负时长立即返回true", func(t *testing.T) {
		start := time.Now()
		ok := failover.SleepWithContext(context.Background(), -1*time.Second)
		require.True(t, ok)
		require.Less(t, time.Since(start), 50*time.Millisecond)
	})

	t.Run("正常等待后返回true", func(t *testing.T) {
		start := time.Now()
		ok := failover.SleepWithContext(context.Background(), 50*time.Millisecond)
		elapsed := time.Since(start)
		require.True(t, ok)
		require.GreaterOrEqual(t, elapsed, 40*time.Millisecond)
		require.Less(t, elapsed, 500*time.Millisecond)
	})

	t.Run("已取消context立即返回false", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		start := time.Now()
		ok := failover.SleepWithContext(ctx, 5*time.Second)
		require.False(t, ok)
		require.Less(t, time.Since(start), 50*time.Millisecond)
	})

	t.Run("等待期间context取消返回false", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(30 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		ok := failover.SleepWithContext(ctx, 5*time.Second)
		elapsed := time.Since(start)
		require.False(t, ok)
		require.Less(t, elapsed, 500*time.Millisecond)
	})
}

// ---------------------------------------------------------------------------
// HandleFailoverError — 基本切换流程
// ---------------------------------------------------------------------------

func TestHandleFailoverError_BasicSwitch(t *testing.T) {
	t.Run("显式停止不切换账号且旧错误默认仍切换", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		stopErr := &UpstreamFailoverError{
			Stage:             GatewayFailureStageAccountAuth,
			Scope:             GatewayFailureScopeProvider,
			NextAccountAction: NextAccountStop,
		}

		action := fs.HandleFailoverError(context.Background(), mock, 100, capability.PlatformGrok, failover.MaxSameAccountRetries, stopErr)

		require.Equal(t, failover.FailoverExhausted, action)
		require.Zero(t, fs.SwitchCount)
		require.Empty(t, fs.FailedAccountIDs)
		require.Equal(t, stopErr, fs.LastFailoverErr)

		legacyErr := newTestFailoverErr(429, false, false)
		action = fs.HandleFailoverError(context.Background(), mock, 100, capability.PlatformGrok, failover.MaxSameAccountRetries, legacyErr)

		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SwitchCount)
		require.Contains(t, fs.FailedAccountIDs, int64(100))
	})

	t.Run("已取消的认证失败不改变切换状态", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := &UpstreamFailoverError{
			Stage:             GatewayFailureStageAccountAuth,
			Scope:             GatewayFailureScopeAccount,
			NextAccountAction: NextAccountRetry,
		}

		action := fs.HandleFailoverError(ctx, mock, 101, capability.PlatformGrok, failover.MaxSameAccountRetries, err)

		require.Equal(t, failover.FailoverCanceled, action)
		require.Zero(t, fs.SwitchCount)
		require.Empty(t, fs.FailedAccountIDs)
		require.Nil(t, fs.LastFailoverErr)
		require.Empty(t, mock.calls)
	})

	t.Run("非重试错误_非Antigravity_直接切换", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(500, false, false)

		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)

		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SwitchCount)
		require.Contains(t, fs.FailedAccountIDs, int64(100))
		require.Equal(t, err, fs.LastFailoverErr)
		require.False(t, fs.ForceCacheBilling)
		require.Empty(t, mock.calls, "不应调用 TempUnschedule")
	})

	t.Run("非重试错误_Antigravity_第一次切换无延迟", func(t *testing.T) {
		// switchCount 从 0→1 时，sleepFailoverDelay(ctx, 1) 的延时 = (1-1)*1s = 0
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(500, false, false)

		start := time.Now()
		action := fs.HandleFailoverError(context.Background(), mock, 100, capability.PlatformAntigravity, failover.MaxSameAccountRetries, err)
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SwitchCount)
		require.Less(t, elapsed, 200*time.Millisecond, "第一次切换延迟应为 0")
	})

	t.Run("非重试错误_Antigravity_第二次切换有1秒延迟", func(t *testing.T) {
		// switchCount 从 1→2 时，sleepFailoverDelay(ctx, 2) 的延时 = (2-1)*1s = 1s
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		fs.SwitchCount = 1 // 模拟已切换一次

		err := newTestFailoverErr(500, false, false)
		start := time.Now()
		action := fs.HandleFailoverError(context.Background(), mock, 200, capability.PlatformAntigravity, failover.MaxSameAccountRetries, err)
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 2, fs.SwitchCount)
		require.GreaterOrEqual(t, elapsed, 800*time.Millisecond, "第二次切换延迟应约 1s")
		require.Less(t, elapsed, 3*time.Second)
	})

	t.Run("连续切换直到耗尽", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](2, false, gatewaytelemetry.Failover)

		// 第一次切换：0→1
		err1 := newTestFailoverErr(500, false, false)
		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err1)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SwitchCount)

		// 第二次切换：1→2
		err2 := newTestFailoverErr(502, false, false)
		action = fs.HandleFailoverError(context.Background(), mock, 200, "openai", failover.MaxSameAccountRetries, err2)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 2, fs.SwitchCount)

		// 第三次已耗尽：SwitchCount(2) >= MaxSwitches(2)
		err3 := newTestFailoverErr(503, false, false)
		action = fs.HandleFailoverError(context.Background(), mock, 300, "openai", failover.MaxSameAccountRetries, err3)
		require.Equal(t, failover.FailoverExhausted, action)
		require.Equal(t, 2, fs.SwitchCount, "耗尽时不应继续递增")

		// 验证失败账号列表
		require.Len(t, fs.FailedAccountIDs, 3)
		require.Contains(t, fs.FailedAccountIDs, int64(100))
		require.Contains(t, fs.FailedAccountIDs, int64(200))
		require.Contains(t, fs.FailedAccountIDs, int64(300))

		// LastFailoverErr 应为最后一次的错误
		require.Equal(t, err3, fs.LastFailoverErr)
	})

	t.Run("MaxSwitches为0时首次即耗尽", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](0, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(500, false, false)

		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, failover.FailoverExhausted, action)
		require.Equal(t, 0, fs.SwitchCount)
		require.Contains(t, fs.FailedAccountIDs, int64(100))
	})
}

// ---------------------------------------------------------------------------
// HandleFailoverError — 缓存计费 (ForceCacheBilling)
// ---------------------------------------------------------------------------

func TestHandleFailoverError_CacheBilling(t *testing.T) {
	t.Run("hasBoundSession为true且实际切换时设置ForceCacheBilling", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, true, gatewaytelemetry.Failover) // hasBoundSession=true
		err := newTestFailoverErr(500, false, false)

		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.True(t, fs.ForceCacheBilling)
	})

	t.Run("同账号重试时仅凭hasBoundSession不设置ForceCacheBilling", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, true, gatewaytelemetry.Failover)
		err := newTestFailoverErr(400, true, false)

		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)

		require.False(t, fs.ForceCacheBilling)
		require.Zero(t, fs.SwitchCount)
	})

	t.Run("OAuth deadline存在时不按普通计数切换", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, true, gatewaytelemetry.Failover)
		fs.SameAccountRetryCount[100] = failover.MaxSameAccountRetries

		err := newTestFailoverErr(429, true, false)
		err.SameAccountRetryDeadline = time.Now().Add(time.Minute)
		err.SameAccountRetryDelay = time.Nanosecond

		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)

		require.False(t, fs.ForceCacheBilling)
		require.Zero(t, fs.SwitchCount)
		require.Equal(t, failover.MaxSameAccountRetries+
			1, fs.SameAccountRetryCount[100])
		require.Empty(t, mock.calls)
	})
	t.Run("同账号重试耗尽并实际切换时设置ForceCacheBilling", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, true, gatewaytelemetry.Failover)
		err := newTestFailoverErr(400, true, false)

		for i := 0; i <
			failover.MaxSameAccountRetries; i++ {
			fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
			require.False(t, fs.ForceCacheBilling)
		}
		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)

		require.True(t, fs.ForceCacheBilling)
		require.Equal(t, 1, fs.SwitchCount)
	})

	t.Run("failoverErr.ForceCacheBilling为true时设置", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(500, false, true) // ForceCacheBilling=true

		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.True(t, fs.ForceCacheBilling)
	})

	t.Run("同账号重试保留显式ForceCacheBilling", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, true, gatewaytelemetry.Failover)
		err := newTestFailoverErr(400, true, true)

		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)

		require.True(t, fs.ForceCacheBilling)
		require.Zero(t, fs.SwitchCount)
	})

	t.Run("两者均为false时不设置", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(500, false, false)

		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.False(t, fs.ForceCacheBilling)
	})

	t.Run("一旦设置不会被后续错误重置", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)

		// 第一次：ForceCacheBilling=true → 设置
		err1 := newTestFailoverErr(500, false, true)
		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err1)
		require.True(t, fs.ForceCacheBilling)

		// 第二次：ForceCacheBilling=false → 仍然保持 true
		err2 := newTestFailoverErr(502, false, false)
		fs.HandleFailoverError(context.Background(), mock, 200, "openai", failover.MaxSameAccountRetries, err2)
		require.True(t, fs.ForceCacheBilling, "ForceCacheBilling 一旦设置不应被重置")
	})
}

// ---------------------------------------------------------------------------
// HandleFailoverError — 同账号重试 (RetryableOnSameAccount)
// ---------------------------------------------------------------------------

func TestHandleFailoverError_SameAccountRetry(t *testing.T) {
	t.Run("第一次重试返回FailoverContinue", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(400, true, false)

		start := time.Now()
		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SameAccountRetryCount[100])
		require.Equal(t, 0, fs.SwitchCount, "同账号重试不应增加切换计数")
		require.NotContains(t, fs.FailedAccountIDs, int64(100), "同账号重试不应加入失败列表")
		require.Empty(t, mock.calls, "同账号重试期间不应调用 TempUnschedule")
		// 验证等待了 sameAccountRetryDelay (500ms)
		require.GreaterOrEqual(t, elapsed, 400*time.Millisecond)
		require.Less(t, elapsed, 2*time.Second)
	})

	t.Run("达到最大重试次数前均返回FailoverContinue", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(400, true, false)

		for i := 1; i <=
			failover.MaxSameAccountRetries; i++ {
			action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
			require.Equal(t, failover.FailoverContinue, action)
			require.Equal(t, i, fs.SameAccountRetryCount[100])
		}

		require.Empty(t, mock.calls, "达到最大重试次数前均不应调用 TempUnschedule")
	})

	t.Run("超过最大重试次数后触发TempUnschedule并切换", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(400, true, false)

		for i := 0; i <
			failover.MaxSameAccountRetries; i++ {
			fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		}
		require.Equal(t, failover.MaxSameAccountRetries, fs.SameAccountRetryCount[100])

		// 第 failover.MaxSameAccountRetries+1 次：重试耗尽，应切换账号
		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SwitchCount)
		require.Contains(t, fs.FailedAccountIDs, int64(100))

		// 验证 TempUnschedule 被调用
		require.Len(t, mock.calls, 1)
		require.Equal(t, int64(100), mock.calls[0].accountID)
		require.Equal(t, err, mock.calls[0].failoverErr)
	})

	t.Run("不同账号独立跟踪重试次数", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](5, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(400, true, false)

		// 账号 100 第一次重试
		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SameAccountRetryCount[100])

		// 账号 200 第一次重试（独立计数）
		action = fs.HandleFailoverError(context.Background(), mock, 200, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SameAccountRetryCount[200])
		require.Equal(t, 1, fs.SameAccountRetryCount[100], "账号 100 的计数不应受影响")
	})

	t.Run("重试耗尽后再次遇到同账号_直接切换", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](5, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(400, true, false)

		// 耗尽账号 100 的重试
		for i := 0; i <
			failover.MaxSameAccountRetries; i++ {
			fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		}
		// 第 failover.MaxSameAccountRetries+1 次: 重试耗尽 → 切换
		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, failover.FailoverContinue, action)

		// 再次遇到账号 100，计数仍为 failover.MaxSameAccountRetries，条件不满足 → 直接切换
		action = fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, failover.FailoverContinue, action)
		require.Len(t, mock.calls, 2, "第二次耗尽也应调用 TempUnschedule")
	})

	t.Run("尊重账号级retryLimit_配置1次只重试1次", func(t *testing.T) {
		// 回归测试：Anthropic 等路径此前硬编码同账号重试 3 次，忽略账号
		// pool_mode_retry_count 配置。此处验证传入 retryLimit=1 时只重试 1 次即切换。
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](5, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(403, true, false)
		const retryLimit = 1

		// 第 1 次：同账号重试
		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", retryLimit, err)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SameAccountRetryCount[100])
		require.Equal(t, 0, fs.SwitchCount, "首次重试不应切换账号")
		require.Empty(t, mock.calls, "未耗尽前不应 TempUnschedule")

		// 第 2 次：已达上限 1 → 不再同账号重试，直接切换 + TempUnschedule
		action = fs.HandleFailoverError(context.Background(), mock, 100, "openai", retryLimit, err)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SameAccountRetryCount[100], "重试计数不应超过 retryLimit")
		require.Equal(t, 1, fs.SwitchCount, "重试耗尽应切换账号")
		require.Contains(t, fs.FailedAccountIDs, int64(100))
		require.Len(t, mock.calls, 1, "重试耗尽应触发 TempUnschedule")
	})

	t.Run("retryLimit为0时立即切换不重试", func(t *testing.T) {
		// pool_mode_retry_count=0 表示关闭同账号重试（如 GPT Image 账号）。
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](5, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(403, true, false)

		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", 0, err)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 0, fs.SameAccountRetryCount[100], "retryLimit=0 不应发生同账号重试")
		require.Equal(t, 1, fs.SwitchCount, "应立即切换账号")
		require.Len(t, mock.calls, 1, "应立即 TempUnschedule")
	})
}

// ---------------------------------------------------------------------------
// HandleFailoverError — TempUnschedule 调用验证
// ---------------------------------------------------------------------------

func TestHandleFailoverError_TempUnschedule(t *testing.T) {
	t.Run("非重试错误不调用TempUnschedule", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(500, false, false) // RetryableOnSameAccount=false

		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.Empty(t, mock.calls)
	})

	t.Run("重试错误耗尽后调用TempUnschedule_传入正确参数", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(502, true, false)

		for i := 0; i <
			failover.MaxSameAccountRetries; i++ {
			fs.HandleFailoverError(context.Background(), mock, 42, "openai", failover.MaxSameAccountRetries, err)
		}
		// 再次触发时才会执行 TempUnschedule + 切换
		fs.HandleFailoverError(context.Background(), mock, 42, "openai", failover.MaxSameAccountRetries, err)

		require.Len(t, mock.calls, 1)
		require.Equal(t, int64(42), mock.calls[0].accountID)
		require.Equal(t, 502, mock.calls[0].failoverErr.StatusCode)
		require.True(t, mock.calls[0].failoverErr.RetryableOnSameAccount)
	})
}

// ---------------------------------------------------------------------------
// HandleFailoverError — Context 取消
// ---------------------------------------------------------------------------

func TestHandleFailoverError_ContextCanceled(t *testing.T) {
	t.Run("同账号重试sleep期间context取消", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(400, true, false)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(30 * time.Millisecond)
			cancel() // 通过入口检查后、sleep 期间取消
		}()

		start := time.Now()
		action := fs.HandleFailoverError(ctx, mock, 100, "openai", failover.MaxSameAccountRetries, err)
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverCanceled, action)
		require.Less(t, elapsed, 400*time.Millisecond, "sleep 应被取消打断")
		// 进入重试分支后才取消：重试计数已递增
		require.Equal(t, 1, fs.SameAccountRetryCount[100])
	})

	t.Run("入口即已取消_不改动任何failover状态", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(520, false, false)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // 调用前客户端已断开

		start := time.Now()
		action := fs.HandleFailoverError(ctx, mock, 100, "openai", failover.MaxSameAccountRetries, err)
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverCanceled, action)
		require.Less(t, elapsed, 100*time.Millisecond, "应立即返回")
		// 入口已取消时不得改变任何 failover 状态。
		require.Equal(t, 0, fs.SwitchCount, "取消的请求不应计入切换")
		require.Equal(t, 0, fs.SameAccountRetryCount[100], "取消的请求不应改动重试计数")
		require.NotContains(t, fs.FailedAccountIDs, int64(100))
		require.Nil(t, fs.LastFailoverErr)
		require.Empty(t, mock.calls, "不应触发 TempUnschedule")
	})

	t.Run("Antigravity延迟期间context取消", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		fs.SwitchCount = 1 // 下一次 switchCount=2 → delay = 1s
		err := newTestFailoverErr(500, false, false)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // 立即取消

		start := time.Now()
		action := fs.HandleFailoverError(ctx, mock, 100, capability.PlatformAntigravity, failover.MaxSameAccountRetries, err)
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverCanceled, action)
		require.Less(t, elapsed, 100*time.Millisecond, "应立即返回而非等待 1s")
	})
}

// ---------------------------------------------------------------------------
// HandleFailoverError — FailedAccountIDs 跟踪
// ---------------------------------------------------------------------------

func TestHandleFailoverError_FailedAccountIDs(t *testing.T) {
	t.Run("切换时添加到失败列表", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)

		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, newTestFailoverErr(500, false, false))
		require.Contains(t, fs.FailedAccountIDs, int64(100))

		fs.HandleFailoverError(context.Background(), mock, 200, "openai", failover.MaxSameAccountRetries, newTestFailoverErr(502, false, false))
		require.Contains(t, fs.FailedAccountIDs, int64(200))
		require.Len(t, fs.FailedAccountIDs, 2)
	})

	t.Run("耗尽时也添加到失败列表", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](0, false, gatewaytelemetry.Failover)

		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, newTestFailoverErr(500, false, false))
		require.Equal(t, failover.FailoverExhausted, action)
		require.Contains(t, fs.FailedAccountIDs, int64(100))
	})

	t.Run("同账号重试期间不添加到失败列表", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)

		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, newTestFailoverErr(400, true, false))
		require.Equal(t, failover.FailoverContinue, action)
		require.NotContains(t, fs.FailedAccountIDs, int64(100))
	})

	t.Run("同一账号多次切换不重复添加", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](5, false, gatewaytelemetry.Failover)

		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, newTestFailoverErr(500, false, false))
		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, newTestFailoverErr(500, false, false))
		require.Len(t, fs.FailedAccountIDs, 1, "map 天然去重")
	})
}

// ---------------------------------------------------------------------------
// HandleFailoverError — LastFailoverErr 更新
// ---------------------------------------------------------------------------

func TestHandleFailoverError_LastFailoverErr(t *testing.T) {
	t.Run("每次调用都更新LastFailoverErr", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)

		err1 := newTestFailoverErr(500, false, false)
		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err1)
		require.Equal(t, err1, fs.LastFailoverErr)

		err2 := newTestFailoverErr(502, false, false)
		fs.HandleFailoverError(context.Background(), mock, 200, "openai", failover.MaxSameAccountRetries, err2)
		require.Equal(t, err2, fs.LastFailoverErr)
	})

	t.Run("同账号重试时也更新LastFailoverErr", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)

		err := newTestFailoverErr(400, true, false)
		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, err, fs.LastFailoverErr)
	})
}

// ---------------------------------------------------------------------------
// HandleFailoverError — 综合集成场景
// ---------------------------------------------------------------------------

func TestHandleFailoverError_IntegrationScenario(t *testing.T) {
	t.Run("模拟完整failover流程_多账号混合重试与切换", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, true, gatewaytelemetry.Failover) // hasBoundSession=true

		// 1. 账号 100 遇到可重试错误，同账号重试 failover.MaxSameAccountRetries 次
		retryErr := newTestFailoverErr(400, true, false)
		for i := 0; i <
			failover.MaxSameAccountRetries; i++ {
			action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, retryErr)
			require.Equal(t, failover.FailoverContinue, action)
			require.False(t, fs.ForceCacheBilling, "同账号重试期间不应仅因绑定会话强制缓存计费")
		}

		// 2. 账号 100 超过重试上限 → TempUnschedule + 切换
		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, retryErr)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SwitchCount)
		require.True(t, fs.ForceCacheBilling, "实际切换账号时应设置 ForceCacheBilling")
		require.Len(t, mock.calls, 1)

		// 3. 账号 200 遇到不可重试错误 → 直接切换
		switchErr := newTestFailoverErr(500, false, false)
		action = fs.HandleFailoverError(context.Background(), mock, 200, "openai", failover.MaxSameAccountRetries, switchErr)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 2, fs.SwitchCount)

		// 4. 账号 300 遇到不可重试错误 → 再切换
		action = fs.HandleFailoverError(context.Background(), mock, 300, "openai", failover.MaxSameAccountRetries, switchErr)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 3, fs.SwitchCount)

		// 5. 账号 400 → 已耗尽 (SwitchCount=3 >= MaxSwitches=3)
		action = fs.HandleFailoverError(context.Background(), mock, 400, "openai", failover.MaxSameAccountRetries, switchErr)
		require.Equal(t, failover.FailoverExhausted, action)

		// 最终状态验证
		require.Equal(t, 3, fs.SwitchCount, "耗尽时不再递增")
		require.Len(t, fs.FailedAccountIDs, 4, "4个不同账号都在失败列表中")
		require.True(t, fs.ForceCacheBilling)
		require.Len(t, mock.calls, 1, "只有账号 100 触发了 TempUnschedule")
	})

	t.Run("模拟Antigravity平台完整流程", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](2, false, gatewaytelemetry.Failover)

		err := newTestFailoverErr(500, false, false)

		// 第一次切换：delay = 0s
		start := time.Now()
		action := fs.HandleFailoverError(context.Background(), mock, 100, capability.PlatformAntigravity, failover.MaxSameAccountRetries, err)
		elapsed := time.Since(start)
		require.Equal(t, failover.FailoverContinue, action)
		require.Less(t, elapsed, 200*time.Millisecond, "第一次切换延迟为 0")

		// 第二次切换：delay = 1s
		start = time.Now()
		action = fs.HandleFailoverError(context.Background(), mock, 200, capability.PlatformAntigravity, failover.MaxSameAccountRetries, err)
		elapsed = time.Since(start)
		require.Equal(t, failover.FailoverContinue, action)
		require.GreaterOrEqual(t, elapsed, 800*time.Millisecond, "第二次切换延迟约 1s")

		// 第三次：耗尽（无延迟，因为在检查延迟之前就返回了）
		start = time.Now()
		action = fs.HandleFailoverError(context.Background(), mock, 300, capability.PlatformAntigravity, failover.MaxSameAccountRetries, err)
		elapsed = time.Since(start)
		require.Equal(t, failover.FailoverExhausted, action)
		require.Less(t, elapsed, 200*time.Millisecond, "耗尽时不应有延迟")
	})

	t.Run("ForceCacheBilling通过错误标志设置", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover) // hasBoundSession=false

		// 第一次：ForceCacheBilling=false
		err1 := newTestFailoverErr(500, false, false)
		fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err1)
		require.False(t, fs.ForceCacheBilling)

		// 第二次：ForceCacheBilling=true（Antigravity 粘性会话切换）
		err2 := newTestFailoverErr(500, false, true)
		fs.HandleFailoverError(context.Background(), mock, 200, "openai", failover.MaxSameAccountRetries, err2)
		require.True(t, fs.ForceCacheBilling, "错误标志应触发 ForceCacheBilling")

		// 第三次：ForceCacheBilling=false，但状态仍保持 true
		err3 := newTestFailoverErr(500, false, false)
		fs.HandleFailoverError(context.Background(), mock, 300, "openai", failover.MaxSameAccountRetries, err3)
		require.True(t, fs.ForceCacheBilling, "不应重置")
	})
}

// ---------------------------------------------------------------------------
// HandleFailoverError — 边界条件
// ---------------------------------------------------------------------------

func TestHandleFailoverError_EdgeCases(t *testing.T) {
	t.Run("StatusCode为0的错误也能正常处理", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(0, false, false)

		action := fs.HandleFailoverError(context.Background(), mock, 100, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, failover.FailoverContinue, action)
	})

	t.Run("AccountID为0也能正常跟踪", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(500, true, false)

		action := fs.HandleFailoverError(context.Background(), mock, 0, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SameAccountRetryCount[0])
	})

	t.Run("负AccountID也能正常跟踪", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		err := newTestFailoverErr(500, true, false)

		action := fs.HandleFailoverError(context.Background(), mock, -1, "openai", failover.MaxSameAccountRetries, err)
		require.Equal(t, failover.FailoverContinue, action)
		require.Equal(t, 1, fs.SameAccountRetryCount[-1])
	})

	t.Run("空平台名称不触发Antigravity延迟", func(t *testing.T) {
		mock := &mockTempUnscheduler{}
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		fs.SwitchCount = 1
		err := newTestFailoverErr(500, false, false)

		start := time.Now()
		action := fs.HandleFailoverError(context.Background(), mock, 100, "", failover.MaxSameAccountRetries, err)
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverContinue, action)
		require.Less(t, elapsed, 200*time.Millisecond, "空平台不应触发 Antigravity 延迟")
	})
}

// ---------------------------------------------------------------------------
// HandleSelectionExhausted 测试
// ---------------------------------------------------------------------------

func TestHandleSelectionExhausted(t *testing.T) {
	t.Run("无LastFailoverErr时返回Exhausted", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		// LastFailoverErr 为 nil

		action := fs.HandleSelectionExhausted(context.Background())
		require.Equal(t, failover.FailoverExhausted, action)
	})

	t.Run("非503错误返回Exhausted", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		fs.LastFailoverErr = newTestFailoverErr(500, false, false)

		action := fs.HandleSelectionExhausted(context.Background())
		require.Equal(t, failover.FailoverExhausted, action)
	})

	t.Run("503且未耗尽_等待后返回Continue并清除失败列表", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		fs.LastFailoverErr = newTestFailoverErr(503, false, false)
		fs.FailedAccountIDs[100] = struct{}{}
		fs.SwitchCount = 1

		start := time.Now()
		action := fs.HandleSelectionExhausted(context.Background())
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverContinue, action)
		require.Empty(t, fs.FailedAccountIDs, "应清除失败账号列表")
		require.GreaterOrEqual(t, elapsed, 1500*time.Millisecond, "应等待约 2s")
		require.Less(t, elapsed, 5*time.Second)
	})

	t.Run("503但SwitchCount已超过MaxSwitches_返回Exhausted", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](2, false, gatewaytelemetry.Failover)
		fs.LastFailoverErr = newTestFailoverErr(503, false, false)
		fs.SwitchCount = 3 // > MaxSwitches(2)

		start := time.Now()
		action := fs.HandleSelectionExhausted(context.Background())
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverExhausted, action)
		require.Less(t, elapsed, 100*time.Millisecond, "不应等待")
	})

	t.Run("503但context已取消_返回Canceled", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		fs.LastFailoverErr = newTestFailoverErr(503, false, false)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		start := time.Now()
		action := fs.HandleSelectionExhausted(ctx)
		elapsed := time.Since(start)

		require.Equal(t, failover.FailoverCanceled, action)
		require.Less(t, elapsed, 100*time.Millisecond, "应立即返回")
	})

	t.Run("context已取消_非503也返回Canceled而非Exhausted", func(t *testing.T) {
		// #4257 核心场景：客户端断开后选号失败源于 context canceled，
		// 不应被当成账号耗尽转成 502。
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
		fs.LastFailoverErr = newTestFailoverErr(520, false, false)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		action := fs.HandleSelectionExhausted(ctx)
		require.Equal(t, failover.FailoverCanceled, action)
	})

	t.Run("context已取消_无LastFailoverErr也返回Canceled", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](3, false, gatewaytelemetry.Failover)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		action := fs.HandleSelectionExhausted(ctx)
		require.Equal(t, failover.FailoverCanceled, action)
	})

	t.Run("503且SwitchCount等于MaxSwitches_仍可重试", func(t *testing.T) {
		fs := failover.NewFailoverState[*UpstreamFailoverError](2, false, gatewaytelemetry.Failover)
		fs.LastFailoverErr = newTestFailoverErr(503, false, false)
		fs.SwitchCount = 2 // == MaxSwitches，条件是 <=，仍可重试

		action := fs.HandleSelectionExhausted(context.Background())
		require.Equal(t, failover.FailoverContinue, action)
	})
}
