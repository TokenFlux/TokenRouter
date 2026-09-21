package account

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// ApplyImageRateLimit 将 OpenAI 生图限流写入能力维度限流，而不是封禁整个账号。
func (s *HealthService) ApplyImageRateLimit(ctx context.Context, account *Record, statusCode int, observe func() (bool, time.Time)) bool {
	if s == nil || account == nil || s.accountRepo == nil {
		return false
	}
	if account.Platform != capability.PlatformOpenAI {
		return false
	}
	// 池模式由上游账号池负责能力切换，不能写入本地生图能力限流。
	if account.IsPoolMode() {
		return false
	}
	if !account.ShouldHandleErrorCode(statusCode) {
		s.options.Info("openai_image_rate_limit_skipped_by_error_code_policy", "account_id", account.ID, "status_code", statusCode)
		return false
	}
	matched, resetAt := observe()
	if !matched {
		return false
	}

	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, OpenAIImageGenerationRateLimitKey, resetAt, OpenAIImageRateLimitReason); err != nil {
		s.options.Warn("openai_image_rate_limit_set_model_rate_limit_failed", "account_id", account.ID, "scope", OpenAIImageGenerationRateLimitKey, "error", err)
		return true
	}
	s.options.Info("openai_image_rate_limited", "account_id", account.ID, "scope", OpenAIImageGenerationRateLimitKey, "reset_at", resetAt, "reset_in", time.Until(resetAt).Truncate(time.Second))
	return true
}

// ApplyImageCapabilityLoss 在上游明确拒绝图片工具时暂时冷却账号的图片调度。
func (s *HealthService) ApplyImageCapabilityLoss(ctx context.Context, account *Record, statusCode int, capabilityLost bool) bool {
	if s == nil || account == nil || s.accountRepo == nil {
		return false
	}
	if account.Platform != capability.PlatformOpenAI {
		return false
	}
	if !account.ShouldHandleErrorCode(statusCode) {
		s.options.Info("openai_image_capability_loss_skipped_by_error_code_policy", "account_id", account.ID, "status_code", statusCode)
		return false
	}
	if !capabilityLost {
		return false
	}

	resetAt := s.options.Now().Add(OpenAIImageCapabilityLossCooldown)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, OpenAIImageGenerationRateLimitKey, resetAt, OpenAIImageCapabilityLossReason); err != nil {
		s.options.Warn("openai_image_capability_loss_set_model_rate_limit_failed", "account_id", account.ID, "scope", OpenAIImageGenerationRateLimitKey, "error", err)
		return true
	}
	s.options.Info("openai_image_capability_lost", "account_id", account.ID, "scope", OpenAIImageGenerationRateLimitKey, "reset_at", resetAt, "reset_in", time.Until(resetAt).Truncate(time.Second))
	return true
}

// 图片能力状态继续沿用原缓存范围、原因和冷却时长。
const (
	OpenAIImageRateLimitReason        = "openai_image_rate_limited"
	OpenAIImageCapabilityLossReason   = "openai_image_capability_lost"
	OpenAIImageCapabilityLossCooldown = 30 * time.Minute
	OpenAIImageGenerationRateLimitKey = "openai:image_generation"
)
