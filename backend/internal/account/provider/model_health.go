package provider

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// ModelHealth 只组合本次模型观测，别名规则由 app 投影，不创建另一份目录缓存。
type ModelHealth struct {
	Health       *account.HealthService
	CodexRules   openai.CodexModelRules
	IsImageModel func(string) bool
}

// LimitKey 保留最终上游模型的一跳规则，不二次应用 OpenAI/Grok 账号映射。
func (s *ModelHealth) LimitKey(value *account.Record, requested string, thinking *bool) string {
	key := strings.TrimSpace(requested)
	if value == nil || key == "" {
		return key
	}
	if value.Platform == account.PlatformAntigravity {
		mapped := FinalAntigravityModel(value, key, thinking)
		if mapped = strings.TrimSpace(mapped); mapped != "" {
			return mapped
		}
		return key
	}
	if value.Platform == account.PlatformOpenAI || value.Platform == account.PlatformGrok {
		normalized := key
		if value.IsGrok() {
			normalized = grok.NormalizeModelID(key)
		} else if value.UsesOpenAICodexProtocol() {
			normalized = openai.NormalizeCodexModel(key, s.CodexRules)
		}
		if normalized = strings.TrimSpace(normalized); normalized != "" {
			return normalized
		}
		return key
	}
	mapped, _ := account.ResolveMappedModel(value.Platform, account.ResolveModelMapping(value, ModelDefaults()), key)
	if mapped = strings.TrimSpace(mapped); mapped != "" {
		return mapped
	}
	return key
}

// Observe 把平台识别结果和当次端点意图交给健康核心，不写共享账号缓存。
func (s *ModelHealth) Observe(ctx context.Context, value *account.Record, model string, status int, body []byte, thinking *bool, imagesEndpoint bool) bool {
	key := s.LimitKey(value, model, thinking)
	image := s.IsImageModel != nil && (s.IsImageModel(model) || s.IsImageModel(key))
	return s.Health.ApplyModelUnavailable(ctx, value, status, account.ModelFailureObservation{NotFound: upstream.IsModelNotFoundError(status, body), CodexPlanGated: openai.IsCodexPlanGatedModelError(status, body), ModelKey: key, ImageModel: image, ImagesEndpoint: imagesEndpoint})
}

// ObserveSparkRateLimit 显式接收当次 thinking，不从旧业务 Context 读取模型意图。
func (s *ModelHealth) ObserveSparkRateLimit(ctx context.Context, value *account.Record, model string, status int, headers http.Header, body []byte, thinking *bool) bool {
	key := openai.NormalizeCodexModel(s.LimitKey(value, model, thinking), s.CodexRules)
	return s.Health.ApplySparkRateLimit(ctx, value, key, status, openai.IsCodexSparkModel(model, s.CodexRules), func() (account.OpenAI429Disposition, *time.Time) { return ClassifyOpenAI429(headers, body) })
}
