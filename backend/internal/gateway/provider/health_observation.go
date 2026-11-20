package provider

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// health_observation.go captures request-aware health evidence and transfers
// observed provider changes to execution targets.
// Provider health receives both the first model argument and an effective
// model resolved from request hints. An empty first argument still counts as
// supplied, even when the effective model falls back to context. This lets
// default model observation distinguish argument presence while temporary-rule
// handling uses the resolved attempt model. Thinking likewise distinguishes a
// missing hint from an explicit false value.
//
// The adapters choose either a borrowed provider record or an ExecutionRecord
// copy for each health operation. Copy-based paths transfer selected mutations
// back to the request target; threshold observation transfers pause fields and
// extras, while error observation transfers credentials and extras. Callers
// serialize target observation with other request reads and writes.

// ApplyExecutionSchedulingThreshold evaluates scheduling thresholds on an
// execution record copy and transfers pause deadline, reason, and extras back
// to value. A nil observer returns false; the returned boolean is the provider
// core's pause result.
func ApplyExecutionSchedulingThreshold(ctx context.Context, observer *provideradapter.UpstreamHealth, value *ExecutionProvider) bool {
	if observer == nil {
		return false
	}
	record := ExecutionRecord(value)
	paused := observer.Core.ApplyProviderSchedulingThreshold(ctx, record)
	if value != nil && record != nil {
		value.Record.TempUnschedulableUntil = record.TempUnschedulableUntil
		value.Record.TempUnschedulableReason = record.TempUnschedulableReason
		value.Record.Extra = record.Extra
	}
	return paused
}

// ObserveExecutionSessionWindow decodes the response headers and records a
// nonempty session-window status through the health core. Empty observations
// return before dependency access; a recognized observation requires a
// configured observer and its Core.
func ObserveExecutionSessionWindow(ctx context.Context, observer *provideradapter.UpstreamHealth, value *ExecutionProvider, headers http.Header) {
	observation := provideradapter.SessionWindowObservation(headers)
	if observation.Status == "" {
		return
	}
	observer.Core.UpdateSessionWindow(ctx, ExecutionRecord(value), observation)
}

// ApplyGrokExecutionHealth combines response and request observations for the
// shared Grok health handler. Missing health or target returns
// ErrorPolicyNone; models remain ordered inputs, and teamModel identifies both
// quota and team cooldown processing.
func ApplyGrokExecutionHealth(ctx context.Context, health *provideradapter.GrokHealth, target *ExecutionProvider, status int, headers http.Header, body []byte, teamModel string, models ...string) provider.UpstreamErrorDecision {
	if health == nil || target == nil {
		return provider.UpstreamErrorDecision{Policy: provider.ErrorPolicyNone}
	}
	return health.ObserveError(ctx, target.View(), provideradapter.GrokHealthInput{
		Observation:     HealthObservationFromContext(ctx, status, headers, body, models),
		Models:          models,
		QuotaModel:      teamModel,
		TeamModel:       teamModel,
		RequestScoped:   IsRequestScopedProviderFailure(target, status, body),
		ServerTransient: IsTransientProviderFailure(status, body),
	})
}

// HealthObservationFromContext captures response evidence and request hints
// for provider health handling. Headers and body are borrowed, so callers keep
// them stable while the health consumer reads.
//
// Model retains the first supplied argument verbatim and ModelProvided records
// its presence, including an empty value. EffectiveModel uses the trimmed
// nonblank first argument or the stored attempt hint; later arguments are
// ignored. HandleDefault uses argument presence for model observation, while
// temporary-rule checks use EffectiveModel.
func HealthObservationFromContext(ctx context.Context, status int, headers http.Header, body []byte, models []string) provideradapter.HealthObservation {
	input := provideradapter.HealthObservation{
		Status:         status,
		Headers:        headers,
		Body:           body,
		EffectiveModel: requeststate.HealthModel(ctx, models),
		ModelProvided:  len(models) > 0,
		Thinking:       requeststate.HealthThinking(ctx),
		Effort:         requeststate.RoutingReasoningEffortFromContext(ctx),
		ImagesEndpoint: requeststate.OpenAIImagesEndpointFromContext(ctx),
	}
	if len(models) > 0 {
		input.Model = models[0]
	}
	return input
}

// ApplyExecutionHealth observes a provider copy and transfers credentials and
// extras back to the target after evaluation. The observer must be configured;
// other fields mutated on the copied record remain on that temporary record.
func ApplyExecutionHealth(ctx context.Context, observer *provideradapter.UpstreamHealth, target *ExecutionProvider, input provideradapter.HealthObservation) provider.UpstreamErrorDecision {
	record := ExecutionRecord(target)
	result := observer.ApplyUpstreamError(ctx, record, input)
	if target != nil && record != nil {
		target.Record.Credentials, target.Record.Extra = record.Credentials, record.Extra
	}
	return result
}

// ExecutionErrorPolicy returns the identity and credential fields used by
// provider error rules. Nil targets return nil; the credential map is borrowed
// from the target, so callers keep it stable during classification.
func ExecutionErrorPolicy(value *ExecutionProvider) *provider.Record {
	if value == nil {
		return nil
	}
	return &provider.Record{ID: value.Record.ID, Platform: value.Record.Platform, Type: value.Record.Type, Credentials: value.Record.Credentials}
}

// ApplyOpenAIResponseHealth combines request hints, model inputs, and
// content/transient classification for OpenAI response health handling. It
// passes the target's borrowed record to health.Apply, with
// suppressDefaultRateLimit controlling the caller's ordinary limit policy.
func ApplyOpenAIResponseHealth(ctx context.Context, health *provideradapter.OpenAIResponseHealth, target *ExecutionProvider, status int, headers http.Header, body []byte, suppressDefaultRateLimit bool, models ...string) provider.UpstreamErrorDecision {
	return health.Apply(ctx, target.View(), provideradapter.OpenAIResponseHealthInput{
		Observation:              HealthObservationFromContext(ctx, status, headers, body, models),
		Models:                   models,
		SuppressDefaultRateLimit: suppressDefaultRateLimit,
		ContentRejected:          IsContentPolicyRejection(body) || IsOpenAICyberWarningPayload(body, upstream.ExtractErrorMessage(body)),
		RequestScoped:            IsRequestScopedProviderFailure(target, status, body),
		SelfBuiltImage:           openai.IsOpenAIImagesSelfBuiltRequest(ctx),
		Transient:                IsTransientProviderFailure(status, body),
	})
}
