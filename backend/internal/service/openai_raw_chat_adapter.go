// 原生 Chat 只在旧边界投影凭据及平台专有操作；请求编排由目标包拥有。
package service

import (
	"context"
	"errors"
	"net/http"

	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type openAIRawChatAdapter struct{ *openAIRawFallbackAdapter }

func (p *openAIRawChatAdapter) Profile() forward.MessagesProfile {
	return forward.MessagesProfile{Profile: openAIForwardProfile(p.account), ID: p.account.Record.ID, GrokOAuth: p.account.View().IsGrokOAuth()}
}
func (p *openAIRawChatAdapter) Error(status int, kind, message string) {
	gatewayhttp.WriteForwardChatError(p.c, status, kind, message)
}
func (p *openAIRawChatAdapter) ReplaceModel(b []byte, m string) []byte {
	return openai.ReplaceModelInBody(b, m)
}
func (p *openAIRawChatAdapter) FastRaw(ctx context.Context, m string, b []byte) ([]byte, error) {
	updated, err := tierpolicy.ApplyBody(b, p.s.fastPolicy.Input(ctx, p.account, m))
	var blocked *tierpolicy.BlockedError
	if errors.As(err, &blocked) {
		gatewayhttp.MarkOpsClientBusinessLimited(p.c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		gatewayhttp.WriteForwardChatError(p.c, http.StatusForbidden, "permission_error", blocked.Message)
	}
	return updated, err
}
func (p *openAIRawChatAdapter) StripViewImage(b []byte) ([]byte, error) {
	return grok.StripRedundantGrokChatViewImageTool(b)
}
func (p *openAIRawChatAdapter) RawCredential(ctx context.Context) (string, string, error) {
	return p.s.requestCredentials.Resolve(ctx, gatewayhttp.RequestCredentialBudget(p.c), gatewayhttp.CredentialObserver{Context: p.c}, p.account)
}
func (p *openAIRawChatAdapter) BridgeImages(ctx context.Context, b []byte, key string) ([]byte, openai.ForwardUsage, bool, error) {
	updated, usage, changed, err := p.s.Grok.BridgeComposerImages(ctx, p.c, p.account, b, key)
	var failover *forwardcore.UpstreamFailoverError
	if err != nil && !errors.As(err, &failover) && p.c != nil && p.c.Writer != nil && !p.c.Writer.Written() {
		gatewayhttp.WriteForwardChatError(p.c, http.StatusBadGateway, "upstream_error", err.Error())
	}
	return updated, usage, changed, err
}
func (p *openAIRawChatAdapter) StripGrokCacheKey(b []byte) ([]byte, error) {
	return grok.StripGrokChatPromptCacheKey(b)
}
func (p *openAIRawChatAdapter) GrokEffort(b []byte, m string) ([]byte, error) {
	return gatewayprovider.GrokBodyCodec().NormalizeGrokChatReasoningEffort(b, m)
}
func (p *openAIRawChatAdapter) OllamaBody(b []byte) []byte {
	return gatewayprovider.ApplyOllamaCloudRawChatCompletionsRequest(p.account, b)
}
func (p *openAIRawChatAdapter) RawTarget() (string, error) {
	return p.s.rawChatCompletionsURL(p.account)
}
func (p *openAIRawChatAdapter) UserAgent() string     { return p.account.View().GetOpenAIUserAgent() }
func (p *openAIRawChatAdapter) GrokUserAgent() string { return grok.DefaultGrokUpstreamUserAgent() }
func (p *openAIRawChatAdapter) SendRaw(ctx context.Context, url string, b []byte, stream bool, key, ua, identity string) (*http.Response, error) {
	return p.s.sendCCUpstreamRequest(ctx, p.c, p.account, url, b, stream, key, ua, identity, p.tls...)
}
func (p *openAIRawChatAdapter) ChatErrorResponse(r *http.Response, m string) (*forward.Result, error) {
	v, e := p.s.handleChatCompletionsErrorResponse(r, p.c, p.account, m)
	return openAIHTTPResultFromForward(v), e
}
func (p *openAIRawChatAdapter) RawOptions(r *http.Response, billing, model string, tier *string) upstreamopenai.RawResponseOptions {
	return p.s.responseOutput.RawOptions(p.c, r, p.account, billing, model, tier, gatewayhttp.WriteForwardChatError)
}

// GrokDecision 复用平台的健康解释，仅返回编排需要的三个判断。
func (p *openAIRawChatAdapter) GrokDecision(ctx context.Context, resp *http.Response, b []byte, m string) forward.RawGrokDecision {
	d := gatewayprovider.ApplyGrokExecutionHealth(ctx, p.s.grokHealth, p.account, resp.StatusCode, resp.Header, b, "", m)
	return forward.RawGrokDecision{Failover: d.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(p.account), resp.StatusCode, gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, b)), Generic: d.ShouldReturnGenericError(), RetrySame: d.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(p.account), resp.StatusCode)}
}
func (p *openAIRawChatAdapter) ObserveGrokError(r *http.Response, msg, kind string) {
	gatewayhttp.AppendOpsUpstreamError(p.c, ops.OpsUpstreamErrorEvent{Platform: p.account.Record.Platform, AccountID: p.account.Record.ID, AccountName: p.account.Record.Name, UpstreamStatusCode: r.StatusCode, UpstreamRequestID: requeststate.FirstNonEmpty(r.Header.Get("x-request-id"), r.Header.Get("xai-request-id")), Kind: kind, Message: msg})
}
func (p *openAIRawChatAdapter) GrokRetry(status int, b []byte) forward.RawGrokRetry {
	retry, delay, deadline, max := gatewayprovider.GrokSameAccountRetryMetadata(p.account, status, b)
	return forward.RawGrokRetry{Retryable: retry, Delay: delay, Deadline: deadline, Max: max}
}
func (p *openAIRawChatAdapter) GrokFailover(r *http.Response, b []byte, retry forward.RawGrokRetry, healthRetry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: r.StatusCode, ResponseBody: b, ResponseHeaders: r.Header.Clone(), RetryableOnSameAccount: retry.Retryable || healthRetry, RequestScopedTransient: retry.Retryable && r.StatusCode == http.StatusTooManyRequests, SameAccountRetryDelay: retry.Delay, SameAccountRetryDeadline: retry.Deadline, SameAccountRetryMax: retry.Max}
}
