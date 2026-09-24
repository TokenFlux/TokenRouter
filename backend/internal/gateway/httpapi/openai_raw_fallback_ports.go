// Chat-only 兼容端口保留旧上下文和账号投影，不持有转换状态或请求循环。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

type openAIRawFallbackAdapter struct {
	*openAIMessagesExecutionAdapter
	kind openaiexecution.NativeAnthropicKind
}

func (p *openAIRawFallbackAdapter) Profile() openaiexecution.MessagesProfile {
	return openaiexecution.MessagesProfile{Profile: openAIForwardProfile(p.account), ID: p.account.Record.ID}
}
func (p *openAIRawFallbackAdapter) errorWriter() func(*gin.Context, int, string, string) {
	if p.kind == openaiexecution.NativeMessages {
		return WriteForwardAnthropicError
	}
	return WriteForwardResponsesFallbackError
}
func (p *openAIRawFallbackAdapter) Error(status int, kind, message string) {
	p.errorWriter()(p.c, status, kind, message)
}
func (p *openAIRawFallbackAdapter) ThinkingFallback(e *string, b []byte, m string) *string {
	return gatewayprovider.ApplyThinkingEnabledFallback(e, b, m)
}
func (p *openAIRawFallbackAdapter) NormalizeGLM(b []byte, m string) ([]byte, bool) {
	return gatewayprovider.NormalizeGLMOpenAIReasoningEffort(b, m)
}
func (p *openAIRawFallbackAdapter) FastFallback(ctx context.Context, m string, b []byte) ([]byte, error) {
	updated, err := tierpolicy.ApplyBody(b, p.s.FastPolicy.Input(ctx, p.account, m))
	var blocked *tierpolicy.BlockedError
	if errors.As(err, &blocked) {
		WriteFastPolicyBlockedResponse(p.c, blocked)
	}
	return updated, err
}
func (p *openAIRawFallbackAdapter) RecacheInput(b json.RawMessage) {
	p.s.Output.Reasoning.FromInput(b)
}
func (p *openAIRawFallbackAdapter) ReasoningContent(id string) string {
	return p.s.Output.Reasoning.Lookup(id)
}
func (p *openAIRawFallbackAdapter) EffectiveEffort(b, original []byte, models ...string) *string {
	return requeststate.ExtractEffectiveOpenAIReasoningEffortFromBody(b, original, models...)
}
func (p *openAIRawFallbackAdapter) ObserveModel(m string) { SetOpsUpstreamModel(p.c, m) }
func (p *openAIRawFallbackAdapter) Target(ctx context.Context) (string, string, error) {
	return p.s.Requests.ChatFallbackTarget(ctx, p.account)
}
func (p *openAIRawFallbackAdapter) Endpoint(v string) {
	SetActualOpenAIUpstreamEndpoint(p.c, v)
}
func (p *openAIRawFallbackAdapter) SendCC(ctx context.Context, url string, b []byte, stream bool, key string) (*http.Response, error) {
	return p.s.Requests.SendChat(ctx, p.c, p.account, url, b, stream, key, p.account.View().GetOpenAIUserAgent(), "", p.tls...)
}
func (p *openAIRawFallbackAdapter) AnthropicError(r *http.Response, m string) (*openaiexecution.Result, error) {
	v, e := p.s.messagesError(r, p.c, p.account, m)
	return openaiexecution.FromForwardResult(v), e
}
func (p *openAIRawFallbackAdapter) ResponsesError(ctx context.Context, r *http.Response, b []byte, m string) (*openaiexecution.Result, error) {
	v, e := p.s.Output.ResponseError(ctx, r, p.c, p.account, b, m)
	return openaiexecution.FromForwardResult(v), e
}
func (p *openAIRawFallbackAdapter) RawOptions(r *http.Response, billing, model string, tier *string) openai.RawResponseOptions {
	return p.s.Output.RawOptions(p.c, r, nil, billing, model, tier, p.errorWriter())
}

func (p *openAIRawFallbackAdapter) AnthropicToChat(r *protocolanthropic.AnthropicRequest) (*protocolopenai.ChatCompletionsRequest, error) {
	return protocolbridge.AnthropicToChatCompletionsRequest(r, protocolforward.ConversionOptionsForModel(r.Model))
}
