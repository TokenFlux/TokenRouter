// 三种原生 Anthropic 调用按原入口选择错误输出，账号与传输只作参数投影。
package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"net/http"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
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
		return httpapi.WriteForwardChatError
	default:
		return httpapi.WriteForwardAnthropicError
	}
}
func (p *openAINativeAnthropicAdapter) Profile() forward.MessagesProfile {
	return forward.MessagesProfile{Profile: openAIForwardProfile(p.account), ID: p.account.Record.ID}
}
func (p *openAINativeAnthropicAdapter) Error(status int, kind, message string) {
	p.errorWriter()(p.c, status, kind, message)
}
func (p *openAINativeAnthropicAdapter) NormalizeThinking(body []byte, model string) ([]byte, bool) {
	return gatewayprovider.NormalizeGLM53AnthropicThinking(body, model)
}
func (p *openAINativeAnthropicAdapter) ThinkingFallback(effort *string, body []byte, model string) *string {
	return gatewayprovider.ApplyThinkingEnabledFallback(effort, body, model)
}
func (p *openAINativeAnthropicAdapter) StripEmpty(body []byte) []byte {
	return protocolanthropic.StripEmptyTextBlocks(body)
}
func (p *openAINativeAnthropicAdapter) FilterSearch(body []byte, model string) []byte {
	return searchtools.FilterWebSearchHistoryBlocks(body, modelidentity.ResolveThinkingProtocol(model) == modelidentity.ThinkingProtocolPassbackRequired)
}
func (p *openAINativeAnthropicAdapter) CacheLimit(body []byte) []byte {
	return anthropic.EnforceCacheControlLimit(body)
}
func (p *openAINativeAnthropicAdapter) Log(format string, args ...any) {
	logging.LegacyPrintf("service.gateway", format, args...)
}
func (p *openAINativeAnthropicAdapter) ProtocolAPIKey() string {
	return p.account.View().GetOpenAIProtocolAPIKey()
}
func (p *openAINativeAnthropicAdapter) TargetURL() (string, error) {
	return p.s.nativeAnthropicTargetURL(p.account)
}
func (p *openAINativeAnthropicAdapter) StreamContext(ctx context.Context, stream bool) (context.Context, context.CancelFunc) {
	return gatewayprovider.DetachStreamUpstreamContext(ctx, stream)
}
func (p *openAINativeAnthropicAdapter) BuildNative(ctx context.Context, body []byte, key, url string) (*http.Request, error) {
	r, _, err := p.s.buildNativeAnthropicUpstreamRequest(ctx, p.c, p.account, body, key, url)
	return r, err
}
func (p *openAINativeAnthropicAdapter) SendNative(r *http.Request) (*http.Response, error) {
	return p.s.httpUpstream.Do(r, p.proxyURL, p.account.Record.ID, p.account.Record.Concurrency)
}
func (p *openAINativeAnthropicAdapter) TransportErrorNative(ctx context.Context, err error) error {
	return p.s.transportFailure.Handle(ctx, p.c, p.account, err, true)
}
func (p *openAINativeAnthropicAdapter) DirectOptions() forward.NativeAnthropicOptions {
	return p.s.responseOutput.AnthropicDirectOptions(p.c, p.account)
}
func (p *openAINativeAnthropicAdapter) AdaptResponsesTools(body []byte) ([]byte, bridge.ResponsesClientToolMapping, error) {
	return protocolforward.AdaptResponsesClientToolsForAnthropic(body)
}
func (p *openAINativeAnthropicAdapter) ResponsesToAnthropic(r *protocolopenai.ResponsesRequest) (*protocolanthropic.AnthropicRequest, error) {
	return bridge.ResponsesToAnthropicRequest(r)
}
func (p *openAINativeAnthropicAdapter) ChatToResponses(r *protocolopenai.ChatCompletionsRequest) (*protocolopenai.ResponsesRequest, error) {
	return bridge.ChatCompletionsToResponses(r, protocolforward.ConversionOptionsForModel(r.Model))
}
func (p *openAINativeAnthropicAdapter) ResponsesEffort(body []byte, models ...string) *string {
	return ExtractResponsesReasoningEffortFromBody(body, models...)
}
func (p *openAINativeAnthropicAdapter) ChatEffort(body []byte, models ...string) *string {
	return extractCCReasoningEffortFromBody(body, models...)
}
func (p *openAINativeAnthropicAdapter) MapStatus(status int) int {
	return protocolforward.MapStatus(status)
}
func (p *openAINativeAnthropicAdapter) OutputOptions() forward.AnthropicOutputOptions {
	return p.s.responseOutput.AnthropicOptions(p.c, p.errorWriter())
}
