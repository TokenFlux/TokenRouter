package provider

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
)

// compatible_eligibility.go evaluates provider eligibility and reports the
// first failed request requirement.
// Compatible selection rejects targets in a fixed order so diagnostics
// identify the first unavailable condition. Protocol and model-window
// permission precede quota pause observations, model support, persistent quota
// exhaustion, endpoint capability, and optional Compact qualification. The
// returned strings describe this ordering rather than an exhaustive list of
// every failing condition.
//
// OpenAI quota thresholds come from the request's execution hints, while Grok
// windows use provider-owned rules. Context hints are replaced by value when
// thresholds are bound, so a derived attempt can use different thresholds
// while keeping its parent context intact. Parent credential health for Spark
// remains a separate lookup, and protocol-specific WS/Compact routes can
// qualify without ordinary HTTP text support.

const (
	// NativeChatFunctionToolsUnsupported identifies local candidate exhaustion
	// caused by the first-party Chat endpoint's function-tool restriction.
	NativeChatFunctionToolsUnsupported = "OPENAI_CHAT_FUNCTION_TOOLS_UNSUPPORTED"
)

// AllowsCompatibleCompact permits Grok or provider-declared OpenAI Compact
// execution targets. Nil targets fail, while the provider policy supplies the
// OpenAI capability decision used by compatible selection.
func AllowsCompatibleCompact(provider *ExecutionProvider) bool {
	return provider != nil && (provider.View().IsGrok() || provider.View().AllowsOpenAICompact())
}

// CompatibleProviderEligible reports whether CompatibleEligibilityReason
// returns no rejection for this request. Selection callers additionally check
// a Spark parent's credential health; that storage lookup is separate from
// these target-local eligibility rules.
func CompatibleProviderEligible(ctx context.Context, provider *ExecutionProvider, platform string, requestedModel string, requireCompact bool, requiredCapability providercore.OpenAIEndpointCapability) bool {
	return CompatibleEligibilityReason(ctx, provider, platform, requestedModel, requireCompact, requiredCapability) == ""
}

// CompatibleEligibilityReason returns the first rejection token, or empty when
// the target passes request eligibility. It checks the optional platform
// constraint and ModelPolicy.Schedulable before quota pause, supported models,
// persistent quota, capability, and Compact.
//
// A schedulable provider failing the first model policy stage is reported as
// model_rate_limited, even if protocol policy caused the failure. Quota pause
// reasons identify their observed window when available. Diagnostics use this
// ordered result to explain selection, and callers retain the separate parent
// credential-health check for shadows.
// @project-doc docs/architecture/provider_scheduling_and_cache.md#advanced_scheduler_selection
func CompatibleEligibilityReason(ctx context.Context, provider *ExecutionProvider, platform string, requestedModel string, requireCompact bool, requiredCapability providercore.OpenAIEndpointCapability) string {
	platform = strings.TrimSpace(platform)
	if provider == nil {
		return "provider_nil"
	}
	if platform != "" && provider.Record.Platform != platform {
		return "platform_mismatch"
	}
	// Eligibility stages short-circuit for stable diagnostic ordering. A healthy
	// record rejected by protocol or model policy uses this existing model-window reason.
	if !ExecutionModelPolicy(provider).Schedulable(ctx, requestedModel) {
		if provider.View().IsSchedulable() {
			return "model_rate_limited"
		}
		return "not_schedulable"
	}
	if provider.View().IsOpenAI() {
		if paused, reason := OpenAIQuotaPause(ctx, provider); paused {
			slog.Debug("provider_auto_paused_by_quota",
				"provider_id", provider.Record.ID,
				"window", reason.Window,
				"threshold", reason.Threshold,
				"utilization", reason.Utilization,
			)
			if reason.Window != "" {
				return "quota_auto_pause_" + reason.Window
			}
			return "quota_auto_pause"
		}
	}
	if provider.View().IsGrok() {
		if paused, reason := GrokQuotaPause(provider); paused {
			slog.Debug("grok_provider_auto_paused_by_quota",
				"provider_id", provider.Record.ID,
				"window", reason.Window,
				"threshold", reason.Threshold,
				"utilization", reason.Utilization,
			)
			if reason.Window != "" {
				return "quota_auto_pause_" + reason.Window
			}
			return "quota_auto_pause"
		}
	}
	if !ExecutionModelPolicy(provider).SupportsCompatibleRouting(ctx, requestedModel) {
		return "model_not_supported"
	}
	if !provider.View().IsSchedulable() || provider.View().IsQuotaExceeded() {
		return "provider_quota_exhausted"
	}
	if !SupportsRequestCapability(ctx, provider, requiredCapability) {
		if provider.View().IsGrok() && requiredCapability == providercore.OpenAIEndpointCapabilityGrokMediaGeneration {
			_, reason := providercore.GrokMediaGenerationEligibility(ExecutionRecord(provider), provideradapter.GrokTierRules())
			slog.Debug("grok_media_provider_ineligible", "provider_id", provider.Record.ID, "reason", reason)
		}
		return "capability_mismatch"
	}
	if requireCompact && !AllowsCompatibleCompact(provider) {
		return "compact_unsupported"
	}
	return ""
}

// CheckNativeChatFunctionTools rejects ordinary function tools on first-party
// GPT-6.2 Chat. The caller supplies an already resolved attempt and its final
// model. A conversion that was selected by group and provider policy is outside
// this check, so skipping a native candidate cannot create a new conversion.
func CheckNativeChatFunctionTools(ctx context.Context, target *ExecutionProvider, model string) error {
	if !requeststate.ChatFunctionToolsRequired(ctx) || target == nil || !target.View().IsOpenAI() ||
		target.Route.Protocol() != protocolcore.ProtocolOpenAIChatCompletions || !modelrules.OpenAIChatFunctionToolsUnsupported(model) {
		return nil
	}
	// The same model ID can belong to a relay that implements Chat function
	// tools itself. Use the selected endpoint's service identity for the check.
	identity := modelrules.Target{
		Platform:     target.Record.Platform,
		ProviderType: target.Record.Type,
		BaseURL:      ExecutionProtocolTarget(target).BaseURL(),
	}
	if identity.ServiceID() != "openai" {
		return nil
	}
	return apperror.BadRequest(NativeChatFunctionToolsUnsupported, "Function tools are not supported for this model on /v1/chat/completions. Use /v1/responses instead.")
}

// OpenAIQuotaPause evaluates current OpenAI quota observations against
// request-bound global thresholds and provider overrides. It uses time.Now for
// window expiry; the provider core returns both pause state and diagnostic
// evidence.
func OpenAIQuotaPause(ctx context.Context, provider *ExecutionProvider) (bool, providercore.QuotaAutoPauseDecision) {
	return evaluateOpenAIQuotaPause(ctx, provider, time.Now())
}

// evaluateOpenAIQuotaPause delegates quota pause using provider platform,
// extras, request thresholds, and now. Nil targets return unpaused with empty
// evidence, leaving provider lookup failure to the caller's eligibility path.
func evaluateOpenAIQuotaPause(ctx context.Context, v *ExecutionProvider, now time.Time) (bool, providercore.QuotaAutoPauseDecision) {
	if v == nil {
		return false, providercore.QuotaAutoPauseDecision{}
	}
	return providercore.EvaluateQuotaAutoPause(v.Record.Platform, v.Record.Extra, QuotaAutoPauseSettings(ctx), now)
}

// WithQuotaAutoPauseSettings stores global five-hour and seven-day thresholds
// in a derived execution-hint context. Nil context becomes Background;
// existing hints survive, and the prior context keeps its own threshold
// values.
func WithQuotaAutoPauseSettings(ctx context.Context, settings providercore.QuotaAutoPauseSettings) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	hints := requeststate.ExecutionHintsFromContext(ctx)
	hints.QuotaAutoPauseThreshold5h = settings.DefaultThreshold5h
	hints.QuotaAutoPauseThreshold7d = settings.DefaultThreshold7d
	return requeststate.WithExecutionHints(ctx, hints)
}

// QuotaAutoPauseSettings extracts both global window thresholds from the
// request execution hints. Missing values remain zero for provider policy to
// interpret; the returned settings value can be used independently of the
// context.
func QuotaAutoPauseSettings(ctx context.Context) providercore.QuotaAutoPauseSettings {
	hints := requeststate.ExecutionHintsFromContext(ctx)
	return providercore.QuotaAutoPauseSettings{DefaultThreshold5h: hints.QuotaAutoPauseThreshold5h, DefaultThreshold7d: hints.QuotaAutoPauseThreshold7d}
}

// GrokQuotaPause delegates provider-specific Grok quota-window evaluation
// using an execution-record copy and the current clock. Provider rules own
// absent observation, threshold, reset, and platform handling for the returned
// decision.
func GrokQuotaPause(value *ExecutionProvider) (bool, providercore.QuotaAutoPauseDecision) {
	return providercore.EvaluateGrokQuotaAutoPause(ExecutionRecord(value), time.Now)
}

// SupportsRequestCapability checks requested endpoint capability using the
// admitted protocol route where that route supplies the relevant
// qualification. Nil targets fail, and noncompatible platforms can qualify
// text through their allowed native or converted protocol.
//
// When no client protocol is stored, that branch tries Messages, Responses,
// and Chat routes against the group. WS and Compact source protocols use route
// permission for text/Responses and additionally require native compaction
// permission for remote compaction v2. Remaining capabilities use provider
// endpoint policy on the current reduced record.
func SupportsRequestCapability(ctx context.Context, provider *ExecutionProvider, capability providercore.OpenAIEndpointCapability) bool {
	if provider == nil {
		return false
	}
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	// Noncompatible platforms can serve text through an allowed conversion route;
	// endpoint checks must follow the admitted protocol rather than require OpenAI HTTP support.
	if !provider.View().IsOpenAICompatible() && (capability == "" || capability == providercore.OpenAIEndpointCapabilityTextGeneration || capability == providercore.OpenAIEndpointCapabilityResponses) {
		policy := ExecutionModelPolicy(provider)
		if source != "" {
			return policy.AllowsProtocol(ctx, "")
		}
		group, _ := requeststate.GroupFromContext(ctx)
		for _, entry := range []protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages, protocolcore.ProtocolOpenAIResponses, protocolcore.ProtocolOpenAIChatCompletions} {
			if _, allowed := policy.ProtocolRoute(group, entry); allowed {
				return true
			}
		}
		return false
	}
	// WS and Compact have separate declared protocols. Their Responses route can
	// qualify even when the provider has no ordinary HTTP text generation capability.
	if source == protocolcore.ProtocolResponsesWebSocket || source == protocolcore.ProtocolResponsesCompact {
		if capability == providercore.OpenAIEndpointCapabilityTextGeneration || capability == providercore.OpenAIEndpointCapabilityResponses {
			return ExecutionModelPolicy(provider).AllowsProtocol(ctx, "")
		}
		if capability == providercore.OpenAIEndpointCapabilityRemoteCompactionV2 {
			return ExecutionModelPolicy(provider).AllowsProtocol(ctx, "") && provider.View().AllowsOpenAINativeCompactionV2()
		}
	}
	return provideradapter.SupportsOpenAIEndpoint(ExecutionProtocolRecord(provider), capability)
}
