package provider

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// antigravity_error_observation.go classifies Antigravity failures into model
// retry, model switching, and provider health actions. Antigravity failures
// can name a limited model, omit that identity, or describe shared model
// capacity. Model metadata is interpreted before general provider health: a
// short model delay asks the caller to wait, a longer delay records model
// windows and requests a switch, and capacity exhaustion stays with the
// platform retry loop.
//
// A generic 429 derives its limit key from the request's final mapped and
// thinking-adjusted name, then uses the normalized request when that
// resolution is empty. Only a missing model key reaches the provider-wide
// writer. This keeps model limits on the same identities consulted by
// scheduling, while unrecognized 503 responses leave ordinary provider health
// unchanged.

// AntigravityModelLimitResult tells retry callers how model-specific handling
// classified a failure.
type AntigravityModelLimitResult struct {
	// Handled identifies a recognized model response. ShouldRetry and WaitDuration
	// describe a short delay; SwitchError requests a switch after a longer model
	// limit. Capacity exhaustion sets Handled with neither action requested.
	Handled      bool                                        // recognized model-limit response
	ShouldRetry  bool                                        // short-delay retry request
	WaitDuration time.Duration                               // retry wait duration
	SwitchError  *antigravity.AntigravityProviderSwitchError // provider-switch request details
}

// AntigravityErrorInput supplies one failed exchange and its provider health
// context.
type AntigravityErrorInput struct {
	Context context.Context // health-write request context
	// Provider is borrowed for synchronous health writes and possible snapshot
	// mutation. The caller supplies a nonnil record and serializes its mutation.
	Provider *provider.Record
	Prefix   string      // diagnostic message prefix
	Status   int         // upstream HTTP status
	Headers  http.Header // observed response headers
	Body     []byte      // observed error bytes
	// RequestedModel and Thinking are resolved together when a generic 429 lacks
	// a usable model identity, so the stored limit uses the forwarded variant.
	RequestedModel   string            // incoming model spelling
	OtherObservation HealthObservation // general health-error observation
	Thinking         *bool             // optional thinking override
	// Sticky labels a resulting switch request. ClearSticky releases the caller
	// binding after a long model limit is attempted, including a failed write.
	Sticky      bool   // sticky attempt classification
	ClearSticky func() // optional binding-release callback
}

// AntigravityErrorObserver translates model windows and general failures into
// provider health writes.
type AntigravityErrorObserver struct {
	// Health writes model windows and can publish the mutated record. Configure it
	// for paths that recognize or derive a model-specific limit.
	Health *provider.AntigravityHealth
	// LogConfig is required for generic 429 handling and returns whether body
	// logging is enabled together with the diagnostic truncation byte limit.
	LogConfig func() (bool, int)
	// TruncateString is called when LogConfig enables body logging. Configure it
	// to apply the byte limit returned by that callback.
	TruncateString func(string, int) string
	// ResetTime is required for generic 429 handling and returns a Unix-second
	// deadline pointer, or nil when its parser supplies no deadline.
	ResetTime func([]byte) *int64
	// DefaultDuration is required for generic 429 handling. Its duration is used
	// when ResetTime returns nil, including for a provider-wide limit.
	DefaultDuration func() time.Duration
	// SetRateLimited is required when a generic 429 produces no model key.
	// Write errors are logged and do not alter the returned retry decision.
	SetRateLimited func(context.Context, int64, time.Time) error
	// Other receives remaining non-429, non-503 failures when configured. A nil
	// value leaves those failures without a general health update here.
	Other *UpstreamHealth
}

// handleModelRateLimit classifies recognized model failures before general health.
// Statuses other than 429 or 503 and responses without a parsed model remain
// unhandled. Shared capacity returns a handled result for platform retries;
// short rate-limit delays return the wait duration, while longer delays attempt
// a model-window write and request a provider switch.
//
// That switch carries the parsed upstream model and the caller's sticky marker.
// The helper clears an available sticky binding even when the model-window
// write fails, so the result represents the handling decision rather than a
// guarantee that persistence succeeded.
func (s *AntigravityErrorObserver) handleModelRateLimit(p *AntigravityErrorInput) *AntigravityModelLimitResult {
	if p.Status != 429 && p.Status != 503 {
		return &AntigravityModelLimitResult{Handled: false}
	}

	info := antigravity.ParseAntigravitySmartRetryInfo(p.Body)
	if info == nil || info.ModelName == "" {
		return &AntigravityModelLimitResult{Handled: false}
	}

	// Shared model capacity is handled by the retry loop across this attempt;
	// recording a provider-specific model window here would change that
	// recovery path.
	if info.IsModelCapacityExhausted {
		log.Printf("%s status=%d model_capacity_exhausted model=%s (not switching provider, retry handled by smart retry)",
			p.Prefix, p.Status, info.ModelName)
		return &AntigravityModelLimitResult{
			Handled: true,
		}
	}

	// Short waits remain within the current attempt; larger delays install windows
	// and release the binding so the caller can seek a usable provider.
	if info.RetryDelay < antigravity.AntigravityRateLimitThreshold {
		logging.LegacyPrintf("service.antigravity_gateway", "%s status=%d model_rate_limit_wait model=%s wait=%v",
			p.Prefix, p.Status, info.ModelName, info.RetryDelay)
		return &AntigravityModelLimitResult{
			Handled:      true,
			ShouldRetry:  true,
			WaitDuration: info.RetryDelay,
		}
	}

	s.setModelRateLimitAndClearSession(p, info)

	return &AntigravityModelLimitResult{
		Handled: true,
		SwitchError: &antigravity.AntigravityProviderSwitchError{
			OriginalProviderID: p.Provider.ID,
			RateLimitedModel:   info.ModelName,
			IsStickySession:    p.Sticky,
		},
	}
}

// setModelRateLimitAndClearSession attempts the parsed model window and
// releases its binding.
// It computes reset from the current time and RetryDelay. ClearSticky runs when
// configured after the write attempt, even when that attempt fails.
func (s *AntigravityErrorObserver) setModelRateLimitAndClearSession(p *AntigravityErrorInput, info *antigravity.AntigravitySmartRetryInfo) {
	resetAt := time.Now().Add(info.RetryDelay)
	logging.LegacyPrintf("service.antigravity_gateway", "%s status=%d model_rate_limited model=%s provider=%d reset_in=%v",
		p.Prefix, p.Status, info.ModelName, p.Provider.ID, info.RetryDelay)

	// Attempt the model window before releasing the sticky binding; a failed write
	// still permits the caller to leave the attempt through its switch decision.
	s.Health.SetAntigravityModelRateLimits(p.Context, s.Health.Store, p.Provider, info.ModelName, p.Prefix, p.Status, resetAt, false)

	if p.ClearSticky != nil {
		p.ClearSticky()
	}
}

// Observe applies model-specific handling and then the remaining provider
// health rules.
// The supplied record and callbacks must support the branches reached by the
// response. Its custom status filter runs first; recognized model metadata can
// return a retry or switch result, and unrecognized 503 responses produce no
// general health change.
//
// A generic 429 attempts the final model window when available, even if persistence
// fails. Mapping or whitelist rejection tries normalized request identity before
// a provider-wide limit. These generic paths return nil, so callers distinguish
// their health observation from an immediate model retry or switch request.
func (s *AntigravityErrorObserver) Observe(p AntigravityErrorInput) *AntigravityModelLimitResult {
	ctx, prefix, value := p.Context, p.Prefix, p.Provider
	statusCode, body, requestedModel := p.Status, p.Body, p.RequestedModel
	// The provider status filter precedes model classification so excluded statuses
	// cannot install model windows or trigger a sticky-session release through
	// this observer.
	if !value.ShouldHandleErrorCode(statusCode) {
		return nil
	}
	result := s.handleModelRateLimit(&p)
	if result.Handled {
		return result
	}

	// Model metadata was already considered. An unrecognized 503 remains available
	// to platform recovery and must not become a general provider-health
	// update here.
	if statusCode == 503 {
		return nil
	}

	// Generic quota metadata supplies timing but no model identity. Derive the key
	// from this attempt so its window can be checked on subsequent requests.
	if statusCode == 429 {
		if logBody, maxBytes := s.LogConfig(); logBody {
			logging.LegacyPrintf("service.antigravity_gateway", "[Antigravity-Debug] 429 response body: %s", s.TruncateString(string(body), maxBytes))
		}

		resetAt := s.ResetTime(body)
		defaultDur := s.DefaultDuration()

		// The requested name may precede mapping and thinking normalization.
		// Record the final model name so scheduling checks the same limit key.
		modelKey := FinalAntigravityModel(value, requestedModel, p.Thinking)
		if strings.TrimSpace(modelKey) == "" {
			// Empty resolution can reflect a denied final whitelist or blank
			// mapped target.
			// Retain a normalized request key for health tracking before
			// considering provider-wide scope.
			modelKey = antigravity.NormalizeAntigravityModelName(requestedModel)
		}
		if modelKey != "" {
			ra := s.resolveResetTime(resetAt, defaultDur)
			if !s.Health.SetAntigravityModelRateLimits(ctx, s.Health.Store, value, modelKey, prefix, statusCode, ra, false) {
				logging.LegacyPrintf("service.antigravity_gateway", "%s status=429 model_rate_limit_set_failed model=%s", prefix, modelKey)
			} else {
				logging.LegacyPrintf("service.antigravity_gateway", "%s status=429 model_rate_limited model=%s provider=%d reset_at=%v reset_in=%v",
					prefix, modelKey, value.ID, ra.Format("15:04:05"), time.Until(ra).Truncate(time.Second))
			}
			return nil
		}

		// Both final and normalized request identities are empty. Use a
		// provider-wide
		// deadline because no model key remains for later scheduler lookups.
		ra := s.resolveResetTime(resetAt, defaultDur)
		logging.LegacyPrintf("service.antigravity_gateway", "%s status=429 rate_limited provider=%d reset_at=%v reset_in=%v (fallback)",
			prefix, value.ID, ra.Format("15:04:05"), time.Until(ra).Truncate(time.Second))
		if err := s.SetRateLimited(ctx, value.ID, ra); err != nil {
			logging.LegacyPrintf("service.antigravity_gateway", "%s status=429 rate_limit_set_failed provider=%d error=%v", prefix, value.ID, err)
		}
		return nil
	}
	if s.Other == nil {
		return nil
	}
	shouldDisable := s.Other.ApplyUpstreamError(ctx, value, p.OtherObservation).StopScheduling
	if shouldDisable {
		logging.LegacyPrintf("service.antigravity_gateway", "%s status=%d marked_error", prefix, statusCode)
	}
	return nil
}

// resolveResetTime turns a parsed Unix-second deadline into a health-write
// timestamp.
// When no deadline is supplied, it adds defaultDur to the current time.
// It accepts the supplied duration and timestamp without further range checks.
func (s *AntigravityErrorObserver) resolveResetTime(resetAt *int64, defaultDur time.Duration) time.Time {
	if resetAt != nil {
		return time.Unix(*resetAt, 0)
	}
	return time.Now().Add(defaultDur)
}

// AntigravityFallbackDuration converts configured seconds to the provider cooldown.
// Nonpositive inputs retain the platform default for callers that bypass process
// validation; normal startup requires a strictly positive configured value.
func AntigravityFallbackDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return antigravity.AntigravityDefaultRateLimitDuration
	}
	// The configuration boundary stores whole seconds; the observer consumes a duration.
	return time.Duration(seconds) * time.Second
}
