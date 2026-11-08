package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/grokforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// grok_forward_execution.go adapts Grok preparation and response observations
// to request-local HTTP execution state.
// Grok platform preparation needs request-local HTTP observations beside
// selected provider state. The adapter carries the resolved token and proxy
// into transport, derives model and cache identities through shared rules, and
// returns the response parser's observed usage and commit state to execution.
//
// Stream reads can retain an observation while returning an error, including
// separate HTTP commitment, safe-retry commitment and client disconnection
// signals. Nonstream parsing instead stops on a read error before translating
// a result. Health classification and failover construction delegate to the
// bound provider services so this adapter records the current attempt without
// creating another provider-switch loop.

// grokForwardAdapter holds credentials and execution services for the current request.
type grokForwardAdapter struct {
	s        *GrokExecutor                      // shared execution dependencies
	c        *gin.Context                       // active HTTP request
	provider *gatewayprovider.ExecutionProvider // selected attempt provider
	// token and proxyURL are populated during credential/proxy preparation and
	// reused by this request's Build and Do calls, including platform replay.
	token, proxyURL string // token: provider credential; proxyURL: selected egress URL
}

// WriteGrokForwardInvalidRequest presents a rejected endpoint model or tool
// parameter as OpenAI JSON. The caller supplies the offending param name, and
// this helper writes HTTP 400 before any upstream exchange.
func WriteGrokForwardInvalidRequest(c *gin.Context, message, param string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": message, "param": param}})
}

// options builds Grok preparation settings with the shared attribute resolver
// and activity entry. A positive output MaxLineSize overrides the default;
// generated IDs use UUID strings for protocol conversion.
func (a *grokForwardAdapter) options() grokforward.Options {
	maxLine := OpenAIResponseDefaultMaxLineSize
	if a.s.Output.Options.MaxLineSize > 0 {
		maxLine = a.s.Output.Options.MaxLineSize
	}
	return grokforward.Options{ResolveAttributes: func(ctx context.Context, model string) (modelcatalog.Attributes, error) {
		result, err := gatewayprovider.ResolveModelAttributes(ctx, a.provider, model, a.s.Attributes, "openai_responses")
		if err != nil {
			WriteOpenAIModelAttributeError(a.c, err, a.c != nil && a.c.Writer.Written())
		}
		return result.Attributes, err
	}, Codec: grok.BodyCodec{
		NewID: uuid.NewString,
	}, MaxLineSize: maxLine, Enter: a.s.Enter}
}

// input captures provider identity and request flags for grokforward
// preparation. It retains the supplied body storage and start time; compact
// detection comes from the active HTTP request path.
func (a *grokForwardAdapter) input(body []byte, model string, stream bool, start time.Time) grokforward.Input {
	return grokforward.Input{
		ProviderID:    a.provider.Record.ID,
		ProviderName:  a.provider.Record.Name,
		ProviderType:  a.provider.Record.Type,
		Platform:      a.provider.Record.Platform,
		OAuth:         a.provider.View().IsGrokOAuth(),
		HTTPPresent:   a.c != nil,
		Compact:       IsOpenAIResponsesCompactPath(a.c),
		Body:          body,
		OriginalModel: model,
		Stream:        stream,
		StartedAt:     start,
	}
}

// BillingModel resolves provider forwarding identity from the supplied request
// model. Its empty dispatch override leaves that request model as the mapping
// input, so provider mapping determines the returned forwarding/billing name.
func (a *grokForwardAdapter) BillingModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.provider).ForwardModel(model, "")
}

// UpstreamModel trims the forwarding model using the provider's OpenAI
// normalization rule. For a Grok provider, an empty model selects
// grok.DefaultResponsesModel; other provider records use trimmed identity
// directly.
func (a *grokForwardAdapter) UpstreamModel(model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.provider).NormalizeOpenAI(model)
}

// InvalidRequest writes the request-local OpenAI parameter error through
// shared presentation.
func (a *grokForwardAdapter) InvalidRequest(message, param string) {
	WriteGrokForwardInvalidRequest(a.c, message, param)
}

// SetError records upstream status, message and detail for HTTP diagnostics.
func (a *grokForwardAdapter) SetError(status int, message, detail string) {
	SetOpsUpstreamError(a.c, status, message, detail)
}

// ClientTools stores this attempt's Responses tool mapping in the HTTP
// context. Its maps remain shared with the supplied mapping, and the response
// reader uses them while restoring client tool representations.
func (a *grokForwardAdapter) ClientTools(mapping bridge.ResponsesClientToolMapping) {
	SetGrokResponsesClientToolMapping(a.c, mapping)
}

// CacheIdentity resolves this request's prompt-cache key using its final
// upstream model. The shared identity rule returns empty for compact requests,
// missing positive Key identity, empty model or absent session/content seed.
func (a *grokForwardAdapter) CacheIdentity(body []byte, model string) string {
	return ResolveGrokCacheIdentity(a.c, body, "", model)
}

// FreeCacheRoute delegates provider/request cache policy with retained tool
// intent and identity. It returns the possibly modified body or a preparation
// error for the platform to stop before transport.
func (a *grokForwardAdapter) FreeCacheRoute(body, intent []byte, identity string) ([]byte, error) {
	return ApplyGrokFreeRequestToolCacheRoute(a.c, body, intent, a.provider, identity)
}

// Credential resolves the token within this HTTP request's shared credential
// budget. It stores the returned token on the adapter and propagates
// resolution errors before any authenticated request is built.
func (a *grokForwardAdapter) Credential(ctx context.Context) error {
	token, _, err := a.s.Credentials.Resolve(ctx, RequestCredentialBudget(a.c), CredentialObserver{Context: a.c}, a.provider)
	a.token = token
	return err
}

// Detach supplies an upstream context retaining request values and server-abort
// state. Client cancellation and deadlines are removed by the shared helper,
// whose returned cancellation callback is currently a no-op.
func (a *grokForwardAdapter) Detach(ctx context.Context) (context.Context, func()) {
	return gatewayprovider.DetachUpstreamContext(ctx)
}

// ResolveProxy records the selected provider's hydrated proxy URL when both
// proxy identity and proxy data exist. Otherwise the adapter keeps its
// existing value, which starts empty for each new request.
func (a *grokForwardAdapter) ResolveProxy() {
	if a.provider.Record.ProxyID != nil && a.provider.Record.Proxy != nil {
		a.proxyURL = a.provider.Record.Proxy.URL()
	}
}

// Build creates a Responses request using this adapter's stored credential and
// cache identity. The settings flag controls runtime route defaults;
// preparation errors are returned before transport begins.
func (a *grokForwardAdapter) Build(ctx context.Context, body []byte, identity string, settings bool) (*http.Request, error) {
	return a.s.BuildResponsesRequest(ctx, a.c, a.provider, body, a.token, identity, settings)
}

// Do submits the prepared request through the shared provider-isolated
// transport. It uses the stored proxy URL and selected provider concurrency,
// leaving final response-body ownership to platform execution.
func (a *grokForwardAdapter) Do(req *http.Request) (*http.Response, error) {
	return a.s.Transport.Do(req, a.proxyURL, a.provider.Record.ID, a.provider.Record.Concurrency)
}

// ReadError reads bounded error-body bytes through the shared Responses
// output. A nil response or body returns nil; read errors are suppressed while
// already read bytes remain available, and platform execution owns body
// closure.
func (a *grokForwardAdapter) ReadError(resp *http.Response) []byte {
	return a.s.Output.ReadErrorBody(resp)
}

// Latency records the supplied upstream elapsed time in milliseconds for Ops.
func (a *grokForwardAdapter) Latency(value int64) {
	SetOpsLatencyMs(a.c, OpsUpstreamLatencyMsKey, value)
}

// TransportError classifies an exchange failure through the request's
// transport-error presenter. The supplied context and provider determine
// cancellation and policy handling before grokforward interprets the returned
// failure.
func (a *grokForwardAdapter) TransportError(ctx context.Context, err error) error {
	return a.s.Failure.Handle(ctx, a.c, a.provider, err, false)
}

// ReplayNotice logs a provider-local decryption replay and whether cache
// identity exists.
func (a *grokForwardAdapter) ReplayNotice(identity bool) {
	slog.Info("grok_replay_decode_retry", "provider_id", a.provider.Record.ID, "cache_identity_present", identity)
}

// ErrorMessage extracts an upstream message and sanitizes query credentials
// for diagnostics. Empty or unrecognized error bytes can produce an empty
// message, which grokforward replaces with status-specific diagnostic text.
func (a *grokForwardAdapter) ErrorMessage(body []byte) string {
	return logredact.SanitizeUpstreamQueries(upstream.ExtractErrorMessage(body))
}

// Health applies Grok response policy and translates its decision into
// execution flags. teamContext supplies a trimmed model for team-scoped
// observation; failover and same-provider retry retain selected provider
// policy.
func (a *grokForwardAdapter) Health(ctx context.Context, status int, headers http.Header, body []byte, model string, teamContext bool) grokforward.Decision {
	teamModel := ""
	if teamContext {
		teamModel = strings.TrimSpace(model)
	}
	d := gatewayprovider.ApplyGrokExecutionHealth(ctx, a.s.Health, a.provider, status, headers, body, teamModel, model)
	return grokforward.Decision{
		Generic:           d.ShouldReturnGenericError(),
		Failover:          d.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(a.provider), status, gatewayprovider.ShouldFailoverGrokResponse(status, body)),
		RetrySameProvider: d.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(a.provider), status),
	}
}

// Observe appends grokforward error metadata to the active HTTP request
// diagnostics.
func (a *grokForwardAdapter) Observe(n grokforward.Notice) {
	AppendOpsUpstreamError(a.c, ops.OpsUpstreamErrorEvent{
		Platform:           n.Platform,
		ProviderID:         n.ProviderID,
		ProviderName:       n.ProviderName,
		UpstreamStatusCode: n.UpstreamStatusCode,
		UpstreamRequestID:  n.UpstreamRequestID,
		Kind:               n.Kind,
		Message:            n.Message,
	})
}

// HandleError delegates final Responses error presentation to the shared
// output service. It passes the current provider and model, returning any
// result and error selected by that presentation path.
func (a *grokForwardAdapter) HandleError(ctx context.Context, resp *http.Response, body []byte, model string) (*forwardcore.OpenAIResult, error) {
	v, err := a.s.Output.ResponseError(ctx, resp, a.c, a.provider, body, model)
	return v, err
}

// ShouldMarkTeam reports whether this response requires a Grok team-model
// quota overlay.
func (a *grokForwardAdapter) ShouldMarkTeam(status int, body []byte) bool {
	return grok.ShouldMarkGrokTeamModelRateLimit(status, body)
}

// MarkTeam records a team-model cooldown using the current provider and
// resolved reset rule. Its ten-minute candidate is passed through the provider
// overlay policy before later requests consult that model block.
func (a *grokForwardAdapter) MarkTeam(model string) {
	// A ten-minute candidate lets the provider overlay choose its applicable
	// reset rule while the selected model, rather than sibling models, carries the block.
	providercore.MarkGrokTeamModelRateLimit(gatewayprovider.ExecutionRecord(a.provider), model, providercore.ResolveGrokTeamRateLimitUntil(time.Now().Add(10*time.Minute), time.Now()))
}

// RetryMetadata returns same-provider retry guidance derived from provider
// policy and response bytes. Delay, deadline and maximum remain separate
// inputs for the outer request loop's retry decision.
func (a *grokForwardAdapter) RetryMetadata(status int, body []byte) grokforward.Retry {
	retry, delay, deadline, max := gatewayprovider.GrokSameProviderRetryMetadata(a.provider, status, body)
	return grokforward.Retry{Retryable: retry, Delay: delay, Deadline: deadline, Max: max}
}

// Failure converts platform failover data into the shared
// UpstreamFailoverError value. It retains response byte/header storage and
// retry metadata so the outer provider loop receives the same attempt
// evidence.
func (a *grokForwardAdapter) Failure(f grokforward.Failure) error {
	return &forwardcore.UpstreamFailoverError{
		StatusCode:                f.StatusCode,
		ResponseBody:              f.ResponseBody,
		ResponseHeaders:           f.ResponseHeaders,
		RetryableOnSameProvider:   f.RetryableOnSameProvider,
		RequestScopedTransient:    f.RequestScopedTransient,
		SameProviderRetryDelay:    f.SameProviderRetryDelay,
		SameProviderRetryDeadline: f.SameProviderRetryDeadline,
		SameProviderRetryMax:      f.SameProviderRetryMax,
	}
}

// ObserveSuccess forwards response headers and status to Grok provider health
// observation.
func (a *grokForwardAdapter) ObserveSuccess(ctx context.Context, headers http.Header, status int, model string) {
	a.s.Health.ObserveResponse(ctx, a.provider.View(), headers, status, model)
}

// ReadStream translates shared Responses parsing into platform observation
// while retaining any accompanying error. A nil parsed observation yields an
// empty value with that error. The supplied start time and models are passed
// to the HTTP stream reader.
//
// For a nonnil observation, the adapter keeps measured usage, response ID,
// media/search units together with HTTP commitment,
// retry commitment and client disconnection. The raw upstream model comes from
// this request's separate observer, so restored client model text does not
// replace the observed vendor identity.
func (a *grokForwardAdapter) ReadStream(ctx context.Context, resp *http.Response, start time.Time, original, mapped string) (upstream.ResponsesObservation, error) {
	v, err := a.s.Output.ReadStreamObservation(ctx, resp, a.c, a.provider, start, original, mapped, "")
	if v == nil {
		return upstream.ResponsesObservation{}, err
	}
	// Preserve observed usage beside the reader error, and take vendor model
	// identity from its observer after client-facing model text may be restored.
	return upstream.ResponsesObservation{
		UpstreamResponseModel: UpstreamResponseModelObserverFromContext(a.c).Model(),
		Usage:                 v.Usage,
		HasUsage:              v.HasUsage,
		Served:                v.Served,
		HTTPCommitted:         v.HttpCommitted,
		RetryCommitted:        v.RetryCommitted,
		ClientDisconnected:    v.ClientDisconnected,

		ResponseID:       v.ResponseID,
		SearchCount:      v.SearchCount,
		ImageCount:       v.ImageCount,
		ImageFrameCount:  v.ImageFrameCount,
		ImageOutputSizes: v.ImageOutputSizes,
	}, err
}

// ReadNonStream translates a successful buffered Responses result into
// platform observation. A parser error returns an empty observation, leaving
// completion to interpret the failure rather than retaining a failed result.
//
// Buffered client output can restore the requested model before this adapter
// builds its observation. The separate request observer supplies the vendor
// model, while the parsed result supplies usage and response identifiers.
// HTTP commitment and the request's retry marker are copied independently
// because writing bytes and closing the retry window are separate decisions.
func (a *grokForwardAdapter) ReadNonStream(ctx context.Context, resp *http.Response, original, mapped string) (upstream.ResponsesObservation, error) {
	v, err := a.s.Output.NonStream(ctx, resp, a.c, a.provider, original, mapped)
	if err != nil {
		return upstream.ResponsesObservation{}, err
	}
	// Buffered output can restore client model text before this translation, so
	// obtain the vendor model from the separate request observer for completion.
	return upstream.ResponsesObservation{
		UpstreamResponseModel: UpstreamResponseModelObserverFromContext(a.c).Model(),
		Usage:                 v.Usage,
		HasUsage:              v.Usage != nil,
		Served:                v.Served,
		HTTPCommitted:         a.c.Writer.Written(),
		RetryCommitted:        IsResponseCommitted(a.c),
		ResponseID:            v.ResponseID,
		SearchCount:           v.SearchCount,
		ImageCount:            v.ImageCount,
		ImageFrameCount:       v.ImageFrameCount,
		ImageOutputSizes:      v.ImageOutputSizes,
	}, nil
}

// Sink returns synchronous HTTP output, or nil when no Gin context exists. The
// returned writer belongs to the active request, and platform code uses it
// while HTTP execution is in progress.
func (a *grokForwardAdapter) Sink() upstream.OutputSink {
	if a.c == nil {
		return nil
	}
	return NewResponseSink(a.c)
}

// Effort returns a reasoning-effort pointer extracted from the OpenAI body.
// Absent or invalid control returns nil, and the model argument is unused
// because extraction precedes model-specific policy.
func (a *grokForwardAdapter) Effort(body []byte, model string) *string {
	return requeststate.ExtractOpenAIReasoningEffortFromBody(body)
}

// ReadBody reads the upstream body with the configured bounded response limit.
// This path supplies no oversize writer; the returned error lets platform
// execution decide the final response and retry result.
func (a *grokForwardAdapter) ReadBody(resp *http.Response) ([]byte, error) {
	return ReadUpstreamResponseBody(resp.Body, a.s.Output.Options.ReadLimit, a.c, nil)
}

// HasTokens reports whether OpenAI usage contains a positive input, output,
// image or cache token bucket. Nil and all-zero usage return false; this
// shared predicate also counts image detail buckets.
func (a *grokForwardAdapter) HasTokens(usage *protocolopenai.ForwardUsage) bool {
	return protocolopenai.OpenAIUsageHasTokens(usage)
}
