package gateway

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
)

// AdminSettings 仅包含入站与转发配置；Fast 使用已有的独立策略准备器。
type AdminSettings struct {
	AntigravityUserAgentVersion            string                                    `json:"antigravity_user_agent_version"`
	BackendModeEnabled                     bool                                      `json:"backend_mode_enabled"`
	ClaudeOAuthSystemPrompt                string                                    `json:"claude_oauth_system_prompt"`
	ClaudeOAuthSystemPromptBlocks          string                                    `json:"claude_oauth_system_prompt_blocks"`
	EnableAnthropicCacheTTL1hInjection     bool                                      `json:"enable_anthropic_cache_ttl_1h_injection"`
	EnableCCHSigning                       bool                                      `json:"enable_cch_signing"`
	EnableClaudeOAuthSystemPromptInjection bool                                      `json:"enable_claude_oauth_system_prompt_injection"`
	EnableClientDatelineNormalization      bool                                      `json:"enable_client_dateline_normalization"`
	EnableFingerprintUnification           bool                                      `json:"enable_fingerprint_unification"`
	EnableIdentityPatch                    bool                                      `json:"enable_identity_patch"`
	EnableMetadataPassthrough              bool                                      `json:"enable_metadata_passthrough"`
	GrokCrossClientModelMapEnabled         bool                                      `json:"grok_cross_client_model_map_enabled"`
	GrokDefaultBaseURLMode                 string                                    `json:"grok_default_base_url_mode"`
	GrokDefaultTextModel                   string                                    `json:"grok_default_text_model"`
	IdentityPatchPrompt                    string                                    `json:"identity_patch_prompt"`
	MaxClaudeCodeVersion                   string                                    `json:"max_claude_code_version"`
	MinClaudeCodeVersion                   string                                    `json:"min_claude_code_version"`
	OpenAIAllowClaudeCodeCodexPlugin       bool                                      `json:"openai_allow_claude_code_codex_plugin"`
	OpenAICodexUserAgent                   string                                    `json:"openai_codex_user_agent"`
	OpenAITTFTMode                         string                                    `json:"openai_ttft_mode"`
	RewriteMessageCacheControl             bool                                      `json:"rewrite_message_cache_control"`
	UserPromptReplacementConfig            *promptpolicy.UserPromptReplacementConfig `json:"user_prompt_replacement_config"`
}

// AdminSettingsRules 由 app 投影平台纯规则；不接收具体供应商服务或完整配置。
type AdminSettingsRules struct {
	GrokDefaultTextModel       string
	NormalizeUserAgentVersion  func(string) string
	ValidateClaudePromptBlocks func(string) error
}

// 网关综合设置沿用原持久键。
const (
	SettingKeyAntigravityUserAgentVersion      = "antigravity_user_agent_version"
	SettingKeyBackendModeEnabled               = "backend_mode_enabled"
	SettingKeyEnableIdentityPatch              = "enable_identity_patch"
	SettingKeyGrokCrossClientModelMapEnabled   = "grok_cross_client_model_map_enabled"
	SettingKeyGrokDefaultBaseURLMode           = "grok_default_base_url_mode"
	SettingKeyGrokDefaultTextModel             = "grok_default_text_model"
	SettingKeyIdentityPatchPrompt              = "identity_patch_prompt"
	SettingKeyOpenAIAllowClaudeCodeCodexPlugin = "openai_allow_claude_code_codex_plugin"
	SettingKeyOpenAICodexUserAgent             = "openai_codex_user_agent"
	SettingKeyUserPromptReplacementConfig      = promptpolicy.SettingKeyUserPromptReplacementConfig
)

// PrepareAdminSettings 只规范化和编码配置，不改变请求执行、缓存或重试。
func PrepareAdminSettings(settings *AdminSettings, rules AdminSettingsRules) (map[string]string, error) {
	updates := map[string]string{}
	if model := strings.TrimSpace(settings.GrokDefaultTextModel); model != "" {
		updates[SettingKeyGrokDefaultTextModel] = model
	} else {
		updates[SettingKeyGrokDefaultTextModel] = rules.GrokDefaultTextModel
	}
	updates[SettingKeyGrokCrossClientModelMapEnabled] = strconv.FormatBool(settings.GrokCrossClientModelMapEnabled)
	updates[SettingKeyGrokDefaultBaseURLMode] = NormalizeGrokDefaultBaseURLMode(settings.GrokDefaultBaseURLMode)
	updates[SettingKeyEnableIdentityPatch] = strconv.FormatBool(settings.EnableIdentityPatch)
	updates[SettingKeyIdentityPatchPrompt] = settings.IdentityPatchPrompt
	updates[SettingKeyMinClaudeCodeVersion] = settings.MinClaudeCodeVersion
	updates[SettingKeyMaxClaudeCodeVersion] = settings.MaxClaudeCodeVersion
	updates[SettingKeyBackendModeEnabled] = strconv.FormatBool(settings.BackendModeEnabled)
	mode := NormalizeOpenAITTFTMode(settings.OpenAITTFTMode)
	if raw := strings.TrimSpace(settings.OpenAITTFTMode); raw != "" && !strings.EqualFold(raw, OpenAITTFTModeSemantic) && !strings.EqualFold(raw, OpenAITTFTModeVisible) {
		return nil, fmt.Errorf("%s must be one of: %s/%s", SettingKeyOpenAITTFTMode, OpenAITTFTModeSemantic, OpenAITTFTModeVisible)
	}
	updates[SettingKeyOpenAITTFTMode] = mode
	updates[SettingKeyEnableFingerprintUnification] = strconv.FormatBool(settings.EnableFingerprintUnification)
	updates[SettingKeyEnableMetadataPassthrough] = strconv.FormatBool(settings.EnableMetadataPassthrough)
	updates[SettingKeyEnableCCHSigning] = strconv.FormatBool(settings.EnableCCHSigning)
	if err := rules.ValidateClaudePromptBlocks(settings.ClaudeOAuthSystemPromptBlocks); err != nil {
		return nil, err
	}
	updates[SettingKeyEnableClaudeOAuthSystemPromptInjection] = strconv.FormatBool(settings.EnableClaudeOAuthSystemPromptInjection)
	updates[SettingKeyClaudeOAuthSystemPrompt] = settings.ClaudeOAuthSystemPrompt
	updates[SettingKeyClaudeOAuthSystemPromptBlocks] = settings.ClaudeOAuthSystemPromptBlocks
	updates[SettingKeyEnableAnthropicCacheTTL1hInjection] = strconv.FormatBool(settings.EnableAnthropicCacheTTL1hInjection)
	updates[SettingKeyRewriteMessageCacheControl] = strconv.FormatBool(settings.RewriteMessageCacheControl)
	updates[SettingKeyEnableClientDatelineNormalization] = strconv.FormatBool(settings.EnableClientDatelineNormalization)
	updates[SettingKeyAntigravityUserAgentVersion] = rules.NormalizeUserAgentVersion(settings.AntigravityUserAgentVersion)
	updates[SettingKeyOpenAICodexUserAgent] = strings.TrimSpace(settings.OpenAICodexUserAgent)
	updates[SettingKeyOpenAIAllowClaudeCodeCodexPlugin] = strconv.FormatBool(settings.OpenAIAllowClaudeCodeCodexPlugin)
	userPromptReplacementConfigJSON, err := promptpolicy.ConfigToRaw(settings.UserPromptReplacementConfig)
	if err != nil {
		return nil, err
	}
	updates[SettingKeyUserPromptReplacementConfig] = userPromptReplacementConfigJSON
	return updates, nil
}

// NormalizeGrokDefaultBaseURLMode 保留原默认 CLI 和五种管理选项。
func NormalizeGrokDefaultBaseURLMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "api":
		return "api"
	case "us-east-1":
		return "us-east-1"
	case "us-west-2":
		return "us-west-2"
	case "eu-west-1":
		return "eu-west-1"
	case "cli":
		return "cli"
	default:
		return "cli"
	}
}
