package provider

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// ImageToolCooldown 只处理已经确认的图片工具冷却，调用方保留触发资格。
type ImageToolCooldown struct {
	Store interface {
		SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error
	}
	Settings func(context.Context) (*account.OpenAIImagesOAuthUnavailableCooldownSettings, error)
}

func (s *ImageToolCooldown) Apply(ctx context.Context, value *account.Record) {
	if s == nil || s.Store == nil || value == nil || value.Platform != capability.PlatformOpenAI {
		return
	}
	stateCtx, cancel := AccountStateContext(ctx)
	defer cancel()
	cooldown := openai.OpenAIImagesOAuthUnavailableDefaultCooldown
	if s.Settings != nil {
		settings, err := s.Settings(stateCtx)
		if err != nil {
			logging.LegacyPrintf("service.openai_gateway", "[OpenAI] Images OAuth tool cooldown setting read failed error=%v", err)
		} else {
			cooldown = time.Duration(settings.CooldownMinutes) * time.Minute
		}
	}
	resetAt := time.Now().Add(cooldown)
	if err := s.Store.SetModelRateLimit(stateCtx, value.ID, account.OpenAIImageGenerationRateLimitKey, resetAt, openai.OpenAIImagesOAuthUnavailableReason); err != nil {
		logging.LegacyPrintf("service.openai_gateway", "[OpenAI] Images OAuth tool cooldown write failed account_id=%d error=%v", value.ID, err)
		return
	}
	logging.LegacyPrintf("service.openai_gateway", "[OpenAI] Images OAuth tool unavailable account_id=%d reset_in=%s", value.ID, time.Until(resetAt).Truncate(time.Second))
}
