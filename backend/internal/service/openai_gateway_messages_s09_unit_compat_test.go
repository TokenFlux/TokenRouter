//go:build unit

// 原行为测试继续经过相同转接；生产消费者清零后仅保留 unit 兼容。
package service

import (
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

func openAICompatTerminalResponse(event *protocolopenai.ResponsesStreamEvent, payload []byte) *protocolopenai.ResponsesResponse {
	return nativeopenai.CompatTerminalResponse(event, payload)
}

func copyOpenAIUsageFromResponsesUsage(usage *protocolopenai.ResponsesUsage) OpenAIUsage {
	return protocolopenai.CopyForwardUsage(usage)
}
