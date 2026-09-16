// 原生 Chat 只在旧边界投影凭据及平台专有操作；请求编排由目标包拥有。
package service

import (
	"context"
	"errors"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"net/http"
)

type openAIRawChatAdapter struct{ *openAIRawFallbackAdapter }

func (p *openAIRawChatAdapter) Profile() forward.MessagesProfile {
	return forward.MessagesProfile{Profile: openAIForwardProfile(p.account), ID: p.account.ID, GrokOAuth: p.account.IsGrokOAuth()}
}
func (p *openAIRawChatAdapter) Error(status int, kind, message string) {
	writeChatCompletionsError(p.c, status, kind, message)
}
func (p *openAIRawChatAdapter) ReplaceModel(b []byte, m string) []byte {
	return ReplaceModelInBody(b, m)
}
func (p *openAIRawChatAdapter) FastRaw(ctx context.Context, m string, b []byte) ([]byte, error) {
	updated, err := p.s.applyOpenAIFastPolicyToBody(ctx, p.account, m, b)
	var blocked *OpenAIFastBlockedError
	if errors.As(err, &blocked) {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		writeChatCompletionsError(p.c, http.StatusForbidden, "permission_error", blocked.Message)
	}
	return updated, err
}
func (p *openAIRawChatAdapter) StripViewImage(b []byte) ([]byte, error) {
	return stripRedundantGrokChatViewImageTool(b)
}
func (p *openAIRawChatAdapter) RawCredential(ctx context.Context) (string, string, error) {
	return p.s.getRequestCredential(ctx, p.c, p.account)
}
func (p *openAIRawChatAdapter) BridgeImages(ctx context.Context, b []byte, key string) ([]byte, OpenAIUsage, bool, error) {
	updated, usage, changed, err := p.s.bridgeGrokComposerImageInputs(ctx, p.c, p.account, b, key)
	var failover *UpstreamFailoverError
	if err != nil && !errors.As(err, &failover) && p.c != nil && p.c.Writer != nil && !p.c.Writer.Written() {
		writeChatCompletionsError(p.c, http.StatusBadGateway, "upstream_error", err.Error())
	}
	return updated, usage, changed, err
}
func (p *openAIRawChatAdapter) StripGrokCacheKey(b []byte) ([]byte, error) {
	return stripGrokChatPromptCacheKey(b)
}
func (p *openAIRawChatAdapter) GrokEffort(b []byte, m string) ([]byte, error) {
	return normalizeGrokChatReasoningEffort(b, m)
}
func (p *openAIRawChatAdapter) OllamaBody(b []byte) []byte {
	return applyOllamaCloudRawChatCompletionsRequest(p.account, b)
}
func (p *openAIRawChatAdapter) RawTarget() (string, error) {
	return p.s.rawChatCompletionsURL(p.account)
}
func (p *openAIRawChatAdapter) UserAgent() string     { return p.account.GetOpenAIUserAgent() }
func (p *openAIRawChatAdapter) GrokUserAgent() string { return defaultGrokUpstreamUserAgent() }
func (p *openAIRawChatAdapter) SendRaw(ctx context.Context, url string, b []byte, stream bool, key, ua, identity string) (*http.Response, error) {
	return p.s.sendCCUpstreamRequest(ctx, p.c, p.account, url, b, stream, key, ua, identity, p.tls...)
}
func (p *openAIRawChatAdapter) ChatErrorResponse(r *http.Response, m string) (*forward.Result, error) {
	v, e := p.s.handleChatCompletionsErrorResponse(r, p.c, p.account, m)
	return openAIHTTPResultFromForward(v), e
}
func (p *openAIRawChatAdapter) RawOptions(r *http.Response, billing, model string, tier *string) native.RawResponseOptions {
	return p.s.nativeRawResponseOptions(p.c, r, p.account, billing, model, tier, writeChatCompletionsError)
}

// GrokDecision 复用平台的健康解释，仅返回编排需要的三个判断。
func (p *openAIRawChatAdapter) GrokDecision(ctx context.Context, resp *http.Response, b []byte, m string) forward.RawGrokDecision {
	d := p.s.applyGrokAccountUpstreamError(ctx, p.account, resp.StatusCode, resp.Header, b, m)
	return forward.RawGrokDecision{Failover: d.ShouldFailover(p.account, resp.StatusCode, p.s.shouldFailoverGrokUpstreamError(resp.StatusCode, b)), Generic: d.ShouldReturnGenericError(), RetrySame: d.RetryableOnSameAccount(p.account, resp.StatusCode)}
}
func (p *openAIRawChatAdapter) ObserveGrokError(r *http.Response, msg, kind string) {
	appendOpsUpstreamError(p.c, OpsUpstreamErrorEvent{Platform: p.account.Platform, AccountID: p.account.ID, AccountName: p.account.Name, UpstreamStatusCode: r.StatusCode, UpstreamRequestID: firstNonEmpty(r.Header.Get("x-request-id"), r.Header.Get("xai-request-id")), Kind: kind, Message: msg})
}
func (p *openAIRawChatAdapter) GrokRetry(status int, b []byte) forward.RawGrokRetry {
	retry, delay, deadline, max := grokSameAccountRetryMetadata(p.account, status, b)
	return forward.RawGrokRetry{Retryable: retry, Delay: delay, Deadline: deadline, Max: max}
}
func (p *openAIRawChatAdapter) GrokFailover(r *http.Response, b []byte, retry forward.RawGrokRetry, healthRetry bool) error {
	return &UpstreamFailoverError{StatusCode: r.StatusCode, ResponseBody: b, ResponseHeaders: r.Header.Clone(), RetryableOnSameAccount: retry.Retryable || healthRetry, RequestScopedTransient: retry.Retryable && r.StatusCode == http.StatusTooManyRequests, SameAccountRetryDelay: retry.Delay, SameAccountRetryDeadline: retry.Deadline, SameAccountRetryMax: retry.Max}
}
