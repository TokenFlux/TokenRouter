package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/antigravity"
	"golang.org/x/sync/singleflight"
)

// cachedVersionBounds 缓存 Claude Code 版本号上下限（进程内缓存，60s TTL）
type cachedVersionBounds struct {
	min       string // 空字符串 = 不检查
	max       string // 空字符串 = 不检查
	expiresAt int64  // unix nano
}

// versionBoundsCache 版本号上下限进程内缓存
var versionBoundsCache atomic.Value // *cachedVersionBounds

// versionBoundsSF 防止缓存过期时 thundering herd
var versionBoundsSF singleflight.Group

// versionBoundsCacheTTL 缓存有效期
const versionBoundsCacheTTL = 60 * time.Second

// versionBoundsErrorTTL DB 错误时的短缓存，快速重试
const versionBoundsErrorTTL = 5 * time.Second

// versionBoundsDBTimeout singleflight 内 DB 查询超时，独立于请求 context
const versionBoundsDBTimeout = 5 * time.Second

// cachedBackendMode Backend Mode cache (in-process, 60s TTL)
type cachedBackendMode struct {
	value     bool
	expiresAt int64 // unix nano
}

var backendModeCache atomic.Value // *cachedBackendMode
var backendModeSF singleflight.Group

const backendModeCacheTTL = 60 * time.Second
const backendModeErrorTTL = 5 * time.Second
const backendModeDBTimeout = 5 * time.Second

// cachedGatewayForwardingSettings 缓存网关转发行为设置（进程内缓存，60s TTL）
type cachedGatewayForwardingSettings struct {
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

var gatewayForwardingCache atomic.Value // *cachedGatewayForwardingSettings
var gatewayForwardingSF singleflight.Group

const gatewayForwardingCacheTTL = 60 * time.Second
const gatewayForwardingErrorTTL = 5 * time.Second
const gatewayForwardingDBTimeout = 5 * time.Second

// cachedAccountSchedulingThresholds 缓存各平台自动停调阈值。
type cachedAccountSchedulingThresholds struct {
	thresholds map[string]int
	expiresAt  int64 // Unix 纳秒时间戳
}

var accountSchedulingThresholdsCache atomic.Value // *cachedAccountSchedulingThresholds
var accountSchedulingThresholdsSF singleflight.Group

const accountSchedulingThresholdsCacheTTL = 60 * time.Second
const accountSchedulingThresholdsErrorTTL = 5 * time.Second
const accountSchedulingThresholdsDBTimeout = 5 * time.Second

// cachedAntigravityUserAgentVersion 缓存 Antigravity UA 版本号（进程内缓存，60s TTL）。
type cachedAntigravityUserAgentVersion struct {
	version   string
	expiresAt int64 // unix nano
}

const antigravityUserAgentVersionCacheTTL = 60 * time.Second
const antigravityUserAgentVersionErrorTTL = 5 * time.Second
const antigravityUserAgentVersionDBTimeout = 5 * time.Second

// DefaultOpenAICodexUserAgent 是 OpenAI Codex 默认 User-Agent，用于规避浏览器 UA 的质询。
// 默认采用 codex-tui 身份。
const DefaultOpenAICodexUserAgent = "codex-tui/0.144.1 (Ubuntu 22.4.0; x86_64) xterm-256color (codex-tui; 0.144.1)"

// cachedOpenAICodexUserAgent 缓存 OpenAI Codex UA（进程内缓存，60s TTL）
type cachedOpenAICodexUserAgent struct {
	value     string
	expiresAt int64 // unix nano
}

type cachedOpenAIQuotaAutoPauseSettings struct {
	settings  OpsOpenAIAccountQuotaAutoPauseSettings
	expiresAt int64
}

const openAICodexUserAgentCacheTTL = 60 * time.Second
const openAICodexUserAgentErrorTTL = 5 * time.Second
const openAICodexUserAgentDBTimeout = 5 * time.Second

// cachedCyberSessionBlockRuntime 缓存 cyber 会话屏蔽运行态，避免网关热路径每次访问 DB。
type cachedCyberSessionBlockRuntime struct {
	enabled   bool
	ttl       time.Duration
	expiresAt int64 // unix nano
}

const cyberSessionBlockRuntimeCacheTTL = 60 * time.Second
const cyberSessionBlockRuntimeErrorTTL = 5 * time.Second
const cyberSessionBlockRuntimeDBTimeout = 5 * time.Second

const openAIQuotaAutoPauseSettingsCacheTTL = 60 * time.Second
const openAIQuotaAutoPauseSettingsErrorTTL = 5 * time.Second
const openAIQuotaAutoPauseSettingsDBTimeout = 5 * time.Second

const openAIQuotaAutoPauseSettingsRefreshKey = "openai_quota_auto_pause_settings"

// GetCyberSessionBlockRuntime 返回 cyber 会话屏蔽开关和 TTL；读取失败时短缓存默认关闭。
func (s *SettingService) GetCyberSessionBlockRuntime(ctx context.Context) (bool, time.Duration) {
	if s == nil || s.settingRepo == nil {
		return false, time.Hour
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cached, ok := s.cyberSessionBlockCache.Load().(*cachedCyberSessionBlockRuntime); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.enabled, cached.ttl
		}
	}
	result, _, _ := s.cyberSessionBlockSF.Do("cyber_session_block_runtime", func() (any, error) {
		if cached, ok := s.cyberSessionBlockCache.Load().(*cachedCyberSessionBlockRuntime); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cyberSessionBlockRuntimeDBTimeout)
		defer cancel()

		enabledVal, enabledErr := s.settingRepo.GetValue(dbCtx, SettingKeyCyberSessionBlockEnabled)
		ttlVal, ttlErr := s.settingRepo.GetValue(dbCtx, SettingKeyCyberSessionBlockTTLSeconds)
		if enabledErr != nil && !errors.Is(enabledErr, ErrSettingNotFound) {
			slog.Warn("failed to get cyber session block setting", "error", enabledErr)
			entry := &cachedCyberSessionBlockRuntime{
				enabled:   false,
				ttl:       time.Hour,
				expiresAt: time.Now().Add(cyberSessionBlockRuntimeErrorTTL).UnixNano(),
			}
			s.cyberSessionBlockCache.Store(entry)
			return entry, nil
		}

		ttl := time.Hour
		if ttlErr == nil {
			if seconds, err := strconv.Atoi(strings.TrimSpace(ttlVal)); err == nil && seconds > 0 {
				ttl = time.Duration(seconds) * time.Second
			}
		}
		entry := &cachedCyberSessionBlockRuntime{
			enabled:   enabledErr == nil && strings.TrimSpace(enabledVal) == "true",
			ttl:       ttl,
			expiresAt: time.Now().Add(cyberSessionBlockRuntimeCacheTTL).UnixNano(),
		}
		s.cyberSessionBlockCache.Store(entry)
		return entry, nil
	})
	if entry, ok := result.(*cachedCyberSessionBlockRuntime); ok && entry != nil {
		return entry.enabled, entry.ttl
	}
	return false, time.Hour
}

// GetAntigravityUserAgentVersion 返回 Antigravity 上游请求使用的版本号。
// 后台设置优先；为空、缺失或非法时回退到 ANTIGRAVITY_USER_AGENT_VERSION / 内置默认值。
func (s *SettingService) GetAntigravityUserAgentVersion(ctx context.Context) string {
	fallback := antigravity.GetDefaultUserAgentVersion()
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.antigravityUAVersionCache.Load().(*cachedAntigravityUserAgentVersion); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.version
		}
	}

	result, _, _ := s.antigravityUAVersionSF.Do("antigravity_user_agent_version", func() (any, error) {
		if cached, ok := s.antigravityUAVersionCache.Load().(*cachedAntigravityUserAgentVersion); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.version, nil
			}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), antigravityUserAgentVersionDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyAntigravityUserAgentVersion)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			slog.Warn("failed to get antigravity user agent version setting", "error", err)
			s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{
				version:   fallback,
				expiresAt: time.Now().Add(antigravityUserAgentVersionErrorTTL).UnixNano(),
			})
			return fallback, nil
		}
		version := antigravity.NormalizeUserAgentVersion(value)
		if version == "" {
			version = fallback
		}
		s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{
			version:   version,
			expiresAt: time.Now().Add(antigravityUserAgentVersionCacheTTL).UnixNano(),
		})
		return version, nil
	})
	if version, ok := result.(string); ok && version != "" {
		return version
	}
	return fallback
}

// GetOpenAICodexUserAgent 返回 OpenAI Codex 上游请求使用的 User-Agent。
// 后台设置优先；为空时回退到内置默认值。
func (s *SettingService) GetOpenAICodexUserAgent(ctx context.Context) string {
	fallback := DefaultOpenAICodexUserAgent
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.openAICodexUACache.Load().(*cachedOpenAICodexUserAgent); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}

	result, _, _ := s.openAICodexUASF.Do("openai_codex_user_agent", func() (any, error) {
		if cached, ok := s.openAICodexUACache.Load().(*cachedOpenAICodexUserAgent); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAICodexUserAgentDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAICodexUserAgent)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			slog.Warn("failed to get openai codex user agent setting", "error", err)
			s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{
				value:     fallback,
				expiresAt: time.Now().Add(openAICodexUserAgentErrorTTL).UnixNano(),
			})
			return fallback, nil
		}
		ua := strings.TrimSpace(value)
		if ua == "" {
			ua = fallback
		}
		s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{
			value:     ua,
			expiresAt: time.Now().Add(openAICodexUserAgentCacheTTL).UnixNano(),
		})
		return ua, nil
	})
	if ua, ok := result.(string); ok && ua != "" {
		return ua
	}
	return fallback
}

// IsBackendModeEnabled checks if backend mode is enabled
// Uses in-process atomic.Value cache with 60s TTL, zero-lock hot path
func (s *SettingService) IsBackendModeEnabled(ctx context.Context) bool {
	if cached, ok := backendModeCache.Load().(*cachedBackendMode); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}
	result, _, _ := backendModeSF.Do("backend_mode", func() (any, error) {
		if cached, ok := backendModeCache.Load().(*cachedBackendMode); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backendModeDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyBackendModeEnabled)
		if err != nil {
			if errors.Is(err, ErrSettingNotFound) {
				// Setting not yet created (fresh install) - default to disabled with full TTL
				backendModeCache.Store(&cachedBackendMode{
					value:     false,
					expiresAt: time.Now().Add(backendModeCacheTTL).UnixNano(),
				})
				return false, nil
			}
			slog.Warn("failed to get backend_mode_enabled setting", "error", err)
			backendModeCache.Store(&cachedBackendMode{
				value:     false,
				expiresAt: time.Now().Add(backendModeErrorTTL).UnixNano(),
			})
			return false, nil
		}
		enabled := value == "true"
		backendModeCache.Store(&cachedBackendMode{
			value:     enabled,
			expiresAt: time.Now().Add(backendModeCacheTTL).UnixNano(),
		})
		return enabled, nil
	})
	if val, ok := result.(bool); ok {
		return val
	}
	return false
}

type gatewayForwardingSettingsResult struct {
	fp, mp, cch, claudeOAuthSystemPromptInjection, cacheTTL1h, rewriteMessageCacheControl bool
	clientDatelineNormalization                                                           bool
	claudeOAuthSystemPrompt, claudeOAuthSystemPromptBlocks                                string
}

func (s *SettingService) getGatewayForwardingSettingsCached(ctx context.Context) gatewayForwardingSettingsResult {
	if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return gatewayForwardingSettingsResult{
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
	val, _, _ := gatewayForwardingSF.Do("gateway_forwarding", func() (any, error) {
		if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return gatewayForwardingSettingsResult{
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
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
				fingerprintUnification:           true,
				metadataPassthrough:              false,
				cchSigning:                       false,
				claudeOAuthSystemPromptInjection: true,
				anthropicCacheTTL1hInjection:     false,
				rewriteMessageCacheControl:       s.defaultRewriteMessageCacheControl(),
				clientDatelineNormalization:      true,
				expiresAt:                        time.Now().Add(gatewayForwardingErrorTTL).UnixNano(),
			})
			return gatewayForwardingSettingsResult{
				fp:                               true,
				claudeOAuthSystemPromptInjection: true,
				rewriteMessageCacheControl:       s.defaultRewriteMessageCacheControl(),
				clientDatelineNormalization:      true,
			}, nil
		}
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
		rewriteMessageCacheControl := s.defaultRewriteMessageCacheControl()
		if v, ok := values[SettingKeyRewriteMessageCacheControl]; ok && v != "" {
			rewriteMessageCacheControl = v == "true"
		}
		clientDatelineNormalization := true
		if v, ok := values[SettingKeyEnableClientDatelineNormalization]; ok && v != "" {
			clientDatelineNormalization = v == "true"
		}
		gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
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
	return gatewayForwardingSettingsResult{fp: true, claudeOAuthSystemPromptInjection: true, clientDatelineNormalization: true}
}

// GetGatewayForwardingSettings returns cached gateway forwarding settings.
// Uses in-process atomic.Value cache with 60s TTL, zero-lock hot path.
// Returns (fingerprintUnification, metadataPassthrough, cchSigning).
func (s *SettingService) GetGatewayForwardingSettings(ctx context.Context) (fingerprintUnification, metadataPassthrough, cchSigning bool) {
	result := s.getGatewayForwardingSettingsCached(ctx)
	return result.fp, result.mp, result.cch
}

// IsAnthropicCacheTTL1hInjectionEnabled 检查是否对 Anthropic OAuth/SetupToken 请求体注入 1h cache_control ttl。
func (s *SettingService) IsAnthropicCacheTTL1hInjectionEnabled(ctx context.Context) bool {
	return s.getGatewayForwardingSettingsCached(ctx).cacheTTL1h
}

// IsRewriteMessageCacheControlEnabled 检查是否启用 messages cache_control 改写。
func (s *SettingService) IsRewriteMessageCacheControlEnabled(ctx context.Context) bool {
	return s.getGatewayForwardingSettingsCached(ctx).rewriteMessageCacheControl
}

// IsClientDatelineNormalizationEnabled 检查是否启用 Anthropic OAuth/SetupToken 请求体
// 的客户端 dateline 归一化。默认开启。
func (s *SettingService) IsClientDatelineNormalizationEnabled(ctx context.Context) bool {
	return s.getGatewayForwardingSettingsCached(ctx).clientDatelineNormalization
}

// GetClaudeOAuthSystemPromptInjectionSettings 返回 Claude OAuth mimic system block 注入配置。
func (s *SettingService) GetClaudeOAuthSystemPromptInjectionSettings(ctx context.Context) (enabled bool, prompt string, blocks string) {
	result := s.getGatewayForwardingSettingsCached(ctx)
	return result.claudeOAuthSystemPromptInjection, result.claudeOAuthSystemPrompt, result.claudeOAuthSystemPromptBlocks
}

// GetClaudeCodeVersionBounds 获取 Claude Code 版本号上下限要求
// 使用进程内 atomic.Value 缓存，60 秒 TTL，热路径零锁开销
// singleflight 防止缓存过期时 thundering herd
// 返回空字符串表示不做对应方向的版本检查
func (s *SettingService) GetClaudeCodeVersionBounds(ctx context.Context) (min, max string) {
	if cached, ok := versionBoundsCache.Load().(*cachedVersionBounds); ok {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.min, cached.max
		}
	}
	// singleflight: 同一时刻只有一个 goroutine 查询 DB，其余复用结果
	type bounds struct{ min, max string }
	result, err, _ := versionBoundsSF.Do("version_bounds", func() (any, error) {
		// 二次检查，避免排队的 goroutine 重复查询
		if cached, ok := versionBoundsCache.Load().(*cachedVersionBounds); ok {
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
			versionBoundsCache.Store(&cachedVersionBounds{
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
		versionBoundsCache.Store(&cachedVersionBounds{
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

// GetOpenAIQuotaAutoPauseSettings 返回当前全局默认配额自动暂停设置。
// 它会在 OpenAI 调度热路径上被调用（每个请求一次），因此设计为绝不阻塞等待 DB：
//
//   - 缓存新鲜：立即返回。
//   - 缓存过期或为空：返回最后已知值，并由后台 goroutine 通过 singleflight 刷新缓存
//     （stale-while-revalidate）。
//   - 首次调用且尚无缓存：返回零默认值并触发同样的异步刷新；下一次调用即可拿到新填充的值。
//
// 需要同步读取最新持久化值的调用方（测试、更新后确认、可选启动预热）
// 应调用 WarmOpenAIQuotaAutoPauseSettings。
func (s *SettingService) GetOpenAIQuotaAutoPauseSettings(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings {
	if s == nil {
		return OpsOpenAIAccountQuotaAutoPauseSettings{}
	}
	cached, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings)
	now := time.Now().UnixNano()
	if cached != nil && now < cached.expiresAt {
		return cached.settings
	}
	// 缓存过期或未设置：触发后台刷新，但不阻塞当前请求。
	// singleflight.DoChan 会合并并发刷新；这里有意忽略返回 channel，
	// 结果会通过 atomic 缓存对外可见。
	s.openAIQuotaAutoPauseSettingsSF.DoChan(openAIQuotaAutoPauseSettingsRefreshKey, func() (any, error) {
		s.refreshOpenAIQuotaAutoPauseSettings(context.Background())
		return nil, nil
	})
	if cached != nil {
		return cached.settings // 刷新期间先返回旧值
	}
	return OpsOpenAIAccountQuotaAutoPauseSettings{}
}

// WarmOpenAIQuotaAutoPauseSettings 同步加载配额自动暂停设置到内存缓存。
// 应用启动时可用它让首个请求命中预热缓存；测试也可用它在构造服务后获得确定性读取。
func (s *SettingService) WarmOpenAIQuotaAutoPauseSettings(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings {
	if s == nil {
		return OpsOpenAIAccountQuotaAutoPauseSettings{}
	}
	s.refreshOpenAIQuotaAutoPauseSettings(ctx)
	cached, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings)
	if cached == nil {
		return OpsOpenAIAccountQuotaAutoPauseSettings{}
	}
	return cached.settings
}

// refreshOpenAIQuotaAutoPauseSettings 从 DB 读取最新设置并写入内存缓存。
// 出错时写入旧值（如果没有旧缓存则写入零默认值）并使用更短的错误 TTL，
// 让下一次刷新更快到来。该方法始终使用自带超时的 context，
// 避免刷新延迟受调用方影响而变得不可预测。
func (s *SettingService) refreshOpenAIQuotaAutoPauseSettings(ctx context.Context) {
	if s == nil || s.settingRepo == nil {
		return
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIQuotaAutoPauseSettingsDBTimeout)
	defer cancel()

	settings := OpsOpenAIAccountQuotaAutoPauseSettings{}
	ttl := openAIQuotaAutoPauseSettingsCacheTTL
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpsAdvancedSettings)
	if err == nil {
		cfg := defaultOpsAdvancedSettings()
		if strings.TrimSpace(raw) != "" {
			if jsonErr := json.Unmarshal([]byte(raw), cfg); jsonErr == nil {
				normalizeOpsAdvancedSettings(cfg)
			}
		}
		settings = cfg.OpenAIAccountQuotaAutoPause
	} else if !errors.Is(err, ErrSettingNotFound) {
		// 真实错误：继续返回旧值，但更快重试刷新。
		if prior, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings); prior != nil {
			settings = prior.settings
		}
		ttl = openAIQuotaAutoPauseSettingsErrorTTL
	}

	s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{
		settings:  settings,
		expiresAt: time.Now().Add(ttl).UnixNano(),
	})
}

// SetOpenAIQuotaAutoPauseSettings 将给定设置直接写入内存缓存。
// 设置写入路径会调用它，让下一次读取立即反映新值，不必等待后台刷新。
func (s *SettingService) SetOpenAIQuotaAutoPauseSettings(settings OpsOpenAIAccountQuotaAutoPauseSettings) {
	if s == nil {
		return
	}
	settings.DefaultThreshold5h = clampOpsQuotaAutoPauseThreshold(settings.DefaultThreshold5h)
	settings.DefaultThreshold7d = clampOpsQuotaAutoPauseThreshold(settings.DefaultThreshold7d)
	s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{
		settings:  settings,
		expiresAt: time.Now().Add(openAIQuotaAutoPauseSettingsCacheTTL).UnixNano(),
	})
}
