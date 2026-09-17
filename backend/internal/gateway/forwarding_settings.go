package gateway

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// 转发配置沿用原持久化键及 TTFT 值域。
const (
	SettingKeyClaudeOAuthSystemPrompt                = "claude_oauth_system_prompt"
	SettingKeyClaudeOAuthSystemPromptBlocks          = "claude_oauth_system_prompt_blocks"
	SettingKeyEnableAnthropicCacheTTL1hInjection     = "enable_anthropic_cache_ttl_1h_injection"
	SettingKeyEnableCCHSigning                       = "enable_cch_signing"
	SettingKeyEnableClaudeOAuthSystemPromptInjection = "enable_claude_oauth_system_prompt_injection"
	SettingKeyEnableClientDatelineNormalization      = "enable_client_dateline_normalization"
	SettingKeyEnableFingerprintUnification           = "enable_fingerprint_unification"
	SettingKeyEnableMetadataPassthrough              = "enable_metadata_passthrough"
	SettingKeyMaxClaudeCodeVersion                   = "max_claude_code_version"
	SettingKeyMinClaudeCodeVersion                   = "min_claude_code_version"
	SettingKeyOpenAITTFTMode                         = "openai_ttft_mode"
	SettingKeyRewriteMessageCacheControl             = "rewrite_message_cache_control"
	OpenAITTFTModeSemantic                           = "semantic"
	OpenAITTFTModeVisible                            = "visible"
)

// cachedVersionBounds 缓存 Claude Code 版本号上下限（进程内缓存，60s TTL）
type cachedVersionBounds struct {
	min       string // 空字符串 = 不检查
	max       string // 空字符串 = 不检查
	expiresAt int64  // unix nano
}

// versionBoundsCacheTTL 缓存有效期
const versionBoundsCacheTTL = 60 * time.Second

// versionBoundsErrorTTL DB 错误时的短缓存，快速重试
const versionBoundsErrorTTL = 5 * time.Second

// versionBoundsDBTimeout singleflight 内 DB 查询超时，独立于请求 context
const versionBoundsDBTimeout = 5 * time.Second

// cachedGatewayForwardingSettings 缓存网关转发行为设置（进程内缓存，60s TTL）
type cachedGatewayForwardingSettings struct {
	openAITTFTMode                   string
	fingerprintUnification           bool
	metadataPassthrough              bool
	cchSigning                       bool
	claudeOAuthSystemPromptInjection bool
	claudeOAuthSystemPrompt          string
	claudeOAuthSystemPromptBlocks    string
	anthropicCacheTTL1hInjection     bool
	rewriteMessageCacheControl       bool
	clientDatelineNormalization      bool
	expiresAt                        int64 // unix nano
}

const gatewayForwardingCacheTTL = 60 * time.Second
const gatewayForwardingErrorTTL = 5 * time.Second

// ForwardingSettingsReadTimeout 保留转发设置及兼容读取的原回源预算。
const ForwardingSettingsReadTimeout = 5 * time.Second
const gatewayForwardingDBTimeout = ForwardingSettingsReadTimeout

type gatewayForwardingSettingsResult struct {
	openAITTFTMode                                                                        string
	fp, mp, cch, claudeOAuthSystemPromptInjection, cacheTTL1h, rewriteMessageCacheControl bool
	clientDatelineNormalization                                                           bool
	claudeOAuthSystemPrompt, claudeOAuthSystemPromptBlocks                                string
}

// ForwardingSnapshot 是综合设置提交后的只读发布输入。
type ForwardingSnapshot struct {
	OpenAITTFTMode                   string
	FingerprintUnification           bool
	MetadataPassthrough              bool
	CCHSigning                       bool
	ClaudeOAuthSystemPromptInjection bool
	ClaudeOAuthSystemPrompt          string
	ClaudeOAuthSystemPromptBlocks    string
	AnthropicCacheTTL1hInjection     bool
	RewriteMessageCacheControl       bool
	ClientDatelineNormalization      bool
}

// NormalizeOpenAITTFTMode 保留原兼容口径。
func NormalizeOpenAITTFTMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), OpenAITTFTModeVisible) {
		return OpenAITTFTModeVisible
	}
	return OpenAITTFTModeSemantic
}

// getGatewayForwardingSettingsCached 复用本实例配置缓存，保持原 TTL、回源与取消边界。
func (s *RuntimeSettings) getGatewayForwardingSettingsCached(ctx context.Context) gatewayForwardingSettingsResult {
	if cached, ok := s.gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return gatewayForwardingSettingsResult{
				openAITTFTMode:                   cached.openAITTFTMode,
				fp:                               cached.fingerprintUnification,
				mp:                               cached.metadataPassthrough,
				cch:                              cached.cchSigning,
				claudeOAuthSystemPromptInjection: cached.claudeOAuthSystemPromptInjection,
				claudeOAuthSystemPrompt:          cached.claudeOAuthSystemPrompt,
				claudeOAuthSystemPromptBlocks:    cached.claudeOAuthSystemPromptBlocks,
				cacheTTL1h:                       cached.anthropicCacheTTL1hInjection,
				rewriteMessageCacheControl:       cached.rewriteMessageCacheControl,
				clientDatelineNormalization:      cached.clientDatelineNormalization,
			}
		}
	}
	val, _, _ := s.gatewayForwardingSF.Do("gateway_forwarding", func() (any, error) {
		if cached, ok := s.gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return gatewayForwardingSettingsResult{
					openAITTFTMode:                   cached.openAITTFTMode,
					fp:                               cached.fingerprintUnification,
					mp:                               cached.metadataPassthrough,
					cch:                              cached.cchSigning,
					claudeOAuthSystemPromptInjection: cached.claudeOAuthSystemPromptInjection,
					claudeOAuthSystemPrompt:          cached.claudeOAuthSystemPrompt,
					claudeOAuthSystemPromptBlocks:    cached.claudeOAuthSystemPromptBlocks,
					cacheTTL1h:                       cached.anthropicCacheTTL1hInjection,
					rewriteMessageCacheControl:       cached.rewriteMessageCacheControl,
					clientDatelineNormalization:      cached.clientDatelineNormalization,
				}, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
		defer cancel()
		values, err := s.settingRepo.GetMultiple(dbCtx, []string{
			SettingKeyOpenAITTFTMode,
			SettingKeyEnableFingerprintUnification,
			SettingKeyEnableMetadataPassthrough,
			SettingKeyEnableCCHSigning,
			SettingKeyEnableClaudeOAuthSystemPromptInjection,
			SettingKeyClaudeOAuthSystemPrompt,
			SettingKeyClaudeOAuthSystemPromptBlocks,
			SettingKeyEnableAnthropicCacheTTL1hInjection,
			SettingKeyRewriteMessageCacheControl,
			SettingKeyEnableClientDatelineNormalization,
		})
		if err != nil {
			slog.Warn("failed to get gateway forwarding settings", "error", err)
			s.gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
				openAITTFTMode:                   OpenAITTFTModeSemantic,
				fingerprintUnification:           true,
				metadataPassthrough:              false,
				cchSigning:                       false,
				claudeOAuthSystemPromptInjection: true,
				anthropicCacheTTL1hInjection:     false,
				rewriteMessageCacheControl:       false,
				clientDatelineNormalization:      true,
				expiresAt:                        time.Now().Add(gatewayForwardingErrorTTL).UnixNano(),
			})
			return gatewayForwardingSettingsResult{
				openAITTFTMode:                   OpenAITTFTModeSemantic,
				fp:                               true,
				claudeOAuthSystemPromptInjection: true,
				rewriteMessageCacheControl:       false,
				clientDatelineNormalization:      true,
			}, nil
		}
		ttftMode := NormalizeOpenAITTFTMode(values[SettingKeyOpenAITTFTMode])
		fp := true
		if v, ok := values[SettingKeyEnableFingerprintUnification]; ok && v != "" {
			fp = v == "true"
		}
		mp := values[SettingKeyEnableMetadataPassthrough] == "true"
		cch := values[SettingKeyEnableCCHSigning] == "true"
		claudeOAuthSystemPromptInjection := true
		if v, ok := values[SettingKeyEnableClaudeOAuthSystemPromptInjection]; ok && v != "" {
			claudeOAuthSystemPromptInjection = v == "true"
		}
		claudeOAuthSystemPrompt := values[SettingKeyClaudeOAuthSystemPrompt]
		claudeOAuthSystemPromptBlocks := values[SettingKeyClaudeOAuthSystemPromptBlocks]
		cacheTTL1h := values[SettingKeyEnableAnthropicCacheTTL1hInjection] == "true"
		rewriteMessageCacheControl := false
		if v, ok := values[SettingKeyRewriteMessageCacheControl]; ok && v != "" {
			rewriteMessageCacheControl = v == "true"
		}
		clientDatelineNormalization := true
		if v, ok := values[SettingKeyEnableClientDatelineNormalization]; ok && v != "" {
			clientDatelineNormalization = v == "true"
		}
		s.gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
			openAITTFTMode:                   ttftMode,
			fingerprintUnification:           fp,
			metadataPassthrough:              mp,
			cchSigning:                       cch,
			claudeOAuthSystemPromptInjection: claudeOAuthSystemPromptInjection,
			claudeOAuthSystemPrompt:          claudeOAuthSystemPrompt,
			claudeOAuthSystemPromptBlocks:    claudeOAuthSystemPromptBlocks,
			anthropicCacheTTL1hInjection:     cacheTTL1h,
			rewriteMessageCacheControl:       rewriteMessageCacheControl,
			clientDatelineNormalization:      clientDatelineNormalization,
			expiresAt:                        time.Now().Add(gatewayForwardingCacheTTL).UnixNano(),
		})
		return gatewayForwardingSettingsResult{
			openAITTFTMode:                   ttftMode,
			fp:                               fp,
			mp:                               mp,
			cch:                              cch,
			claudeOAuthSystemPromptInjection: claudeOAuthSystemPromptInjection,
			claudeOAuthSystemPrompt:          claudeOAuthSystemPrompt,
			claudeOAuthSystemPromptBlocks:    claudeOAuthSystemPromptBlocks,
			cacheTTL1h:                       cacheTTL1h,
			rewriteMessageCacheControl:       rewriteMessageCacheControl,
			clientDatelineNormalization:      clientDatelineNormalization,
		}, nil
	})
	if r, ok := val.(gatewayForwardingSettingsResult); ok {
		return r
	}
	return gatewayForwardingSettingsResult{openAITTFTMode: OpenAITTFTModeSemantic, fp: true, claudeOAuthSystemPromptInjection: true, clientDatelineNormalization: true}
}

// GetOpenAITTFTMode 复用本实例配置缓存，保持原 TTL、回源与取消边界。
func (s *RuntimeSettings) GetOpenAITTFTMode(ctx context.Context) string {
	return NormalizeOpenAITTFTMode(s.getGatewayForwardingSettingsCached(ctx).openAITTFTMode)
}

// GetGatewayForwardingSettings 复用本实例配置缓存，保持原 TTL、回源与取消边界。
func (s *RuntimeSettings) GetGatewayForwardingSettings(ctx context.Context) (fingerprintUnification, metadataPassthrough, cchSigning bool) {
	result := s.getGatewayForwardingSettingsCached(ctx)
	return result.fp, result.mp, result.cch
}

// IsAnthropicCacheTTL1hInjectionEnabled 复用本实例配置缓存，保持原 TTL、回源与取消边界。
func (s *RuntimeSettings) IsAnthropicCacheTTL1hInjectionEnabled(ctx context.Context) bool {
	return s.getGatewayForwardingSettingsCached(ctx).cacheTTL1h
}

// IsRewriteMessageCacheControlEnabled 复用本实例配置缓存，保持原 TTL、回源与取消边界。
func (s *RuntimeSettings) IsRewriteMessageCacheControlEnabled(ctx context.Context) bool {
	return s.getGatewayForwardingSettingsCached(ctx).rewriteMessageCacheControl
}

// IsClientDatelineNormalizationEnabled 复用本实例配置缓存，保持原 TTL、回源与取消边界。
func (s *RuntimeSettings) IsClientDatelineNormalizationEnabled(ctx context.Context) bool {
	return s.getGatewayForwardingSettingsCached(ctx).clientDatelineNormalization
}

// GetClaudeOAuthSystemPromptInjectionSettings 复用本实例配置缓存，保持原 TTL、回源与取消边界。
func (s *RuntimeSettings) GetClaudeOAuthSystemPromptInjectionSettings(ctx context.Context) (enabled bool, prompt string, blocks string) {
	result := s.getGatewayForwardingSettingsCached(ctx)
	return result.claudeOAuthSystemPromptInjection, result.claudeOAuthSystemPrompt, result.claudeOAuthSystemPromptBlocks
}

// GetClaudeCodeVersionBounds 复用本实例配置缓存，保持原 TTL、回源与取消边界。
func (s *RuntimeSettings) GetClaudeCodeVersionBounds(ctx context.Context) (min, max string) {
	if cached, ok := s.versionBoundsCache.Load().(*cachedVersionBounds); ok {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.min, cached.max
		}
	}
	// singleflight: 同一时刻只有一个 goroutine 查询 DB，其余复用结果
	type bounds struct{ min, max string }
	result, err, _ := s.versionBoundsSF.Do("version_bounds", func() (any, error) {
		// 二次检查，避免排队的 goroutine 重复查询
		if cached, ok := s.versionBoundsCache.Load().(*cachedVersionBounds); ok {
			if time.Now().UnixNano() < cached.expiresAt {
				return bounds{cached.min, cached.max}, nil
			}
		}
		// 使用独立 context：断开请求取消链，避免客户端断连导致空值被长期缓存
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), versionBoundsDBTimeout)
		defer cancel()
		values, err := s.settingRepo.GetMultiple(dbCtx, []string{
			SettingKeyMinClaudeCodeVersion,
			SettingKeyMaxClaudeCodeVersion,
		})
		if err != nil {
			// fail-open: DB 错误时不阻塞请求，但记录日志并使用短 TTL 快速重试
			slog.Warn("failed to get claude code version bounds setting, skipping version check", "error", err)
			s.versionBoundsCache.Store(&cachedVersionBounds{
				min:       "",
				max:       "",
				expiresAt: time.Now().Add(versionBoundsErrorTTL).UnixNano(),
			})
			return bounds{"", ""}, nil
		}
		b := bounds{
			min: values[SettingKeyMinClaudeCodeVersion],
			max: values[SettingKeyMaxClaudeCodeVersion],
		}
		s.versionBoundsCache.Store(&cachedVersionBounds{
			min:       b.min,
			max:       b.max,
			expiresAt: time.Now().Add(versionBoundsCacheTTL).UnixNano(),
		})
		return b, nil
	})
	if err != nil {
		return "", ""
	}
	b, ok := result.(bounds)
	if !ok {
		return "", ""
	}
	return b.min, b.max
}

// PublishForwarding 按原更新顺序发布已持久化值，TTL 和 singleflight 行为保持。
func (s *RuntimeSettings) PublishForwarding(minVersion, maxVersion string, value ForwardingSnapshot) {
	s.versionBoundsSF.Forget("version_bounds")
	s.versionBoundsCache.Store(&cachedVersionBounds{min: minVersion, max: maxVersion, expiresAt: time.Now().Add(versionBoundsCacheTTL).UnixNano()})
	s.gatewayForwardingSF.Forget("gateway_forwarding")
	s.gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
		openAITTFTMode:                   value.OpenAITTFTMode,
		fingerprintUnification:           value.FingerprintUnification,
		metadataPassthrough:              value.MetadataPassthrough,
		cchSigning:                       value.CCHSigning,
		claudeOAuthSystemPromptInjection: value.ClaudeOAuthSystemPromptInjection,
		claudeOAuthSystemPrompt:          value.ClaudeOAuthSystemPrompt,
		claudeOAuthSystemPromptBlocks:    value.ClaudeOAuthSystemPromptBlocks,
		anthropicCacheTTL1hInjection:     value.AnthropicCacheTTL1hInjection,
		rewriteMessageCacheControl:       value.RewriteMessageCacheControl,
		clientDatelineNormalization:      value.ClientDatelineNormalization,
		expiresAt:                        time.Now().Add(gatewayForwardingCacheTTL).UnixNano(),
	})
}

// InvalidateForwarding 使此实例下一次读取回源，不影响其它实例或请求。
func (s *RuntimeSettings) InvalidateForwarding() {
	s.gatewayForwardingSF.Forget("gateway_forwarding")
	s.gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
}
