package service

// 国产供应商 Anthropic 协议账号的 Responses 入站反向路径。
//
// 客户端说 OpenAI Responses（/v1/responses，Codex 等）、上游是供应商原生
// Anthropic 端点（api_protocol=anthropic）时的交叉组合：请求 Responses→Anthropic
// 单次转换，响应 Anthropic 事件→Responses 事件转换。转换链与 Anthropic 平台的
// gateway_forward_as_responses.go 完全一致（复用同一组 apicompat 状态机），仅上游
// 发送/错误处理对齐 OpenAI 网关语义（模型映射、failover、transport error）。

import (
	"context"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"

	"github.com/gin-gonic/gin"
)

// forwardResponsesViaNativeAnthropic 通过国产供应商的原生 Anthropic 端点
// 承接 OpenAI /v1/responses 客户端请求。
//
// 转换链：
//
//	请求：Responses → Anthropic（单次转换）
//	响应：Anthropic 事件 → Responses 事件（流式状态机）
func (s *OpenAIGatewayService) forwardResponsesViaNativeAnthropic(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	adapter := &openAINativeAnthropicAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account}, kind: forward.NativeResponses}
	result, err := forward.ForwardNativeResponses(ctx, body, defaultMappedModel, adapter)
	return openAIForwardResultFromHTTP(result), err
}

// handleResponsesBufferedFromNativeAnthropic 读取 Anthropic SSE 事件并组装完整
// 响应，再按 Anthropic → Responses 转换。
func (s *OpenAIGatewayService) handleResponsesBufferedFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*OpenAIForwardResult, error) {
	result, err := forward.ResponsesFromAnthropicBuffered(resp, upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeAnthropicOutputOptions(c, writeResponsesError), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
	return openAIForwardResultFromHTTP(result), err
}

// handleResponsesStreamingFromNativeAnthropic 逐个读取 Anthropic SSE 事件，
// 转换为 Responses SSE 事件后写给客户端。
func (s *OpenAIGatewayService) handleResponsesStreamingFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*OpenAIForwardResult, error) {
	result, err := forward.ResponsesFromAnthropicStreaming(resp, upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeAnthropicOutputOptions(c, writeResponsesError), originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
	return openAIForwardResultFromHTTP(result), err
}
