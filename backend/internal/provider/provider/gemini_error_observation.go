package provider

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/provider"
)

// gemini_error_observation.go selects recovery deadlines and health handling
// for Gemini upstream failures. Gemini 429 recovery depends on whether the
// provider uses a Google quota route, a third-party endpoint or a shared pool.
// The configured status filter runs before observation, and ordinary pool
// responses leave request retries to their caller instead of installing a
// local cooldown.
//
// For remaining providers, third-party endpoints use a generic configured
// duration. Official routes first use a parsed response deadline, then select
// tier cooldown for Code Assist, or the supplied daily reset for API keys and
// AI Studio. Missing daily timing leaves a five-minute window.
// These choices are persisted best effort, while selected authentication and
// overload errors delegate to the common health service.

// GeminiErrorObserver applies provider health updates using platform reset
// times or third-party cooldowns.
type GeminiErrorObserver struct {
	// Other handles accepted 401, 403 and 529 observations when configured. Observe
	// returns the input record after that call regardless of its health decision.
	Other *UpstreamHealth
	// Precheck optionally supplies dynamic cooldowns for third-party and tier
	// routes. Nil uses their local or static tier duration.
	Precheck *provider.GeminiPrecheck
	// SetRateLimited is required for a 429 reaching deadline persistence. Its
	// errors are discarded because observation leaves the upstream failure
	// with the caller.
	SetRateLimited func(context.Context, int64, time.Time) error
	// ResetTime is required for official 429 parsing and returns an absolute
	// Unix-second deadline. A nil result selects the provider-type timing rules.
	ResetTime func([]byte) *int64
	// DailyReset is required when an official API-key or AI Studio response lacks
	// parsed timing. It returns an absolute Unix-second deadline; nil selects a
	// five-minute cooldown from observation time.
	DailyReset func() *int64
}

// Observe applies accepted Gemini failure observations to common health or
// quota state.
// A nonnil record and branch-specific callbacks are required. Accepted 401, 403
// and 529 responses return the input record when Other is configured; remaining
// paths return nil, including 429 writes whose errors are discarded.
//
// Ordinary pool 429 responses stop before local deadline selection. Other 429
// responses choose third-party cooldown, parsed reset, tier cooldown or daily
// reset in that order according to provider identity. A missing daily reset
// uses five minutes, so absent timing still produces a bounded scheduling window.
func (s *GeminiErrorObserver) Observe(ctx context.Context, value *provider.Record, statusCode int, headers http.Header, body []byte, observation HealthObservation) *provider.Record {
	// Honor the provider status filter before common health or quota branches so
	// an excluded error cannot install a default cooldown through this observer.
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
	// Shared-pool requests recover through retry or switching; skip local windows
	// unless a custom status selection asks this observer to handle the 429.
	if value.IsPoolMode() && !value.IsCustomErrorCodesEnabled() {
		return nil
	}

	// Every deadline branch writes best effort and returns nil. Storage errors
	// remain separate from the upstream failure the request caller is handling.
	oauthType := value.GeminiOAuthType()
	tierID := value.GeminiTierID()
	projectID := strings.TrimSpace(value.GetCredential("project_id"))
	isCodeAssist := value.IsGeminiCodeAssist()

	if value.IsGeminiThirdPartyProvider() {
		// Third-party quota rules are selected locally. Their 429 response does not
		// enter the official Google reset parser or the Google daily-reset
		// timing path.
		cooldown := 5 * time.Minute
		if s.Precheck != nil {
			cooldown = s.Precheck.GeminiCooldown(ctx, value)
		}
		ra := time.Now().Add(cooldown)
		_ = s.SetRateLimited(ctx, value.ID, ra)
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Provider %d (third-party API Key) rate limited, cooldown=%v", value.ID, time.Until(ra).Truncate(time.Second))
		return nil
	}

	resetAt := s.ResetTime(body)
	if resetAt == nil {
		var ra time.Time
		if isCodeAssist {
			// Code Assist OAuth routes use a tier-based window when reset metadata is
			// absent. A configured Precheck can replace the static tier duration
			// at observation time.
			cooldown := provider.GeminiCooldownForTier(tierID)
			if s.Precheck != nil {
				cooldown = s.Precheck.GeminiCooldown(ctx, value)
			}
			ra = time.Now().Add(cooldown)
			logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Provider %d (Code Assist, tier=%s, project=%s) rate limited, cooldown=%v", value.ID, tierID, projectID, time.Until(ra).Truncate(time.Second))
		} else {
			// These official routes use the supplied daily reset after response
			// timing is absent. Missing daily timing leaves a short cooldown
			// for subsequent selection.
			if ts := s.DailyReset(); ts != nil {
				// DailyReset already supplies absolute seconds for deadline storage.
				ra = time.Unix(*ts, 0)
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Provider %d (API Key/AI Studio, type=%s) rate limited, reset at PST midnight (%v)", value.ID, value.Type, ra)
			} else {
				// Use a short deadline when both timings are missing so the
				// attempted write can defer immediate reuse of this exhausted route.
				ra = time.Now().Add(5 * time.Minute)
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Provider %d rate limited, fallback to 5min", value.ID)
			}
		}
		_ = s.SetRateLimited(ctx, value.ID, ra)
		return nil
	}

	// Convert the parser's absolute seconds for provider deadline storage.
	resetTime := time.Unix(*resetAt, 0)
	_ = s.SetRateLimited(ctx, value.ID, resetTime)
	logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Provider %d rate limited until %v (oauth_type=%s, tier=%s)",
		value.ID, resetTime, oauthType, tierID)
	return nil
}
