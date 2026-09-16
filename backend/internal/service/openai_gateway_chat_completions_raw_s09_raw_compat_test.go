//go:build unit

// 原单元测试继续通过所属唯一实现的兼容入口执行。
package service

import (
	rawwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

func isOpenAIChatUsageOnlyStreamChunk(payload string) bool {
	return rawwire.IsOpenAIChatUsageOnlyStreamChunk(payload)
}
