// Package failover 拥有请求级重试状态；平台错误只提供不可变策略投影。
package failover

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// FailureInfo 不携带平台响应、凭据或 HTTP 对象。
type FailureInfo struct {
	StatusCode               int
	ForceCacheBilling        bool
	RetryableOnSameAccount   bool
	RequestScopedTransient   bool
	SameAccountRetryDelay    time.Duration
	SameAccountRetryDeadline time.Time
	SameAccountRetryMax      int
	RetryNext                bool
}
type Failure interface {
	comparable
	RetryFailure() *FailureInfo
}
type Observe func(context.Context, string, map[string]any)

// TempUnscheduler 用于 HandleFailoverError 中同账号重试耗尽后的临时封禁。
// 执行适配器提供账号读取与故障转移所需的窄接口。
type TempUnscheduler[E Failure] interface {
	TempUnscheduleRetryableError(ctx context.Context, accountID int64, failoverErr E)
}

// FailoverAction 表示 failover 错误处理后的下一步动作
type FailoverAction int

const (
	// FailoverContinue 继续循环（同账号重试或切换账号，调用方统一 continue）
	FailoverContinue FailoverAction = iota
	// FailoverExhausted 切换次数耗尽（调用方应返回错误响应）
	FailoverExhausted
	// FailoverCanceled context 已取消（调用方应直接 return）
	FailoverCanceled
)

const (
	// MaxSameAccountRetries 同账号重试次数默认上限（针对 RetryableOnSameAccount 错误）。
	// 生产调用方通常传入账号级配置 account.GetPoolModeRetryCount()，该常量仅作兜底/测试默认值。
	MaxSameAccountRetries = 3
	// SameAccountRetryDelay 同账号重试间隔
	SameAccountRetryDelay = 500 * time.Millisecond
	// MaxRequestScopedRetryDelay 限制请求级瞬时错误的指数退避上限，避免高重试配置
	// 将单次请求拖入分钟级等待。
	MaxRequestScopedRetryDelay = 8 * time.Second
	// SingleAccountBackoffDelay 单账号分组 503 退避重试固定延时。
	// Service 层在 SingleAccountRetry 模式下已做充分原地重试（最多 3 次、总等待 30s），
	// Handler 层只需短暂间隔后重新进入 Service 层即可。
	SingleAccountBackoffDelay = 2 * time.Second
)

// SameAccountRetryDelayFor 为请求级瞬时错误计算有上限的指数退避；
// 其它同账号错误继续使用固定 500ms，保持既有重试时延。
func SameAccountRetryDelayFor(failoverErr *FailureInfo, retryCount int) time.Duration {
	if failoverErr != nil && failoverErr.SameAccountRetryDelay > 0 {
		return failoverErr.SameAccountRetryDelay
	}
	if failoverErr == nil || !failoverErr.RequestScopedTransient || retryCount <= 1 {
		return SameAccountRetryDelay
	}

	delay := SameAccountRetryDelay
	for i := 1; i < retryCount; i++ {
		if delay >= MaxRequestScopedRetryDelay/2 {
			return MaxRequestScopedRetryDelay
		}
		delay *= 2
	}
	return delay
}

func SameAccountRetryAllowed(failoverErr *FailureInfo, retryCount, retryLimit int) bool {
	if failoverErr == nil || !failoverErr.RetryableOnSameAccount {
		return false
	}
	if !SameAccountRetryDeadlineAllows(failoverErr) {
		return false
	}
	// 错误级上限（Grok 容量/流空闲）即使带有重建的 deadline 也必须生效。
	if failoverErr.SameAccountRetryMax > 0 {
		if retryLimit <= 0 {
			return false
		}
		if failoverErr.SameAccountRetryMax < retryLimit {
			retryLimit = failoverErr.SameAccountRetryMax
		}
		return retryCount < retryLimit
	}
	// OAuth 429 明确使用时间窗口，不受普通池重试次数限制。
	if !failoverErr.SameAccountRetryDeadline.IsZero() {
		return true
	}
	return retryLimit > 0 && retryCount < retryLimit
}

// SameAccountRetryDeadlineAllows 保证服务层提供的重试窗口未过期。
func SameAccountRetryDeadlineAllows(failoverErr *FailureInfo) bool {
	return failoverErr == nil || failoverErr.SameAccountRetryDeadline.IsZero() || time.Now().Before(failoverErr.SameAccountRetryDeadline)
}

// FailoverState 跨循环迭代共享的 failover 状态
type FailoverState[E Failure] struct {
	observe               Observe
	SwitchCount           int
	MaxSwitches           int
	FailedAccountIDs      map[int64]struct{}
	SameAccountRetryCount map[int64]int
	LastFailoverErr       E
	ForceCacheBilling     bool
	HasBoundSession       bool
}

// NewFailoverState 创建 failover 状态
func NewFailoverState[E Failure](maxSwitches int, HasBoundSession bool, observers ...Observe) *FailoverState[E] {
	var observe Observe
	if len(observers) > 0 {
		observe = observers[0]
	}
	return &FailoverState[E]{
		observe:               observe,
		MaxSwitches:           maxSwitches,
		FailedAccountIDs:      make(map[int64]struct{}),
		SameAccountRetryCount: make(map[int64]int),
		HasBoundSession:       HasBoundSession,
	}
}

// HandleFailoverError 处理 UpstreamFailoverError，返回下一步动作。
// 包含：缓存计费判断、同账号重试、临时封禁、切换计数、Antigravity 延时。
func (s *FailoverState[E]) HandleFailoverError(
	ctx context.Context,
	gatewayService TempUnscheduler[E],
	accountID int64,
	platform string,
	retryLimit int,
	failoverErr E,
) FailoverAction {
	// 客户端已断开：failover 只会用已取消的 context 重新选号并必然失败，
	// 不应再被当成账号耗尽处理（误报 502）。
	if ctx != nil && ctx.Err() != nil {
		return FailoverCanceled
	}
	s.LastFailoverErr = failoverErr
	failure := failoverErr.RetryFailure()
	if failure == nil || !failure.RetryNext {
		return FailoverExhausted
	}

	// 同账号重试不算切换账号，粘性会话仅在实际切换时强制缓存计费。
	retryCount := s.SameAccountRetryCount[accountID]
	sameAccountRetry := SameAccountRetryAllowed(failure, retryCount, retryLimit)
	if NeedForceCacheBilling(s.HasBoundSession, failure, sameAccountRetry) {
		s.ForceCacheBilling = true
	}

	// 同账号重试：对 RetryableOnSameAccount 的临时性错误，先在同一账号上重试。
	// 重试次数上限 retryLimit 由调用方传入（账号级 pool_mode_retry_count 配置）。
	if sameAccountRetry {
		s.SameAccountRetryCount[accountID]++
		retryDelay := SameAccountRetryDelayFor(failure, s.SameAccountRetryCount[accountID])
		s.emit(ctx, "gateway.failover_same_account_retry", map[string]any{"account_id": accountID, "upstream_status": failure.StatusCode, "same_account_retry_count": s.SameAccountRetryCount[accountID], "same_account_retry_max": retryLimit, "retry_delay": retryDelay})
		if !SleepWithContext(ctx, retryDelay) {
			return FailoverCanceled
		}
		return FailoverContinue
	}

	// 同账号重试用尽，执行临时封禁
	if failure.RetryableOnSameAccount {
		gatewayService.TempUnscheduleRetryableError(ctx, accountID, failoverErr)
	}

	// 加入失败列表
	s.FailedAccountIDs[accountID] = struct{}{}

	// 检查是否耗尽
	if s.SwitchCount >= s.MaxSwitches {
		return FailoverExhausted
	}

	// 递增切换计数
	s.SwitchCount++
	s.emit(ctx, "gateway.failover_switch_account", map[string]any{"account_id": accountID, "upstream_status": failure.StatusCode, "switch_count": s.SwitchCount, "max_switches": s.MaxSwitches})

	// Antigravity 平台换号线性递增延时
	if platform == capability.PlatformAntigravity {
		delay := time.Duration(s.SwitchCount-1) * time.Second
		if !SleepWithContext(ctx, delay) {
			return FailoverCanceled
		}
	}

	return FailoverContinue
}

// HandleSelectionExhausted 处理选号失败（所有候选账号都在排除列表中）时的退避重试决策。
// 针对 Antigravity 单账号分组的 503 (MODEL_CAPACITY_EXHAUSTED) 场景：
// 清除排除列表、等待退避后重新选号。
//
// 返回 FailoverContinue 时，调用方应设置 SingleAccountRetry context 并 continue。
// 返回 FailoverExhausted 时，调用方应返回错误响应。
// 返回 FailoverCanceled 时，调用方应直接 return。
func (s *FailoverState[E]) HandleSelectionExhausted(ctx context.Context) FailoverAction {
	// 客户端已断开时选号失败是 context canceled 的必然结果，
	// 不代表账号耗尽，直接按取消终止。
	if ctx.Err() != nil {
		return FailoverCanceled
	}

	failure := s.LastFailoverErr.RetryFailure()
	if failure != nil &&
		failure.StatusCode == 503 &&
		s.SwitchCount <= s.MaxSwitches {

		s.emit(ctx, "gateway.failover_single_account_backoff", map[string]any{"backoff_delay": SingleAccountBackoffDelay, "switch_count": s.SwitchCount, "max_switches": s.MaxSwitches})
		if !SleepWithContext(ctx, SingleAccountBackoffDelay) {
			return FailoverCanceled
		}
		s.emit(ctx, "gateway.failover_single_account_retry", map[string]any{"switch_count": s.SwitchCount, "max_switches": s.MaxSwitches})
		s.FailedAccountIDs = make(map[int64]struct{})
		return FailoverContinue
	}
	return FailoverExhausted
}

// NeedForceCacheBilling 判断 failover 时是否需要强制缓存计费。
// 粘性会话实际切换账号、或上游明确标记时，将 input_tokens 转为 cache_read 计费。
func NeedForceCacheBilling(hasBoundSession bool, failoverErr *FailureInfo, sameAccountRetry bool) bool {
	return (hasBoundSession && !sameAccountRetry) || (failoverErr != nil && failoverErr.ForceCacheBilling)
}

// SleepWithContext 等待指定时长，返回 false 表示 context 已取消。
func SleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
func (s *FailoverState[E]) emit(ctx context.Context, event string, fields map[string]any) {
	if s.observe != nil {
		s.observe(ctx, event, fields)
	}
}

// EffectiveSameAccountRetryLimit 保留账号预算与错误级更小上限的组合语义。
func EffectiveSameAccountRetryLimit(failure *FailureInfo, limit int) int {
	if limit > 0 && failure != nil && failure.SameAccountRetryMax > 0 && failure.SameAccountRetryMax < limit {
		return failure.SameAccountRetryMax
	}
	return limit
}
