package composite

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway"
)

// GatewayAdminSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) GatewayAdminSettings() gateway.AdminSettings {
	return gateway.AdminSettings{
		AntigravityUserAgentVersion:            s.AntigravityUserAgentVersion,
		BackendModeEnabled:                     s.BackendModeEnabled,
		ClaudeOAuthSystemPrompt:                s.ClaudeOAuthSystemPrompt,
		ClaudeOAuthSystemPromptBlocks:          s.ClaudeOAuthSystemPromptBlocks,
		EnableAnthropicCacheTTL1hInjection:     s.EnableAnthropicCacheTTL1hInjection,
		EnableCCHSigning:                       s.EnableCCHSigning,
		EnableClaudeOAuthSystemPromptInjection: s.EnableClaudeOAuthSystemPromptInjection,
		EnableClientDatelineNormalization:      s.EnableClientDatelineNormalization,
		EnableFingerprintUnification:           s.EnableFingerprintUnification,
		EnableIdentityPatch:                    s.EnableIdentityPatch,
		EnableMetadataPassthrough:              s.EnableMetadataPassthrough,
		GrokCrossClientModelMapEnabled:         s.GrokCrossClientModelMapEnabled,
		GrokDefaultBaseURLMode:                 s.GrokDefaultBaseURLMode,
		GrokDefaultTextModel:                   s.GrokDefaultTextModel,
		IdentityPatchPrompt:                    s.IdentityPatchPrompt,
		MaxClaudeCodeVersion:                   s.MaxClaudeCodeVersion,
		MinClaudeCodeVersion:                   s.MinClaudeCodeVersion,
		OpenAIAllowClaudeCodeCodexPlugin:       s.OpenAIAllowClaudeCodeCodexPlugin,
		OpenAICodexUserAgent:                   s.OpenAICodexUserAgent,
		OpenAITTFTMode:                         s.OpenAITTFTMode,
		RewriteMessageCacheControl:             s.RewriteMessageCacheControl,
		UserPromptReplacementConfig:            s.UserPromptReplacementConfig,
	}
}
