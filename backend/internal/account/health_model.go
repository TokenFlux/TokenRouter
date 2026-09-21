package account

import (
	"context"
	"time"
)

// ModelFailureObservation 不携带请求 Context；端点和最终模型由当前尝试显式投影。
type ModelFailureObservation struct {
	NotFound       bool
	CodexPlanGated bool
	ModelKey       string
	ImageModel     bool
	ImagesEndpoint bool
}

const (
	ModelNotFoundCooldown       = 30 * time.Minute
	ModelNotFoundReason         = "upstream_404_model_not_found"
	CodexPlanGatedModelCooldown = 30 * time.Minute
	CodexPlanGatedModelReason   = "upstream_400_codex_plan_gated_model"
)

// ApplyModelUnavailable 只暂停当前账号与模型组合，保留池模式和错误码策略边界。
func (s *HealthService) ApplyModelUnavailable(ctx context.Context, value *Record, status int, observation ModelFailureObservation) bool {
	if s == nil || s.accountRepo == nil || value == nil || value.IsPoolMode() || !value.ShouldHandleErrorCode(status) {
		return false
	}
	var cooldown time.Duration
	var reason string
	switch {
	case observation.NotFound:
		cooldown, reason = ModelNotFoundCooldown, ModelNotFoundReason
	case value.IsOpenAIOAuthLike() && observation.CodexPlanGated:
		cooldown, reason = CodexPlanGatedModelCooldown, CodexPlanGatedModelReason
	default:
		return false
	}
	if observation.ModelKey == "" {
		return false
	}
	// 文本端点对图片模型的套餐拒绝不冷却专用 Images 能力。
	if reason == CodexPlanGatedModelReason && !observation.ImagesEndpoint && observation.ImageModel {
		return true
	}
	reset := s.options.Now().Add(cooldown)
	if err := s.accountRepo.SetModelRateLimit(ctx, value.ID, observation.ModelKey, reset, reason); err != nil {
		s.options.Warn("upstream_model_not_found_set_model_rate_limit_failed", "account_id", value.ID, "model", observation.ModelKey, "reason", reason, "error", err)
		return true
	}
	s.options.Info("upstream_model_not_found_model_rate_limited", "account_id", value.ID, "model", observation.ModelKey, "reason", reason, "reset_at", reset)
	return true
}
