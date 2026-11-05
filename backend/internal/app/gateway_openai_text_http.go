package app

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// gateway_openai_text_http.go constructs OpenAI text handlers and shared
// concurrency and image admission resources.
// OpenAI text endpoints share user slots and a local image limiter with media
// and WS handlers. Assembly keeps image admission options beside those
// resources, then binds route planning, session ownership, prompt policy, and
// protocol-specific Responses loop options.

// provideOpenAIHTTPResources constructs shared concurrency and local image
// admission resources. Nil cfg leaves zero ping duration and nil image options;
// supplied ping seconds configure the helper, while the image limiter starts
// with its zero state.
func provideOpenAIHTTPResources(concurrency *scheduler.ConcurrencyService, cfg *config.Config) *gatewayhttp.OpenAIHTTPResources {
	ping := time.Duration(0)
	imageOptions := openAIImageAdmissionOptions(cfg)
	if cfg != nil {
		// Waiting response heartbeats use the configured ping interval in seconds.
		ping = time.Duration(cfg.Gateway.Streaming.WaitPingIntervalSeconds) * time.Second
	}
	return &gatewayhttp.OpenAIHTTPResources{Concurrency: gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatComment, ping), Images: &scheduler.ImageConcurrencyLimiter{}, ImageOptions: imageOptions}
}

// openAIImageAdmissionOptions returns nil for absent configuration or copied
// image settings. OverflowMode enables waiting only after trimming matches the
// configured wait constant, and timeout seconds become a duration for
// admission.
func openAIImageAdmissionOptions(cfg *config.Config) *gatewayhttp.OpenAIImageAdmissionOptions {
	if cfg == nil {
		return nil
	}
	value := cfg.Gateway.Images.Concurrency
	// Image wait timeout is expressed in configuration seconds and consumed as a duration.
	return &gatewayhttp.OpenAIImageAdmissionOptions{Enabled: value.Enabled, Limit: value.MaxConcurrentRequests, Wait: strings.TrimSpace(value.OverflowMode) == config.ImageConcurrencyOverflowModeWait, Timeout: time.Duration(value.WaitTimeoutSeconds) * time.Second, MaxWaiting: value.MaxWaitingRequests}
}

// provideOpenAITextHTTP assembles OpenAI text preparation around the attempt
// runtime. It takes client-version reads from messages when present, enables
// the first-output budget on the applicable Responses loop, and requires
// activity for application request tracking.
func provideOpenAITextHTTP(
	source *gatewayhttp.OpenAIResponsesExecutor,
	funding *admission.FundingAdmission,
	keys *apikey.APIKeyService,
	resources *gatewayhttp.OpenAIHTTPResources,
	cyber *gatewayhttp.CyberHandler,
	rules *errorpolicy.ErrorPassthroughService,
	moderator *moderation.ContentModerationService,
	prompts *promptpolicy.Service,
	cfg *config.Config,
	runtime *openaiattempt.Runtime,
	activity *gatewayRequestActivity,
	planner *gatewayadapter.RoutePlanner, cache session.GatewayCache,
	messages *messageHTTPBindings,
	subscriptions *billing.SubscriptionService,
) *gatewayhttp.OpenAITextHandler {
	options := openAITextOptions(cfg)
	bindings := openAITextBindings(source, funding, keys, resources, cyber, rules, moderator, planner, cache, subscriptions)
	if messages != nil {
		bindings.ClientVersions = messages.bindings.ClientVersions
	}
	result := gatewayhttp.NewBoundOpenAITextHandler(options, bindings, prompts, textflow.NewResponsesExecutor(runtime, textflow.ResponseOptions{MaxSwitches: options.MaxSwitches}, textflow.ResponseOptions{MaxSwitches: options.MaxSwitches, FirstOutputBudget: true}))
	result.BindRequestActivity(activity.Enter)
	return result
}

// openAITextOptions captures body limits and a default three-switch budget.
// Positive configured switches replace the default, positive keepalive seconds
// become a Compact duration, and ForceCodexCLI is copied from startup
// configuration.
func openAITextOptions(cfg *config.Config) gatewayhttp.OpenAITextOptions {
	options := gatewayhttp.OpenAITextOptions{MaxSwitches: 3}
	if cfg != nil {
		options.ForceCodexCLI = cfg.Gateway.OpenAI.Codex.ForceCLI
		options.MaxBodyBytes = cfg.Gateway.MaxRequestBodyBytes
		if cfg.Gateway.Failover.MaxProviderSwitches > 0 {
			options.MaxSwitches = cfg.Gateway.Failover.MaxProviderSwitches
		}
		if cfg.Gateway.Streaming.KeepaliveIntervalSeconds > 0 {
			// Compact heartbeats use the positive keepalive interval in configured seconds.
			options.CompactKeepaliveInterval = time.Duration(cfg.Gateway.Streaming.KeepaliveIntervalSeconds) * time.Second
		}
	}
	return options
}

// openAITextBindings binds preparation, owner lookup, admission, and
// destination fallback. Dependency flags let the HTTP handler reject missing
// services; owner and planning callbacks require source.Lineage and planner
// when invoked after those checks.
func openAITextBindings(source *gatewayhttp.OpenAIResponsesExecutor, funding *admission.FundingAdmission, keys *apikey.APIKeyService, resources *gatewayhttp.OpenAIHTTPResources, cyber *gatewayhttp.CyberHandler, rules *errorpolicy.ErrorPassthroughService, moderator *moderation.ContentModerationService, planner *gatewayadapter.RoutePlanner, cache session.GatewayCache, subscriptions *billing.SubscriptionService) gatewayhttp.OpenAITextBindings {
	var moderationPort gatewayhttp.ModerationPort
	if moderator != nil {
		moderationPort = moderator
	}
	bindings := gatewayhttp.OpenAITextBindings{
		Dependencies: gatewayhttp.OpenAIDependencies{
			Handler:     true,
			Gateway:     source != nil,
			Funding:     funding != nil,
			Keys:        keys != nil,
			Concurrency: resources != nil && resources.Concurrency != nil && resources.Concurrency.Service() != nil,
		},
		Resources:     resources,
		ResponseOwner: func() session.HTTPResponseOwnerReader { return source.Lineage.Store },
		Moderation:    moderationPort,
		PlanRoute: func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			return planner.PlanKey(ctx, key, model)
		},
		ReplaceModel:        openaiwire.ReplaceModelInBody,
		ClientGroupFallback: provideClientGroupFallbackResolver(keys, funding, subscriptions, cache),
		Errors:              rules,
		Funding:             funding,
		Cyber:               cyber,
		IsolateSession:      messageSessionIsolation(cache),
	}
	return bindings
}
