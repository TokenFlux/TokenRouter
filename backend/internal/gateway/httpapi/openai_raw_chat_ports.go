package httpapi

import (
	"context"
	"errors"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_raw_chat_ports.go binds raw Chat execution to platform policy and
// Grok retry observations.
// Native Chat execution applies fast-tier, Grok image/cache/effort and Ollama
// compatibility policies before sending one selected provider request. The raw
// adapter reuses fallback services for credentials and observations, while
// Grok health and retry metadata remain platform-specific inputs to the shared
// runner.
//
// GrokFailover combines the retry helper's eligibility with the health
// decision's same-provider permission, so either can enable retry.
// Request-scoped transience follows the narrower metadata-eligible HTTP 429
// condition. Delay, deadline and cap still come from the retry metadata, and
// the returned headers are copied for the outer loop. The runner separately
// enforces output commitment and its request-wide budget before another
// attempt can use those values.

// openAIRawChatAdapter binds native Chat execution to shared fallback attempt
// services.
type openAIRawChatAdapter struct {
	// openAIRawFallbackAdapter shares credentials, request state and response
	// services with the native Chat runner for the current selected provider.
	*openAIRawFallbackAdapter
}

// Profile describes this selected provider and its Grok OAuth mode.
func (p *openAIRawChatAdapter) Profile() openaiexecution.MessagesProfile {
	return openaiexecution.MessagesProfile{Profile: openAIForwardProfile(p.provider), ID: p.provider.Record.ID, GrokOAuth: p.provider.View().IsGrokOAuth()}
}

// Error writes a committed Chat rejection for this provider attempt.
func (p *openAIRawChatAdapter) Error(status int, kind, message string) {
	WriteForwardChatError(p.c, status, kind, message)
}

// FastRaw applies the provider fast-tier policy to the outgoing native Chat
// body. A BlockedError marks local business limitation and writes a committed
// permission error; the runner stops before upstream sending on that returned
// error.
func (p *openAIRawChatAdapter) FastRaw(ctx context.Context, m string, b []byte) ([]byte, error) {
	updated, err := tierpolicy.ApplyBody(b, p.s.FastPolicy.Input(ctx, p.provider, m))
	if blocked, ok := errors.AsType[*tierpolicy.BlockedError](err); ok {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		WriteForwardChatError(p.c, http.StatusForbidden, "permission_error", blocked.Message)
	}
	return updated, err
}

// RawCredential resolves the selected provider's credential and
// credential-kind through the request budget and observer. Resolution errors
// reach the raw runner before sending; the provider service owns credential
// refresh and any recovery decisions.
func (p *openAIRawChatAdapter) RawCredential(ctx context.Context) (string, string, error) {
	return p.s.Credentials.Resolve(ctx, RequestCredentialBudget(p.c), CredentialObserver{Context: p.c}, p.provider)
}

// BridgeImages delegates Grok Composer image description and returns its body,
// observed usage and applicability. A nonfailover error writes Chat 502 while
// the writer is still available; a typed failover error remains with the
// runner for recovery.
func (p *openAIRawChatAdapter) BridgeImages(ctx context.Context, b []byte, key string) ([]byte, openai.ForwardUsage, bool, error) {
	updated, usage, changed, err := p.s.Grok.BridgeComposerImages(ctx, p.c, p.provider, b, key)
	var failover *forwardcore.UpstreamFailoverError
	// A nonrecoverable image-preflight failure needs a client rejection while
	// the response remains unwritten; typed failover stays available to the provider loop.
	if err != nil && !errors.As(err, &failover) && p.c != nil && p.c.Writer != nil && !p.c.Writer.Written() {
		WriteForwardChatError(p.c, http.StatusBadGateway, "upstream_error", err.Error())
	}
	return updated, usage, changed, err
}

// GrokEffort resolves model attributes for the selected Chat provider and
// supplies them to the Grok body codec. Resolution and normalization errors
// reach the runner; attribute data for this normalization comes from that
// resolved model.
func (p *openAIRawChatAdapter) GrokEffort(b []byte, m string) ([]byte, error) {
	result, err := gatewayprovider.ResolveModelAttributes(p.c.Request.Context(), p.provider, m, p.s.modelAttributes(), "openai_chat_completions")
	if err != nil {
		return nil, err
	}
	codec := gatewayprovider.GrokBodyCodec()
	codec.Attributes = func(string) modelcatalog.Attributes { return result.Attributes }
	return codec.NormalizeGrokChatReasoningEffort(b, m)
}

// OllamaBody applies request repairs for recognized Ollama Cloud API-key Chat
// targets.
func (p *openAIRawChatAdapter) OllamaBody(b []byte) []byte {
	return gatewayprovider.ApplyOllamaCloudRawChatCompletionsRequest(p.provider, b)
}

// RawTarget resolves the selected provider's native Chat Completions endpoint
// URL.
func (p *openAIRawChatAdapter) RawTarget() (string, error) {
	return p.s.Requests.RawChatURL(p.provider)
}

// UserAgent reads the provider's configured OpenAI-compatible User-Agent for
// native Chat.
func (p *openAIRawChatAdapter) UserAgent() string { return p.provider.View().GetOpenAIUserAgent() }

// SendRaw resets model observation before sending the prepared native Chat
// request. It supplies this attempt's provider and TLS matches to SendChat;
// the runner closes a successful response body after reading or error
// handling.
func (p *openAIRawChatAdapter) SendRaw(ctx context.Context, url string, b []byte, stream bool, key, ua, identity string) (*http.Response, error) {
	resetResponseModel(p.c)
	return p.s.Requests.SendChat(ctx, p.c, p.provider, url, b, stream, key, ua, identity, p.tls...)
}

// ChatErrorResponse delegates native Chat HTTP rejection handling and converts
// its returned result. The runner retains response cleanup and treats any
// failover error separately from final protocol output already written by the
// handler.
func (p *openAIRawChatAdapter) ChatErrorResponse(r *http.Response, m string) (*openaiexecution.Result, error) {
	v, e := p.s.chatError(r, p.c, p.provider, m)
	return openaiexecution.FromForwardResult(v), e
}

// RawOptions binds raw Chat response reading to this provider and request. The
// runner owns response closure and keeps response headers and Gin state
// available until observations, provider-specific transforms and synchronous
// error output finish.
func (p *openAIRawChatAdapter) RawOptions(r *http.Response, billing, model string, tier *string) upstreamopenai.RawResponseOptions {
	return p.s.Output.RawOptions(p.c, r, p.provider, billing, model, tier, WriteForwardChatError)
}

// GrokDecision applies Grok health and returns failover, generic-output and
// same-provider retry decisions for the runner. It combines the health result
// with provider error settings and Grok payload classification; the runner
// separately checks its response-output window.
func (p *openAIRawChatAdapter) GrokDecision(ctx context.Context, resp *http.Response, b []byte, m string) openaiexecution.RawGrokDecision {
	d := gatewayprovider.ApplyGrokExecutionHealth(ctx, p.s.Grok.Health, p.provider, resp.StatusCode, resp.Header, b, "", m)
	return openaiexecution.RawGrokDecision{Failover: d.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(p.provider), resp.StatusCode, gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, b)), Generic: d.ShouldReturnGenericError(), RetrySame: d.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(p.provider), resp.StatusCode)}
}

// ObserveGrokError records the selected provider's native Chat rejection in
// request Ops state.
func (p *openAIRawChatAdapter) ObserveGrokError(r *http.Response, msg, kind string) {
	AppendOpsUpstreamError(p.c, ops.OpsUpstreamErrorEvent{Platform: p.provider.Record.Platform, ProviderID: p.provider.Record.ID, ProviderName: p.provider.Record.Name, UpstreamStatusCode: r.StatusCode, UpstreamRequestID: requeststate.FirstNonEmpty(r.Header.Get("x-request-id"), r.Header.Get("xai-request-id")), Kind: kind, Message: msg})
}

// GrokRetry delegates same-provider retry eligibility, delay, deadline and cap
// calculation. Ineligible failures return empty metadata, while eligible
// capacity failures add the provider helper's bounded retry policy for the raw
// runner to enforce.
func (p *openAIRawChatAdapter) GrokRetry(status int, b []byte) openaiexecution.RawGrokRetry {
	retry, delay, deadline, max := gatewayprovider.GrokSameProviderRetryMetadata(p.provider, status, b)
	return openaiexecution.RawGrokRetry{Retryable: retry, Delay: delay, Deadline: deadline, Max: max}
}

// GrokFailover packages native Grok failure evidence and recovery metadata for
// the outer loop. It clones response headers and retains the supplied body
// bytes, so callers leave those bytes stable while the failure is consumed.
//
// Same-provider retry is enabled when either retry metadata or healthRetry
// permits it. RequestScopedTransient uses the narrower condition that retry
// metadata is eligible and the HTTP status is 429. Delay, deadline and cap are
// copied from the metadata even when health alone grants eligibility; the
// runner applies its own request budget and output-window checks.
func (p *openAIRawChatAdapter) GrokFailover(r *http.Response, b []byte, retry openaiexecution.RawGrokRetry, healthRetry bool) error {
	// Health can grant retry with empty metadata; carry the metadata as supplied
	// so the outer loop still applies its own delay, cap and request budget rules.
	return &forwardcore.UpstreamFailoverError{StatusCode: r.StatusCode, ResponseBody: b, ResponseHeaders: r.Header.Clone(), RetryableOnSameProvider: retry.Retryable || healthRetry, RequestScopedTransient: retry.Retryable && r.StatusCode == http.StatusTooManyRequests, SameProviderRetryDelay: retry.Delay, SameProviderRetryDeadline: retry.Deadline, SameProviderRetryMax: retry.Max}
}
