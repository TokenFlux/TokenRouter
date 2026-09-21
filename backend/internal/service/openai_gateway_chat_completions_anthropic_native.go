package service

// 国产供应商 Anthropic 协议账号的 CC 入站反向路径。
//
// 客户端说 OpenAI Chat Completions、上游是供应商原生 Anthropic 端点
// （api_protocol=anthropic）时的交叉组合：请求 CC→Responses→Anthropic 转换，
// 响应 Anthropic→Responses→CC 转换。转换链与 Anthropic 平台的
// gateway_forward_as_chat_completions.go 完全一致（复用同一组 apicompat
// 状态机），仅上游发送/错误处理对齐 OpenAI 网关语义。

import (
	"context"
	"net/http"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/gin-gonic/gin"
)

// forwardChatCompletionsViaNativeAnthropic 通过国产供应商的原生 Anthropic
// 端点承接 OpenAI /v1/chat/completions 客户端请求。
//
// 转换链：
//
//	请求：Chat Completions → Responses → Anthropic（链式转换）
//	响应：Anthropic 事件 → Responses 事件 → CC 分块（链式状态机）
func (s *OpenAIGatewayService) forwardChatCompletionsViaNativeAnthropic(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAINativeAnthropicAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account}, kind: forward.NativeChat}
	result, err := forward.ForwardNativeChat(ctx, body, defaultMappedModel, adapter)
	return openAIForwardResultFromHTTP(result), err
}

// handleCCBufferedFromNativeAnthropic 读取 Anthropic SSE 事件并组装完整响应，
// 再按 Anthropic → Responses → Chat Completions 转换。
func (s *OpenAIGatewayService) handleCCBufferedFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
) (*forwardcore.OpenAIResult, error) {
	result, err := forward.ChatFromAnthropicBuffered(resp, upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeAnthropicOutputOptions(c, writeChatCompletionsError), originalModel, billingModel, upstreamModel, reasoningEffort, startTime)
	return openAIForwardResultFromHTTP(result), err
}

// handleCCStreamingFromNativeAnthropic 逐个读取 Anthropic SSE 事件，先转换为
// Responses 事件，再转换为 Chat Completions 分块并写出。
func (s *OpenAIGatewayService) handleCCStreamingFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	includeUsage bool,
) (*forwardcore.OpenAIResult, error) {
	result, err := forward.ChatFromAnthropicStreaming(resp, upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeAnthropicOutputOptions(c, writeChatCompletionsError), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, includeUsage)
	return openAIForwardResultFromHTTP(result), err
}
