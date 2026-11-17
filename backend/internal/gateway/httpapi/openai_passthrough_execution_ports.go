package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/compact"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_passthrough_execution_ports.go binds Responses passthrough execution
// to HTTP policy, recovery and response state.
// Passthrough preparation applies required model, credential and compatibility
// policy while retaining raw request bytes for the selected provider.
// Request-local alias and fingerprint state follows the body adopted by the
// runner, and synchronous HTTP output uses the same provider and error
// observations.
//
// Rejected-field recovery separates body deduplication from retry allowance:
// each provider attempt starts a new body history, while states on one Gin
// request reuse a shared budget. That budget is initialized by the sequential
// request runner and later protected by the upstream retry types. Compact,
// Agent Identity and field repairs remain distinct recovery decisions.
// The output sink records turn-state provenance only while response metadata
// remains applicable. The runner owns each body across retries and final output.

// openAIResponsesRejectedFieldRetryBudgetContextKey identifies the request-wide repair allowance
// reused by per-provider body-deduplication states during passthrough recovery.
const openAIResponsesRejectedFieldRetryBudgetContextKey = "openai_responses_rejected_field_retry_budget"

// openAIPassthroughExecutionAdapter binds passthrough Responses execution to
// shared text attempt services.
type openAIPassthroughExecutionAdapter struct {
	// openAIMessagesExecutionAdapter shares provider, HTTP state and services
	// with this passthrough call until its synchronous response handling finishes.
	*openAIMessagesExecutionAdapter
}

// Profile describes the selected provider and shadow state for passthrough
// execution.
func (p *openAIPassthroughExecutionAdapter) Profile() forward.MessagesProfile {
	return forward.MessagesProfile{Profile: openAIForwardProfile(p.provider), ID: p.provider.Record.ID, Shadow: p.provider.View().IsShadow()}
}

// CompactPath reports whether this request uses the legacy Responses Compact
// path.
func (p *openAIPassthroughExecutionAdapter) CompactPath() bool {
	return IsOpenAIResponsesCompactPath(p.c)
}

// ForwardModel applies Compact model configuration before ordinary single-hop
// upstream model policy. A nonempty Compact executor result wins; when
// unavailable or empty, the provider model helper resolves the supplied model
// and Compact flag.
func (p *openAIPassthroughExecutionAdapter) ForwardModel(model string, compact bool) string {
	if compact && p.s.Compact != nil {
		if configured := p.s.Compact.ResolveModel(p.provider, model); configured != "" {
			return configured
		}
	}
	// An absent executor override still leaves the provider's own Compact mapping
	// applicable. Resolve through its model policy to retain dedicated mapping
	// and raw-Chat normalization order.
	return gatewayadapter.ExecutionModelPolicy(p.provider).OpenAIUpstream(model, compact)
}

// InstructionsRejection delegates Codex passthrough instruction-validation
// reason extraction from raw body and model.
func (p *openAIPassthroughExecutionAdapter) InstructionsRejection(model string, body []byte) string {
	return openai.DetectOpenAIPassthroughInstructionsRejectReason(model, body)
}

// PolicyDenied marks this request as locally limited by provider admission
// policy.
func (p *openAIPassthroughExecutionAdapter) PolicyDenied() {
	MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
}

// LogInstructionsRejected records this attempt's provider and request
// instruction-rejection diagnostics.
func (p *openAIPassthroughExecutionAdapter) LogInstructionsRejected(ctx context.Context, model, reason string, body []byte) {
	logOpenAIPassthroughInstructionsRejected(ctx, p.c, p.provider, model, reason, body)
}

// Reject writes the passthrough request rejection with its optional parameter
// field.
func (p *openAIPassthroughExecutionAdapter) Reject(status int, kind, message, param string) {
	WriteOpenAIForwardRejection(p.c, status, kind, message, param)
}

// CodexModel reports whether the supplied model qualifies for Codex
// compatibility handling.
func (p *openAIPassthroughExecutionAdapter) CodexModel(model string) bool {
	return openai.IsOpenAICodexModel(model)
}

// OAuthBody delegates raw OAuth passthrough normalization with the current
// Compact flag. Updated bytes and change state return to RunPassthrough;
// normalization errors stop the request before the exchange loop starts.
func (p *openAIPassthroughExecutionAdapter) OAuthBody(body []byte, compact bool) ([]byte, bool, error) {
	return openai.NormalizeOpenAIPassthroughOAuthBody(body, compact)
}

// ProviderIdentityRaw rewrites raw client metadata with the current Codex
// namespace and API-key ID. It returns bytes, change state and errors to
// preparation, retaining the same identity source later used for request
// headers.
func (p *openAIPassthroughExecutionAdapter) ProviderIdentityRaw(body []byte) ([]byte, bool, error) {
	return openai.ApplyCodexProviderIdentityClientMetadataRaw(body, provideradapter.CodexIdentityNamespace(CodexIdentityRecord(p.c, p.provider.View())), APIKeyIDFromContext(p.c))
}

// StageFingerprint replaces request-local fingerprint state with the supplied
// attempt IDs.
func (p *openAIPassthroughExecutionAdapter) StageFingerprint(ids *openai.FingerprintIDs) {
	StageCodexFingerprintIDs(p.c, ids)
}

// Fingerprint derives IDs from this provider and inbound headers for the
// current attempt. A missing HTTP request supplies nil headers to the shared
// derivation helper, and later preparation decides where those returned IDs
// are staged.
func (p *openAIPassthroughExecutionAdapter) Fingerprint() *openai.FingerprintIDs {
	var headers http.Header
	if p.c != nil && p.c.Request != nil {
		headers = p.c.Request.Header
	}
	return provideradapter.CodexFingerprintIDsFromRequest(p.provider.View(), headers)
}

// FingerprintBody delegates raw metadata rewriting using the supplied
// fingerprint IDs. Callers use the same IDs for outgoing headers, and
// preparation errors prevent adoption of an incompatible metadata
// representation before forwarding.
func (p *openAIPassthroughExecutionAdapter) FingerprintBody(body []byte, ids *openai.FingerprintIDs) ([]byte, bool, error) {
	return openai.ApplyCodexFingerprintClientMetadataRaw(body, ids)
}

// HasContext reports whether this adapter holds an active Gin request context.
func (p *openAIPassthroughExecutionAdapter) HasContext() bool {
	return p.c != nil
}

// LiteHeader reports whether the inbound request header selects Responses
// Lite.
func (p *openAIPassthroughExecutionAdapter) LiteHeader() bool {
	return gatewayadapter.ImageIntent().IsOpenAIResponsesLiteHeader(p.c.GetHeader(media.ResponsesLiteHeader))
}

// LitePayloadFlag reports whether raw input declares the Responses Lite
// payload flag.
func (p *openAIPassthroughExecutionAdapter) LitePayloadFlag(body []byte) bool {
	return gatewayadapter.ImageIntent().IsOpenAIResponsesLiteWebSocketPayload(body)
}

// CompatibilityBody delegates credential-aware Responses compatibility
// normalization for raw passthrough bytes. The shared helper orders its
// preparation stages and returns original input on error, so RunPassthrough
// adopts only a successful transformed request.
func (p *openAIPassthroughExecutionAdapter) CompatibilityBody(body []byte, lite bool) ([]byte, bool, error) {
	return gatewayadapter.NormalizeOpenAIResponsesWebSocketCompatibilityBody(body, gatewayadapter.ExecutionProtocolRecord(p.provider), lite)
}

// CompatibilityBodyWithSkip runs credential-aware Responses compatibility
// normalization while skipping stages already completed in PreparePrelude on the
// identical input or tool schema bytes.
func (p *openAIPassthroughExecutionAdapter) CompatibilityBodyWithSkip(body []byte, lite bool, skipToolSchemasPlatform string, skipReasoningReplay, skipInputItemIDs bool) ([]byte, bool, error) {
	return gatewayadapter.NormalizeOpenAIResponsesCompatibilityBodyWithOptions(
		body,
		gatewayadapter.ExecutionProtocolRecord(p.provider),
		lite,
		gatewayadapter.ResponsesCompatibilitySkipOptions{
			SkipReasoningContentReplay: skipReasoningReplay,
			SkipInputItemIDs:           skipInputItemIDs,
			SkipToolSchemasPlatform:    skipToolSchemasPlatform,
		},
	)
}

// ReservedToolNames delegates reserved OAuth function-name aliasing with its
// restoration mapping. It returns change state and any collision or parsing
// error; the passthrough runner merges the mapping only after successful
// preparation.
func (p *openAIPassthroughExecutionAdapter) ReservedToolNames(body []byte) ([]byte, map[string]string, bool, error) {
	return openai.AliasOpenAIOAuthReservedToolNamesBody(body)
}

// MergeToolNames merges this preparation's reverse aliases into request-local
// Codex tool state.
func (p *openAIPassthroughExecutionAdapter) MergeToolNames(value map[string]string) {
	MergeCodexToolNameReverse(p.c, value)
}

// NeedsClientTools reports whether raw Responses input contains tools
// requiring client adaptation.
func (p *openAIPassthroughExecutionAdapter) NeedsClientTools(body []byte) bool {
	return protocolbridge.NeedsOpenAIResponsesClientToolAdaptation(body)
}

// AdaptClientTools lowers client tool nodes and publishes their restoration
// mapping on success. An error leaves current mapping state unchanged; callers
// clear prior attempt mappings before preparation to keep the later response
// associated with adopted bytes.
func (p *openAIPassthroughExecutionAdapter) AdaptClientTools(body []byte) ([]byte, error) {
	updated, mapping, err := protocolbridge.AdaptOpenAIResponsesClientTools(body)
	// Bind reverse names only after successful lowering; the response mapping
	// must describe the prepared tool body the passthrough runner actually adopts.
	if err == nil {
		SetOpenAIResponsesClientToolMapping(p.c, mapping)
	}
	return updated, err
}

// NormalizeLite delegates provider-specific Responses Lite request
// normalization for passthrough. Its change result determines adoption, and
// errors return to the runner before a request with unsupported Lite controls
// can be sent.
func (p *openAIPassthroughExecutionAdapter) NormalizeLite(body []byte) ([]byte, bool, error) {
	return gatewayadapter.NormalizeResponsesLiteForProvider(p.provider.View(), body)
}

// ApplyFastPass applies the selected provider's fast-tier policy to prepared
// passthrough bytes. A BlockedError writes the dedicated heartbeat-aware
// rejection, and the runner returns the error before entering its upstream
// exchange loop.
func (p *openAIPassthroughExecutionAdapter) ApplyFastPass(ctx context.Context, model string, body []byte) ([]byte, error) {
	updated, err := tierpolicy.ApplyBody(body, p.s.FastPolicy.Input(ctx, p.provider, model))
	if blocked, ok := errors.AsType[*tierpolicy.BlockedError](err); ok {
		WriteFastPolicyBlockedResponse(p.c, blocked)
	}
	return updated, err
}

// ImageIntent resolves passthrough image intent from canonical input, policy
// and current attempt bytes. The invalidated flag permits the shared resolver
// to reconsider mapped intent without replacing the original canonical cache
// decision.
func (p *openAIPassthroughExecutionAdapter) ImageIntent(model string, canonical []byte, policy string, body []byte, invalidated bool) bool {
	return ResolveOpenAIPassthroughImageIntent(p.c, model, canonical, policy, body, invalidated, gatewayadapter.ImageIntent().IsImageGenerationIntent)
}

// ExplicitImageIntent delegates client-declared image detection at the
// Responses endpoint.
func (p *openAIPassthroughExecutionAdapter) ExplicitImageIntent(model string, body []byte) bool {
	return gatewayadapter.ImageIntent().IsExplicitImageGenerationIntent(media.OpenAIResponsesEndpoint, model, body)
}

// ImageAllowed reports image admission for the current request's effective key
// group.
func (p *openAIPassthroughExecutionAdapter) ImageAllowed() bool {
	return routing.GroupAllowsResponsesImages(openAIRequestGroup(p.c))
}

// FeatureDenied records local image-feature limitation on this request for
// Ops.
func (p *openAIPassthroughExecutionAdapter) FeatureDenied() {
	MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalFeatureGate)
}

// ImagePermissionMessage returns the shared client message for denied image
// generation.
func (p *openAIPassthroughExecutionAdapter) ImagePermissionMessage() string {
	return media.ImageGenerationPermissionMessage
}

// ImageBilling resolves image model and size metadata from prepared Responses
// bytes. It translates the provider helper's detailed result into runner
// fields. Capability failures receive a local protocol error with their
// structured reason before passthrough stops the upstream exchange.
func (p *openAIPassthroughExecutionAdapter) ImageBilling(body []byte, model string) (forward.ImageBilling, error) {
	v, err := gatewayadapter.ResolveResponsesImageBilling(p.c.Request.Context(), p.provider, body, p.s.modelAttributes())
	if err != nil {
		WriteOpenAIModelAttributeError(p.c, err, p.c.Writer.Written())
	}
	return forward.ImageBilling{Model: v.Model, SizeTier: v.SizeTier, InputSize: v.InputSize}, err
}

// UpstreamError records the supplied rejection status and message in request
// Ops state.
func (p *openAIPassthroughExecutionAdapter) UpstreamError(status int, message string) {
	SetOpsUpstreamError(p.c, status, message, "")
}

// Log writes passthrough execution diagnostics under the OpenAI gateway
// logging component.
func (p *openAIPassthroughExecutionAdapter) Log(format string, args ...any) {
	logging.LegacyPrintf("service.openai_gateway", format, args...)
}

// WarnTimeoutHeaders logs recognized inbound timeout headers and their
// configured forwarding policy. Missing HTTP request state or matching headers
// produces no warning; the request builder separately applies the actual
// header filtering decision.
func (p *openAIPassthroughExecutionAdapter) WarnTimeoutHeaders(ctx context.Context) {
	if p.c == nil || p.c.Request == nil {
		return
	}
	if h := collectOpenAIPassthroughTimeoutHeaders(p.c.Request.Header); len(h) > 0 {
		log := logging.FromContext(ctx).With(zap.String("component", "service.openai_gateway"), zap.Int64("provider_id", p.provider.Record.ID), zap.Strings("timeout_headers", h))
		if p.s.Requests.AllowTimeoutHeaders() {
			log.Warn("OpenAI passthrough is forwarding timeout headers as configured; the upstream may end the stream early")
		} else {
			log.Warn("OpenAI passthrough detected timeout headers and will filter them as configured to reduce early stream termination")
		}
	}
}

// AccessToken resolves execution credentials for the selected passthrough
// provider record.
func (p *openAIPassthroughExecutionAdapter) AccessToken(ctx context.Context) (string, error) {
	token, _, err := p.s.Requests.Credentials.Resolve(ctx, gatewayadapter.ExecutionRecord(p.provider))
	return token, err
}

// PrepareTransportPass resets the attempt proxy URL before resolving attached
// proxy state. A URL is used only when both ProxyID and Proxy are present; the
// subsequent sender reads that prepared transport value.
func (p *openAIPassthroughExecutionAdapter) PrepareTransportPass() {
	p.proxyURL = ""
	if p.provider.Record.ProxyID != nil && p.provider.Record.Proxy != nil {
		p.proxyURL = p.provider.Record.Proxy.URL()
	}
}

// MarkPassthrough records the enabled passthrough request hint in the live Gin
// context.
func (p *openAIPassthroughExecutionAdapter) MarkPassthrough() {
	if p.c != nil {
		p.c.Set("openai_passthrough", true)
	}
}

// RetryState creates this provider attempt's body history with the
// request-shared repair budget. The HTTP request keeps budget initialization
// sequential; each new state remembers its own initial body while sharing
// later retry allowance across provider attempts.
func (p *openAIPassthroughExecutionAdapter) RetryState(body []byte) *openai.ResponsesRejectedFieldRetryState {
	return openAIResponsesRejectedFieldRetryStateForRequest(p.c, body)
}

// UpstreamModelObserved records the currently prepared upstream model in
// request Ops metadata.
func (p *openAIPassthroughExecutionAdapter) UpstreamModelObserved(model string) {
	SetOpsUpstreamModel(p.c, model)
}

// BuildPass constructs the prepared raw passthrough request with token and TLS
// matches. It uses this attempt's live HTTP state for header and identity
// policy; construction errors return before SendPass can open a response.
func (p *openAIPassthroughExecutionAdapter) BuildPass(ctx context.Context, body []byte, token string) (*http.Request, error) {
	return p.s.Requests.BuildPassthrough(ctx, p.c, p.provider, body, token, p.tls...)
}

// SendPass clears model observation and sends using prepared proxy, provider
// concurrency and TLS profile. Callers run PrepareTransportPass first, and the
// passthrough runner closes each successful response body after reading or
// retry/error handling.
func (p *openAIPassthroughExecutionAdapter) SendPass(r *http.Request) (*http.Response, error) {
	resetResponseModel(p.c)
	return p.s.Requests.Transport.DoWithTLS(r, p.proxyURL, p.provider.Record.ID, p.provider.Record.Concurrency, p.s.Requests.TLSProfile(p.provider, p.tls...))
}

// Latency records the upstream exchange duration as integer milliseconds in
// Ops.
func (p *openAIPassthroughExecutionAdapter) Latency(d time.Duration) {
	SetOpsLatencyMs(p.c, OpsUpstreamLatencyMsKey, d.Milliseconds())
}

// TransportErrorPass delegates sanitized transport failure observation with
// the passthrough marker. The shared handler retains client cancellation and
// otherwise returns a provider recovery signal, while the outer text loop
// enforces its actual output window.
func (p *openAIPassthroughExecutionAdapter) TransportErrorPass(ctx context.Context, err error) error {
	return p.s.Requests.Failure.Handle(ctx, p.c, p.provider, err, true)
}

// CompactRetry delegates Compact model-recovery preparation using this
// provider, request and prior-retry flag. The shared Compact executor returns
// repaired bytes/model and eligibility; RunPassthrough owns the subsequent
// retry ordering and its response cleanup.
func (p *openAIPassthroughExecutionAdapter) CompactRetry(model string, body []byte, status int, message string, payload []byte, tried bool) ([]byte, string, bool) {
	return p.s.Compact.Prepare(p.c, p.provider, model, body, status, message, payload, tried)
}

// CompactObserved records the Compact recovery response through the shared
// executor with passthrough observation enabled.
func (p *openAIPassthroughExecutionAdapter) CompactObserved(r *http.Response, body []byte, message string) {
	p.s.Compact.Observe(p.c, p.provider, r, body, message, true)
}

// ErrorCode extracts the upstream classification code from captured HTTP error
// bytes.
func (p *openAIPassthroughExecutionAdapter) ErrorCode(body []byte) string {
	return upstream.ExtractErrorCode(body)
}

// ShouldFailover delegates passthrough HTTP classification for this selected
// provider and payload.
func (p *openAIPassthroughExecutionAdapter) ShouldFailover(status int, body []byte) bool {
	return shouldFailoverOpenAIPassthroughResponse(p.provider, status, body)
}

// FailoverError delegates health and classified recovery for a captured
// passthrough rejection. The prepared request body supplies scheduling
// identity, and the shared output handler may write generic policy rejection
// instead of returning a provider-switch signal.
func (p *openAIPassthroughExecutionAdapter) FailoverError(ctx context.Context, r *http.Response, body, payload []byte) error {
	return p.s.Output.PassthroughFailoverError(ctx, r, p.c, p.provider, body, payload)
}

// ErrorResponsePass delegates final passthrough rejection or configured
// recovery using captured bytes. Request-specific errors bypass provider
// health in the shared handler; the runner retains response cleanup and
// interprets the returned error before further attempts.
func (p *openAIPassthroughExecutionAdapter) ErrorResponsePass(ctx context.Context, r *http.Response, body, payload []byte) error {
	return p.s.Output.PassthroughError(ctx, r, p.c, p.provider, body, payload)
}

// WrapResponseBody installs client-tool SSE restoration when this request has
// a mapping and the upstream declares event-stream. A positive MaxLineSize is
// used regardless of Configured; otherwise the default applies, and response
// closure follows the wrapper's underlying body.
func (p *openAIPassthroughExecutionAdapter) WrapResponseBody(r *http.Response) {
	if mapping, ok := OpenAIResponsesClientToolMapping(p.c); ok && openai.IsEventStreamResponse(r.Header) {
		limit := OpenAIResponseDefaultMaxLineSize
		if p.s.Output.Options.MaxLineSize > 0 {
			limit = p.s.Output.Options.MaxLineSize
		}
		// Restore lowered client-tool nodes while the reader consumes this same
		// response; the wrapper retains closure of the original body through Close.
		r.Body = upstream.NewResponsesClientToolStreamBody(r.Body, mapping, limit)
	}
}

// Sink binds passthrough header ownership to actual response metadata application.
// A heartbeat may already have committed the client response before upstream
// headers arrive; those undelivered values must not replace the remembered provider.
func (p *openAIPassthroughExecutionAdapter) Sink() upstream.OutputSink {
	sink := NewResponseSink(p.c)
	sink.headersApplied = func(headers http.Header) {
		if ExtractCodexTurnState(headers) != "" {
			p.s.Requests.Turns.Commit(p.c, p.provider, headers)
		}
	}
	return sink
}

// ResponseOptions binds passthrough reading, health and output callbacks to
// the current request and provider. The caller retains HTTP state and
// configured dependencies through reading, and the execution runner owns
// closing the upstream body.
func (p *openAIPassthroughExecutionAdapter) ResponseOptions(ctx context.Context) openai.PassthroughOptions {
	return p.s.Output.PassthroughOptions(ctx, p.c, p.provider)
}

// CompactFromSignal delegates recovery from a Compact read failure with the
// prior-retry guard and current response. The shared executor chooses repaired
// bytes/model; the passthrough runner performs the next exchange and owns
// response cleanup.
func (p *openAIPassthroughExecutionAdapter) CompactFromSignal(model string, body []byte, err error, tried bool, r *http.Response) ([]byte, string, bool) {
	return p.s.Compact.ApplySignal(p.c, p.provider, model, body, err, tried, r)
}

// CompactSignal extracts recognized Compact failure data for the shared
// runner. A miss returns false with empty data; recognized payload and message
// are retained for final recovery classification or client presentation.
func (p *openAIPassthroughExecutionAdapter) CompactSignal(err error) (forward.CompactFailure, bool) {
	v, ok := compact.AsFailure(err)
	if !ok {
		return forward.CompactFailure{}, false
	}
	return forward.CompactFailure{Message: v.Message, Payload: v.Payload}, true
}

// CompactErrorResponse constructs the shared synthetic response and body for a
// Compact signal. The runner passes this evidence to ordinary passthrough
// health/error handling after it has closed the original response body.
func (p *openAIPassthroughExecutionAdapter) CompactErrorResponse(r *http.Response, v forward.CompactFailure) (*http.Response, []byte) {
	return CompactFallbackErrorResponse(r, &compact.Failure{Message: v.Message, Payload: v.Payload})
}

// BindOwner delegates response-provider and HTTP-owner binding for the
// selected request group. The shared binder logs storage failures and
// continues handling, using the configured response TTL and currently
// authenticated request ownership.
func (p *openAIPassthroughExecutionAdapter) BindOwner(ctx context.Context, id string) {
	p.s.Output.BindResponseProvider(ctx, p.c, p.provider, id)
}

// ObservedServiceTier reads this request's observed upstream service tier for
// result capture.
func (p *openAIPassthroughExecutionAdapter) ObservedServiceTier() string {
	return ObservedUpstreamResponseServiceTier(p.c)
}

// openAIRequestGroup returns the group attached to the current Gin API-key
// value. Missing context, absent or differently typed key, or a nil key
// returns nil; the returned group remains borrowed from that request key.
func openAIRequestGroup(c *gin.Context) *routing.Group {
	if c == nil {
		return nil
	}
	v, ok := c.Get("api_key")
	if !ok {
		return nil
	}
	key, ok := v.(*apikey.APIKey)
	if !ok || key == nil {
		return nil
	}
	return key.Group
}

// openAIResponsesRejectedFieldRetryStateForRequest creates fresh body
// deduplication state with a request-shared field-repair budget. It reuses a
// correctly typed budget from Gin or installs a new one when unavailable; nil
// contexts receive a fresh budget for this call.
//
// Callers initialize states sequentially for one HTTP request, because the
// context lookup and budget publication are separate operations. The upstream
// state and budget implement their own locking after construction. Distinct
// provider states can therefore admit the same transformed body while
// consuming one combined request allowance, and each state remembers its own
// supplied initial body.
func openAIResponsesRejectedFieldRetryStateForRequest(c *gin.Context, initialBody []byte) *openai.ResponsesRejectedFieldRetryState {
	var budget *openai.ResponsesRejectedFieldRetryBudget
	if c != nil {
		if existing, ok := c.Get(openAIResponsesRejectedFieldRetryBudgetContextKey); ok {
			budget, _ = existing.(*openai.ResponsesRejectedFieldRetryBudget)
		}
	}
	// A fresh provider state needs local body history but shares the request
	// allowance; sequential initialization prevents installing competing budget handles in Gin.
	if budget == nil {
		budget = &openai.ResponsesRejectedFieldRetryBudget{}
		if c != nil {
			c.Set(openAIResponsesRejectedFieldRetryBudgetContextKey, budget)
		}
	}
	return openai.NewOpenAIResponsesRejectedFieldRetryStateWithBudget(initialBody, budget)
}

// ModelAttributes resolves final passthrough capabilities before permission
// checks and billing. The selected provider retains the resolution for later
// image-tool identity selection within the same request snapshot.
func (p *openAIPassthroughExecutionAdapter) ModelAttributes(ctx context.Context, model string) (modelrules.Resolution, error) {
	result, err := gatewayadapter.ResolveModelAttributes(ctx, p.provider, model, p.s.modelAttributes(), "openai_responses")
	if err == nil {
		p.provider.Capabilities = &result
	} else {
		WriteOpenAIModelAttributeError(p.c, err, p.c.Writer.Written())
	}
	return result, err
}
