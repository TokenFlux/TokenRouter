// 平台流空闲预算保持正数配置优先和原默认值；调度冷却与重试由调用方拥有。
package grok

import "time"

const DefaultStreamIdleTimeout = 180 * time.Second

// ResolveStreamIdleTimeout 返回 Grok 上游读取空闲超时；优先使用正数全局设置，
// 否则使用 Grok 默认值，使挂起 SSE 仍可触发切换。
func ResolveStreamIdleTimeout(cfgStreamIntervalSec int) time.Duration {
	if cfgStreamIntervalSec > 0 {
		return time.Duration(cfgStreamIntervalSec) * time.Second
	}
	return DefaultStreamIdleTimeout
}
