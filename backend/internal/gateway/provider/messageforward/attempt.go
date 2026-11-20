package messageforward

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// attempt.go binds Messages preparation and execution to one provider and its
// tool restoration state.
// A Messages attempt can rewrite tool names before building its upstream
// request, and response restoration needs the mapping chosen for that same
// attempt. RewriteTools saves the selected mapping and BindTools exposes it
// through the attempt state before the response reader uses it.
//
// Credentials, proxy, TLS selection, and response handles stay with the
// selected provider while gateway/forward controls preparation order. Health
// calls distinguish retry, failover, synthetic failover, and persistence modes
// using the retained response or supplied evidence. The executor invokes
// synchronous hooks for wire bytes, acceptance, response inspection, and stream
// observations; the outer gateway remains responsible for choosing a
// replacement provider.

// attempt retains preparation, credentials, and response state for one Messages
// exchange.
type attempt struct {
	// s, c, and provider are borrowed through synchronous forwarding; the
	// application and HTTP adapter keep them usable while preparation,
	// exchange, and response callbacks run.
	//
	// shared Messages forwarding runtime
	s *Runtime
	// current HTTP presentation adapter
	c HTTPBoundary
	// selected execution provider state
	provider *gatewayprovider.ExecutionProvider
	// ctx retains the attempt execution context for effort-aware model mapping
	// callbacks invoked without a context parameter.
	ctx context.Context
	// Credential saves token and tokenType even with a resolution error;
	// Transport then selects proxyURL, and Execute consumes those values
	// after callers check the error.
	//
	// token access credential; tokenType credential kind; proxyURL exchange
	// proxy
	token, tokenType, proxyURL string
	// selected transport TLS profile
	profile *tlsfingerprint.Profile
	// toolRewrite is replaced by RewriteTools and later bound into state so
	// request rewriting and response restoration use the same attempt
	// mapping.
	//
	// prepared request tool mapping
	toolRewrite *claude.ToolNameRewrite
	// state is fresh for ordinary attempts; mimic replaces it with the
	// conversion attempt state so prepared tool names reach that conversion
	// output.
	//
	// current preparation state pointer
	state *AttemptState
	// response is saved before the response hook runs, including HTTP
	// failures; the executor closes its current body after successful
	// exchange handling.
	//
	// successful upstream response handle
	response *http.Response
}

// newAttempt creates fresh preparation state and retains the runtime, output,
// and selected provider. Those borrowed references remain usable through
// synchronous forwarding; another provider attempt receives another state
// instance.
func newAttempt(s *Runtime, c HTTPBoundary, provider *gatewayprovider.ExecutionProvider) *attempt {
	return newAttemptWithContext(context.Background(), s, c, provider)
}

// newAttemptWithContext creates fresh preparation state bound to ctx alongside
// the runtime, output, and selected provider so effort-aware mapping helpers
// read the active routing effort even without a planned route.
func newAttemptWithContext(ctx context.Context, s *Runtime, c HTTPBoundary, provider *gatewayprovider.ExecutionProvider) *attempt {
	return &attempt{s: s, c: c, provider: provider, ctx: ctx, state: &AttemptState{PrepareStartedAt: time.Now()}}
}

// input captures provider presence and startup failure policy for the
// forwarding flow. A missing provider leaves provider fields unset, while
// Configured gates diagnostic and status-400 failover options.
func (a *attempt) input() forwardcore.MessageInput {
	in := forwardcore.MessageInput{ProviderPresent: a.provider != nil, HTTPPresent: a.c.Present()}
	if v := a.provider; v != nil {
		in.ProviderID = v.Record.ID
		in.ProviderName = v.Record.Name
		in.ProviderType = v.Record.Type
		in.Platform = v.Record.Platform
		in.OAuth = v.View().IsOAuth()
		in.Passthrough = v.View().IsAnthropicAPIKeyPassthroughEnabled()
		in.Bedrock = v.View().IsBedrock()
	}
	if a.s.options.Configured {
		in.LogErrorBody = a.s.options.LogErrorBody
		in.LogErrorBodyMaxBytes = a.s.options.LogErrorBodyMaxBytes
		in.FailoverOn400 = a.s.options.FailoverOn400
	}
	return in
}

// ShouldEmulate delegates search admission using the selected provider and
// group.
func (a *attempt) ShouldEmulate(ctx context.Context, group *int64, body []byte) bool {
	return a.s.shouldEmulate(ctx, a.provider, group, body)
}

// Emulate delegates parsed Messages search execution and returns its completion
// observations.
func (a *attempt) Emulate(ctx context.Context, p *requeststate.ParsedRequest) (*forwardcore.Result, error) {
	return a.s.emulate(ctx, a.c, a.provider, p)
}

// MappedModel returns the selected effort-aware provider mapping for the
// supplied request model.
func (a *attempt) MappedModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.provider).WithContext(a.ctx).Mapped(model)
}

// ReplaceModel delegates rewriting the request model field in body bytes.
func (a *attempt) ReplaceModel(body []byte, model string) []byte {
	return protocolopenai.ReplaceModelInBody(body, model)
}

// Passthrough delegates native API key execution with this flow input. The
// runtime creates fresh passthrough state and returns partial observations or a
// typed failover error according to that path.
func (a *attempt) Passthrough(ctx context.Context, in forwardcore.PassthroughInput) (*forwardcore.Result, error) {
	return a.s.passthrough(ctx, a.c, a.provider, forwardcore.APIKeyInput{Body: in.Body, Parsed: in.Parsed, RequestModel: in.RequestModel, OriginalModel: in.OriginalModel, RequestStream: in.Stream, StartTime: in.StartedAt})
}

// Bedrock delegates AWS execution using the selected provider and parsed
// request. It shares this attempt beta state and returns the platform result or
// classified failure to the flow.
func (a *attempt) Bedrock(ctx context.Context, p *requeststate.ParsedRequest, start time.Time) (*forwardcore.Result, error) {
	return a.s.bedrock(ctx, a.c, a.state, a.provider, p, start)
}

// Begin enters the optional application attempt barrier and returns its release
// callback. When admission is unavailable it returns an empty release;
// successful callers invoke release after their attempt finishes.
func (a *attempt) Begin() (func(), error) {
	if a.s.dependencies.Enter != nil {
		return a.s.dependencies.Enter()
	}
	return func() {}, nil
}

// Beta evaluates the inbound beta header for the selected model and attempt. A
// blocking error stops preparation; success stores the filter result for reuse
// by later request builders.
func (a *attempt) Beta(ctx context.Context, model string) error {
	return a.s.checkBeta(ctx, a.state, a.provider, a.c.RequestHeaders().Get("anthropic-beta"), model)
}

// DebugOriginal sends the supplied client body and request fields to optional
// diagnostics.
func (a *attempt) DebugOriginal(body []byte, model string, stream bool) {
	if a.s.dependencies.Debug != nil {
		a.s.dependencies.Debug.Snapshot("CLIENT_ORIGINAL", a.c.RequestHeaders(), body, map[string]string{"provider": fmt.Sprintf("%d(%s)", a.provider.Record.ID, a.provider.Record.Name), "provider_type": a.provider.Record.Type, "model": model, "stream": strconv.FormatBool(stream)})
	}
}

// ProviderMappedModel returns the selected effort-aware provider mapping for
// later preparation decisions.
func (a *attempt) ProviderMappedModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.provider).WithContext(a.ctx).Mapped(model)
}

// IsClaudeCode recognizes direct or proxied Claude Code requests from available
// client evidence.
func (a *attempt) IsClaudeCode(ctx context.Context, body []byte, metadata string) bool {
	ua := ""
	if a.c.Present() {
		ua = a.c.RequestHeaders().Get("User-Agent")
	}
	return requeststate.IsClaudeCodeClient(ctx) || clientmeta.IsClaudeCodeClient(ua, metadata) || claude.IsProxiedClaudeCodeRequest(body, metadata)
}

// SystemSettings reads current OAuth system injection choices for this request.
// An absent settings reader enables injection with empty prompt and block
// values, letting the platform helper apply its defaults.
func (a *attempt) SystemSettings(ctx context.Context) (bool, string, string) {
	if a.s.dependencies.Settings == nil {
		return true, "", ""
	}
	return a.s.dependencies.Settings.GetClaudeOAuthSystemPromptInjectionSettings(ctx)
}

// RewriteSystem applies the selected OAuth prompt blocks to the parsed system
// value.
func (a *attempt) RewriteSystem(body []byte, p *requeststate.ParsedRequest, prompt, blocks string) []byte {
	system, _ := p.SystemValue()
	return claude.RewriteSystemForNonClaudeCodeWithPromptBlocks(body, system, prompt, blocks)
}

// Metadata derives missing OAuth metadata after a usable fingerprint lookup for
// the HTTP context. Missing fingerprint data, lookup failure, or enabled
// metadata passthrough returns empty for the caller injection decision.
func (a *attempt) Metadata(ctx context.Context, p *requeststate.ParsedRequest) string {
	if a.s.dependencies.Fingerprint == nil || !a.c.Present() {
		return ""
	}
	fp, err := a.s.dependencies.Fingerprint.GetOrCreateFingerprint(ctx, a.provider.Record.ID, a.c.RequestHeaders())
	// Failed or empty fingerprint lookup yields no injected metadata,
	// leaving existing request identity available to later OAuth
	// preparation rather than failing the attempt.
	if err != nil || fp == nil {
		return ""
	}
	_, mimic, _ := a.s.dependencies.Settings.GetGatewayForwardingSettings(ctx)
	if mimic {
		return ""
	}
	return metadataUserID(p, a.provider, fp)
}

// NormalizeOAuth delegates Anthropic OAuth body normalization using the
// supplied preparation options.
func (a *attempt) NormalizeOAuth(body []byte, o forwardcore.NormalizeOptions) []byte {
	return claude.NormalizeClaudeOAuthRequestBody(body, claude.ClaudeOAuthNormalizeOptions{StripSystemCacheControl: o.StripSystemCacheControl, InjectMetadata: o.InjectMetadata, MetadataUserID: o.MetadataUserID})
}

// RewriteCache delegates the runtime message cache policy for current request
// bytes.
func (a *attempt) RewriteCache(ctx context.Context, body []byte) []byte {
	return a.s.rewriteCache(ctx, body)
}

// RewriteTools stores the selected tool map and returns rewritten bytes when a
// map exists. Nil mapping returns the supplied body and false, letting the
// caller choose its tools-last cache path.
func (a *attempt) RewriteTools(body []byte) ([]byte, bool) {
	a.toolRewrite = claude.BuildToolNameRewriteFromBody(body)
	if a.toolRewrite == nil {
		return body, false
	}
	return claude.ApplyToolNameRewriteToBody(body, a.toolRewrite), true
}

// BindTools assigns the saved rewrite map to response restoration state.
func (a *attempt) BindTools() {
	a.state.ToolNames = a.toolRewrite
}

// ToolsLast delegates the Anthropic tools-last cache breakpoint rewrite for
// request bytes.
func (a *attempt) ToolsLast(body []byte) []byte {
	return claude.ApplyToolsLastCacheBreakpoint(body)
}

// NormalizeDateline delegates provider-qualified date normalization and its
// changed-body flag.
func (a *attempt) NormalizeDateline(ctx context.Context, body []byte) ([]byte, bool) {
	return a.s.normalizeDateline(ctx, a.provider, body)
}

// CacheLimit delegates Anthropic cache breakpoint limit enforcement for
// prepared bytes.
func (a *attempt) CacheLimit(body []byte) []byte {
	return claude.EnforceCacheControlLimit(body)
}

// PlatformModel returns the Anthropic wire identifier for the supplied mapped
// model.
func (a *attempt) PlatformModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.provider).AnthropicUpstream(model)
}

// InjectTTL reports whether current provider and per-Key runtime policy permit
// cache TTL injection for the resolved upstream model.
func (a *attempt) InjectTTL(ctx context.Context, model string) bool {
	return a.s.injectTTL(ctx, a.provider, model)
}

// CacheTTL delegates one-hour TTL injection into Anthropic cache control
// annotations and marks the attempt state so buildPassthroughRequest does not
// repeat the scan.
func (a *attempt) CacheTTL(body []byte) []byte {
	if a.state != nil {
		a.state.CacheTTL1hApplied = true
	}
	return claude.InjectAnthropicCacheControlTTL1h(body)
}

// Credential resolves and saves the selected provider token and credential
// kind. Saved values are assigned with the returned error, so callers check
// that error before Transport and Execute.
func (a *attempt) Credential(ctx context.Context) error {
	token, kind, e := a.s.dependencies.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(a.provider))
	a.token, a.tokenType = token, kind
	return e
}

// Transport selects the attempt proxy and TLS profile from the provider. An
// enabled nonempty custom relay leaves proxy handling with requestURL, while
// ordinary forwarding stores the attached proxy URL.
func (a *attempt) Transport() {
	if a.provider.Record.ProxyID != nil && a.provider.Record.Proxy != nil && (!a.provider.View().IsCustomBaseURLEnabled() || a.provider.View().GetCustomBaseURL() == "") {
		a.proxyURL = a.provider.Record.Proxy.URL()
	}
	a.profile = a.s.requestTLS(a.provider)
	logging.LegacyPrintf("service.gateway", "[Forward] Using provider: ID=%d Name=%s Platform=%s Type=%s TLSFingerprint=%v Proxy=%s", a.provider.Record.ID, a.provider.Record.Name, a.provider.Record.Platform, a.provider.Record.Type, a.profile, a.proxyURL)
}

// FilterSearchHistory delegates model-specific web search history filtering for
// prepared request bytes.
func (a *attempt) FilterSearchHistory(body []byte, model string) []byte {
	return searchtools.FilterWebSearchHistoryBlocks(body, modelidentity.ResolveThinkingProtocol(model) == modelidentity.ThinkingProtocolPassbackRequired)
}

// FilterThinking delegates model-specific thinking block filtering for prepared
// request bytes.
func (a *attempt) FilterThinking(body []byte, model string) []byte {
	return gatewayprovider.FilterThinkingBlocks(body, model)
}

// PassbackThinking reports whether the selected model requires thinking block
// passback.
func (a *attempt) PassbackThinking(model string) bool {
	return modelidentity.ResolveThinkingProtocol(model) == modelidentity.ThinkingProtocolPassbackRequired
}

// NormalizeThinking delegates Chinese-model thinking normalization and its
// changed-body flag.
func (a *attempt) NormalizeThinking(body []byte, model string) ([]byte, bool) {
	return gatewayprovider.NormalizeChineseLLMThinking(body, model)
}

// ReadErrorBody reads bounded diagnostics from the retained response without
// closing it. The caller checks reading errors and owns any close or
// replacement before further health and response processing.
func (a *attempt) ReadErrorBody() ([]byte, error) {
	return a.s.readErrorBody(a.response)
}

// ResetErrorBody closes the retained body and replaces it with the supplied
// diagnostic bytes. The response must exist, and later readers consume those
// bytes from the rebuilt body.
func (a *attempt) ResetErrorBody(body []byte) {
	// Close the consumed network reader before replacing it with captured
	// diagnostics so subsequent health and presentation steps read the same
	// error evidence.
	_ = a.response.Body.Close()
	a.response.Body = io.NopCloser(bytes.NewReader(body))
}

// Health selects retry, failover, synthetic failover, or persistence
// observation for this response mode. Synthetic failover uses supplied headers
// and body, while ordinary retry and failover inspect the retained response.
func (a *attempt) Health(ctx context.Context, mode string, status int, headers map[string][]string, body []byte, model string) forwardcore.ErrorDecision {
	var d providercore.UpstreamErrorDecision
	switch mode {
	case "retry":
		d = a.s.retryHealth(ctx, a.response, a.provider, model)
	case "failover":
		d = a.s.failoverHealth(ctx, a.response, a.provider, model)
	case "failover_synthetic":
		resp := &http.Response{StatusCode: status, Header: http.Header(headers).Clone(), Body: io.NopCloser(bytes.NewReader(body))}
		d = a.s.failoverHealth(ctx, resp, a.provider, model)
		_ = resp.Body.Close()
	default:
		d = providercore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(a.provider), status)
		if mode == "persist" && a.s.dependencies.Health != nil {
			d = gatewayprovider.ApplyExecutionHealth(ctx, a.s.dependencies.Health, a.provider, gatewayprovider.HealthObservationFromContext(ctx, status, headers, body, []string{model}))
		}
	}
	return forwardcore.ErrorDecision{
		Generic:           d.ShouldReturnGenericError(),
		Failover:          d.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(a.provider), status, true),
		RetrySameProvider: d.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(a.provider), status),
	}
}

// HandleError delegates final Messages error handling with the retained
// response and state. The retry flag selects exhausted-retry processing, and
// the supplied model informs health observation before output presentation.
func (a *attempt) HandleError(ctx context.Context, model string, retry bool) (*forwardcore.Result, error) {
	return a.s.handleError(ctx, a.c, a.state, a.provider, a.response, retry, model)
}

// Observe delegates one upstream notice to the current HTTP observation
// adapter.
func (a *attempt) Observe(n forwardcore.Notice) {
	a.c.Observe(n)
}

// Failover400 reports whether the error body contains recognized Messages
// compatibility failures.
func (a *attempt) Failover400(body []byte) bool { return messageFailover400(body) }

// FailoverError returns an upstream failure signal for outer provider
// orchestration. Response bytes remain shared with its input, and retry carries
// the caller eligibility for same-provider replay.
func (a *attempt) FailoverError(status int, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameProvider: retry}
}

// IsFailover reports whether the error chain contains an UpstreamFailoverError.
func (a *attempt) IsFailover(err error) bool {
	var v *forwardcore.UpstreamFailoverError
	return errors.As(err, &v)
}

// Truncate delegates a UTF-8 diagnostic prefix capped by the supplied byte
// limit.
func (a *attempt) Truncate(value string, n int) string {
	return logredact.TruncateUTF8(value, n)
}

// TruncateBytes delegates bounded one-line diagnostic formatting for supplied
// body bytes.
func (a *attempt) TruncateBytes(body []byte, n int) string {
	return logredact.TruncateLine(body, n)
}

// Sanitize removes sensitive upstream query values from supplied diagnostic
// text.
func (a *attempt) Sanitize(value string) string {
	return logredact.SanitizeUpstreamQueries(value)
}

// Log writes the supplied diagnostic message under the gateway logging
// component.
func (a *attempt) Log(value string) {
	logging.LegacyPrintf("service.gateway", "%s", value)
}

// Execute delegates one prepared Messages exchange to the Anthropic executor.
// Hooks synchronously observe wire bytes, acceptance, response inspection, and
// stream usage; the executor owns successful response closure before returning.
func (a *attempt) Execute(ctx context.Context, in forwardcore.MessageExecution) (upstream.AttemptResult, error) {
	exchange := a.s.exchangeOptions(a.c, a.state, a.provider, a.token, a.tokenType, in.Model, in.Stream, in.Mimic, a.proxyURL, a.profile, in.ReplaceBody)
	target := &claude.Target{
		ProviderID: a.provider.Record.ID, Model: in.Model, Exchange: exchange, Response: a.s.responseOptions(ctx, a.c, a.state, a.provider, in.Model, false), StartedAt: in.StartedAt, MimicClaudeCode: in.Mimic,
		BeforeResponse: func(ctx context.Context, resp *http.Response, wire []byte) (bool, error) {
			a.response = resp
			return in.Hooks.Before(ctx, &forwardcore.ExchangeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, RequestID: resp.Header.Get("x-request-id")}, wire)
		},
		OnWireBody: in.Hooks.Wire, Accepted: in.Hooks.Accepted, BeforeStream: in.Hooks.BeforeStream,
		OnStream: func(r *claude.StreamResult, _ error) {
			if r != nil {
				in.Hooks.Stream(&forwardcore.StreamOutcome{Usage: r.Usage, Timings: timing.PerformanceFromContext(ctx), ClientDisconnect: r.ClientDisconnect})
			}
		},
	}
	return (claude.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocolcore.ProtocolAnthropicMessages, Body: in.Body, ResponseModel: in.OriginalModel, Stream: in.Stream, Target: target}, a.c.Sink())
}

// StreamError extracts raw Anthropic error-event data from an error chain. The
// boolean distinguishes a recognized typed event, including empty data, from an
// unrelated failure for outer handling.
func (a *attempt) StreamError(err error) (string, bool) {
	if e, ok := errors.AsType[*claude.StreamErrorEventError](err); ok {
		return e.RawData, true
	}
	return "", false
}

// Size returns the heartbeat-adjusted response byte count for this attempt.
func (a *attempt) Size() int { return a.c.Size() }

// Written reports whether the attempt HTTP writer has begun its response.
func (a *attempt) Written() bool { return a.c.Written() }

// GenericError delegates terminal generic Messages presentation to the current
// HTTP adapter.
func (a *attempt) GenericError() {
	a.c.GenericError()
}

// ServiceTier returns the tier selected by the current HTTP response observer.
func (a *attempt) ServiceTier() string {
	return a.c.ServiceTier()
}
