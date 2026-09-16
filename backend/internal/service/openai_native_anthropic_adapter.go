// 三种原生 Anthropic 调用按原入口选择错误输出，账号与传输只作参数投影。
package service

import (
	"context"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
	"net/http"
)

type openAINativeAnthropicAdapter struct {
	*openAIMessagesExecutionAdapter
	kind forward.NativeAnthropicKind
}

func (p *openAINativeAnthropicAdapter) errorWriter() func(*gin.Context, int, string, string) {
	switch p.kind {
	case forward.NativeResponses:
		return writeResponsesError
	case forward.NativeChat:
		return writeChatCompletionsError
	default:
		return writeAnthropicError
	}
}
func (p *openAINativeAnthropicAdapter) Profile() forward.MessagesProfile {
	return forward.MessagesProfile{Profile: openAIForwardProfile(p.account), ID: p.account.ID}
}
func (p *openAINativeAnthropicAdapter) Error(status int, kind, message string) {
	p.errorWriter()(p.c, status, kind, message)
}
func (p *openAINativeAnthropicAdapter) NormalizeThinking(body []byte, model string) ([]byte, bool) {
	return NormalizeGLM53AnthropicThinking(body, model)
}
func (p *openAINativeAnthropicAdapter) ThinkingFallback(effort *string, body []byte, model string) *string {
	return ApplyThinkingEnabledFallback(effort, body, model)
}
func (p *openAINativeAnthropicAdapter) StripEmpty(body []byte) []byte {
	return StripEmptyTextBlocks(body)
}
func (p *openAINativeAnthropicAdapter) FilterSearch(body []byte, model string) []byte {
	return FilterWebSearchHistoryBlocks(body, model)
}
func (p *openAINativeAnthropicAdapter) CacheLimit(body []byte) []byte {
	return enforceCacheControlLimit(body)
}
func (p *openAINativeAnthropicAdapter) Log(format string, args ...any) {
	logger.LegacyPrintf("service.gateway", format, args...)
}
func (p *openAINativeAnthropicAdapter) ProtocolAPIKey() string {
	return p.account.GetOpenAIProtocolAPIKey()
}
func (p *openAINativeAnthropicAdapter) TargetURL() (string, error) {
	return p.s.nativeAnthropicTargetURL(p.account)
}
func (p *openAINativeAnthropicAdapter) StreamContext(ctx context.Context, stream bool) (context.Context, context.CancelFunc) {
	return detachStreamUpstreamContext(ctx, stream)
}
func (p *openAINativeAnthropicAdapter) BuildNative(ctx context.Context, body []byte, key, url string) (*http.Request, error) {
	r, _, err := p.s.buildNativeAnthropicUpstreamRequest(ctx, p.c, p.account, body, key, url)
	return r, err
}
func (p *openAINativeAnthropicAdapter) SendNative(r *http.Request) (*http.Response, error) {
	return p.s.httpUpstream.Do(r, p.proxyURL, p.account.ID, p.account.Concurrency)
}
func (p *openAINativeAnthropicAdapter) TransportErrorNative(ctx context.Context, err error) error {
	return p.s.handleOpenAIUpstreamTransportError(ctx, p.c, p.account, err, true)
}
func (p *openAINativeAnthropicAdapter) DirectOptions() forward.NativeAnthropicOptions {
	return p.s.nativeAnthropicDirectOptions(p.c, p.account)
}
func (p *openAINativeAnthropicAdapter) AdaptResponsesTools(body []byte) ([]byte, bridge.ResponsesClientToolMapping, error) {
	return adaptResponsesClientToolsForAnthropic(body)
}
func (p *openAINativeAnthropicAdapter) ResponsesToAnthropic(r *protocolopenai.ResponsesRequest) (*protocolanthropic.AnthropicRequest, error) {
	return apicompat.ResponsesToAnthropicRequest(r)
}
func (p *openAINativeAnthropicAdapter) ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error) {
	return apicompat.ChatCompletionsToResponses(r)
}
func (p *openAINativeAnthropicAdapter) ResponsesEffort(body []byte, models ...string) *string {
	return ExtractResponsesReasoningEffortFromBody(body, models...)
}
func (p *openAINativeAnthropicAdapter) ChatEffort(body []byte, models ...string) *string {
	return extractCCReasoningEffortFromBody(body, models...)
}
func (p *openAINativeAnthropicAdapter) MapStatus(status int) int {
	return mapUpstreamStatusCode(status)
}
func (p *openAINativeAnthropicAdapter) OutputOptions() forward.AnthropicOutputOptions {
	return p.s.nativeAnthropicOutputOptions(p.c, p.errorWriter())
}
