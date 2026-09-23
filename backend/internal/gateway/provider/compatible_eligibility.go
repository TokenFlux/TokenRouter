package provider

import (
	"context"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// AllowsCompatibleCompact 保留 Grok 与 OpenAI 的 Compact 资格差异。
func AllowsCompatibleCompact(account *ExecutionAccount) bool {
	return account != nil && (account.View().IsGrok() || account.View().AllowsOpenAICompact())
}

// CompatibleAccountEligible 判断 OpenAI 兼容账号是否满足本次请求的调度条件。
// 检查内容包括：平台匹配、账号可用性、quota 自动暂停、spark 路由限制、模型支持及端点能力。
//
// 注意：对 spark 影子账号，调用方还须额外调用 parentHealthyForShadow(account, lookup)
// 检查母账号凭据可用性；该检查未内置于本函数，以避免注入 DB 依赖。
func CompatibleAccountEligible(ctx context.Context, account *ExecutionAccount, platform string, requestedModel string, requireCompact bool, requiredCapability accountcore.OpenAIEndpointCapability) bool {
	return CompatibleEligibilityReason(ctx, account, platform, requestedModel, requireCompact, requiredCapability) == ""
}

// CompatibleEligibilityReason 在保留旧布尔判定的同时返回首个拦截原因。
// 负载批处理只使用该原因生成服务端无账号诊断，不改变实际准入行为。
// @project-doc docs/architecture/account_scheduling_and_cache.md#advanced_scheduler_selection
func CompatibleEligibilityReason(ctx context.Context, account *ExecutionAccount, platform string, requestedModel string, requireCompact bool, requiredCapability accountcore.OpenAIEndpointCapability) string {
	platform = routing.NormalizeOpenAICompatiblePlatform(platform)
	if account == nil {
		return "account_nil"
	}
	if account.Record.Platform != platform || !account.View().IsOpenAICompatible() {
		return "platform_mismatch"
	}
	if !ExecutionModelPolicy(account).Schedulable(ctx, requestedModel) {
		if account.View().IsSchedulable() {
			return "model_rate_limited"
		}
		return "not_schedulable"
	}
	if account.View().IsOpenAI() {
		if paused, reason := OpenAIQuotaPause(ctx, account); paused {

			slog.Debug("account_auto_paused_by_quota",
				"account_id", account.Record.ID,
				"window", reason.Window,
				"threshold", reason.Threshold,
				"utilization", reason.Utilization,
			)
			if reason.Window != "" {
				return "quota_auto_pause_" + reason.Window
			}
			return "quota_auto_pause"
		}
	}
	if account.View().IsGrok() {
		if paused, reason := GrokQuotaPause(account); paused {
			slog.Debug("grok_account_auto_paused_by_quota",
				"account_id", account.Record.ID,
				"window", reason.Window,
				"threshold", reason.Threshold,
				"utilization", reason.Utilization,
			)
			if reason.Window != "" {
				return "quota_auto_pause_" + reason.Window
			}
			return "quota_auto_pause"
		}
	}
	if !ExecutionModelPolicy(account).SupportsCompatibleRouting(ctx, requestedModel) {
		return "model_not_supported"
	}
	if !SupportsRequestCapability(ctx, account, requiredCapability) {
		if account.View().IsGrok() && requiredCapability == accountcore.OpenAIEndpointCapabilityGrokMediaGeneration {
			_, reason := accountcore.GrokMediaGenerationEligibility(ExecutionRecord(account), accountprovider.GrokTierRules())
			slog.Debug("grok_media_account_ineligible", "account_id", account.Record.ID, "reason", reason)
		}
		return "capability_mismatch"
	}
	if requireCompact && !AllowsCompatibleCompact(account) {
		return "compact_unsupported"
	}
	return ""
}

// OpenAIQuotaPause 使用当前时刻和本次请求阈值读取账号派生状态。
func OpenAIQuotaPause(ctx context.Context, account *ExecutionAccount) (bool, accountcore.QuotaAutoPauseDecision) {
	return evaluateOpenAIQuotaPause(ctx, account, time.Now())
}

// evaluateOpenAIQuotaPause 只投影执行目标与请求阈值，复用账号模块的唯一裁决。
func evaluateOpenAIQuotaPause(ctx context.Context, v *ExecutionAccount, now time.Time) (bool, accountcore.QuotaAutoPauseDecision) {
	if v == nil {
		return false, accountcore.QuotaAutoPauseDecision{}
	}
	return accountcore.EvaluateQuotaAutoPause(v.Record.Platform, v.Record.Extra, QuotaAutoPauseSettings(ctx), now)
}

// WithQuotaAutoPauseSettings 把 OpenAI 配额自动暂停全局设置放进 context，
// 让调度、展示和容量统计复用完全一致的阈值解析逻辑。
func WithQuotaAutoPauseSettings(ctx context.Context, settings accountcore.QuotaAutoPauseSettings) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	hints := requeststate.ExecutionHintsFromContext(ctx)
	hints.QuotaAutoPauseThreshold5h = settings.DefaultThreshold5h
	hints.QuotaAutoPauseThreshold7d = settings.DefaultThreshold7d
	return requeststate.WithExecutionHints(ctx, hints)
}

// QuotaAutoPauseSettings 读取选择和固定账号复核共享的请求快照。
func QuotaAutoPauseSettings(ctx context.Context) accountcore.QuotaAutoPauseSettings {
	hints := requeststate.ExecutionHintsFromContext(ctx)
	return accountcore.QuotaAutoPauseSettings{DefaultThreshold5h: hints.QuotaAutoPauseThreshold5h, DefaultThreshold7d: hints.QuotaAutoPauseThreshold7d}
}

// GrokQuotaPause 只投影执行目标，窗口规则由 account 唯一拥有。
func GrokQuotaPause(value *ExecutionAccount) (bool, accountcore.QuotaAutoPauseDecision) {
	return accountcore.EvaluateGrokQuotaAutoPause(ExecutionRecord(value), time.Now)
}

// SupportsRequestCapability 保留 WS、Compact 和普通 HTTP 的原协议资格顺序。
func SupportsRequestCapability(ctx context.Context, account *ExecutionAccount, capability accountcore.OpenAIEndpointCapability) bool {
	if account == nil {
		return false
	}
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	if source == protocolcore.ProtocolResponsesWebSocket || source == protocolcore.ProtocolResponsesCompact {
		if capability == accountcore.OpenAIEndpointCapabilityTextGeneration || capability == accountcore.OpenAIEndpointCapabilityResponses {
			return ExecutionModelPolicy(account).AllowsProtocol(ctx)
		}
		if capability == accountcore.OpenAIEndpointCapabilityRemoteCompactionV2 {
			return ExecutionModelPolicy(account).AllowsProtocol(ctx) && account.View().AllowsOpenAINativeCompactionV2()
		}
	}
	return accountprovider.SupportsOpenAIEndpoint(ExecutionProtocolRecord(account), capability)
}
