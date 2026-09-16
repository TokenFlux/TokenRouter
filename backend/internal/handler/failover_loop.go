// 重试算法只在 gateway/failover；旧 HTTP 入口保留平台错误投影与响应收尾。
package handler

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type TempUnscheduler = failover.TempUnscheduler[*service.UpstreamFailoverError]
type FailoverState = failover.FailoverState[*service.UpstreamFailoverError]
type FailoverAction = failover.FailoverAction

const FailoverContinue = failover.FailoverContinue
const FailoverExhausted = failover.FailoverExhausted
const FailoverCanceled = failover.FailoverCanceled
const maxSameAccountRetries = failover.MaxSameAccountRetries
const sameAccountRetryDelay = failover.SameAccountRetryDelay

func NewFailoverState(max int, bound bool) *FailoverState {
	return failover.NewFailoverState[*service.UpstreamFailoverError](max, bound, telemetry.Failover)
}
func sameAccountRetryDelayFor(e *service.UpstreamFailoverError, n int) time.Duration {
	return failover.SameAccountRetryDelayFor(e.RetryFailure(), n)
}
func sameAccountRetryAllowed(e *service.UpstreamFailoverError, n, limit int) bool {
	return failover.SameAccountRetryAllowed(e.RetryFailure(), n, limit)
}
func sameAccountRetryDeadlineAllows(e *service.UpstreamFailoverError) bool {
	return failover.SameAccountRetryDeadlineAllows(e.RetryFailure())
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	return failover.SleepWithContext(ctx, d)
}

// effectiveSameAccountRetryLimit 合并账号默认预算与错误级别的更小上限。
// 兼容仍使用该辅助函数的媒体与测试路径，零值表示沿用账号配置。
func effectiveSameAccountRetryLimit(failoverErr *service.UpstreamFailoverError, account *service.Account) int {
	if account == nil {
		return 0
	}
	return failover.EffectiveSameAccountRetryLimit(failoverErr.RetryFailure(), account.GetPoolModeRetryCount())
}

// failoverClientGone 判断下游客户端是否已断开（请求 context 已取消）。
// 客户端断开后 failover 必须静默终止：用已取消的 context 重新选号只会得到
// context.Canceled，并被误报成账号耗尽（通用 502）；上游 detach 的在途请求
// 照常完成计费，但不再为无人接收的响应启动新的上游尝试。
// 响应尚未提交时把状态码标记为 499（client closed request），供访问日志归类。
func failoverClientGone(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.Context().Err() == nil {
		return false
	}
	// 先停 compact 心跳（接管 ResponseWriter，建立 happens-before），与
	// handleStreamingAwareError/errorResponse 等终结路径对齐，避免心跳
	// goroutine 与下面的状态标记并发触碰同一 writer。心跳已提交 200 时
	// 状态码已固化，不再标 499。
	if service.StopOpenAICompactSSEKeepaliveCommitted(c) {
		return true
	}
	if !c.Writer.Written() {
		c.Status(statusClientClosedRequest)
	}
	return true
}
