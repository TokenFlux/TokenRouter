// 图片意图只解释显式请求和工具声明；平台工具资格由固定纯函数选项提供。
package media

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/tidwall/gjson"
)

const OpenAIResponsesEndpoint = "/v1/responses"
const OpenAIResponsesCompactEndpoint = "/v1/responses/compact"
const ResponsesLiteHeader = "X-OpenAI-Internal-Codex-Responses-Lite"
const ResponsesLiteHeaderKey = "x-openai-internal-codex-responses-lite"
const ResponsesLiteWSMetadataKey = "ws_request_header_x_openai_internal_codex_responses_lite"
const ImageGenerationPermissionMessage = "Image generation is not enabled for this group"

// ImageToolRules 复用平台纯解析函数，不保存配置、账号或平台可变状态。
type ImageToolRules struct {
	IsImageType     func(string) bool
	IsNamespaceName func(string) bool
	HasTool         func(map[string]any) bool
	ToolChoice      func(any) bool
	FirstString     func(...any) string
}
type ImageIntentPolicy struct{ rules ImageToolRules }

func NewImageIntentPolicy(rules ImageToolRules) ImageIntentPolicy {
	return ImageIntentPolicy{rules: rules}
}

// GroupImagePermission 接收明确的存在性和权限位，保留无分组 Key 的行为。
func GroupImagePermission(groupPresent, allowed bool) bool { return !groupPresent || allowed }
func (p ImageIntentPolicy) IsOpenAIResponsesLiteHeader(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

func (p ImageIntentPolicy) IsOpenAIResponsesLiteWebSocketPayload(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	return p.IsOpenAIResponsesLiteHeader(gjson.GetBytes(body, "client_metadata."+ResponsesLiteWSMetadataKey).String())
}

func (p ImageIntentPolicy) ImageGenerationPermissionMessage() string {
	return ImageGenerationPermissionMessage
}

func (p ImageIntentPolicy) IsImageGenerationIntent(endpoint string, requestedModel string, body []byte) bool {
	if p.IsImageGenerationEndpoint(endpoint) {
		return true
	}
	if IsImageGenerationModel(requestedModel) {
		return true
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}

	var modelSeen, toolsSeen, inputSeen, toolChoiceSeen bool
	imageIntent := false
	wirejson.ParseView(body).ForEach(func(key, value gjson.Result) bool {
		// 保持 GetBytes 对重复字段取首个值的语义，同时只遍历一次根对象。
		switch key.Str {
		case "model":
			if !modelSeen {
				modelSeen = true
				imageIntent = IsImageGenerationModel(strings.TrimSpace(value.String()))
			}
		case "tools":
			if !toolsSeen {
				toolsSeen = true
				imageIntent = p.OpenAIJSONToolsContainImageGeneration(value)
			}
		case "input":
			if !inputSeen {
				inputSeen = true
				imageIntent = p.OpenAIJSONInputContainsImageGenTool(value)
			}
		case "tool_choice":
			if !toolChoiceSeen {
				toolChoiceSeen = true
				imageIntent = p.OpenAIJSONToolChoiceSelectsImageGeneration(value)
			}
		}
		return !imageIntent && (!modelSeen || !toolsSeen || !inputSeen || !toolChoiceSeen)
	})
	return imageIntent
}

func (p ImageIntentPolicy) IsExplicitImageGenerationIntent(endpoint string, requestedModel string, body []byte) bool {
	if p.IsImageGenerationEndpoint(endpoint) || IsImageGenerationModel(requestedModel) {
		return true
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	var modelSeen, toolsSeen, toolChoiceSeen bool
	imageIntent := false
	wirejson.ParseView(body).ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "model":
			if !modelSeen {
				modelSeen = true
				imageIntent = IsImageGenerationModel(strings.TrimSpace(value.String()))
			}
		case "tools":
			if !toolsSeen {
				toolsSeen = true
				imageIntent = p.OpenAIJSONToolsContainNativeImageGeneration(value)
			}
		case "tool_choice":
			if !toolChoiceSeen {
				toolChoiceSeen = true
				imageIntent = p.OpenAIJSONToolChoiceSelectsExplicitImageGeneration(value)
			}
		}
		return !imageIntent && (!modelSeen || !toolsSeen || !toolChoiceSeen)
	})
	return imageIntent
}

func (p ImageIntentPolicy) IsImageGenerationIntentForPlatform(endpoint string, requestedModel string, body []byte, explicitOnly bool) bool {
	if !explicitOnly {
		return p.IsImageGenerationIntent(endpoint, requestedModel, body)
	}
	return p.IsExplicitImageGenerationIntent(endpoint, requestedModel, body)
}

func (p ImageIntentPolicy) IsImageGenerationIntentMap(endpoint string, requestedModel string, reqBody map[string]any) bool {
	if p.IsImageGenerationEndpoint(endpoint) {
		return true
	}
	if IsImageGenerationModel(requestedModel) {
		return true
	}
	if reqBody == nil {
		return false
	}
	if IsImageGenerationModel(p.rules.FirstString(reqBody["model"])) {
		return true
	}
	if p.rules.HasTool(reqBody) {
		return true
	}
	return p.OpenAIAnyToolChoiceSelectsImageGeneration(reqBody["tool_choice"])
}

func (p ImageIntentPolicy) IsExplicitImageGenerationIntentMap(endpoint string, requestedModel string, reqBody map[string]any) bool {
	if p.IsImageGenerationEndpoint(endpoint) || IsImageGenerationModel(requestedModel) {
		return true
	}
	if reqBody == nil {
		return false
	}
	if IsImageGenerationModel(p.rules.FirstString(reqBody["model"])) {
		return true
	}
	if p.OpenAIAnyToolsContainNativeImageGeneration(reqBody["tools"]) {
		return true
	}
	return p.OpenAIAnyToolChoiceSelectsExplicitImageGeneration(reqBody["tool_choice"])
}

func (p ImageIntentPolicy) IsImageGenerationEndpoint(endpoint string) bool {
	switch p.NormalizeImageGenerationEndpoint(endpoint) {
	case "/v1/images/generations", "/v1/images/edits", "/images/generations", "/images/edits":
		return true
	default:
		return false
	}
}

func (p ImageIntentPolicy) NormalizeImageGenerationEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(strings.ToLower(endpoint))
	if endpoint == "" {
		return ""
	}
	endpoint = strings.TrimPrefix(endpoint, "https://api.openai.com")
	if idx := strings.IndexByte(endpoint, '?'); idx >= 0 {
		endpoint = endpoint[:idx]
	}
	return strings.TrimRight(endpoint, "/")
}

func (p ImageIntentPolicy) OpenAIJSONToolsContainImageGeneration(tools gjson.Result) bool {
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, item gjson.Result) bool {
		if p.IsOpenAIImageGenerationType(p.OpenAIJSONString(item.Get("type"))) {
			found = true
			return false
		}
		if p.IsImageGenNamespaceTool(item) {
			found = true
			return false
		}
		return true
	})
	return found
}

func (p ImageIntentPolicy) OpenAIJSONToolsContainNativeImageGeneration(tools gjson.Result) bool {
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, item gjson.Result) bool {
		found = p.IsOpenAIImageGenerationType(p.OpenAIJSONString(item.Get("type")))
		return !found
	})
	return found
}

func (p ImageIntentPolicy) OpenAIAnyToolsContainNativeImageGeneration(rawTools any) bool {
	tools, ok := rawTools.([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if ok && p.IsOpenAIImageGenerationType(p.rules.FirstString(tool["type"])) {
			return true
		}
	}
	return false
}

func (p ImageIntentPolicy) IsOpenAIImageGenerationType(value string) bool {
	return p.rules.IsImageType(value)
}

func (p ImageIntentPolicy) IsOpenAIImageGenNamespaceName(value string) bool {
	return p.rules.IsNamespaceName(value)
}

func (p ImageIntentPolicy) IsImageGenNamespaceTool(tool gjson.Result) bool {
	return p.OpenAIJSONString(tool.Get("type")) == "namespace" &&
		p.IsOpenAIImageGenNamespaceName(p.OpenAIJSONString(tool.Get("name")))
}

func (p ImageIntentPolicy) OpenAIJSONInputContainsImageGenTool(input gjson.Result) bool {
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if p.OpenAIJSONString(item.Get("type")) != "additional_tools" {
			return true
		}
		found = p.OpenAIJSONToolsContainImageGeneration(item.Get("tools"))
		return !found
	})
	return found
}

func (p ImageIntentPolicy) OpenAIRequestBodyHasImageGenerationDeclaration(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	return p.OpenAIJSONToolsContainImageGeneration(gjson.GetBytes(body, "tools")) ||
		p.OpenAIJSONInputContainsImageGenTool(gjson.GetBytes(body, "input")) ||
		p.OpenAIJSONToolChoiceSelectsImageGeneration(gjson.GetBytes(body, "tool_choice"))
}

func (p ImageIntentPolicy) OpenAIRequestBodyImageGenerationToolNeedsNormalization(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	needsNormalization := false
	tools.ForEach(func(_, item gjson.Result) bool {
		if p.OpenAIJSONString(item.Get("type")) != "image_generation" {
			return true
		}
		// 只有旧字段或明确的模型不兼容字段需要修正时才进入 map 修改。
		if item.Get("format").Exists() || item.Get("compression").Exists() {
			needsNormalization = true
			return false
		}
		imageModel := strings.ToLower(strings.TrimSpace(item.Get("model").String()))
		if strings.HasPrefix(imageModel, "gpt-image-2") && item.Get("input_fidelity").Exists() {
			needsNormalization = true
			return false
		}
		return true
	})
	return needsNormalization
}

func (p ImageIntentPolicy) OpenAIJSONToolChoiceSelectsImageGeneration(choice gjson.Result) bool {
	if !choice.Exists() {
		return false
	}
	if choice.Type == gjson.String {
		return p.IsOpenAIImageGenerationType(choice.String())
	}
	if !choice.IsObject() {
		return false
	}
	choiceType := p.OpenAIJSONString(choice.Get("type"))
	if p.IsOpenAIImageGenerationType(choiceType) {
		return true
	}
	if choiceType == "namespace" &&
		(p.IsOpenAIImageGenNamespaceName(p.OpenAIJSONString(choice.Get("name"))) ||
			p.IsOpenAIImageGenNamespaceName(p.OpenAIJSONString(choice.Get("namespace")))) {
		return true
	}
	if tool := choice.Get("tool"); tool.IsObject() && p.OpenAIJSONToolChoiceSelectsImageGeneration(tool) {
		return true
	}
	if p.IsOpenAIImageGenerationType(p.OpenAIJSONString(choice.Get("function.name"))) {
		return true
	}
	return false
}

func (p ImageIntentPolicy) OpenAIJSONToolChoiceSelectsExplicitImageGeneration(choice gjson.Result) bool {
	if p.OpenAIJSONToolChoiceSelectsImageGeneration(choice) {
		return true
	}
	if !choice.IsObject() {
		return false
	}
	if tool := choice.Get("tool"); tool.IsObject() && p.OpenAIJSONToolChoiceSelectsExplicitImageGeneration(tool) {
		return true
	}
	if p.IsOpenAIImageGenFunctionReference(
		p.OpenAIJSONString(choice.Get("namespace")),
		p.OpenAIJSONString(choice.Get("name")),
	) {
		return true
	}
	if fn := choice.Get("function"); fn.IsObject() {
		return p.IsOpenAIImageGenFunctionReference(
			p.OpenAIJSONString(fn.Get("namespace")),
			p.OpenAIJSONString(fn.Get("name")),
		)
	}
	return false
}

func (p ImageIntentPolicy) IsOpenAIImageGenFunctionReference(namespace string, name string) bool {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if namespace == "image_gen" && name == "imagegen" {
		return true
	}
	switch name {
	case "image_gen.imagegen", "image_gen__imagegen":
		return true
	default:
		return false
	}
}

func (p ImageIntentPolicy) OpenAIAnyToolChoiceSelectsImageGeneration(choice any) bool {
	return p.rules.ToolChoice(choice)
}

func (p ImageIntentPolicy) OpenAIAnyToolChoiceSelectsExplicitImageGeneration(choice any) bool {
	if p.OpenAIAnyToolChoiceSelectsImageGeneration(choice) {
		return true
	}
	choiceMap, ok := choice.(map[string]any)
	if !ok {
		return false
	}
	if tool, ok := choiceMap["tool"].(map[string]any); ok && p.OpenAIAnyToolChoiceSelectsExplicitImageGeneration(tool) {
		return true
	}
	if p.IsOpenAIImageGenFunctionReference(
		p.rules.FirstString(choiceMap["namespace"]),
		p.rules.FirstString(choiceMap["name"]),
	) {
		return true
	}
	if fn, ok := choiceMap["function"].(map[string]any); ok {
		return p.IsOpenAIImageGenFunctionReference(
			p.rules.FirstString(fn["namespace"]),
			p.rules.FirstString(fn["name"]),
		)
	}
	return false
}

type OpenAIResponsesImageBillingConfig struct {
	Model     string
	SizeTier  string
	InputSize string
}

func (p ImageIntentPolicy) ResolveOpenAIResponsesImageBillingConfigDetailed(reqBody map[string]any, fallbackModel string) (OpenAIResponsesImageBillingConfig, error) {
	imageModel := ""
	imageSize := ""
	hasImageTool := false
	if reqBody != nil {
		rawTools, _ := reqBody["tools"].([]any)
		for _, rawTool := range rawTools {
			toolMap, ok := rawTool.(map[string]any)
			if !ok || strings.TrimSpace(p.rules.FirstString(toolMap["type"])) != "image_generation" {
				continue
			}
			hasImageTool = true
			imageModel = strings.TrimSpace(p.rules.FirstString(toolMap["model"]))
			imageSize = strings.TrimSpace(p.rules.FirstString(toolMap["size"]))
			break
		}
		if imageSize == "" {
			imageSize = strings.TrimSpace(p.rules.FirstString(reqBody["size"]))
		}
	}
	if imageModel == "" && reqBody != nil {
		bodyModel := strings.TrimSpace(p.rules.FirstString(reqBody["model"]))
		if p.IsOpenAIImageBillingModelAlias(bodyModel) || !hasImageTool {
			imageModel = bodyModel
		}
	}
	if imageModel == "" && hasImageTool {
		imageModel = "gpt-image-2"
	}
	if imageModel == "" {
		imageModel = strings.TrimSpace(fallbackModel)
	}
	sizeTier := NormalizeImageSizeTier(imageSize)
	return OpenAIResponsesImageBillingConfig{
		Model:     imageModel,
		SizeTier:  sizeTier,
		InputSize: imageSize,
	}, nil
}

func (p ImageIntentPolicy) ResolveOpenAIResponsesImageBillingConfigFromBody(body []byte, fallbackModel string) (string, string, error) {
	cfg, err := p.ResolveOpenAIResponsesImageBillingConfigDetailedFromBody(body, fallbackModel)
	if err != nil {
		return "", "", err
	}
	return cfg.Model, cfg.SizeTier, nil
}

func (p ImageIntentPolicy) ResolveOpenAIResponsesImageBillingConfigDetailedFromBody(body []byte, fallbackModel string) (OpenAIResponsesImageBillingConfig, error) {
	imageModel := ""
	imageSize := ""
	hasImageTool := false
	if len(body) > 0 && gjson.ValidBytes(body) {
		tools := gjson.GetBytes(body, "tools")
		if tools.IsArray() {
			tools.ForEach(func(_, item gjson.Result) bool {
				if p.OpenAIJSONString(item.Get("type")) != "image_generation" {
					return true
				}
				hasImageTool = true
				imageModel = p.OpenAIJSONString(item.Get("model"))
				imageSize = p.OpenAIJSONString(item.Get("size"))
				return false
			})
		}
		if imageSize == "" {
			imageSize = p.OpenAIJSONString(gjson.GetBytes(body, "size"))
		}
		if imageModel == "" {
			bodyModel := p.OpenAIJSONString(gjson.GetBytes(body, "model"))
			if p.IsOpenAIImageBillingModelAlias(bodyModel) || !hasImageTool {
				imageModel = bodyModel
			}
		}
	}
	if imageModel == "" && hasImageTool {
		imageModel = "gpt-image-2"
	}
	if imageModel == "" {
		imageModel = strings.TrimSpace(fallbackModel)
	}
	return OpenAIResponsesImageBillingConfig{
		Model:     imageModel,
		SizeTier:  NormalizeImageSizeTier(imageSize),
		InputSize: imageSize,
	}, nil
}

func (p ImageIntentPolicy) IsOpenAIImageBillingModelAlias(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return false
	}
	return IsImageGenerationModel(normalized) || strings.Contains(normalized, "image")
}

func (p ImageIntentPolicy) OpenAIJSONString(value gjson.Result) string {
	if value.Type != gjson.String {
		return ""
	}
	return strings.TrimSpace(value.String())
}
