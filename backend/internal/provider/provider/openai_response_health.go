package provider

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_response_health.go selects provider and model health actions for
// failed OpenAI responses. Response health distinguishes failures caused by
// the request from failures that make a provider or model unsuitable for
// scheduling. Content rejection exits first, and request-scoped failures exit
// unless an administrator's custom-code policy matches; account access errors
// and self-built image denials have separate handling before general policy.
//
// Explicit pool and custom-code choices precede image, model, temporary-rule,
// and Spark cooldown observations. Default 429 handling can be suppressed
// after the more precise model branches, and default health processing
// receives a clone with model observation cleared to avoid applying that
// branch again. Runtime blocks are shared with scheduling, while changed
// Credentials and Extra from the default path are copied back to the caller's
// record. API-key transient streaks apply only after provider-level handling
// leaves the record eligible and pool retry rules permit that recording.

// OpenAIResponseHealth orders response observations and updates runtime health
// shared with scheduling.
type OpenAIResponseHealth struct {
	// Health optionally supplies durable error, team, and model rules. Configure
	// its subservices for every observation branch that the caller can reach.
	Health *UpstreamHealth
	// Runtime holds shared cooldown and retry-window state. It must be configured
	// when markOAuth429 clears a recovery window.
	Runtime *providercore.RuntimeBlockState
	// ModelTransient optionally records eligible API-key model failure streaks. A
	// nil state skips transient recording after provider handling.
	ModelTransient *providercore.ModelTransientState
	// Deferred optionally queues Ollama Cloud last-used activity. Its absence
	// skips that update when response health accepts an observation.
	Deferred *providercore.DeferredService
}

// OpenAIResponseHealthInput supplies response classification and
// current-attempt model intent from gateway execution.
type OpenAIResponseHealthInput struct {
	// Observation supplies response data and effective request intent. Apply
	// clears its model flag before default handling after the precise model
	// branches have run.
	Observation HealthObservation
	// Models carries canonical upstream names for health handling. Apply uses its
	// first entry; an absent or blank entry can require a provider-wide temporary
	// block.
	Models []string
	// SuppressDefaultRateLimit skips the ordinary image-limit and default 429
	// paths. Model and Spark observations still run before the later default-429
	// suppression.
	SuppressDefaultRateLimit bool
	// ContentRejected, RequestScoped, SelfBuiltImage, and Transient describe
	// caller classification at different stages of Apply. Earlier request
	// rejection can prevent later image or transient handling.
	//
	// ContentRejected reports content policy rejection and immediately ends all
	// provider health handling for this observation.
	//
	// RequestScoped reports a request-local failure. Apply skips provider health
	// unless configured custom status policy matches.
	//
	// SelfBuiltImage permits image-capability denial handling for a
	// gateway-constructed image request whose tool payload is controlled.
	//
	// Transient marks a retryable upstream failure. Eligible API-key requests can
	// record model streaks after other handling leaves scheduling active.
	// ContentRejected: policy-rejected content; RequestScoped: request-local failure.
	// SelfBuiltImage: constructed image request; Transient: retryable upstream failure.
	ContentRejected, RequestScoped, SelfBuiltImage, Transient bool
}

// Apply returns the scheduling and error-policy decision for an upstream
// response. It can update the caller's Credentials and Extra after default
// handling; callers keep that record exclusive during evaluation and configure
// the health subservices reached by the observation.
//
// Request rejection and request-scoped policy precede account access and
// self-built image handling. Explicit custom or pool decisions then take
// precedence over image, model, temporary-rule, and Spark observations, with
// suppression affecting the later default 429 path. The default observation
// clears its model flag because model health has already run.
//
// Accepted state writes receive a bounded context detached from request
// cancellation. Runtime provider blocks supplement durable health state, and
// eligible API-key transient failures update their model streak only after
// default handling leaves scheduling active.
func (s *OpenAIResponseHealth) Apply(ctx context.Context, provider *providercore.Record, input OpenAIResponseHealthInput) providercore.UpstreamErrorDecision {
	statusCode, headers, responseBody := input.Observation.Status, input.Observation.Headers, input.Observation.Body
	canonicalModel, suppressDefaultRateLimitState := input.Models, input.SuppressDefaultRateLimit

	customStatusMatched := provider != nil && provider.IsCustomErrorCodesEnabled() && provider.ShouldHandleErrorCode(statusCode)
	if input.ContentRejected {
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}
	if input.RequestScoped && !customStatusMatched {
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}
	// Record activity for upstream failures that reach provider health handling.
	if s != nil {
		s.recordActivity(provider)
	}
	// Capacity shedding belongs to the request retry path. Returning before
	// provider-state handling keeps a temporary capacity response from disabling
	// the account.
	if provider != nil && provider.Platform == capability.PlatformOpenAI && openai.IsOpenAIRequestScopedCapacityShed("", responseBody) {
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}
	// State observations can outlive the response context. Use the bounded
	// detached provider-state budget so request cancellation does not discard an
	// accepted health update.
	stateCtx, cancel := ProviderStateContext(ctx)
	defer cancel()
	// Account or workspace access loss applies before model and temporary rules.
	// Persist authentication failure first, then install the runtime block for
	// subsequent scheduling.
	if provider != nil && provider.Platform == capability.PlatformOpenAI && openai.IsOpenAIHTTPUpstreamAccessStateError(0, "", responseBody) {
		message := "OpenAI upstream provider or workspace is unavailable"
		if upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(responseBody)); upstreamMsg != "" {
			message = upstreamMsg
		}
		if s != nil && s.Health != nil {
			s.Health.Core.ApplyAuthenticationFailure(stateCtx, providercore.CloneRecord(provider), message)
		}
		if s != nil {
			s.Runtime.BlockProviderScheduling(provider, time.Time{}, "openai_access_state")
		}
		return providercore.UpstreamErrorDecision{StopScheduling: true}
	}

	// Self-built image requests include a matching image_generation tool, so a missing-tool error
	// can indicate lost image capability. Passthrough clients control tools and tool_choice themselves;
	// applying this rule to their malformed requests could cool a healthy provider.
	if input.SelfBuiltImage && openai.IsImageCapabilityLossError(statusCode, responseBody) {
		if s != nil && s.Health != nil {
			_ = ObserveOpenAIImageCapabilityLoss(stateCtx, s.Health.Core, providercore.CloneRecord(provider), statusCode, responseBody)
		}
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}

	if s == nil || provider == nil {
		return providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
	}
	// Propagate workspace deactivation before later model and temporary-rule
	// branches return, so an accepted team-level failure reaches the shared
	// workspace handler.
	if s.Health != nil {
		s.Health.Team.HandleWorkspaceDeactivated(stateCtx, providercore.CloneRecord(provider), statusCode == http.StatusPaymentRequired && ClientRejectionObservation("", responseBody).WorkspaceDeactivated)
	}
	observation := input.Observation
	observation.Headers = nil
	decision := providercore.ErrorDecisionWithoutPersistence(provider, statusCode)
	// Administrator custom-code and pool rules determine whether more specific
	// observations may run. An explicit skip or bypass returns before default
	// cooldown and disable handling.
	if s.Health != nil {
		if provider.IsPoolMode() || provider.IsCustomErrorCodesEnabled() {
			decision.Policy = s.Health.ApplyExplicitErrorPolicy(stateCtx, providercore.CloneRecord(provider), observation)
			decision.StopScheduling = decision.Policy == providercore.ErrorPolicyCustomMatched || decision.Policy == providercore.ErrorPolicyTempUnscheduled
		} else {
			decision = providercore.UpstreamErrorDecision{Policy: providercore.ErrorPolicyNone}
		}
	}
	switch decision.Policy {
	case providercore.ErrorPolicyCustomMatched:
		decision.StopScheduling = true
		s.Runtime.BlockProviderScheduling(provider, time.Time{}, "upstream_disable")
		return decision
	case providercore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case providercore.ErrorPolicyCustomSkipped, providercore.ErrorPolicyPoolBypassed:
		return decision
	}

	if !suppressDefaultRateLimitState && openai.IsImageRateLimitError(statusCode, responseBody) {
		if s.Health != nil {
			_ = ObserveOpenAIImageRateLimit(stateCtx, s.Health.Core, providercore.CloneRecord(provider), statusCode, headers, responseBody)
		}
		return decision
	}
	if s.Health != nil && len(canonicalModel) > 0 &&
		s.Health.Models.Observe(stateCtx, providercore.CloneRecord(provider), canonicalModel[0], statusCode, responseBody, observation.Thinking, observation.ImagesEndpoint) {
		decision.StopScheduling = true
		return decision
	}
	// Handle precise model errors before administrator-defined temporary rules for ordinary providers.
	// Known models receive a provider/model block; unknown models also block the whole provider at runtime.
	if s.Health != nil && statusCode != http.StatusUnauthorized &&
		!provider.IsPoolMode() && !provider.IsCustomErrorCodesEnabled() &&
		s.Health.Core.HandleTempUnschedulable(stateCtx, providercore.CloneRecord(provider), statusCode, responseBody, observation.EffectiveModel) {
		decision.Policy = providercore.ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		if len(canonicalModel) == 0 || strings.TrimSpace(canonicalModel[0]) == "" {
			s.Runtime.BlockProviderScheduling(provider, time.Time{}, "upstream_disable")
		}
		return decision
	}
	if statusCode == http.StatusTooManyRequests && s.Health != nil && len(canonicalModel) > 0 &&
		s.Health.Models.ObserveSparkRateLimitForEffort(stateCtx, providercore.CloneRecord(provider), canonicalModel[0], observation.Effort, statusCode, headers, responseBody, observation.Thinking) {
		return decision
	}
	if suppressDefaultRateLimitState && statusCode == http.StatusTooManyRequests {
		return decision
	}
	if statusCode == http.StatusTooManyRequests {
		s.markOAuth429(stateCtx, provider, headers, responseBody)
	}
	if s.Health == nil {
		return decision
	}
	// Model handling has already had its turn. Clear the model flag before default
	// processing, then copy back the mutable credential and Extra results from its
	// clone.
	defaultObservation := observation
	defaultObservation.Model, defaultObservation.ModelProvided = "", false
	defaultObservation.Headers = headers
	updated := providercore.CloneRecord(provider)
	decision.StopScheduling = s.Health.HandleDefault(stateCtx, updated, defaultObservation)
	provider.Credentials, provider.Extra = updated.Credentials, updated.Extra

	modelTempMatched := statusCode != http.StatusUnauthorized && observation.EffectiveModel != "" &&
		len(providercore.MatchTempUnschedulableRules(providercore.CloneRecord(provider), statusCode, responseBody)) > 0
	if decision.StopScheduling && !modelTempMatched {
		s.Runtime.BlockProviderScheduling(provider, time.Time{}, "upstream_disable")
	}
	// Pool-mode retry budgets already constrain these errors. A generic provider/model cooldown
	// would prevent the next authorized retry before that budget is exhausted.
	poolModeRetryable := provider.IsPoolMode() && provider.IsPoolModeRetryableStatus(statusCode)
	if !decision.StopScheduling && provider.Platform == capability.PlatformOpenAI && provider.Type == capability.ProviderTypeAPIKey &&
		input.Transient && !poolModeRetryable {
		model := ""
		if len(canonicalModel) > 0 {
			model = canonicalModel[0]
		}
		s.RecordModelFailure(provider, model)
	}
	return decision
}

// recordActivity queues a coalesced last-used update for an Ollama Cloud usage
// record. A missing deferred service, nil record, or other provider category
// skips activity publication.
func (s *OpenAIResponseHealth) recordActivity(value *providercore.Record) {
	if s.Deferred != nil && value != nil && providercore.IsOllamaCloudUsageProvider(providercore.CloneRecord(value)) {
		s.Deferred.ScheduleLastUsedUpdate(value.ID)
	}
}

// markOAuth429 installs a runtime cooldown for a nonshadow OpenAI OAuth-like
// record. A transient failure inside the recovery window leaves scheduling
// available, while a quota reset bypasses that retry opportunity.
//
// A future supplied reset wins over the health service's positive fallback
// duration, with five seconds used when neither supplies a deadline. The
// runtime block is installed before the retry window is cleared, so later
// admission observes cooldown state rather than opening another window
// immediately. Runtime must be configured for this update.
func (s *OpenAIResponseHealth) markOAuth429(ctx context.Context, value *providercore.Record, headers http.Header, body []byte) {
	if s == nil || value == nil || !value.IsOpenAIOAuthLike() || value.IsShadow() {
		return
	}
	disposition, reset := ClassifyOpenAI429(headers, body)
	if disposition == providercore.OpenAI429Transient && s.Runtime.RetryWindowActive(value.ID) {
		return
	}
	// A short default also covers absent Health services, so runtime retry
	// admission receives a finite block after its transient recovery window closes.
	until := time.Now().Add(5 * time.Second)
	if reset != nil && reset.After(time.Now()) {
		until = *reset
	} else if s.Health != nil {
		// Absent or stale reset signals permit the configured health fallback. A
		// positive fallback replaces the short default while quota deadlines retain
		// their precedence.
		if cooldown, ok := s.Health.Core.Fallback429Cooldown(ctx, providercore.CloneRecord(value)); ok && cooldown > 0 {
			until = time.Now().Add(cooldown)
		}
	}
	// Install the chosen cooldown before clearing retry admission, so subsequent
	// scheduling checks see the block after this recovery window is closed.
	s.Runtime.BlockProviderScheduling(value, until, "429")
	s.Runtime.ResetRetry(value.ID)
}

// RetryOAuth429 reports same-provider retry eligibility for an enabled HTTP 429
// response. Nil receivers, disabled decisions, and other statuses return false;
// the shared eligibility helper then applies identity, quota, and
// runtime-window checks.
func (s *OpenAIResponseHealth) RetryOAuth429(value *providercore.Record, status int, disabled bool, headers http.Header, body []byte) bool {
	if s == nil || disabled || status != http.StatusTooManyRequests {
		return false
	}
	return CanRetryOpenAI429(s.Runtime, value, headers, body)
}

// RetryDeadline returns the runtime recovery deadline for a nonshadow OpenAI
// OAuth-like record. Nil receivers, unsupported records, and absent retry
// windows return zero time; reading the deadline does not create a window.
func (s *OpenAIResponseHealth) RetryDeadline(value *providercore.Record) time.Time {
	if s == nil || value == nil || !value.IsOpenAIOAuthLike() || value.IsShadow() {
		return time.Time{}
	}
	return s.Runtime.RetryDeadline(value.ID)
}

// RecordModelFailure submits a normalized provider/model failure to shared
// transient state and logs accepted streak decisions. Missing dependencies,
// invalid IDs, blank models, and oversized model names produce no recorded
// streak or warning.
func (s *OpenAIResponseHealth) RecordModelFailure(value *providercore.Record, model string) {
	if s == nil || value == nil || s.ModelTransient == nil {
		return
	}
	model = providercore.NormalizeTransientModel(model)
	result := s.ModelTransient.RecordFailure(value.ID, model, time.Now())
	if result.FailureStreak == 0 {
		return
	}
	slog.Warn("openai_model_transient_state", "provider_id", value.ID, "platform", value.Platform, "model", model, "failure_streak", result.FailureStreak, "cooldown_ms", result.Cooldown.Milliseconds(), "block_scope", "provider_model")
}
