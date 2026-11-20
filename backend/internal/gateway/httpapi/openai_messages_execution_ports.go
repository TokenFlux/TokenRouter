package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_messages_execution_ports.go binds Messages and Chat conversion
// runners to request preparation and response services.
// Messages conversion needs cache and continuation identities that follow the
// selected provider and downstream key across turns. The adapter binds those
// identities to the same HTTP request used for model and error observation,
// while the shared Messages runner owns conversion, response resources and
// recovery ordering.
//
// The process-local continuation store can retain response ID, opaque turn
// state and a disablement decision under one deadline; its merged updates need
// caller coordination for the same key. Digest history is cloned with specific
// shallow-copy rules before model conversion. Fresh adapter transport state
// belongs to one attempt, but the services and session stores are shared
// across calls. These distinctions keep body rewrites, stored history and
// observed output attached to their intended request.

// OpenAICompatMessagesBridgeContextKey identifies the enabled request hint consumed by
// Codex compatibility handling after Messages input has been converted into Responses.
const OpenAICompatMessagesBridgeContextKey = "openai_compat_messages_bridge"

// openAIMessagesExecutionAdapter binds one text attempt to provider, HTTP and
// session services.
type openAIMessagesExecutionAdapter struct {
	// proxyURL is replaced by PrepareTransport before Send. Each adapter keeps
	// this URL local to its currently resolved provider attempt.
	proxyURL string
	// s, c, provider and tls are borrowed for one synchronous attempt. Services
	// remain shared across calls, while Prepare can replace provider before
	// credential, request and output callbacks consume that attempt state.
	s *OpenAITextExecutor // shared text execution services
	c *gin.Context        // active HTTP request context
	// selected protocol attempt provider
	provider *gatewayprovider.ExecutionProvider
	// tls carries supplied routing matches through request construction. Its
	// storage is borrowed, so callers keep it stable until forwarding finishes.
	tls []egress.TLSFingerprintRouterMatchResult
}

// compatResponseKey namespaces a trimmed prompt-cache identity by provider and
// authenticated API-key ID. Missing provider or blank key returns empty;
// missing HTTP context supplies key ID zero, and the session helper constructs
// the shared storage key.
func compatResponseKey(c *gin.Context, provider *gatewayprovider.ExecutionProvider, promptCacheKey string) string {
	key := strings.TrimSpace(promptCacheKey)
	if provider == nil || key == "" {
		return ""
	}
	apiKeyID := int64(0)
	if c != nil {
		apiKeyID = APIKeyIDFromContext(c)
	}
	return session.CompatResponseKey(provider.Record.ID, apiKeyID, key)
}

// SetOpenAICompatMessagesBridgeContext records enabled Messages-bridge state
// in the request. Nil contexts and false values are ignored, so passing false
// does not clear a true marker left by another preparation on this same
// context.
func SetOpenAICompatMessagesBridgeContext(c *gin.Context, enabled bool) {
	if c == nil || !enabled {
		return
	}
	c.Set(OpenAICompatMessagesBridgeContextKey, true)
}

// IsOpenAICompatMessagesBridgeContext reports a correctly typed true
// Messages-bridge marker. Missing context, absent entry or a differently typed
// value returns false; the marker is a request hint consumed by compatibility
// handling.
func IsOpenAICompatMessagesBridgeContext(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(OpenAICompatMessagesBridgeContextKey)
	if !ok {
		return false
	}
	enabled, ok := value.(bool)
	return ok && enabled
}

// Prepare resolves this attempt's provider and resets model, endpoint and
// reverse-tool observations before preparing identity. It replaces provider
// state on successful route resolution even if later identity preparation
// fails. The returned profile carries credential and continuation
// capabilities.
//
// Native Anthropic and adaptive protocol routes choose NativeMessages
// dispatch, while raw-Chat model policy or a resolved other-platform Chat preference
// selects Chat conversion. The remaining route uses Responses conversion.
// These routing hints describe one selected provider; RunMessages owns the
// subsequent exchange and the outer text loop owns provider switching.
func (p *openAIMessagesExecutionAdapter) Prepare(ctx context.Context) (openaiexecution.MessagesProfile, openaiexecution.Dispatch, error) {
	provider, err := gatewayprovider.ProviderForProtocolAttempt(ctx, p.provider)
	if err != nil {
		return openaiexecution.MessagesProfile{}, openaiexecution.DispatchResponses, err
	}
	p.provider = provider
	BeginUpstreamResponseModelObservation(p.c)
	ClearActualOpenAIUpstreamEndpoint(p.c)
	if gatewayprovider.ExecutionModelPolicy(provider).RawChat() {
		SetActualOpenAIUpstreamEndpoint(p.c, "/v1/chat/completions")
	}
	SetCodexToolNameReverse(p.c, nil)
	if _, err := PrepareCodexIdentity(ctx, p.c, p.s.Requests.Providers, provider); err != nil {
		return openaiexecution.MessagesProfile{}, openaiexecution.DispatchResponses, err
	}
	profile := openaiexecution.MessagesProfile{Profile: openAIForwardProfile(provider), ID: provider.Record.ID, GrokOAuth: provider.View().IsGrokOAuth(), Shadow: provider.View().IsShadow(), ContinuationSupported: providercore.ResolveResponsesContinuationSupported(provider.Record.Extra)}
	route := openaiexecution.DispatchResponses
	if gatewayprovider.ExecutionProtocolTarget(provider).IsAnthropicProtocol() || gatewayprovider.ExecutionProtocolTarget(provider).IsAdaptiveAPIProtocol() {
		route = openaiexecution.DispatchAnthropic
	} else if gatewayprovider.ExecutionModelPolicy(provider).RawChat() || (!provider.View().HasProtocolEndpointDefaults() && resolveOpenAITextProtocolForAttempt(p.c, provider, providercore.TextProtocolResponses) == providercore.TextProtocolChatCompletions) {
		route = openaiexecution.DispatchRawChat
	}
	return profile, route, nil
}

// Dispatch executes native Messages for DispatchAnthropic and
// MessagesViaRawChat for other supplied routes. The prompt-key argument is
// unused by this adapter; the delegated runner derives its own request fields
// while model and TLS inputs follow the selected attempt.
func (p *openAIMessagesExecutionAdapter) Dispatch(ctx context.Context, route openaiexecution.Dispatch, body []byte, key, model string) (*openaiexecution.Result, error) {
	var r *protocolforward.OpenAIResult
	var err error
	if route == openaiexecution.DispatchAnthropic {
		r, err = p.s.NativeMessages(ctx, p.c, p.provider, body, model)
	} else {
		r, err = p.s.MessagesViaRawChat(ctx, p.c, p.provider, body, model, p.tls...)
	}
	return openaiexecution.FromForwardResult(r), err
}

// Error marks semantic commitment and writes the current request's rejection in
// Anthropic format. When event-stream framing is already committed, it emits a
// terminal Anthropic SSE error event and records Ops stream status instead of
// appending raw JSON.
func (p *openAIMessagesExecutionAdapter) Error(status int, kind, message string) {
	MarkResponseCommitted(p.c)
	if CommittedSSE(p.c) {
		DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(p.c, status, kind, message, true)
		return
	}
	WriteAnthropicError(p.c, status, kind, "", message)
}

// BillingModel applies effort-aware provider model policy to the requested and
// group-fallback model.
func (p *openAIMessagesExecutionAdapter) BillingModel(model, fallback string) string {
	policy := gatewayprovider.ExecutionModelPolicy(p.provider)
	if p.c != nil && p.c.Request != nil {
		policy = policy.WithContext(p.c.Request.Context())
	}
	return policy.ForwardModel(model, fallback)
}

// UpstreamModel normalizes the billing model for this provider's
// OpenAI-compatible upstream.
func (p *openAIMessagesExecutionAdapter) UpstreamModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(p.provider).NormalizeOpenAI(model)
}

// APIKeyID reads the authenticated downstream key identifier for this HTTP
// request.
func (p *openAIMessagesExecutionAdapter) APIKeyID() int64 {
	return APIKeyIDFromContext(p.c)
}

// ClaudeSession extracts the supported Claude Code session seed from headers
// or body.
func (p *openAIMessagesExecutionAdapter) ClaudeSession(body []byte) string {
	return ExtractClaudeCodeSessionID(p.c, body)
}

// FindDigestKey searches the shared prompt cache by provider, key ID and
// digest chain. A missing provider supplies ID zero to the cache; the returned
// key and matched chain guide later history binding in RunMessages.
func (p *openAIMessagesExecutionAdapter) FindDigestKey(key int64, chain string) (string, string) {
	var id int64
	if p.provider != nil {
		id = p.provider.Record.ID
	}
	return p.s.PromptCache.Find(id, key, chain)
}

// ContinuationEnabled delegates automatic continuation admission using
// provider credentials, saved capability and model.
func (p *openAIMessagesExecutionAdapter) ContinuationEnabled(model string) bool {
	return gatewayprovider.CompatContinuationEnabled(p.provider, model)
}

// ResponseID reads saved continuation for this provider/key/prompt identity
// from process-local state. The store can expire or remove unusable bindings
// during lookup; callers coordinate same-session access when retaining merged
// state across concurrent updates matters.
func (p *openAIMessagesExecutionAdapter) ResponseID(ctx context.Context, key string) string {
	return p.s.Continuation.Response(compatResponseKey(p.c, p.provider, key))
}

// ContinuationDisabled reads the process-local unsupported-continuation
// decision for the shared session key. Missing or expired evidence permits
// ordinary policy handling, and callers coordinate same-session updates
// because store lookup and cleanup are separate operations.
func (p *openAIMessagesExecutionAdapter) ContinuationDisabled(ctx context.Context, key string) bool {
	return p.s.Continuation.Disabled(compatResponseKey(p.c, p.provider, key))
}

// Convert resolves model capabilities and converts Anthropic input to
// Responses using their options. Successful resolution updates
// provider.Capabilities before request serialization; later serialization or
// conversion errors can leave that request-local capability publication
// visible.
func (p *openAIMessagesExecutionAdapter) Convert(r *protocolanthropic.AnthropicRequest) (*protocolopenai.ResponsesRequest, error) {
	result, err := p.modelResolution(r.Model, r, "openai_responses")
	if err != nil {
		return nil, err
	}
	return protocolbridge.AnthropicToResponses(r, gatewayprovider.ModelConversionOptions(result))
}

// BetaFast reports whether the inbound Anthropic beta list contains fast mode.
func (p *openAIMessagesExecutionAdapter) BetaFast() bool {
	return claude.ContainsBetaToken(p.c.GetHeader("anthropic-beta"), claude.BetaFastMode)
}

// MessagesEffort delegates model-sensitive effort selection using the
// Anthropic request and resolved attributes.
func (p *openAIMessagesExecutionAdapter) MessagesEffort(r *protocolanthropic.AnthropicRequest, model, effort string) string {
	return gatewayprovider.OpenAICompatAnthropicReasoningEffort(r, model, effort, gatewayprovider.ExecutionModelAttributes(p.provider, model))
}

// Debug writes supplied structured execution diagnostics through the shared
// logger.
func (p *openAIMessagesExecutionAdapter) Debug(msg string, fields ...zap.Field) {
	logging.L().Debug(msg, fields...)
}

// ToolNameReverse installs this attempt's Codex reverse-tool names in request
// state.
func (p *openAIMessagesExecutionAdapter) ToolNameReverse(value map[string]string) {
	SetCodexToolNameReverse(p.c, value)
}

// ForcedTemplate returns the configured Messages-to-Codex instructions
// template text for execution.
func (p *openAIMessagesExecutionAdapter) ForcedTemplate() string {
	return p.s.ForcedTemplate
}

// ForcedInstructions delegates template rendering using the writable request
// map and supplied template data. The returned change flag identifies
// installed instructions, and a render error stops RunMessages before outgoing
// request serialization.
func (p *openAIMessagesExecutionAdapter) ForcedInstructions(body map[string]any, text string, data openaiexecution.TemplateData) (bool, error) {
	return applyForcedCodexInstructionsTemplate(body, text, forcedCodexInstructionsTemplateData(data))
}

// ProviderIdentity applies the current Codex namespace and key ID to writable
// client metadata. The identity record comes from this request's prepared
// provider state, so body metadata and later isolated session construction use
// that attempt identity.
func (p *openAIMessagesExecutionAdapter) ProviderIdentity(body map[string]any, key int64) {
	openai.ApplyCodexProviderIdentityClientMetadataMap(body, provideradapter.CodexIdentityNamespace(CodexIdentityRecord(p.c, p.provider.View())), key)
}

// TurnState reads opaque process-local state for this provider/key/prompt
// identity. The store permits it even when continuation is disabled; expiry
// and same-key update coordination follow CompatResponses rather than the
// unused context argument.
func (p *openAIMessagesExecutionAdapter) TurnState(ctx context.Context, key string) string {
	return p.s.Continuation.TurnState(compatResponseKey(p.c, p.provider, key))
}

// ApplyEffort applies the request-context group reasoning policy after
// Messages conversion. An over-limit error marks local policy limitation and
// writes an Anthropic forbidden error; callers stop forwarding on the returned
// error and use changed only for adopted bytes.
func (p *openAIMessagesExecutionAdapter) ApplyEffort(ctx context.Context, body []byte) ([]byte, bool, error) {
	updated, changed, err := requeststate.ApplyOpenAIReasoningEffortPolicyFromContext(ctx, body)
	if limited, ok := errors.AsType[*routing.ReasoningEffortOverLimitError](err); ok {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		p.Error(403, "forbidden_error", limited.Error())
	}
	return updated, changed, err
}

// ApplyFast applies current provider fast-tier policy to the prepared
// Responses body. A BlockedError marks local policy limitation and writes
// Anthropic forbidden output; RunMessages returns that preparation error
// before acquiring upstream request resources.
func (p *openAIMessagesExecutionAdapter) ApplyFast(ctx context.Context, model string, body []byte) ([]byte, error) {
	updated, err := tierpolicy.ApplyBody(body, p.s.FastPolicy.Input(ctx, p.provider, model))
	if blocked, ok := errors.AsType[*tierpolicy.BlockedError](err); ok {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		p.Error(403, "forbidden_error", blocked.Message)
	}
	return updated, err
}

// ResolvedServiceTier combines observed request tier with the supplied
// outgoing tier for result attribution.
func (p *openAIMessagesExecutionAdapter) ResolvedServiceTier(tier *string) *string {
	return ResolvedOpenAIUpstreamServiceTier(p.c, tier)
}

// GrokCacheIdentity resolves the request's isolated Grok cache identity from
// body, key and model.
func (p *openAIMessagesExecutionAdapter) GrokCacheIdentity(body []byte, key, model string) string {
	return ResolveGrokCacheIdentity(p.c, body, key, model)
}

// PatchGrokBody prepares Grok Responses fields with the selected model and
// runtime attributes. It uses the live HTTP request context for resolution,
// and errors return to RunMessages before the outgoing request can be
// serialized or sent.
func (p *openAIMessagesExecutionAdapter) PatchGrokBody(body []byte, model string) ([]byte, error) {
	return gatewayprovider.PrepareGrokResponses(p.c.Request.Context(), p.provider, model, body, p.s.modelAttributes())
}

// GrokFreeToolRoute delegates cache-route preparation for free Grok Messages
// function tools. Provider policy and canonical intent determine the route;
// the returned body or error reaches RunMessages before the exchange begins.
func (p *openAIMessagesExecutionAdapter) GrokFreeToolRoute(body, intent []byte, key string) ([]byte, error) {
	return gatewayprovider.ApplyGrokFreeMessagesFunctionToolCacheRoute(body, intent, p.provider, key)
}

// Credential resolves this attempt's text credential using request budget and
// observation callbacks. It returns the token or resolution error, while the
// credential service owns refresh and identity recovery for the selected
// provider.
func (p *openAIMessagesExecutionAdapter) Credential(ctx context.Context) (string, error) {
	token, _, err := p.s.Credentials.Resolve(ctx, RequestCredentialBudget(p.c), CredentialObserver{Context: p.c}, p.provider)
	return token, err
}

// BindMessagesBridge publishes the request's enabled Messages-compatibility
// marker through the shared helper. A false value is ignored by that helper,
// so this call does not clear an existing true marker.
func (p *openAIMessagesExecutionAdapter) BindMessagesBridge(v bool) {
	SetOpenAICompatMessagesBridgeContext(p.c, v)
}

// Build constructs the prepared Responses exchange for this selected provider.
// Grok uses its request builder with streaming forced true; other platforms
// use the supplied stream/cache values and TLS matches, and the first context
// argument is unused.
func (p *openAIMessagesExecutionAdapter) Build(_ context.Context, ctx context.Context, body []byte, token string, stream bool, key, grokIdentity string) (*http.Request, error) {
	if p.provider.Record.Platform == capability.PlatformGrok {
		return p.s.Grok.BuildResponsesRequest(ctx, p.c, p.provider, body, token, grokIdentity, true)
	}
	return p.s.Requests.Build(ctx, p.c, p.provider, body, token, stream, key, false, p.tls...)
}

// IsolatedSessionID derives the session UUID from key, Codex namespace and
// prompt seed.
func (p *openAIMessagesExecutionAdapter) IsolatedSessionID(key int64, cache string) string {
	return upstream.GenerateSessionUUID(openai.IsolateOpenAIUpstreamSessionID(key, provideradapter.CodexIdentityNamespace(CodexIdentityRecord(p.c, p.provider.View())), cache))
}

// Header reads the named inbound header from the active HTTP request.
func (p *openAIMessagesExecutionAdapter) Header(key string) string {
	return p.c.GetHeader(key)
}

// PrepareTransport resolves the provider proxy URL into this adapter before
// sending. It resets a previously stored URL and uses the attached Proxy when
// present, so later Send reads current attempt transport state.
func (p *openAIMessagesExecutionAdapter) PrepareTransport() {
	p.proxyURL = ""
	if p.provider.Record.Proxy != nil {
		p.proxyURL = p.provider.Record.Proxy.URL()
	}
}

// Send clears response-model observation and performs the prepared request
// with proxy, provider concurrency and TLS policy. Callers run
// PrepareTransport first and own the successful response body until reading or
// error handling has finished.
func (p *openAIMessagesExecutionAdapter) Send(r *http.Request) (*http.Response, error) {
	resetResponseModel(p.c)
	return p.s.Requests.Transport.DoWithTLS(r, p.proxyURL, p.provider.Record.ID, p.provider.Record.Concurrency, p.s.Requests.TLSProfile(p.provider, p.tls...))
}

// TransportError delegates sanitized transport observation with the ordinary
// text Ops marker. Client cancellation remains its cause, while other failures
// return a provider recovery signal; the outer text loop enforces the actual
// retry window.
func (p *openAIMessagesExecutionAdapter) TransportError(ctx context.Context, err error) error {
	return p.s.Requests.Failure.Handle(ctx, p.c, p.provider, err, false)
}

// ReadErrorBody delegates bounded upstream error capture while retaining
// available partial bytes. The shared reader suppresses read errors and leaves
// body cleanup with the caller, which can replace the capture for later
// protocol handling.
func (p *openAIMessagesExecutionAdapter) ReadErrorBody(r *http.Response) []byte {
	return p.s.Output.ReadErrorBody(r)
}

// ReadUpstreamError delegates bounded capture, original-body closure and
// replay-body replacement. Callers supply a live response/body; the returned
// message is query-sanitized while returned bytes are the captured content for
// subsequent identity redaction and classification.
func (p *openAIMessagesExecutionAdapter) ReadUpstreamError(r *http.Response) ([]byte, string) {
	return p.s.Output.ReadReplayableError(r)
}

// IsAgentIdentity reports whether this selected provider uses Agent Identity
// execution credentials.
func (p *openAIMessagesExecutionAdapter) IsAgentIdentity(ctx context.Context) bool {
	return p.s.Requests.Identity.UsesAgentIdentity(ctx, p.provider)
}

// RecoverAgentTask delegates recovery using the selected provider's current
// task ID. The identity service owns the exchange and credential update;
// RunMessages controls whether this request has already consumed its recovery
// attempt.
func (p *openAIMessagesExecutionAdapter) RecoverAgentTask(ctx context.Context) error {
	return p.s.Requests.Identity.Recover(ctx, p.provider, p.provider.View().GetCredential("task_id"))
}

// RedactErrorBody delegates captured error cleanup using this provider's
// execution identity. Callers retain the returned bytes for health and
// protocol handling; the identity service decides whether it can replace
// sensitive provider fields.
func (p *openAIMessagesExecutionAdapter) RedactErrorBody(ctx context.Context, body []byte) []byte {
	return p.s.Requests.Identity.Redact(ctx, p.provider, body)
}

// DisableContinuation records unsupported continuation for this
// provider/key/prompt binding. The shared store retains opaque turn state and
// renews its lifetime; callers coordinate same-key writes before replaying
// history without previous_response_id.
func (p *openAIMessagesExecutionAdapter) DisableContinuation(ctx context.Context, key string) {
	p.s.Continuation.Disable(compatResponseKey(p.c, p.provider, key))
}

// DeleteResponseID removes the saved response ID after continuation becomes
// unavailable. The shared store can retain opaque turn state or disablement
// and renew their lifetime; callers coordinate same-key updates before
// submitting full replay input.
func (p *openAIMessagesExecutionAdapter) DeleteResponseID(ctx context.Context, key string) {
	p.s.Continuation.DeleteResponse(compatResponseKey(p.c, p.provider, key))
}

// FailoverHTTP delegates text HTTP recovery classification for the selected
// provider. A nil concrete failure returns a nil error interface; a classified
// failure reaches the runner before final protocol writing, with output-window
// enforcement left to outer execution.
func (p *openAIMessagesExecutionAdapter) FailoverHTTP(ctx context.Context, r *http.Response, body []byte, msg, model string) error {
	failure := p.s.httpFailover(ctx, p.c, p.provider, r, body, msg, model)
	// A nil concrete pointer is an absent recovery proposal. Return nil as an
	// error interface so the runner continues to its final Messages rejection path.
	if failure == nil {
		return nil
	}
	return failure
}

// ErrorResponse handles the captured upstream rejection through the Messages
// error formatter. It returns the handler error and no result; the runner has
// prepared a replayable body and retains its overall response cleanup.
func (p *openAIMessagesExecutionAdapter) ErrorResponse(r *http.Response, model string) (*openaiexecution.Result, error) {
	_, err := p.s.messagesError(r, p.c, p.provider, model)
	return nil, err
}

// UpdateGrokUsage delegates Grok quota-header observation for this response
// and model.
func (p *openAIMessagesExecutionAdapter) UpdateGrokUsage(ctx context.Context, model string, h http.Header, status int) {
	p.s.Grok.Health.ObserveResponse(ctx, p.provider.View(), h, status, model)
}

// BindTurnState stores opaque state for the selected provider/key/prompt
// identity. The process-local store renews the shared binding deadline and
// retains loaded response/disablement fields; callers coordinate same-key
// writes to retain those merged values.
func (p *openAIMessagesExecutionAdapter) BindTurnState(ctx context.Context, key, state string) {
	p.s.Continuation.BindTurnState(compatResponseKey(p.c, p.provider, key), state)
}

// Sink binds synchronous upstream output to this HTTP request's response
// writer.
func (p *openAIMessagesExecutionAdapter) Sink() upstream.OutputSink {
	return NewResponseSink(p.c)
}

// ResponseOptions binds Responses-to-Messages reading callbacks to the current
// request and provider. Callers keep the response and configured output
// services live through reading, and the runner retains responsibility for
// closing the response body afterward.
func (p *openAIMessagesExecutionAdapter) ResponseOptions(r *http.Response, original, billing, model string) openai.MessagesResponseOptions {
	return p.s.Output.MessagesOptions(p.c, p.provider, r, original, billing, model)
}

// CyberPolicy reports whether this request holds observed OpenAI cyber-policy
// evidence.
func (p *openAIMessagesExecutionAdapter) CyberPolicy() bool {
	return GetOpsCyberPolicy(p.c) != nil
}

// BindResponseID stores a successful response ID for this provider/key/prompt
// binding. The shared store retains loaded turn state and renews the binding
// lifetime; callers coordinate same-key updates, and a disabled binding
// remains disabled after this operation.
func (p *openAIMessagesExecutionAdapter) BindResponseID(ctx context.Context, key, id string) {
	p.s.Continuation.BindResponse(compatResponseKey(p.c, p.provider, key), id)
}

// BindDigestKey publishes the Anthropic digest-chain cache binding for this
// provider and downstream key. Missing executor or provider is ignored;
// callers supply ResponseTTL when both are present. Nil PromptCache is handled
// by its binder, which owns ordered prefix publication and expiry.
func (p *openAIMessagesExecutionAdapter) BindDigestKey(id int64, chain, key, matched string) {
	if p.s == nil || p.provider == nil {
		return
	}
	p.s.PromptCache.Bind(p.provider.Record.ID, id, chain, key, matched, p.s.ResponseTTL())
}

// UpdateCodexUsage parses response quota headers and passes any snapshot to
// the shared Codex observer. Missing snapshot causes no observation; the
// observer handles nil service/storage, throttling and asynchronous
// persistence under its own lifecycle rules.
func (p *openAIMessagesExecutionAdapter) UpdateCodexUsage(ctx context.Context, h http.Header) {
	if snapshot := openai.ParseCodexRateLimitHeaders(h); snapshot != nil {
		p.s.CodexUsage.Observe(ctx, p.provider.Record.ID, snapshot)
	}
}
