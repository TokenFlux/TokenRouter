package service

import (
	"fmt"
	"strings"
	"time"
)

// gateway.stream_data_interval_timeout 为 0 时使用该 Grok 流空闲默认值，
// 既容纳慢推理模型，又能及时释放挂起连接。
const defaultGrokStreamIdleTimeout = 180 * time.Second

// Grok 流空闲失败后使用较短冷却，使账号可较快恢复且不会立刻在切换循环中被重新选中。
const grokStreamIdleCooldown = 2 * time.Minute

// resolveGrokStreamIdleTimeout 返回 Grok 上游读取空闲超时；优先使用正数全局设置，
// 否则使用 Grok 默认值，使挂起 SSE 仍可触发切换。
func resolveGrokStreamIdleTimeout(cfgStreamIntervalSec int) time.Duration {
	if cfgStreamIntervalSec > 0 {
		return time.Duration(cfgStreamIntervalSec) * time.Second
	}
	return defaultGrokStreamIdleTimeout
}

// grokStreamIdleFailoverError 构造响应提交前可见的切换错误，使挂起 Grok 流能更换 OAuth 账号。
func grokStreamIdleFailoverError(account *Account, idle time.Duration) *UpstreamFailoverError {
	msg := fmt.Sprintf("Grok stream idle timeout after %s with no upstream data", idle.Round(time.Second))
	return &UpstreamFailoverError{
		StatusCode:               502,
		ResponseBody:             []byte(`{"error":{"code":"empty_upstream","message":"` + strings.ReplaceAll(msg, `"`, `'`) + `"}}`),
		SafeToFailoverAfterWrite: true,
		// 公共池模式允许同账号重试，普通 OAuth 由 handler 切换账号。
		RetryableOnSameAccount: account != nil && account.IsPoolMode(),
	}
}
