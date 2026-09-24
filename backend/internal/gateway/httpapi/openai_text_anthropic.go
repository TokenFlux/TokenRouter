package httpapi

import (
	"context"

	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/gin-gonic/gin"
)

// NativeMessages 将 Anthropic Messages 请求零转换
// 直通到国产供应商的原生 Anthropic 端点。仅做模型名映射与少量 body 清洗
// （空文本块 / web-search 历史块），协议本身不转换。
func (s *OpenAITextExecutor) NativeMessages(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	defaultMappedModel string,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAINativeAnthropicAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account}, kind: openaiexecution.NativeMessages}
	result, err := openaiexecution.ForwardNativeMessages(ctx, body, defaultMappedModel, adapter)
	return openaiexecution.ToForwardResult(result), err
}

// NativeChat 通过国产供应商的原生 Anthropic
// 端点承接 OpenAI /v1/chat/completions 客户端请求。
//
// 转换链：
//
//	请求：Chat Completions → Responses → Anthropic（链式转换）
//	响应：Anthropic 事件 → Responses 事件 → CC 分块（链式状态机）
func (s *OpenAITextExecutor) NativeChat(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	defaultMappedModel string,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAINativeAnthropicAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account}, kind: openaiexecution.NativeChat}
	result, err := openaiexecution.ForwardNativeChat(ctx, body, defaultMappedModel, adapter)
	return openaiexecution.ToForwardResult(result), err
}

// NativeResponses 通过国产供应商的原生 Anthropic 端点
// 承接 OpenAI /v1/responses 客户端请求。
//
// 转换链：
//
//	请求：Responses → Anthropic（单次转换）
//	响应：Anthropic 事件 → Responses 事件（流式状态机）
func (s *OpenAITextExecutor) NativeResponses(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	defaultMappedModel string,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAINativeAnthropicAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account}, kind: openaiexecution.NativeResponses}
	result, err := openaiexecution.ForwardNativeResponses(ctx, body, defaultMappedModel, adapter)
	return openaiexecution.ToForwardResult(result), err
}
