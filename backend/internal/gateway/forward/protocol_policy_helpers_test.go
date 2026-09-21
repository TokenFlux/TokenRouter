package forward

import (
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// 以下测试助手只组合型号选项与纯转换，不保存算法或运行状态。
func AnthropicToResponses(req *protocolanthropic.AnthropicRequest) (*protocolopenai.ResponsesRequest, error) {
	return bridge.AnthropicToResponses(req, ConversionOptionsForModel(req.Model))
}

func AnthropicToChatCompletionsRequest(req *protocolanthropic.AnthropicRequest) (*protocolopenai.ChatCompletionsRequest, error) {
	return bridge.AnthropicToChatCompletionsRequest(req, ConversionOptionsForModel(req.Model))
}

func ChatCompletionsToResponses(req *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error) {
	return bridge.ChatCompletionsToResponses(req, ConversionOptionsForModel(req.Model))
}
