package handler

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// reasoningEffortPolicyForRequest 返回请求目标平台对应的分组策略。
// 复合 Key 已由鉴权中间件投影到具体分组，这里不重新引入旧的复合平台解析层。
func reasoningEffortPolicyForRequest(c *gin.Context, apiKey *apikey.APIKey, platform string) (string, []routing.ReasoningEffortMapping, string, bool) {
	if apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != platform {
		return "", nil, "", false
	}
	if effectiveAPIKeyPlatform(c, apiKey) != platform {
		return "", nil, "", false
	}
	return apiKey.Group.MaxReasoningEffort,
		apiKey.Group.ReasoningEffortMappings,
		apiKey.Group.MaxReasoningEffortOverLimit,
		true
}

// openAIReasoningEffortPolicyForRequest 返回 OpenAI 分组策略。
func openAIReasoningEffortPolicyForRequest(c *gin.Context, apiKey *apikey.APIKey) (string, []routing.ReasoningEffortMapping, string, bool) {
	return reasoningEffortPolicyForRequest(c, apiKey, capability.PlatformOpenAI)
}

// anthropicReasoningEffortPolicyForRequest 返回 Anthropic 分组策略。
func anthropicReasoningEffortPolicyForRequest(c *gin.Context, apiKey *apikey.APIKey) (string, []routing.ReasoningEffortMapping, string, bool) {
	// /v1/messages 由通用 Anthropic handler 独立承接，不能使用 OpenAI 兼容
	// handler 的默认平台推断，否则 Anthropic 分组会被误判为 OpenAI。
	if apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != capability.PlatformAnthropic {
		return "", nil, "", false
	}
	if c != nil {
		if platform, forced := middleware.GetForcePlatformFromContext(c); forced && platform != capability.PlatformAnthropic {
			return "", nil, "", false
		}
	}
	return apiKey.Group.MaxReasoningEffort,
		apiKey.Group.ReasoningEffortMappings,
		apiKey.Group.MaxReasoningEffortOverLimit,
		true
}

// applyOpenAIReasoningEffortPolicyForRequest 在策略改写前保存客户端档位，并执行分组裁决。
func applyOpenAIReasoningEffortPolicyForRequest(c *gin.Context, apiKey *apikey.APIKey, body []byte) ([]byte, bool, error) {
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	bindRequestedReasoningEffort(c, body, model)
	maxEffort, mappings, overLimit, ok := openAIReasoningEffortPolicyForRequest(c, apiKey)
	if !ok {
		return body, false, nil
	}
	return service.ApplyOpenAIReasoningEffortPolicy(body, maxEffort, mappings, overLimit)
}

// applyAnthropicReasoningEffortPolicyForRequest 在 Anthropic Messages/Responses
// 请求转发前执行分组策略，支持 output_config.effort 和 reasoning.effort。
func applyAnthropicReasoningEffortPolicyForRequest(c *gin.Context, apiKey *apikey.APIKey, body []byte) ([]byte, bool, error) {
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	bindRequestedReasoningEffort(c, body, model)
	maxEffort, mappings, overLimit, ok := anthropicReasoningEffortPolicyForRequest(c, apiKey)
	if !ok {
		return body, false, nil
	}
	return service.ApplyOpenAIReasoningEffortPolicy(body, maxEffort, mappings, overLimit)
}

// bindOpenAIReasoningEffortPolicyForMessagesRequest 只为显式 output_config.effort
// 绑定策略，避免桥接器为缺省请求补出的 medium 被错误地当作客户端请求。
func bindOpenAIReasoningEffortPolicyForMessagesRequest(c *gin.Context, apiKey *apikey.APIKey, body []byte) {
	if c == nil || c.Request == nil {
		return
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	bindRequestedReasoningEffort(c, body, model)
	effort := gjson.GetBytes(body, "output_config.effort")
	if !effort.Exists() || effort.Type != gjson.String || strings.TrimSpace(effort.String()) == "" {
		return
	}
	maxEffort, mappings, overLimit, ok := openAIReasoningEffortPolicyForRequest(c, apiKey)
	if !ok {
		return
	}
	ctx := service.WithOpenAIReasoningEffortPolicyForModel(
		c.Request.Context(),
		maxEffort,
		mappings,
		overLimit,
		model,
	)
	c.Request = c.Request.WithContext(ctx)
}
