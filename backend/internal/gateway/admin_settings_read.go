package gateway

import (
	"log/slog"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	settingvalues "github.com/TokenFlux/TokenRouter/internal/settings"
)

// AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	AntigravityUserAgentVersion            string
	BackendModeEnabled                     bool
	ClaudeOAuthSystemPrompt                string
	ClaudeOAuthSystemPromptBlocks          string
	EnableAnthropicCacheTTL1hInjection     bool
	EnableCCHSigning                       bool
	EnableClaudeOAuthSystemPromptInjection bool
	EnableClientDatelineNormalization      bool
	EnableFingerprintUnification           bool
	EnableIdentityPatch                    bool
	EnableMetadataPassthrough              bool
	GrokCrossClientModelMapEnabled         bool
	GrokDefaultBaseURLMode                 string
	GrokDefaultTextModel                   string
	IdentityPatchPrompt                    string
	MaxClaudeCodeVersion                   string
	MinClaudeCodeVersion                   string
	OpenAIAllowClaudeCodeCodexPlugin       bool
	OpenAICodexUserAgent                   string
	OpenAITTFTMode                         string
	RewriteMessageCacheControl             bool
	UserPromptReplacementConfig            *promptpolicy.UserPromptReplacementConfig
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string, rules AdminSettingsRules) *AdminReadSettings {

	result := &AdminReadSettings{}
	result.BackendModeEnabled = settings[SettingKeyBackendModeEnabled] == "true"
	if v, ok := settings[SettingKeyEnableIdentityPatch]; ok && v != "" {
		result.EnableIdentityPatch = v == "true"
	} else {
		result.EnableIdentityPatch = true
	}
	result.IdentityPatchPrompt = settings[SettingKeyIdentityPatchPrompt]
	result.GrokDefaultTextModel = strings.TrimSpace(settings[SettingKeyGrokDefaultTextModel])
	if result.GrokDefaultTextModel == "" {
		result.GrokDefaultTextModel = rules.GrokDefaultTextModel
	}
	result.GrokCrossClientModelMapEnabled = !settingvalues.IsExplicitFalse(settings[SettingKeyGrokCrossClientModelMapEnabled])
	result.GrokDefaultBaseURLMode = NormalizeGrokDefaultBaseURLMode(settings[SettingKeyGrokDefaultBaseURLMode])
	result.MinClaudeCodeVersion = settings[SettingKeyMinClaudeCodeVersion]
	result.MaxClaudeCodeVersion = settings[SettingKeyMaxClaudeCodeVersion]
	result.OpenAITTFTMode = NormalizeOpenAITTFTMode(settings[SettingKeyOpenAITTFTMode])
	if v, ok := settings[SettingKeyEnableFingerprintUnification]; ok && v != "" {
		result.EnableFingerprintUnification = v == "true"
	} else {
		result.EnableFingerprintUnification = true // default: enabled (current behavior)
	}
	result.EnableMetadataPassthrough = settings[SettingKeyEnableMetadataPassthrough] == "true"
	result.EnableCCHSigning = settings[SettingKeyEnableCCHSigning] == "true"
	if v, ok := settings[SettingKeyEnableClaudeOAuthSystemPromptInjection]; ok && v != "" {
		result.EnableClaudeOAuthSystemPromptInjection = v == "true"
	} else {
		result.EnableClaudeOAuthSystemPromptInjection = true
	}
	result.ClaudeOAuthSystemPrompt = settings[SettingKeyClaudeOAuthSystemPrompt]
	result.ClaudeOAuthSystemPromptBlocks = settings[SettingKeyClaudeOAuthSystemPromptBlocks]
	result.EnableAnthropicCacheTTL1hInjection = settings[SettingKeyEnableAnthropicCacheTTL1hInjection] == "true"
	if v, ok := settings[SettingKeyRewriteMessageCacheControl]; ok && v != "" {
		result.RewriteMessageCacheControl = v == "true"
	} else {
		result.RewriteMessageCacheControl = false
	}
	if v, ok := settings[SettingKeyEnableClientDatelineNormalization]; ok && v != "" {
		result.EnableClientDatelineNormalization = v == "true"
	} else {
		result.EnableClientDatelineNormalization = true
	}
	result.AntigravityUserAgentVersion = rules.NormalizeUserAgentVersion(settings[SettingKeyAntigravityUserAgentVersion])
	result.OpenAICodexUserAgent = strings.TrimSpace(settings[SettingKeyOpenAICodexUserAgent])
	result.OpenAIAllowClaudeCodeCodexPlugin = settings[SettingKeyOpenAIAllowClaudeCodeCodexPlugin] == "true"
	result.UserPromptReplacementConfig = parseAdminPromptConfig(settings[SettingKeyUserPromptReplacementConfig])
	return result
}

// parseAdminPromptConfig 复用请求提示词策略的唯一解析与诊断。
func parseAdminPromptConfig(raw string) *promptpolicy.UserPromptReplacementConfig {
	return promptpolicy.ParseConfig(raw, slog.Warn)
}
