package httpapi

import (
	"context"

	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/gin-gonic/gin"
)

// RawChat 直转客户端的 Chat Completions 请求到上游
// `{base_url}/v1/chat/completions`，**不**做 CC↔Responses 协议转换。
//
// 适用场景：account.platform=openai && account.type=apikey && 上游已被探测确认
// 不支持 /v1/responses 端点（如 GLM/Qwen 等第三方 OpenAI 兼容上游）；CN 供应商
// 固定 chat_completions 协议也走此路径。
//
// 与 Chat 的关键差异：
//
//   - 不调用 apicompat.ChatCompletionsToResponses，body 仅做模型 ID 改写
//   - 上游 URL 拼到 /v1/chat/completions 而非 /v1/responses
//   - 流式响应 SSE 直接透传给客户端（上游 chunk 已是 CC 格式）
//   - 非流式响应 JSON 直接透传，仅按需提取 usage
//   - 不应用 codex OAuth transform（APIKey 路径无 OAuth）
//   - 不注入 prompt_cache_key（OAuth 专属机制）
//
// 调用入口：openai_gateway_chat_completions.go::Chat
// 在函数顶部通过统一文本协议解析器分流。
func (s *OpenAITextExecutor) RawChat(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	defaultMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAIRawChatAdapter{openAIRawFallbackAdapter: &openAIRawFallbackAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}, kind: openaiexecution.NativeChat}}
	result, err := openaiexecution.RunRawChat(ctx, body, defaultMappedModel, adapter)
	return openaiexecution.ToForwardResult(result), err
}

// MessagesViaRawChat 将 `/v1/messages` 客户端请求桥接到
// 仅支持 `/v1/chat/completions` 的 OpenAI 兼容上游。
//
// 转换链直接跳过 Responses 中间表示：
//
//	请求：Anthropic Messages → Chat Completions
//	响应：Chat Completions chunk/response → Anthropic events/response
//
// 该函数与服务 `/v1/responses` 的 ResponsesViaRawChat 对应，
// 但每个流式 token 只经过一个状态机，不再往返 Responses 表示。
func (s *OpenAITextExecutor) MessagesViaRawChat(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	defaultMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAIRawFallbackAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}, kind: openaiexecution.NativeMessages}
	result, err := openaiexecution.MessagesViaRawChat(ctx, body, defaultMappedModel, adapter)
	return openaiexecution.ToForwardResult(result), err
}

// ResponsesViaRawChat 将 `/v1/responses` 入站请求桥接到
// 只支持 `/v1/chat/completions` 的上游。
func (s *OpenAITextExecutor) ResponsesViaRawChat(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAIRawFallbackAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}, kind: openaiexecution.NativeResponses}
	result, err := openaiexecution.ResponsesViaRawChat(ctx, body, adapter)
	return openaiexecution.ToForwardResult(result), err
}
