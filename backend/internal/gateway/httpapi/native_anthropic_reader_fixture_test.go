package httpapi

import (
	"net/http"
	"time"

	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
)

// nativeAnthropicReaderFixture 只为原协议读取契约提供输出参数。
type nativeAnthropicReaderFixture struct{ output *OpenAIResponseOutput }

// handleCCBufferedFromNativeAnthropic 读取 Anthropic SSE 事件并组装完整响应，
// 再按 Anthropic → Responses → Chat Completions 转换。
func (s *nativeAnthropicReaderFixture) handleCCBufferedFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
) (*forwardcore.OpenAIResult, error) {
	result, err := openaiexecution.ChatFromAnthropicBuffered(resp, upstream.NewDeferredOutputContext(ResponseSink{Writer: c.Writer}), s.output.AnthropicOptions(c, WriteForwardChatError), originalModel, billingModel, upstreamModel, reasoningEffort, startTime)
	return openaiexecution.ToForwardResult(result), err
}

// handleCCStreamingFromNativeAnthropic 逐个读取 Anthropic SSE 事件，先转换为
// Responses 事件，再转换为 Chat Completions 分块并写出。
func (s *nativeAnthropicReaderFixture) handleCCStreamingFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	includeUsage bool,
) (*forwardcore.OpenAIResult, error) {
	result, err := openaiexecution.ChatFromAnthropicStreaming(resp, upstream.NewDeferredOutputContext(ResponseSink{Writer: c.Writer}), s.output.AnthropicOptions(c, WriteForwardChatError), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, includeUsage)
	return openaiexecution.ToForwardResult(result), err
}

// handleResponsesBufferedFromNativeAnthropic 读取 Anthropic SSE 事件并组装完整
// 响应，再按 Anthropic → Responses 转换。
func (s *nativeAnthropicReaderFixture) handleResponsesBufferedFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping bridge.ResponsesClientToolMapping,
) (*forwardcore.OpenAIResult, error) {
	result, err := openaiexecution.ResponsesFromAnthropicBuffered(resp, upstream.NewDeferredOutputContext(ResponseSink{Writer: c.Writer}), s.output.AnthropicOptions(c, writeTextResponsesError), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
	return openaiexecution.ToForwardResult(result), err
}

// handleResponsesStreamingFromNativeAnthropic 逐个读取 Anthropic SSE 事件，
// 转换为 Responses SSE 事件后写给客户端。
func (s *nativeAnthropicReaderFixture) handleResponsesStreamingFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping bridge.ResponsesClientToolMapping,
) (*forwardcore.OpenAIResult, error) {
	result, err := openaiexecution.ResponsesFromAnthropicStreaming(resp, upstream.NewDeferredOutputContext(ResponseSink{Writer: c.Writer}), s.output.AnthropicOptions(c, writeTextResponsesError), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
	return openaiexecution.ToForwardResult(result), err
}
