package service

// 国产供应商（kimi/zhipu/deepseek）原生 Anthropic 端点直通路径。
//
// 当账号 credentials["api_protocol"] = "anthropic" 时，入站 /v1/messages 请求
// 不再做 Anthropic→CC→Anthropic 双重转换，而是零转换直通供应商的官方
// Anthropic 兼容端点（如 https://open.bigmodel.cn/api/anthropic/v1/messages），
// 适配 Claude Code 等原生 Anthropic 客户端。转发骨架以
// gateway_anthropic_passthrough.go 的 APIKey 透传为模板（字节级 SSE 中继 +
// usage 解析），错误/failover 语义对齐 OpenAI 网关其他路径
// （failoverOpenAIUpstreamHTTPError / handleAnthropicErrorResponse）。

import (
	"context"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/gin-gonic/gin"
)

// forwardAnthropicViaNativeAnthropicEndpoint 将 Anthropic Messages 请求零转换
// 直通到国产供应商的原生 Anthropic 端点。仅做模型名映射与少量 body 清洗
// （空文本块 / web-search 历史块），协议本身不转换。
func (s *OpenAIGatewayService) forwardAnthropicViaNativeAnthropicEndpoint(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAINativeAnthropicAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account}, kind: forward.NativeMessages}
	result, err := forward.ForwardNativeMessages(ctx, body, defaultMappedModel, adapter)
	return openAIForwardResultFromHTTP(result), err
}

// nativeAnthropicTargetURL 保留旧入口，目标校验与路径拼接由唯一实现执行。
func (s *OpenAIGatewayService) nativeAnthropicTargetURL(account *Account) (string, error) {
	return forward.NativeAnthropicTargetURL(account.ID, account.GetAnthropicProtocolBaseURL(), s.validateUpstreamBaseURL)
}

// buildNativeAnthropicUpstreamRequest 只投影本次请求 Header 与账号策略。
func (s *OpenAIGatewayService) buildNativeAnthropicUpstreamRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, apiKey, targetURL string) (*http.Request, []byte, error) {
	var headers http.Header
	if c != nil && c.Request != nil {
		headers = c.Request.Header
	}
	return forward.BuildNativeAnthropicRequest(ctx, body, apiKey, targetURL, forward.NativeAnthropicRequestOptions{
		Headers: headers, GetHeader: anthropic.GetHeaderRaw, OverrideValue: account.HeaderOverrideValue,
		Sanitize: anthropic.SanitizeAnthropicBodyForBetaTokens, AllowedHeader: func(key string) bool { return allowedHeaders[key] },
		WireCasing: anthropic.ResolveWireCasing, AddHeader: anthropic.AddHeaderRaw, SetHeader: anthropic.SetHeaderRaw,
		AuthHeader: func(h http.Header, key string) { setAnthropicAPIKeyAuthHeader(h, account, key) }, ApplyOverrides: account.ApplyHeaderOverrides,
	})
}
