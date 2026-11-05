package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// gateway_response_output.go constructs OpenAI response handling with shared
// health, session state, and header policy.
// OpenAI output shares provider health, reasoning history, response lineage,
// and the proxy stream circuit. Its startup options retain platform stream
// budgets and a default response-read limit. The compatible provider selector
// supplies the continuation lifetime used when recording response lineage.

// provideOpenAIResponseHealth combines shared containment, transient models,
// and deferred writes.
func provideOpenAIResponseHealth(observer *provideradapter.UpstreamHealth, blocks *provider.RuntimeBlockState, models *provider.ModelTransientState, deferred *provider.DeferredService) *provideradapter.OpenAIResponseHealth {
	return &provideradapter.OpenAIResponseHealth{Health: observer, Runtime: blocks, ModelTransient: models, Deferred: deferred}
}

// provideOpenAIResponseOutput constructs response handling around the shared
// health and session services. Nil cfg uses the default read bytes; positive
// configured bytes replace them. Identity supplies redaction, and choices
// supplies the response-lineage lifetime callback used during completion.
func provideOpenAIResponseOutput(cfg *config.Config, health *provideradapter.OpenAIResponseHealth, grok *provideradapter.GrokHealth, observer *provideradapter.UpstreamHealth, headers *egress.CompiledHeaderFilter, turns *gatewayhttp.CodexTurnStateHeaders, circuit *egress.ProxyStreamCircuit, responses session.OpenAIWSStateStore, choices *selection.Compatible, history *session.ReasoningHistory, identity *gatewayadapter.ExecutionAgentIdentity) *gatewayhttp.OpenAIResponseOutput {
	output := &gatewayhttp.OpenAIResponseOutput{
		Reasoning: history, Redact: identity.Redact, Health: health, GrokHealth: grok, Observer: observer, Headers: headers, Turns: turns,
		Corrector: openai.NewCodexToolCorrector(), ProxyCircuit: circuit, Responses: responses,
		ResponseTTL: choices.OpenAIHTTPResponseStickyTTL,
		Options:     gatewayhttp.OpenAIResponseOptions{ReadLimit: config.DefaultUpstreamResponseReadMaxBytes},
	}
	if cfg != nil {
		v := cfg.Gateway
		output.Options.Configured = true
		output.Options.MaxLineSize = v.MaxSSELineBytes
		output.Options.StreamDataIntervalTimeout = v.Streaming.DataIdleTimeoutSeconds
		output.Options.ImageStreamDataIntervalTimeout = v.Images.StreamDataIdleTimeoutSeconds
		output.Options.ImageStreamKeepaliveInterval = v.Images.StreamKeepaliveIntervalSeconds
		output.Options.OpenAIFirstOutputTimeoutSeconds = v.OpenAI.FirstOutputTimeoutSeconds
		output.Options.OpenAIHighEffortFirstOutputTimeoutSeconds = v.OpenAI.HighEffortFirstOutputTimeoutSeconds
		output.Options.LogUpstreamErrorBody = v.UpstreamErrorBodyLog.Enabled
		output.Options.LogUpstreamErrorBodyMaxBytes = v.UpstreamErrorBodyLog.MaxBytes
		output.Options.ResponseHeadersEnabled = cfg.Gateway.ResponseHeaders.Enabled
		if v.UpstreamResponseMaxBytes > 0 {
			output.Options.ReadLimit = v.UpstreamResponseMaxBytes
		}
	}
	return output
}

// provideReasoningHistory binds the gateway cache's optional reasoning-content
// capability. Failed capability assertion leaves Cache nil, which the reasoning
// history adapter receives together with its cache-failure warning callback.
func provideReasoningHistory(cache session.GatewayCache) *session.ReasoningHistory {
	store, _ := cache.(session.ReasoningContentCache)
	return &session.ReasoningHistory{Cache: store, Warn: gatewayadapter.WarnReasoningCacheFailure}
}

// provideOpenAIResponseState constructs shared HTTP and WS continuation state
// from cache.
func provideOpenAIResponseState(cache session.GatewayCache) session.OpenAIWSStateStore {
	return session.NewOpenAIWSStateStore(cache, gatewayadapter.LogOpenAIWSModeInfo)
}

// provideResponseHeaderFilter compiles response-header policy or returns nil
// for missing cfg. Configured allow and removal slices are consumed by
// CompileHeaderFilter, whose compiled result is independent of later changes
// to those input slices.
func provideResponseHeaderFilter(cfg *config.Config) *egress.CompiledHeaderFilter {
	if cfg == nil {
		return nil
	}
	return egress.CompileHeaderFilter(egress.ResponseHeaderOptions{Enabled: cfg.Gateway.ResponseHeaders.Enabled, AdditionalAllowed: cfg.Gateway.ResponseHeaders.AdditionalAllowed, ForceRemove: cfg.Gateway.ResponseHeaders.ForceRemove})
}
