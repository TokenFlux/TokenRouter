package service

import (
	"strings"

	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

const (
	openAIResponsesEndpoint          = gatewaymedia.OpenAIResponsesEndpoint
	openAIResponsesCompactEndpoint   = gatewaymedia.OpenAIResponsesCompactEndpoint
	responsesLiteHeader              = gatewaymedia.ResponsesLiteHeader
	responsesLiteHeaderKey           = gatewaymedia.ResponsesLiteHeaderKey
	responsesLiteWSMetadataKey       = gatewaymedia.ResponsesLiteWSMetadataKey
	imageGenerationPermissionMessage = gatewaymedia.ImageGenerationPermissionMessage
)

// isOpenAIResponsesLiteHeader 判断请求是否来自 Codex Responses Lite 通道。
func isOpenAIResponsesLiteHeader(value string) bool {
	return imageIntentPolicy().IsOpenAIResponsesLiteHeader(value)
}

// isOpenAIResponsesLiteWebSocketPayload 从 WS client_metadata 中读取对应的 Lite 标记。
func isOpenAIResponsesLiteWebSocketPayload(body []byte) bool {
	return imageIntentPolicy().IsOpenAIResponsesLiteWebSocketPayload(body)
}

// ImageGenerationPermissionMessage returns the stable end-user error text for disabled groups.
func ImageGenerationPermissionMessage() string {
	return imageIntentPolicy().ImageGenerationPermissionMessage()
}

// GroupAllowsImageGeneration preserves ungrouped-key behavior and enforces the flag when a group is present.
func GroupAllowsImageGeneration(group *Group) bool {
	if group == nil {
		return gatewaymedia.GroupImagePermission(false, false)
	}
	return gatewaymedia.GroupImagePermission(true, group.AllowImageGeneration)
}

// IsImageGenerationIntent classifies requests that can produce generated images.
func IsImageGenerationIntent(endpoint string, requestedModel string, body []byte) bool {
	return imageIntentPolicy().IsImageGenerationIntent(endpoint, requestedModel, body)
}

// IsExplicitImageGenerationIntent 仅检测原生 image_generation 工具、图片模型和显式 tool_choice，
// 不检测被动的 image_gen namespace 声明。用于 capability 路由决策，被动 namespace 不应
// 强制要求原生 Responses 能力，否则 Chat Completions-only 账号会被误过滤（#4476）。
func IsExplicitImageGenerationIntent(endpoint string, requestedModel string, body []byte) bool {
	return imageIntentPolicy().IsExplicitImageGenerationIntent(endpoint, requestedModel, body)
}

// IsImageGenerationIntentForPlatform 按平台应用生图意图判定规则。
//
// Codex 会在普通 Responses 请求中声明 image_gen 命名空间，供模型按需调用。
// Grok 转发前会移除命名空间和 Responses Lite additional_tools 声明，因此仅有
// 这些被动声明时不能把所有 Codex 请求都判为生图请求。原生 image_generation
// 工具、显式生图选择和生图模型仍属于生图意图；其他平台保持原有声明语义。
func IsImageGenerationIntentForPlatform(endpoint string, requestedModel string, body []byte, platform string) bool {
	return imageIntentPolicy().IsImageGenerationIntentForPlatform(endpoint, requestedModel, body, strings.EqualFold(strings.TrimSpace(platform), PlatformGrok))
}

// IsImageGenerationIntentMap 在服务层修改请求后，使用 map 结构判断宽泛生图意图。
func IsImageGenerationIntentMap(endpoint string, requestedModel string, reqBody map[string]any) bool {
	return imageIntentPolicy().IsImageGenerationIntentMap(endpoint, requestedModel, reqBody)
}

// IsExplicitImageGenerationIntentMap 在服务层修改请求后，仅判断显式生图意图。
func IsExplicitImageGenerationIntentMap(endpoint string, requestedModel string, reqBody map[string]any) bool {
	return imageIntentPolicy().IsExplicitImageGenerationIntentMap(endpoint, requestedModel, reqBody)
}

// IsImageGenerationEndpoint identifies dedicated generated-image endpoints.
func IsImageGenerationEndpoint(endpoint string) bool {
	return imageIntentPolicy().IsImageGenerationEndpoint(endpoint)
}

func openAIRequestBodyHasImageGenerationDeclaration(body []byte) bool {
	return imageIntentPolicy().OpenAIRequestBodyHasImageGenerationDeclaration(body)
}

func openAIRequestBodyImageGenerationToolNeedsNormalization(body []byte) bool {
	return imageIntentPolicy().OpenAIRequestBodyImageGenerationToolNeedsNormalization(body)
}

func getAPIKeyFromContext(c interface{ Get(string) (any, bool) }) *APIKey {
	if c == nil {
		return nil
	}
	v, exists := c.Get("api_key")
	if !exists {
		return nil
	}
	apiKey, _ := v.(*APIKey)
	return apiKey
}

func apiKeyGroup(apiKey *APIKey) *Group {
	if apiKey == nil {
		return nil
	}
	return apiKey.Group
}

type OpenAIResponsesImageBillingConfig = gatewaymedia.OpenAIResponsesImageBillingConfig

func resolveOpenAIResponsesImageBillingConfigDetailed(reqBody map[string]any, fallbackModel string) (OpenAIResponsesImageBillingConfig, error) {
	return imageIntentPolicy().ResolveOpenAIResponsesImageBillingConfigDetailed(reqBody, fallbackModel)
}

func resolveOpenAIResponsesImageBillingConfigFromBody(body []byte, fallbackModel string) (string, string, error) {
	return imageIntentPolicy().ResolveOpenAIResponsesImageBillingConfigFromBody(body, fallbackModel)
}

func resolveOpenAIResponsesImageBillingConfigDetailedFromBody(body []byte, fallbackModel string) (OpenAIResponsesImageBillingConfig, error) {
	return imageIntentPolicy().ResolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, fallbackModel)
}

// imageIntentPolicy 仅绑定现有平台纯工具解析，主体与资格不进入目标策略。
func imageIntentPolicy() gatewaymedia.ImageIntentPolicy {
	return gatewaymedia.NewImageIntentPolicy(gatewaymedia.ImageToolRules{
		IsImageType:     nativeopenai.IsOpenAIImageGenerationType,
		IsNamespaceName: nativeopenai.IsOpenAIImageGenNamespaceName,
		HasTool:         nativeopenai.HasOpenAIImageGenerationTool,
		ToolChoice:      nativeopenai.OpenAIAnyToolChoiceSelectsImageGeneration,
		FirstString:     nativeopenai.FirstNonEmptyString,
	})
}
