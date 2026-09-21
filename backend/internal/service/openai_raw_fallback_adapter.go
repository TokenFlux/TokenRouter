// Chat-only 兼容端口保留旧上下文和账号投影，不持有转换状态或请求循环。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

type openAIRawFallbackAdapter struct {
	*openAIMessagesExecutionAdapter
	kind forward.NativeAnthropicKind
}

func (p *openAIRawFallbackAdapter) Profile() forward.MessagesProfile {
	return forward.MessagesProfile{Profile: openAIForwardProfile(p.account), ID: p.account.ID}
}
func (p *openAIRawFallbackAdapter) errorWriter() func(*gin.Context, int, string, string) {
	if p.kind == forward.NativeMessages {
		return httpapi.WriteForwardAnthropicError
	}
	return httpapi.WriteForwardResponsesFallbackError
}
func (p *openAIRawFallbackAdapter) Error(status int, kind, message string) {
	p.errorWriter()(p.c, status, kind, message)
}
func (p *openAIRawFallbackAdapter) ThinkingFallback(e *string, b []byte, m string) *string {
	return ApplyThinkingEnabledFallback(e, b, m)
}
func (p *openAIRawFallbackAdapter) NormalizeGLM(b []byte, m string) ([]byte, bool) {
	return NormalizeGLMOpenAIReasoningEffort(b, m)
}
func (p *openAIRawFallbackAdapter) FastFallback(ctx context.Context, m string, b []byte) ([]byte, error) {
	updated, err := p.s.applyOpenAIFastPolicyToBody(ctx, p.account, m, b)
	var blocked *tierpolicy.BlockedError
	if errors.As(err, &blocked) {
		writeOpenAIFastPolicyBlockedResponse(p.c, blocked)
	}
	return updated, err
}
func (p *openAIRawFallbackAdapter) RecacheInput(b json.RawMessage) {
	p.s.recacheReasoningItemsFromInput(b)
}
func (p *openAIRawFallbackAdapter) ReasoningContent(id string) string {
	return p.s.reasoningContentByID(id)
}
func (p *openAIRawFallbackAdapter) EffectiveEffort(b, original []byte, models ...string) *string {
	return extractEffectiveOpenAIReasoningEffortFromBody(b, original, models...)
}
func (p *openAIRawFallbackAdapter) ObserveModel(m string) { httpapi.SetOpsUpstreamModel(p.c, m) }
func (p *openAIRawFallbackAdapter) Target(ctx context.Context) (string, string, error) {
	return p.s.resolveCCFallbackTarget(ctx, p.account)
}
func (p *openAIRawFallbackAdapter) Endpoint(v string) {
	httpapi.SetActualOpenAIUpstreamEndpoint(p.c, v)
}
func (p *openAIRawFallbackAdapter) SendCC(ctx context.Context, url string, b []byte, stream bool, key string) (*http.Response, error) {
	return p.s.sendCCUpstreamRequest(ctx, p.c, p.account, url, b, stream, key, p.account.GetOpenAIUserAgent(), "", p.tls...)
}
func (p *openAIRawFallbackAdapter) AnthropicError(r *http.Response, m string) (*forward.Result, error) {
	v, e := p.s.handleAnthropicErrorResponse(r, p.c, p.account, m)
	return openAIHTTPResultFromForward(v), e
}
func (p *openAIRawFallbackAdapter) ResponsesError(ctx context.Context, r *http.Response, b []byte, m string) (*forward.Result, error) {
	v, e := p.s.handleErrorResponse(ctx, r, p.c, p.account, b, m)
	return openAIHTTPResultFromForward(v), e
}
func (p *openAIRawFallbackAdapter) RawOptions(r *http.Response, billing, model string, tier *string) openai.RawResponseOptions {
	return p.s.nativeRawResponseOptions(p.c, r, nil, billing, model, tier, p.errorWriter())
}

func (p *openAIRawFallbackAdapter) AnthropicToChat(r *protocolanthropic.AnthropicRequest) (*protocolopenai.ChatCompletionsRequest, error) {
	return protocolbridge.AnthropicToChatCompletionsRequest(r, protocolforward.ConversionOptionsForModel(r.Model))
}
