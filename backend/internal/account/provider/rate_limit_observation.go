package provider

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

	openaiupstream "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	anthropicupstream "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// Observe429 处理429限流错误
// 解析响应头获取重置时间，标记账号为限流状态
func (s *RateLimitObserver) Observe429(ctx context.Context, account *accountcore.Record, headers http.Header, responseBody []byte) {
	// Spark 影子：限流/熔断状态 100% 由 QueryUsage(/wham/usage body 的 codex_bengalfox)驱动。
	// /responses 的 429 携带的 x-codex-*/usage_limit_reached 是 global codex 道(plan/spec §8),
	// 套到影子会把 spark 误耦合到 global 窗口——即便 spark 仍有配额也会被冷却到 global reset,
	// 单影子场景直接变成无可用账号(外审第8轮 P1)。整段跳过;影子的 codex_* 仅由 account_usage 的
	// QueryUsage→persistOpenAICodexProbeSnapshot 维护,枯竭由调度守卫处理。
	if account.IsShadow() {
		return
	}
	if account.Platform == capability.PlatformOpenAI && account.IsOpenAIOAuthLike() {
		if s.RetryOpenAI != nil && s.RetryOpenAI(account, headers, responseBody) {
			return
		}
	}
	// 国产供应商（kimi/zhipu/deepseek）的 429 走专用可恢复路径：余额不足 → 临时停调，
	// Coding Plan 窗口耗尽 → 冷却到快照重置点。未命中则继续默认 429 逻辑。
	if account.IsCNProvider() {
		if s.observeCNQuota(ctx, account, responseBody) {
			return
		}
	}
	// 1. OpenAI 平台：优先尝试解析 x-codex-* 响应头（用于 rate_limit_exceeded）
	if account.Platform == capability.PlatformOpenAI {
		accountcore.PersistOpenAIObservedPlan(ctx, s.Plans, account, openaiupstream.ParseUsageLimitPlanType(responseBody), slog.Info, slog.Warn)
		s.PersistCodexSnapshot(ctx, account, headers)
		if resetAt := accountcore.OpenAI429ResetTime(openaiupstream.ParseCodexRateLimitHeaders(headers), time.Now, slog.Info); resetAt != nil {
			if !s.Health.ApplyObservedRateLimit(ctx, account, *resetAt) {
				return
			}
			slog.Info("openai_account_rate_limited", "account_id", account.ID, "reset_at", *resetAt)
			return
		}
	}

	// 2. Anthropic 平台：尝试解析 per-window 头（5h / 7d），选择实际触发的窗口
	if result := anthropicupstream.CalculateRateLimitReset(headers, slog.Info); result != nil {
		if !s.Health.ApplyObservedRateLimit(ctx, account, result.ResetAt) {
			return
		}

		// 更新 session window：优先使用 5h-reset 头精确计算，否则从 resetAt 反推
		windowEnd := result.ResetAt
		if result.FiveHourReset != nil {
			windowEnd = *result.FiveHourReset
		}
		s.Health.UpdateRejectedSessionWindow(ctx, account, windowEnd)

		slog.Info("anthropic_account_rate_limited", "account_id", account.ID, "reset_at", result.ResetAt, "reset_in", time.Until(result.ResetAt).Truncate(time.Second))
		return
	}

	// 3. 尝试从响应头解析重置时间（Anthropic 聚合头，向后兼容）
	resetTimestamp := headers.Get("anthropic-ratelimit-unified-reset")

	// 4. 如果响应头没有，尝试从响应体解析（OpenAI usage_limit_reached, Gemini）
	if resetTimestamp == "" {
		switch account.Platform {
		case capability.PlatformOpenAI:
			// 尝试解析 OpenAI 的 usage_limit_reached 错误
			if resetAt := openaiupstream.ParseUsageLimitResetTime(responseBody, time.Now); resetAt != nil {
				resetTime := time.Unix(*resetAt, 0)
				if !s.Health.ApplyObservedRateLimit(ctx, account, resetTime) {
					return
				}
				slog.Info("account_rate_limited", "account_id", account.ID, "platform", account.Platform, "reset_at", resetTime, "reset_in", time.Until(resetTime).Truncate(time.Second))
				return
			}
		case capability.PlatformGemini, capability.PlatformAntigravity:
			// 尝试解析 Gemini 格式（用于其他平台）
			if resetAt := gemini.ParseGeminiRateLimitResetTime(responseBody, s.NextGeminiDaily); resetAt != nil {
				resetTime := time.Unix(*resetAt, 0)
				if !s.Health.ApplyObservedRateLimit(ctx, account, resetTime) {
					return
				}
				slog.Info("account_rate_limited", "account_id", account.ID, "platform", account.Platform, "reset_at", resetTime, "reset_in", time.Until(resetTime).Truncate(time.Second))
				return
			}
		}

		// Anthropic 平台：没有限流重置时间的 429 可能是非真实限流（如 Extra usage required），
		// 不适合按 5h/7d 窗口长时间封禁；但完全不标记会导致账号永不冷却，
		// 调度器会让每个请求反复命中同一批持续返回 429 的账号并耗尽 failover 预算。
		// 因此使用可配置的秒级兜底回避，管理端仍可调整或关闭。
		if account.Platform == capability.PlatformAnthropic {
			slog.Warn("rate_limit_429_no_reset_time",
				"account_id", account.ID,
				"platform", account.Platform,
				"reason", "no rate limit reset time in headers, likely not a real rate limit")
			s.Health.Apply429Fallback(ctx, account, "anthropic_no_reset_time")
			return
		}

		// 其他平台：没有重置时间，使用可配置的秒级默认回避，避免误伤长时间不可调度。
		s.Health.Apply429Fallback(ctx, account, "no_reset_time")
		return
	}

	// 解析Unix时间戳
	ts, err := strconv.ParseInt(resetTimestamp, 10, 64)
	if err != nil {
		slog.Warn("rate_limit_reset_parse_failed", "reset_timestamp", resetTimestamp, "error", err)
		s.Health.Apply429Fallback(ctx, account, "reset_parse_failed")
		return
	}

	resetAt := time.Unix(ts, 0)

	// 标记限流状态
	if !s.Health.ApplyObservedRateLimit(ctx, account, resetAt) {
		return
	}

	// 根据重置时间反推5h窗口
	windowEnd := resetAt
	s.Health.UpdateRejectedSessionWindow(ctx, account, windowEnd)

	slog.Info("account_rate_limited", "account_id", account.ID, "reset_at", resetAt)
}

// PersistCodexSnapshot 先执行原影子/空头短路，再投影观测给账号核心。
func (s *RateLimitObserver) PersistCodexSnapshot(ctx context.Context, value *accountcore.Record, headers http.Header) {
	if s == nil || s.Health == nil || value == nil || headers == nil || value.IsShadow() {
		return
	}
	s.Health.PersistCodexObservation(ctx, value, openaiupstream.ParseCodexRateLimitHeaders(headers))
}

// RateLimitObserver 组合供应商限流观测与原生健康写入；自身不持有缓存或存储客户端。
type RateLimitObserver struct {
	Health          *accountcore.HealthService
	Plans           accountcore.OpenAIPlanWriter
	RetryOpenAI     func(*accountcore.Record, http.Header, []byte) bool
	NextGeminiDaily func() *int64
}

func (s *RateLimitObserver) observeCNQuota(ctx context.Context, value *accountcore.Record, body []byte) bool {
	if !value.IsCNProvider() {
		return false
	}
	if upstream.CNResponseIndicatesInsufficientBalance(body) {
		s.Health.ApplyCNInsufficientBalance(ctx, value, upstream.ExtractErrorMessage(body))
		return true
	}
	return s.Health.ApplyCNQuotaSnapshotCooldown(ctx, value)
}
