package provider

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// model_health.go resolves failed model identities and forwards
// unavailable-model and Spark limit observations to health services. OpenAI
// and Grok inputs already name the mapped target, while Antigravity can still
// resolve a request model through its thinking-aware rules; other platforms
// use the provider mapping.
//
// LimitKey retains the trimmed input when resolution produces no usable
// target, preventing a blank cooldown key from replacing a known request
// identity. Observe passes that key with model-not-found and plan-gating
// classifications to the health service, and checks image recognition against
// both input and resolved names. Spark rate-limit handling uses the same key
// resolution with the current attempt's thinking setting.

// ModelHealth translates model identities and platform errors into provider
// health observations.
type ModelHealth struct {
	// ImageOutput supplies final-route output evidence for cooldown classification.
	// Missing readers leave explicit image endpoint observations as the available evidence.
	ImageOutput func(context.Context, *provider.Record, string) bool
	Health      *provider.HealthService // provider health rule service
}

// LimitKey resolves the model identity used by provider cooldown state without
// an explicit effort override, delegating to LimitKeyForEffort.
func (s *ModelHealth) LimitKey(value *provider.Record, requested string, thinking *bool) string {
	return s.LimitKeyForEffort(value, requested, "", thinking)
}

// LimitKeyForEffort resolves the model identity used by provider cooldown state
// with an optional reasoning effort. A nil record or empty trimmed input returns
// that trimmed input immediately. OpenAI and Grok callers supply already mapped
// upstream names, with Grok applying its platform normalization.
//
// Antigravity resolution includes the attempt's effort and thinking settings,
// and remaining platforms resolve one effort-aware provider mapping. If
// resolution yields an empty result, the original trimmed name remains the key,
// so model health does not lose the identity that the caller observed.
func (s *ModelHealth) LimitKeyForEffort(value *provider.Record, requested, effort string, thinking *bool) string {
	key := strings.TrimSpace(requested)
	if value == nil || key == "" {
		return key
	}
	if value.Platform == provider.PlatformAntigravity {
		mapped := FinalAntigravityModelForEffort(value, key, effort, thinking)
		if mapped = strings.TrimSpace(mapped); mapped != "" {
			return mapped
		}
		// An empty Antigravity target leaves the observed input available as the
		// cooldown identity, including the name supplied for this thinking setting.
		return key
	}
	// These callers have already applied provider mapping to the upstream model.
	// Reapplying an alias chain would associate the observed failure with another
	// model's cooldown.
	if value.Platform == provider.PlatformOpenAI || value.Platform == provider.PlatformGrok {
		normalized := key
		if value.IsGrok() {
			normalized = grok.NormalizeModelID(key)
		}
		if normalized = strings.TrimSpace(normalized); normalized != "" {
			return normalized
		}
		// An empty normalization cannot replace the failed upstream identity. Keep
		// that input for health state associated with this attempt's model.
		return key
	}
	mapped, _, _ := provider.ResolveMappedModelForEffort(
		provider.ResolveModelMapping(value, ModelDefaults()),
		provider.ResolveEffortModelMapping(value),
		key,
		effort,
	)
	if mapped = strings.TrimSpace(mapped); mapped != "" {
		return mapped
	}
	// A blank configured target supplies no usable cooldown name. Retain the
	// observed request identity so model state can still reference this failure.
	return key
}

// Observe classifies a model failure without an explicit effort override and
// returns whether Health handled it.
func (s *ModelHealth) Observe(ctx context.Context, value *provider.Record, model string, status int, body []byte, thinking *bool, imagesEndpoint bool) bool {
	return s.ObserveForEffort(ctx, value, model, "", status, body, thinking, imagesEndpoint)
}

// ObserveForEffort classifies a model failure under an optional reasoning
// effort and returns whether Health handled it. Callers provide the current
// attempt's model, effort, and endpoint intent; image classification uses
// endpoint intent or resolved output evidence. Health must be configured
// before observations are submitted.
func (s *ModelHealth) ObserveForEffort(ctx context.Context, value *provider.Record, model, effort string, status int, body []byte, thinking *bool, imagesEndpoint bool) bool {
	key := s.LimitKeyForEffort(value, model, effort, thinking)
	image := imagesEndpoint
	if !image && s.ImageOutput != nil {
		image = s.ImageOutput(ctx, value, key)
	}
	return s.Health.ApplyModelUnavailable(ctx, value, status, provider.ModelFailureObservation{NotFound: upstream.IsModelNotFoundError(status, body), CodexPlanGated: openai.IsCodexPlanGatedModelError(status, body), ModelKey: key, ImageModel: image, ImagesEndpoint: imagesEndpoint})
}

// ObserveSparkRateLimit passes the resolved model key without an explicit
// effort override and Spark classification to Health.
func (s *ModelHealth) ObserveSparkRateLimit(ctx context.Context, value *provider.Record, model string, status int, headers http.Header, body []byte, thinking *bool) bool {
	return s.ObserveSparkRateLimitForEffort(ctx, value, model, "", status, headers, body, thinking)
}

// ObserveSparkRateLimitForEffort passes the effort-aware resolved model key
// and Spark classification to Health and returns whether it handled the limit.
// Callers supply this attempt's model, effort, thinking setting, headers, and
// body for reset classification.
func (s *ModelHealth) ObserveSparkRateLimitForEffort(ctx context.Context, value *provider.Record, model, effort string, status int, headers http.Header, body []byte, thinking *bool) bool {
	key := s.LimitKeyForEffort(value, model, effort, thinking)
	return s.Health.ApplySparkRateLimit(ctx, value, key, status, openai.IsCodexSparkModel(model), func() (provider.OpenAI429Disposition, *time.Time) { return ClassifyOpenAI429(headers, body) })
}
