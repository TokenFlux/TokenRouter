package provider

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// GeminiErrorObserver 保留官方日配额与第三方冷却的区别，只处理本次账号健康观测。
type GeminiErrorObserver struct {
	Other          *UpstreamHealth
	Precheck       *account.GeminiPrecheck
	SetRateLimited func(context.Context, int64, time.Time) error
	ResetTime      func([]byte) *int64
	DailyReset     func() *int64
}

func (s *GeminiErrorObserver) Observe(ctx context.Context, value *account.Record, statusCode int, headers http.Header, body []byte, observation HealthObservation) *account.Record {
	// 遵守自定义错误码策略：未命中则跳过所有限流处理
	if !value.ShouldHandleErrorCode(statusCode) {
		return nil
	}
	if s.Other != nil && (statusCode == 401 || statusCode == 403 || statusCode == 529) {
		s.Other.ApplyUpstreamError(ctx, value, observation)
		return value
	}
	if statusCode != 429 {
		return nil
	}
	// 池模式账号保留在上游账号池中，由请求级重试或切号消化 429；
	// 管理员显式配置的自定义错误策略优先，命中时仍允许写入账号状态。
	if value.IsPoolMode() && !value.IsCustomErrorCodesEnabled() {
		return nil
	}

	oauthType := value.GeminiOAuthType()
	tierID := value.GeminiTierID()
	projectID := strings.TrimSpace(value.GetCredential("project_id"))
	isCodeAssist := value.IsGeminiCodeAssist()

	if value.IsGeminiThirdPartyProvider() {
		// 第三方兼容端点不得解析 Google 官方日配额文案，始终使用通用 429 冷却。
		cooldown := 5 * time.Minute
		if s.Precheck != nil {
			cooldown = s.Precheck.GeminiCooldown(ctx, value)
		}
		ra := time.Now().Add(cooldown)
		_ = s.SetRateLimited(ctx, value.ID, ra)
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (third-party API Key) rate limited, cooldown=%v", value.ID, time.Until(ra).Truncate(time.Second))
		return nil
	}

	resetAt := s.ResetTime(body)
	if resetAt == nil {
		// 根据账号类型使用不同的默认重置时间
		var ra time.Time
		if isCodeAssist || oauthType == "google_one" {
			// Gemini CLI / Google One：按层级回退冷却时间
			cooldown := account.GeminiCooldownForTier(tierID)
			if s.Precheck != nil {
				cooldown = s.Precheck.GeminiCooldown(ctx, value)
			}
			ra = time.Now().Add(cooldown)
			if isCodeAssist {
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (Code Assist, tier=%s, project=%s) rate limited, cooldown=%v", value.ID, tierID, projectID, time.Until(ra).Truncate(time.Second))
			} else {
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (Google One OAuth, tier=%s, project=%s) rate limited, cooldown=%v", value.ID, tierID, projectID, time.Until(ra).Truncate(time.Second))
			}
		} else {
			// API Key / AI Studio OAuth: PST 午夜
			if ts := s.DailyReset(); ts != nil {
				ra = time.Unix(*ts, 0)
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (API Key/AI Studio, type=%s) rate limited, reset at PST midnight (%v)", value.ID, value.Type, ra)
			} else {
				// 兜底：5 分钟
				ra = time.Now().Add(5 * time.Minute)
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d rate limited, fallback to 5min", value.ID)
			}
		}
		_ = s.SetRateLimited(ctx, value.ID, ra)
		return nil
	}

	// 使用解析到的重置时间
	resetTime := time.Unix(*resetAt, 0)
	_ = s.SetRateLimited(ctx, value.ID, resetTime)
	logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d rate limited until %v (oauth_type=%s, tier=%s)",
		value.ID, resetTime, oauthType, tierID)
	return nil
}
