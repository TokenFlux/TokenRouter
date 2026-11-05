package app

import (
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// gateway_messages_http.go constructs Messages, compatible text, and Gemini
// HTTP handlers with shared preparation dependencies.
// Messages and Gemini HTTP handlers share prompt policy, route planning, and
// concurrency helpers. Their execution options differ for partial failure and
// cancellation, so each handler constructs the appropriate text loop around the
// same attempt runtime and activity barrier.

// messageHTTPBindings retains shared preparation dependencies for Messages-
// family HTTP handlers.
type messageHTTPBindings struct {
	// options and bindings are copied into each handler; prompt and concurrency
	// are shared pointers, keeping prompt-cache and slot behavior common across those handlers.
	options     gatewayhttp.MessagesHTTPOptions // startup request limits
	bindings    gatewayhttp.MessagesBindings    // shared preparation callbacks
	prompt      *promptpolicy.Service           // cached prompt policy
	concurrency *gatewayhttp.ConcurrencyHelper  // user slot helper
}

// provideMessageHTTPBindings constructs shared Messages preparation and
// concurrency helpers. The switch defaults are ten and three for Gemini;
// positive configuration overrides them, while configured body bytes and ping
// seconds are passed through to the handlers.
func provideMessageHTTPBindings(
	planner *gatewayprovider.RoutePlanner,
	cache session.GatewayCache,
	shared *schedulerSharedState,
	funding *admission.FundingAdmission,
	rules *errorpolicy.ErrorPassthroughService,
	moderationService *moderation.ContentModerationService,
	settings *gateway.RuntimeSettings,
	prompts *promptpolicy.Service,
	concurrency *scheduler.ConcurrencyService,
	cfg *config.Config, choices *selection.Generic,
	keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService,
) *messageHTTPBindings {
	options := gatewayhttp.MessagesHTTPOptions{MaxSwitches: 10, MaxGeminiSwitches: 3}
	ping := time.Duration(0)
	if cfg != nil {
		options.MaxBodyBytes = cfg.Gateway.MaxRequestBodyBytes
		options.ForceCodexCLI = cfg.Gateway.OpenAI.Codex.ForceCLI
		// Waiting handlers emit pings using the configured interval in seconds.
		ping = time.Duration(cfg.Gateway.Streaming.WaitPingIntervalSeconds) * time.Second
		if cfg.Gateway.Failover.MaxProviderSwitches > 0 {
			options.MaxSwitches = cfg.Gateway.Failover.MaxProviderSwitches
		}
		if cfg.Gateway.Failover.MaxProviderSwitchesGemini > 0 {
			options.MaxGeminiSwitches = cfg.Gateway.Failover.MaxProviderSwitchesGemini
		}
	}
	var moderationPort gatewayhttp.ModerationPort
	if moderationService != nil {
		moderationPort = moderationService
	}
	bindings := gatewayhttp.MessagesBindings{
		PlanRoute:           planner.PlanKey,
		ClientGroupFallback: provideClientGroupFallbackResolver(keys, funding, subscriptions, cache),
		ClientVersions:      settings.GetClaudeCodeVersionBounds, Funding: funding, Moderation: moderationPort, Errors: rules,
		IsolateSession: messageSessionIsolation(cache), CachedSession: choices.GetCachedSessionProviderID,
		ObserveCompatibility: func(log *zap.Logger) {
			gatewayhttp.LogCompatibilityFallback(log, func() gatewayhttp.CompatibilityLogSnapshot {
				return gatewayCompatibilitySnapshot(shared)
			})
		},
	}
	return &messageHTTPBindings{options: options, bindings: bindings, prompt: prompts, concurrency: gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatClaude, ping)}
}

// provideMessagesHTTP constructs Messages HTTP with the shared bindings and
// activity barrier. The primary text loop enables partial-failure completion,
// while its Gemini loop uses the separately configured switch budget.
func provideMessagesHTTP(
	shared *messageHTTPBindings,
	runtime *textattempt.Runtime,
	activity *gatewayRequestActivity,
) *gatewayhttp.MessagesHandler {
	result := gatewayhttp.NewBoundMessagesHandler(
		shared.options,
		shared.bindings,
		shared.prompt,
		shared.concurrency,
		textflow.NewMessagesExecutor(
			runtime,
			textflow.MessageOptions{
				MaxSwitches:            shared.options.MaxSwitches,
				CompletePartialFailure: true,
				Observe:                telemetry.Failover,
			},
			textflow.MessageOptions{
				MaxSwitches: shared.options.MaxGeminiSwitches,
				Observe:     telemetry.Failover,
			},
		),
	)
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideCompatibleTextHTTP constructs compatible text handlers using shared
// Messages dependencies. Both text loops stop on canceled context, model
// replacement uses the OpenAI wire helper, and activity tracks each request
// through the application barrier.
func provideCompatibleTextHTTP(
	shared *messageHTTPBindings,
	runtime *textattempt.Runtime,
	activity *gatewayRequestActivity,
) *gatewayhttp.CompatibleTextHandler {
	result := gatewayhttp.NewBoundCompatibleTextHandler(
		shared.options,
		shared.bindings,
		openaiwire.ReplaceModelInBody,
		shared.prompt,
		shared.concurrency,
		textflow.NewMessagesExecutor(
			runtime,
			textflow.MessageOptions{
				MaxSwitches:           shared.options.MaxSwitches,
				StopOnCanceledContext: true,
				Observe:               telemetry.Failover,
			},
			textflow.MessageOptions{
				MaxSwitches:           shared.options.MaxGeminiSwitches,
				StopOnCanceledContext: true,
				Observe:               telemetry.Failover,
			},
		),
	)
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideGeminiNativeHTTP constructs Gemini native handlers with shared
// preparation and request activity. It adds safe model-path checks, shared
// digest lookup, sticky binding, and generated session IDs; both execution
// loops use the Gemini switch budget.
func provideGeminiNativeHTTP(
	shared *messageHTTPBindings,
	digest *session.DigestSessionStore,
	runtime *textattempt.Runtime,
	activity *gatewayRequestActivity, choices *selection.Generic,
) *gatewayhttp.GeminiNativeHandler {
	result := gatewayhttp.NewBoundGeminiNativeHandler(
		gatewayhttp.GeminiNativeOptions{
			MaxSwitches: shared.options.MaxGeminiSwitches,
		},
		shared.bindings,
		gatewayhttp.GeminiHTTPBindings{
			SafeModelSegment: gemini.IsSafeGeminiModelPathSegment,
			FindSession:      messageDigestFind(digest),
			BindSticky:       choices.BindStickySession,
		},
		shared.prompt,
		shared.concurrency,
		func() string { return uuid.New().String() },
		textflow.NewMessagesExecutor(
			runtime,
			textflow.MessageOptions{
				MaxSwitches: shared.options.MaxGeminiSwitches,
				Observe:     telemetry.Failover,
			},
			textflow.MessageOptions{
				MaxSwitches: shared.options.MaxGeminiSwitches,
				Observe:     telemetry.Failover,
			},
		),
	)
	result.BindRequestActivity(activity.Enter)
	return result
}
