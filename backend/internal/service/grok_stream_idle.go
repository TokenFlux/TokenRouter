package service

import (
	"fmt"
	"strings"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// Grok 流空闲失败后使用较短冷却，使账号可较快恢复且不会立刻在切换循环中被重新选中。
const grokStreamIdleCooldown = 2 * time.Minute

// grokStreamIdleFailoverError 构造响应提交前可见的切换错误，使挂起 Grok 流能更换 OAuth 账号。
func grokStreamIdleFailoverError(account *Account, idle time.Duration) *forwardcore.UpstreamFailoverError {
	msg := fmt.Sprintf("Grok stream idle timeout after %s with no upstream data", idle.Round(time.Second))
	return &forwardcore.UpstreamFailoverError{
		StatusCode:               502,
		ResponseBody:             []byte(`{"error":{"code":"empty_upstream","message":"` + strings.ReplaceAll(msg, `"`, `'`) + `"}}`),
		SafeToFailoverAfterWrite: true,
		// 空闲上游流属于瞬时故障，先使用同账号重试预算再切换凭据；
		// handler 仍负责执行请求级重试上限。
		RetryableOnSameAccount: account != nil && account.Platform == capability.PlatformGrok,
		RequestScopedTransient: true,
		// 空闲失败后最多允许一次同账号重放；截止时间从失败时刻计算，避免
		// 长时间挂起的流耗尽正常的三次重试预算后才切换账号。
		SameAccountRetryMax:      1,
		SameAccountRetryDeadline: time.Now().Add(idle),
	}
}
