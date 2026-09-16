// 已选择账号的 HTTP 等待适配保留快速抢槽、计数、粘性绑定及原错误格式。
package httpapi

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SelectedAccountSlot 不携带凭据或可变账号实体。
type SelectedAccountSlot struct {
	AccountID   int64
	Acquired    bool
	ReleaseFunc func()
	WaitPlan    *scheduler.AccountWaitPlan
}
type SlotStickyBinder interface {
	BindStickySession(context.Context, *int64, string, int64) error
}
type AccountSlotHooks struct {
	Acquired        func(*gin.Context)
	CapacityLimited func(*gin.Context)
}

// AcquireSelectedAccountSlot 只有取得的资源才交给请求释放，不变更原故障放行语义。
func AcquireSelectedAccountSlot(c *gin.Context, groupID *int64, sessionHash string, selection *SelectedAccountSlot, reqStream bool, streamStarted *bool, reqLog *zap.Logger, writeError func(int, string, string, string), concurrency *ConcurrencyHelper, sticky SlotStickyBinder, hooks AccountSlotHooks) (func(), bool) {

	if selection == nil {
		hooks.CapacityLimited(c)
		writeError(http.StatusServiceUnavailable, "api_error", "", "No available accounts")
		return nil, false
	}

	ctx := c.Request.Context()
	accountID := selection.AccountID
	if selection.Acquired {
		hooks.Acquired(c)
		return scheduler.WrapRelease(ctx, scheduler.ReleaseOnCancel, selection.ReleaseFunc), true
	}
	if selection.WaitPlan == nil {
		hooks.CapacityLimited(c)
		writeError(http.StatusServiceUnavailable, "api_error", "", "No available accounts")
		return nil, false
	}

	fastReleaseFunc, fastAcquired, err := concurrency.TryAcquireAccountSlot(
		ctx,
		accountID,
		selection.WaitPlan.MaxConcurrency,
	)
	if err != nil {
		reqLog.Warn("openai.account_slot_quick_acquire_failed", zap.Int64("account_id", accountID), zap.Error(err))
		status, errType, code, message := ConcurrencyErrorResponse(err, "account")
		writeError(status, errType, code, message)
		return nil, false
	}
	if fastAcquired {
		hooks.Acquired(c)
		if err := sticky.BindStickySession(ctx, groupID, sessionHash, accountID); err != nil {
			reqLog.Warn("openai.bind_sticky_session_failed", zap.Int64("account_id", accountID), zap.Error(err))
		}
		return scheduler.WrapRelease(ctx, scheduler.ReleaseOnCancel, fastReleaseFunc), true
	}

	waitEntry, waitErr := concurrency.EnterAccountWait(ctx, accountID, selection.WaitPlan.MaxWaiting)
	canWait := waitEntry.Allowed
	if waitErr != nil {
		reqLog.Warn("openai.account_wait_counter_increment_failed", zap.Int64("account_id", accountID), zap.Error(waitErr))
	} else if !canWait {
		reqLog.Info("openai.account_wait_queue_full",
			zap.Int64("account_id", accountID),
			zap.Int("max_waiting", selection.WaitPlan.MaxWaiting),
		)
		writeError(http.StatusTooManyRequests, "rate_limit_error", GatewayQueueFullCode, "Too many pending requests, please retry later")
		return nil, false
	}

	accountWaitCounted := waitErr == nil && canWait
	releaseWait := func() {
		if accountWaitCounted {
			waitEntry.Release()
			accountWaitCounted = false
		}
	}
	defer releaseWait()

	accountReleaseFunc, err := concurrency.AcquireAccountSlotWithWaitTimeout(
		c,
		accountID,
		selection.WaitPlan.MaxConcurrency,
		selection.WaitPlan.Timeout,
		reqStream,
		streamStarted,
	)
	if err != nil {
		reqLog.Warn("openai.account_slot_acquire_failed", zap.Int64("account_id", accountID), zap.Error(err))
		status, errType, code, message := ConcurrencyErrorResponse(err, "account")
		writeError(status, errType, code, message)
		return nil, false
	}

	// Slot acquired: no longer waiting in queue.
	releaseWait()
	hooks.Acquired(c)
	if err := sticky.BindStickySession(ctx, groupID, sessionHash, accountID); err != nil {
		reqLog.Warn("openai.bind_sticky_session_failed", zap.Int64("account_id", accountID), zap.Error(err))
	}
	return scheduler.WrapRelease(ctx, scheduler.ReleaseOnCancel, accountReleaseFunc), true
}
