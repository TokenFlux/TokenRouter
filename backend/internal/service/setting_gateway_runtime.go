package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
)

// cachedAntigravityUserAgentVersion 缓存 Antigravity UA 版本号（进程内缓存，60s TTL）。

// DefaultOpenAICodexUserAgent 是 OpenAI Codex 默认 User-Agent，用于规避浏览器 UA 的质询。
// 默认采用 codex-tui 身份。
const DefaultOpenAICodexUserAgent = gateway.DefaultOpenAICodexUserAgent

// cachedOpenAICodexUserAgent 缓存 OpenAI Codex UA（进程内缓存，60s TTL）

// cachedCyberSessionBlockRuntime 缓存 cyber 会话屏蔽运行态，避免网关热路径每次访问 DB。

// GetCyberSessionBlockRuntime 返回 cyber 会话屏蔽开关和 TTL；读取失败时短缓存默认关闭。
func (s *SettingService) GetCyberSessionBlockRuntime(ctx context.Context) (bool, time.Duration) {
	return s.ModerationSettings().GetCyberSessionBlockRuntime(ctx)
}

// GetAntigravityUserAgentVersion 返回 Antigravity 上游请求使用的版本号。
// 后台设置优先；为空、缺失或非法时回退到 ANTIGRAVITY_USER_AGENT_VERSION / 内置默认值。
func (s *SettingService) GetAntigravityUserAgentVersion(ctx context.Context) string {
	if s == nil {
		return antigravity.GetDefaultUserAgentVersion()
	}
	return s.GatewaySettings().GetAntigravityUserAgentVersion(ctx)
}

// GetOpenAICodexUserAgent 返回 OpenAI Codex 上游请求使用的 User-Agent。
// 后台设置优先；为空时回退到内置默认值。
func (s *SettingService) GetOpenAICodexUserAgent(ctx context.Context) string {
	return s.GatewaySettings().GetOpenAICodexUserAgent(ctx)
}

// MigrateGrokDefaultTextModel 将历史内置 Grok 4.5 默认值升级为 4.6，保留管理员明确选择的模型。
func (s *SettingService) MigrateGrokDefaultTextModel(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
	defer cancel()

	value, err := s.settingRepo.GetValue(dbCtx, SettingKeyGrokDefaultTextModel)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil
		}
		return fmt.Errorf("get %s setting: %w", SettingKeyGrokDefaultTextModel, err)
	}
	if strings.TrimSpace(value) != "grok-4.5" {
		return nil
	}
	if err := s.settingRepo.Set(dbCtx, SettingKeyGrokDefaultTextModel, "grok-4.6"); err != nil {
		return fmt.Errorf("set %s setting: %w", SettingKeyGrokDefaultTextModel, err)
	}
	return nil
}

// IsBackendModeEnabled 委托唯一网关准入运行实例。
func (s *SettingService) IsBackendModeEnabled(ctx context.Context) bool {
	return s.backendMode.Enabled(ctx)
}

// GetOpenAITTFTMode 返回 Responses first_token_ms 的统计口径。
func (s *SettingService) GetOpenAITTFTMode(ctx context.Context) string {
	return s.GatewaySettings().GetOpenAITTFTMode(ctx)
}

// GetGatewayForwardingSettings returns cached gateway forwarding settings.
// Uses in-process atomic.Value cache with 60s TTL, zero-lock hot path.
// Returns (fingerprintUnification, metadataPassthrough, cchSigning).
func (s *SettingService) GetGatewayForwardingSettings(ctx context.Context) (fingerprintUnification, metadataPassthrough, cchSigning bool) {
	return s.GatewaySettings().GetGatewayForwardingSettings(ctx)
}

// IsAnthropicCacheTTL1hInjectionEnabled 检查是否对 Anthropic OAuth/SetupToken 请求体注入 1h cache_control ttl。
func (s *SettingService) IsAnthropicCacheTTL1hInjectionEnabled(ctx context.Context) bool {
	return s.GatewaySettings().IsAnthropicCacheTTL1hInjectionEnabled(ctx)
}

// IsRewriteMessageCacheControlEnabled 检查是否启用 messages cache_control 改写。
func (s *SettingService) IsRewriteMessageCacheControlEnabled(ctx context.Context) bool {
	return s.GatewaySettings().IsRewriteMessageCacheControlEnabled(ctx)
}

// IsClientDatelineNormalizationEnabled 检查是否启用 Anthropic OAuth/SetupToken 请求体
// 的客户端 dateline 归一化。默认开启。
func (s *SettingService) IsClientDatelineNormalizationEnabled(ctx context.Context) bool {
	return s.GatewaySettings().IsClientDatelineNormalizationEnabled(ctx)
}

// GetClaudeOAuthSystemPromptInjectionSettings 返回 Claude OAuth mimic system block 注入配置。
func (s *SettingService) GetClaudeOAuthSystemPromptInjectionSettings(ctx context.Context) (enabled bool, prompt string, blocks string) {
	return s.GatewaySettings().GetClaudeOAuthSystemPromptInjectionSettings(ctx)
}

// GetClaudeCodeVersionBounds 获取 Claude Code 版本号上下限要求
// 使用进程内 atomic.Value 缓存，60 秒 TTL，热路径零锁开销
// singleflight 防止缓存过期时 thundering herd
// 返回空字符串表示不做对应方向的版本检查
func (s *SettingService) GetClaudeCodeVersionBounds(ctx context.Context) (min, max string) {
	return s.GatewaySettings().GetClaudeCodeVersionBounds(ctx)
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
	return s.QuotaSettings().GetOpenAIQuotaAutoPauseSettings(ctx)
}

// WarmOpenAIQuotaAutoPauseSettings 同步加载配额自动暂停设置到内存缓存。
// 应用启动时可用它让首个请求命中预热缓存；测试也可用它在构造服务后获得确定性读取。
func (s *SettingService) WarmOpenAIQuotaAutoPauseSettings(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings {
	return s.QuotaSettings().WarmOpenAIQuotaAutoPauseSettings(ctx)
}

// refreshOpenAIQuotaAutoPauseSettings 从 DB 读取最新设置并写入内存缓存。
// 出错时写入旧值（如果没有旧缓存则写入零默认值）并使用更短的错误 TTL，
// 让下一次刷新更快到来。该方法始终使用自带超时的 context，
// 避免刷新延迟受调用方影响而变得不可预测。

// SetOpenAIQuotaAutoPauseSettings 将给定设置直接写入内存缓存。
// 设置写入路径会调用它，让下一次读取立即反映新值，不必等待后台刷新。
func (s *SettingService) SetOpenAIQuotaAutoPauseSettings(settings OpsOpenAIAccountQuotaAutoPauseSettings) {
	s.QuotaSettings().SetOpenAIQuotaAutoPauseSettings(settings)
}
