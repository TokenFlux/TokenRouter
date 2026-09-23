package text

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// ResponsesCapability 根据显式生图意图选择账号必须支持的端点能力。
func ResponsesCapability(imageIntent bool, platform string) accountcore.OpenAIEndpointCapability {
	if imageIntent && platform == capability.PlatformOpenAI {
		return accountcore.OpenAIEndpointCapabilityResponses
	}
	return accountcore.OpenAIEndpointCapabilityTextGeneration
}

// RequiredResponsesCapability 让两类压缩都要求 Responses 能力，
// 其中原生 V2 还必须通过自身独立的账号模式和探测状态门禁。
func RequiredResponsesCapability(imageIntent bool, nativeCompactionV2 bool, legacyCompact bool, platform string) accountcore.OpenAIEndpointCapability {
	if nativeCompactionV2 && platform == capability.PlatformOpenAI {
		return accountcore.OpenAIEndpointCapabilityRemoteCompactionV2
	}
	if legacyCompact && platform == capability.PlatformOpenAI {
		return accountcore.OpenAIEndpointCapabilityResponses
	}
	return ResponsesCapability(imageIntent, platform)
}
