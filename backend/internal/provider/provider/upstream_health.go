package provider

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	anthropicupstream "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
)

// upstream_health.go classifies upstream errors and applies provider, model,
// and quota health policies.
// Provider health depends on request intent as well as upstream status and
// body. Workspace deactivation reaches the shared team handler before
// configured policy can return, while custom-code and pool decisions determine
// whether built-in status handling runs.
//
// Default handling gives Jev retry deadlines and global overload special
// priority, then observes models and Anthropic quota windows before
// administrator temporary rules. Authentication failures retain their separate
// recovery path, and precise model or image intent prevents cooldowns from
// being attached to the wrong scope. Parsed messages are sanitized and bounded
// for health diagnostics. The returned scheduling decision concerns this
// attempt; quota or overload cooldowns can still be written by branches that
// return a false stop flag.

// HealthObservation carries response evidence and current-attempt model intent
// into provider health decisions.
type HealthObservation struct {
	Status int // upstream HTTP response status
	// Headers and Body retain the supplied response values for synchronous
	// parsing. Callers keep their map, slices, and bytes stable until health
	// handling finishes.
	Headers http.Header // upstream response header values
	Body    []byte      // upstream response body bytes
	// Model and ModelProvided distinguish an observed model from no model
	// argument. HandleDefault invokes model health only when ModelProvided is
	// true, including an empty supplied name.
	Model         string // observed attempt model name
	ModelProvided bool   // attempt model argument supplied
	// EffectiveModel scopes temporary-rule handling separately from Model. Callers
	// supply the current attempt's effective name, which may already reflect
	// mapping or normalization.
	EffectiveModel string
	// Thinking optionally carries the attempt's thinking choice into final model
	// resolution. Nil means no choice was supplied; keep a supplied pointed-to
	// value stable during synchronous health handling.
	Thinking *bool
	// Effort optionally carries the attempt's normalized routing reasoning effort
	// so effort_model_mapping scopes model cooldowns to the resolved target key.
	Effort string
	// ImagesEndpoint marks a dedicated image endpoint. Model health uses it to
	// distinguish capability denial there from a text-endpoint mismatch for an
	// image model.
	ImagesEndpoint bool
}

// UpstreamHealth orders platform error classification and invokes provider
// health policy services.
type UpstreamHealth struct {
	Core *providercore.HealthService // provider health policy service
	// Team optionally propagates OpenAI workspace deactivation. Its nil receiver
	// skips fanout, while configured instances share their own deduplication
	// state.
	Team *providercore.TeamLinkedHealth
	// Limits handles the default 429 branch. Configure it before observations can
	// reach that branch after explicit and precise-model handling.
	Limits *RateLimitObserver
	// Models resolves and records the observed model scope. Configure it when
	// ModelProvided can be true during default handling.
	Models *ModelHealth
}

// QuotaWindowObservation creates the provider representation of a parsed
// Anthropic window. Nil input returns nil; FiveHourReset remains the same
// optional time pointer, so callers keep that referenced observation immutable
// while health consumes it.
func QuotaWindowObservation(value *anthropicupstream.WindowLimit) *providercore.QuotaWindowObservation {
	if value == nil {
		return nil
	}
	return &providercore.QuotaWindowObservation{Window: value.Window, ResetAt: value.ResetAt, FiveHourReset: value.FiveHourReset, Reason: value.Reason}
}

// SessionWindowObservation extracts the Anthropic five-hour status, reset text,
// and passive measurements for health handling. Missing or nil headers yield
// empty strings and whatever observations the passive parser can recognize.
func SessionWindowObservation(headers http.Header) providercore.SessionWindowObservation {
	return providercore.SessionWindowObservation{Status: headers.Get("anthropic-ratelimit-unified-5h-status"), Reset: headers.Get("anthropic-ratelimit-unified-5h-reset"), Passive: anthropicupstream.PassiveUsageFields(headers)}
}

// ClientRejectionObservation classifies supplied message text and the body's
// detail.code. Message comparisons ignore case, while workspace deactivation
// requires its exact code; the original message is retained for later health
// diagnostics.
func ClientRejectionObservation(message string, body []byte) providercore.ClientRejectionObservation {
	lower := strings.ToLower(message)
	return providercore.ClientRejectionObservation{Message: message, OrganizationDisabled: strings.Contains(lower, "organization has been disabled"), CreditBalanceExhausted: strings.Contains(lower, "credit balance"), IdentityVerificationRequired: strings.Contains(lower, "identity verification is required"), WorkspaceDeactivated: gjson.GetBytes(body, "detail.code").String() == "deactivated_workspace"}
}

// ForbiddenObservation classifies the raw body and supplied message for
// forbidden handling. Validation URL extraction applies only to validation
// classification; Body remains shared with the caller, who keeps it stable
// through health processing.
func ForbiddenObservation(value *providercore.Record, message string, body []byte) providercore.ForbiddenObservation {
	kind := antigravity.ClassifyForbiddenType(string(body))
	url := ""
	if kind == providercore.ForbiddenTypeValidation {
		url = antigravity.ExtractValidationURL(string(body))
	}
	return providercore.ForbiddenObservation{Message: message, Body: body, HTML: upstream.IsHTMLResponse(body), Kind: kind, ValidationURL: url, ConcurrentRequestLimited: KimiConcurrencyLimit403(value, message), ConcurrencyReason: "cn_concurrency_limit: " + kimi.ConcurrentRequestLimitMessage}
}

// KimiConcurrencyLimit403 reports the platform's recognized Kimi concurrency
// message for a matching record. Nil records and other platforms return false,
// and the platform helper determines which trimmed message spelling qualifies.
func KimiConcurrencyLimit403(value *providercore.Record, message string) bool {
	return value != nil && value.Platform == providercore.PlatformKimi && kimi.IsConcurrencyLimitMessage(message)
}

// LogAPIKeyHealthWarning logs complete string-key/value pairs as Zap
// attributes. Nonstring keys and an unmatched final value are ignored; callers
// supply diagnostic text and values already suitable for application logging.
func LogAPIKeyHealthWarning(message string, fields ...any) {
	// Size attributes for complete key and value pairs.
	attrs := make([]zap.Field, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if ok {
			attrs = append(attrs, zap.Any(key, fields[i+1]))
		}
	}
	logging.L().Warn(message, attrs...)
}

// UnauthorizedObservation extracts revocation and permanently unauthorized
// evidence while retaining the supplied message. Revocation uses recognized
// error codes, and the permanent marker requires exact Unauthorized text in the
// body's detail field.
func UnauthorizedObservation(body []byte, message string) providercore.UnauthorizedObservation {
	code := upstream.ExtractErrorCode(body)
	return providercore.UnauthorizedObservation{Message: message, TokenRevoked: code == "token_invalidated" || code == "token_revoked", PermanentlyUnauthorized: gjson.GetBytes(body, "detail").String() == "Unauthorized"}
}

// CheckErrorPolicy asks Core for the configured response policy and
// effective-model scope. Nil records return none; Antigravity disables the
// temporary-rule escalation choice that other platform records pass into the
// core check.
func (s *UpstreamHealth) CheckErrorPolicy(ctx context.Context, provider *providercore.Record, observation HealthObservation) providercore.ErrorPolicyResult {
	statusCode, responseBody := observation.Status, observation.Body
	if provider == nil {
		return providercore.ErrorPolicyNone
	}
	return s.Core.CheckErrorPolicy(ctx, provider, statusCode, responseBody, observation.EffectiveModel, provider == nil || provider.Platform != capability.PlatformAntigravity)
}

// ApplyExplicitErrorPolicy evaluates configured policy and writes custom-code
// failure state when matched. It sanitizes an upstream message or supplies the
// generic custom-code diagnostic, then returns the policy result independently
// of later built-in handling.
func (s *UpstreamHealth) ApplyExplicitErrorPolicy(ctx context.Context, provider *providercore.Record, observation HealthObservation) providercore.ErrorPolicyResult {
	statusCode, responseBody := observation.Status, observation.Body
	result := s.CheckErrorPolicy(ctx, provider, observation)
	if result != providercore.ErrorPolicyCustomMatched || provider == nil {
		return result
	}
	message := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(responseBody)))
	if message == "" {
		message = "Custom error code triggered"
	}
	s.Core.ApplyCustomErrorCode(ctx, provider, statusCode, message)
	return result
}

// ApplyUpstreamError returns the scheduling and policy decision for one
// upstream failure. The caller supplies a stable mutable record and configures
// the health components reached by its response and model observation.
//
// Workspace deactivation is propagated before explicit pool or custom-code
// policy can end handling. Matched or temporarily unscheduled policy stops this
// attempt, while explicit skips and pool bypasses return without built-in
// status rules. Remaining responses enter HandleDefault, where model intent,
// quota resets, and authentication recovery choose the health scope. A cooldown
// write can accompany a decision whose stop flag remains false.
func (s *UpstreamHealth) ApplyUpstreamError(ctx context.Context, provider *providercore.Record, observation HealthObservation) providercore.UpstreamErrorDecision {
	statusCode, responseBody := observation.Status, observation.Body
	// Team workspace suspension precedes pool, custom-code, and temporary suspension checks.
	// HandleWorkspaceDeactivated deduplicates calls from the fast path.
	s.Team.HandleWorkspaceDeactivated(ctx, provider, statusCode == http.StatusPaymentRequired && ClientRejectionObservation("", responseBody).WorkspaceDeactivated)
	policy := providercore.ErrorPolicyNone
	// Explicit pool or custom-code choices control admission to built-in status
	// rules. Evaluate them first so a configured skip cannot acquire a later
	// default disable or cooldown.
	if provider != nil && (provider.IsPoolMode() || provider.IsCustomErrorCodesEnabled()) {
		policy = s.ApplyExplicitErrorPolicy(ctx, provider, observation)
	}
	decision := providercore.UpstreamErrorDecision{Policy: policy}
	switch policy {
	case providercore.ErrorPolicyCustomMatched, providercore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case providercore.ErrorPolicyCustomSkipped, providercore.ErrorPolicyPoolBypassed:
		return decision
	}
	decision.StopScheduling = s.HandleDefault(ctx, provider, observation)
	return decision
}

// HandleDefault applies platform health after the caller has resolved explicit
// policy. Nil records return false; other calls require the components used by
// their status and model intent, and can mutate the supplied record through
// health operations.
//
// Jev Retry-After and overload handling precede models, then Anthropic
// exhausted and Fable windows precede temporary rules. A 401 reaches
// authentication recovery before generic temporary handling. Sanitized bounded
// messages feed status classification, while model and image intent keep
// observations scoped to the attempted capability. The bool reports stop
// scheduling for this attempt, with false still permitting quota or overload
// state writes.
func (s *UpstreamHealth) HandleDefault(ctx context.Context, provider *providercore.Record, observation HealthObservation) (shouldDisable bool) {
	statusCode, headers, responseBody := observation.Status, observation.Headers, observation.Body
	if provider == nil {
		return false
	}
	// Jev and OpenCode provide concrete cooldown deadlines through Retry-After.
	// Prefer those deadlines before general overload or temporary rules choose
	// another window for the same provider response.
	if (provider.Platform == capability.PlatformJev || provider.IsOpenCode()) && (statusCode == http.StatusTooManyRequests || statusCode == 529) {
		if reset := upstream.RetryAfterResetTime(headers, time.Now()); reset != nil {
			s.Core.ApplyObservedRateLimit(ctx, provider, *reset)
			return false
		}
		// Without a retry deadline, an explicit maintenance rule can still define
		// its suspension. Try that rule before the general overload branch returns.
		if statusCode == 529 && s.Core.TryTempUnschedulable(ctx, provider, statusCode, responseBody, true, observation.EffectiveModel) {
			return true
		}
	}

	if statusCode == 529 {
		s.Core.ApplyOverload(ctx, provider)
		return false
	}

	if observation.ModelProvided && s.Models.ObserveForEffort(ctx, provider, observation.Model, observation.Effort, statusCode, responseBody, observation.Thinking, observation.ImagesEndpoint) {
		return true
	}

	// Apply exhausted Anthropic 5h/7d windows before local temporary suspension rules.
	// A broad 429 keyword rule could otherwise shorten an hours- or days-long cooldown.
	if statusCode == http.StatusTooManyRequests && provider.Platform == capability.PlatformAnthropic {
		// The Fable-only window can pause its own model set while other models remain
		// candidates. Record that scope before checking provider-wide exhausted
		// windows.
		fableLimited := s.observeFableWindow(ctx, provider, headers)
		if s.observeExhaustedWindow(ctx, provider, headers) {
			return false
		}
		if fableLimited {
			return false
		}
	}

	// Authentication recovery decides whether credentials can refresh and which
	// owner to pause. Keep 401 out of generic temporary handling until that
	// dedicated recovery path runs.
	if statusCode != http.StatusUnauthorized && s.Core.TryTempUnschedulable(ctx, provider, statusCode, responseBody, provider.Platform != capability.PlatformAntigravity, observation.EffectiveModel) {
		return true
	}

	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(responseBody))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	if upstreamMsg != "" {
		// Bound the captured message to 512 bytes before newline escaping.
		upstreamMsg = logredact.TruncateLine([]byte(upstreamMsg), 512)
	}

	switch statusCode {
	case 400:
		shouldDisable = s.Core.ApplyBadRequest(ctx, provider, ClientRejectionObservation(upstreamMsg, responseBody))

	case 401:
		shouldDisable = s.Core.ApplyUnauthorized(ctx, provider, UnauthorizedObservation(responseBody, upstreamMsg))

	case 402:
		shouldDisable = s.Core.ApplyPaymentRequired(ctx, provider, ClientRejectionObservation(upstreamMsg, responseBody))

	case 403:
		logging.LegacyPrintf(
			"service.ratelimit",
			"[HandleUpstreamErrorRaw] provider_id=%d platform=%s type=%s status=403 request_id=%s cf_ray=%s upstream_msg=%s raw_body=%s",
			provider.ID,
			provider.Platform,
			provider.Type,
			strings.TrimSpace(headers.Get("x-request-id")),
			strings.TrimSpace(headers.Get("cf-ray")),
			upstreamMsg,
			logredact.TruncateLine(responseBody, 1024),
		)
		shouldDisable = s.Core.ApplyForbiddenObservation(ctx, provider, ForbiddenObservation(provider, upstreamMsg, responseBody))
	case 429:
		s.Limits.Observe429(ctx, provider, headers, responseBody)
		shouldDisable = false
	default:
		if statusCode >= 500 {
			// Unclassified server failures retain their diagnostic log while leaving
			// this decision's stop flag false. Earlier explicit, model, and temporary
			// handling already had precedence.
			slog.Warn("provider_upstream_error", "provider_id", provider.ID, "status_code", statusCode)
			shouldDisable = false
		}
	}

	return shouldDisable
}

// observeFableWindow selects the Fable-only quota window and passes it with
// passive fields to Core. Missing windows return false, and the core owns
// affected model scope and persistence of the selected observation.
func (s *UpstreamHealth) observeFableWindow(ctx context.Context, value *providercore.Record, headers http.Header) bool {
	limit := anthropicupstream.SelectFableWindowLimit(headers, time.Now())
	if limit == nil {
		return false
	}
	return s.Core.ApplyFableQuotaWindow(ctx, value, QuotaWindowObservation(limit), anthropicupstream.PassiveUsageFields(headers))
}

// observeExhaustedWindow selects an exhausted Anthropic provider window at the
// current time and delegates it to Core. The same time anchors selection and
// handling, and missing or ineligible observations return false through the
// core rule.
func (s *UpstreamHealth) observeExhaustedWindow(ctx context.Context, value *providercore.Record, headers http.Header) bool {
	now := time.Now()
	return s.Core.ApplyExhaustedQuotaWindow(ctx, value, QuotaWindowObservation(anthropicupstream.SelectExhaustedWindow(headers, now)), now)
}
