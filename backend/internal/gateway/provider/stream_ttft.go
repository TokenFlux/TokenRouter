package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// OpenAIStreamDataStartsTTFT 按管理员设置选择语义事件或真实可见内容作为 TTFT 起点。
func OpenAIStreamDataStartsTTFT(data, eventType string, forceOutput bool, mode string) bool {
	if gateway.NormalizeOpenAITTFTMode(mode) == gateway.OpenAITTFTModeVisible {
		return protocolopenai.StreamDataStartsVisibleOutput(data, eventType)
	}
	return forceOutput || openai.OpenAIStreamDataStartsSemanticTTFT(data, eventType)
}
