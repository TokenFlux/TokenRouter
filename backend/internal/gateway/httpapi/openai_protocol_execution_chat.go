package httpapi

import (
	"context"
	"net/http"

	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Chat accepts a Chat Completions request body, converts it
// to OpenAI Responses API format, forwards to the OpenAI upstream, and converts
// the response back to Chat Completions format.
//
// 历史背景：该函数原本对所有 OpenAI 账号无差别走 CC→Responses 转换 + /v1/responses
// 端点——这在 OAuth（ChatGPT 内部 API 仅支持 Responses）和官方 APIKey 账号上是
// 正确的，但 sub2api 接入 DeepSeek/Kimi/GLM 等第三方 OpenAI 兼容上游后假设破裂：
// 这些上游普遍只支持 /v1/chat/completions，无 /v1/responses 端点。
//
// 当前路由策略由客户端首选协议、账号协议配置和 Responses 探测状态共同决定。
func (s *OpenAITextExecutor) Chat(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	promptCacheKey string,
	defaultMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	return s.ChatWithCacheIsolation(ctx, c, account, body, promptCacheKey, defaultMappedModel, false, tlsRouterMatch...)
}

// 旧调用面只投影固定实例和本次参数，Chat 转换与恢复只由目标执行器推进。
func (s *OpenAITextExecutor) ChatWithCacheIsolation(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, promptCacheKey, defaultMappedModel string, compatPromptCacheTenantIsolated bool, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error) {
	p := &openAIChatExecutionAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}}
	result, err := openaiexecution.RunChat(ctx, body, promptCacheKey, defaultMappedModel, compatPromptCacheTenantIsolated, p)
	return openaiexecution.ToForwardResult(result), err
}
func normalizeResponsesRequestServiceTier(req *protocolopenai.ResponsesRequest) {
	if req == nil {
		return
	}
	req.ServiceTier = protocolopenai.ServiceTierValue(req.ServiceTier)
}
func normalizeResponsesBodyServiceTier(body []byte) ([]byte, string, error) {
	if len(body) == 0 {
		return body, "", nil
	}
	rawServiceTier := gjson.GetBytes(body, "service_tier").String()
	if rawServiceTier == "" {
		return body, "", nil
	}
	normalizedServiceTier := protocolopenai.ServiceTierValue(rawServiceTier)
	if normalizedServiceTier == "" {
		trimmed, err := sjson.DeleteBytes(body, "service_tier")
		return trimmed, "", err
	}
	if normalizedServiceTier == rawServiceTier {
		return body, normalizedServiceTier, nil
	}
	trimmed, err := sjson.SetBytes(body, "service_tier", normalizedServiceTier)
	return trimmed, normalizedServiceTier, err
}

// chatError reads an upstream error and returns it in
// OpenAI Chat Completions error format.
func (s *OpenAITextExecutor) chatError(
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	return s.Output.CompatError(resp, c, account, WriteForwardChatError, WriteForwardChatErrorBody, requestedModel...)
}
