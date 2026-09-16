// Qoder 请求转换只处理显式报文与站点选项，HTTP 和账号读取由调用方拥有。
package qoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

var QoderClaudeBillingCCHRe = regexp.MustCompile(`(x-anthropic-billing-header:[^\n\r;]*?(?:;[^\n\r;]*?)*\bcch=)[0-9a-fA-F]{5}(;)`)

// QoderThinkingDirective 表示下游思考参数归一化后的开关和等级。
type QoderThinkingDirective struct {
	Enabled bool
	Effort  string
}

type QoderPayloadRequest struct {
	Model           string
	Site            Site
	System          string
	Messages        []QoderMessage
	Tools           []any
	MaxTokens       int
	Thinking        QoderThinkingDirective
	UserType        string
	ExplicitSession string
	PromptCacheKey  string
	MetadataUserID  string
	ResponseID      string
	// autoResponseSession 表示 explicitSession 是根据本次响应 id 合成的，
	// 而不是客户端显式传入的。它不能遮蔽 prompt_cache_key 或 header 这类
	// 更稳定的旧 session key。
	AutoResponseSession bool
	// previousResponseID 标记 OpenAI Responses 续写请求。不同于
	// session_id/conversation_id，previous_response_id 通常只携带新的 input item，
	// 因此对话规划器可以把它追加到旧状态，而不是要求完整回放前缀。
	PreviousResponseID string
}

func BuildQoderPayloadFromChatCompletions(body []byte, userType string) (map[string]any, string, error) {
	return BuildQoderPayloadFromChatCompletionsForSite(body, userType, SiteGlobal)
}

// BuildQoderPayloadFromChatCompletionsForSite 按账号站点解析默认模型 alias。
func BuildQoderPayloadFromChatCompletionsForSite(body []byte, userType string, site Site) (map[string]any, string, error) {
	request, err := ParseQoderChatCompletionsPayload(body)
	if err != nil {
		return nil, "", err
	}
	request.UserType = userType
	request.Site = site
	payload, modelKey := BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
	return payload, modelKey, nil
}

func ParseQoderChatCompletionsPayload(body []byte) (QoderPayloadRequest, error) {
	var req protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return QoderPayloadRequest{}, fmt.Errorf("parse chat completions request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return QoderPayloadRequest{}, errors.New("model is required")
	}

	messages, err := QoderMessagesFromChatCompletions(req.Messages)
	if err != nil {
		return QoderPayloadRequest{}, err
	}
	messages = EnrichQoderToolResultMessages(messages)

	rawReq := QoderRequestMap(body)
	maxTokens := QoderDefaultMaxTokens
	// max_completion_tokens 优先于 max_tokens
	if req.MaxCompletionTokens != nil && *req.MaxCompletionTokens > 0 {
		maxTokens = *req.MaxCompletionTokens
	} else if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	return QoderPayloadRequest{
		Model:           req.Model,
		System:          QoderChatSystemText(req.Messages),
		Messages:        messages,
		Tools:           QoderChatCompletionsTools(req, rawReq),
		MaxTokens:       maxTokens,
		Thinking:        QoderThinkingDirectiveFromBody(body, "reasoning_effort", "reasoning.effort"),
		ExplicitSession: FirstNonEmptyQoder(QoderStringField(rawReq, "session_id"), QoderStringField(rawReq, "conversation_id")),
		PromptCacheKey:  QoderStringField(rawReq, "prompt_cache_key"),
		MetadataUserID:  QoderMetadataUserID(rawReq["metadata"]),
	}, nil
}

func BuildQoderPayloadFromAnthropicMessages(body []byte, userType string) (map[string]any, string, error) {
	request, err := ParseQoderAnthropicMessagesPayload(body)
	if err != nil {
		return nil, "", err
	}
	request.UserType = userType
	payload, modelKey := BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
	return payload, modelKey, nil
}

func ParseQoderResponsesPayload(body []byte) (QoderPayloadRequest, error) {
	var req protocolopenai.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return QoderPayloadRequest{}, fmt.Errorf("parse responses request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return QoderPayloadRequest{}, errors.New("model is required")
	}
	messages, err := QoderMessagesFromResponsesInput(req.Input)
	if err != nil {
		return QoderPayloadRequest{}, err
	}
	messages = EnrichQoderToolResultMessages(messages)

	// 从 instructions 和 input 中的 developer/system 项收集 system prompt
	systemParts := make([]string, 0)
	if req.Instructions != "" {
		systemParts = append(systemParts, strings.TrimSpace(req.Instructions))
	}
	// 从 input 中提取 developer/system 消息
	if len(req.Input) > 0 {
		var items []protocolopenai.ResponsesInputItem
		if err := json.Unmarshal(req.Input, &items); err == nil {
			for _, item := range items {
				if item.Role == "developer" || item.Role == "system" {
					text := QoderResponsesContentText(item.Content)
					if text != "" {
						systemParts = append(systemParts, text)
					}
				}
			}
		}
	}
	system := strings.Join(systemParts, "\n\n")

	rawReq := QoderRequestMap(body)
	sessionID := QoderStringField(rawReq, "session_id")
	conversationID := QoderStringField(rawReq, "conversation_id")
	previousResponseID := QoderStringField(rawReq, "previous_response_id")
	explicitSession := FirstNonEmptyQoder(sessionID, conversationID, previousResponseID)
	responseID := QoderResponsesID()
	autoResponseSession := explicitSession == ""
	if explicitSession == "" {
		explicitSession = responseID
	}
	maxTokens := QoderDefaultMaxTokens
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		maxTokens = *req.MaxOutputTokens
	}
	return QoderPayloadRequest{
		Model:               req.Model,
		System:              system,
		Messages:            messages,
		Tools:               QoderResponsesToolsToQoderTools(req.Tools),
		MaxTokens:           maxTokens,
		Thinking:            QoderThinkingDirectiveFromBody(body, "reasoning.effort", "reasoning_effort"),
		ExplicitSession:     explicitSession,
		PromptCacheKey:      QoderStringField(rawReq, "prompt_cache_key"),
		MetadataUserID:      QoderMetadataUserID(rawReq["metadata"]),
		ResponseID:          responseID,
		AutoResponseSession: autoResponseSession,
		PreviousResponseID:  previousResponseID,
	}, nil
}

func ParseQoderAnthropicMessagesPayload(body []byte) (QoderPayloadRequest, error) {
	var req protocolanthropic.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return QoderPayloadRequest{}, fmt.Errorf("parse anthropic messages request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return QoderPayloadRequest{}, errors.New("model is required")
	}

	system, err := QoderAnthropicSystemText(req.System)
	if err != nil {
		return QoderPayloadRequest{}, err
	}
	toolReq := req
	toolReq.System = nil
	toolReq.Messages = nil
	// 这里只借用兼容层转换工具定义；思考字段由 Qoder 自行容错和映射。
	toolReq.Thinking = nil
	toolReq.OutputConfig = nil
	responsesReq, err := bridge.AnthropicToResponses(&toolReq, bridge.RequestOptions{})
	if err != nil {
		return QoderPayloadRequest{}, fmt.Errorf("convert anthropic messages request: %w", err)
	}
	messages, err := QoderMessagesFromAnthropicMessages(req.Messages)
	if err != nil {
		return QoderPayloadRequest{}, err
	}
	messages = EnrichQoderToolResultMessages(messages)

	rawReq := QoderRequestMap(body)
	maxTokens := QoderDefaultMaxTokens
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}
	return QoderPayloadRequest{
		Model:           req.Model,
		System:          system,
		Messages:        messages,
		Tools:           QoderResponsesToolsToQoderTools(responsesReq.Tools),
		MaxTokens:       maxTokens,
		Thinking:        QoderThinkingDirectiveFromBody(body, "output_config.effort"),
		ExplicitSession: FirstNonEmptyQoder(QoderStringField(rawReq, "session_id"), QoderStringField(rawReq, "conversation_id")),
		PromptCacheKey:  QoderStringField(rawReq, "prompt_cache_key"),
		MetadataUserID:  QoderMetadataUserID(rawReq["metadata"]),
	}, nil
}

// QoderThinkingDirectiveFromBody 将不同下游协议的思考字段归一为 Qoder 开关和通用等级。
// 明确关闭的优先级最高；非法等级不会阻断请求，仍可由预算或显式开关启用。
func QoderThinkingDirectiveFromBody(body []byte, effortPaths ...string) QoderThinkingDirective {
	thinkingType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()))
	if thinkingType == "disabled" || thinkingType == "none" || thinkingType == "off" {
		return QoderThinkingDirective{}
	}

	effort := ""
	for _, path := range effortPaths {
		raw := strings.TrimSpace(gjson.GetBytes(body, path).String())
		if raw == "" {
			continue
		}
		mapped, disabled := NormalizeQoderThinkingEffort(raw)
		if disabled {
			return QoderThinkingDirective{}
		}
		if effort == "" && mapped != "" {
			effort = mapped
		}
	}
	if effort != "" {
		return QoderThinkingDirective{Enabled: true, Effort: effort}
	}

	if gjson.GetBytes(body, "thinking.budget_tokens").Int() > 0 {
		return QoderThinkingDirective{Enabled: true, Effort: "max"}
	}
	if thinkingType == "enabled" || thinkingType == "adaptive" {
		return QoderThinkingDirective{Enabled: true, Effort: "max"}
	}
	return QoderThinkingDirective{}
}

// NormalizeQoderThinkingEffort 将常见下游等级归一化，最终档位由所选模型能力决定。
func NormalizeQoderThinkingEffort(raw string) (effort string, disabled bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
	switch value {
	case "none", "disabled", "off":
		return "", true
	case "minimal", "low":
		return "low", false
	case "medium":
		return "medium", false
	case "high":
		return "high", false
	case "xhigh", "veryhigh", "extrahigh", "max":
		return "max", false
	default:
		return "", false
	}
}

func QoderClaudeCodeStablePrefixKey(request QoderPayloadRequest) string {
	parts := []string{
		"model=" + strings.TrimSpace(request.Model),
		"system=" + QoderNormalizeSystemForFingerprint(request.System),
		"tools=" + QoderFingerprintAny(request.Tools),
	}
	if firstUser := QoderFirstUserMessageText(request.Messages); firstUser != "" {
		parts = append(parts, "first_user="+firstUser)
	}
	return strings.Join(parts, "|")
}

func QoderFirstUserMessageText(messages []QoderMessage) string {
	for _, message := range messages {
		if strings.TrimSpace(message.Role) != "user" {
			continue
		}
		if text := strings.TrimSpace(message.Text); text != "" {
			return text
		}
	}
	return ""
}

func QoderMetadataUserID(raw any) string {
	metadata, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	return QoderStringField(metadata, "user_id")
}

func QoderMessageFingerprints(messages []QoderMessage) []string {
	fingerprints := make([]string, 0, len(messages))
	for _, message := range messages {
		fingerprints = append(fingerprints, QoderFingerprintAny(QoderPayloadMessageFromMessage(message)))
	}
	return fingerprints
}

func QoderFingerprintString(value string) string {
	return qoderPayloadFingerprint([]byte(value))
}

func QoderSystemFingerprint(system string) string {
	return QoderFingerprintString(QoderNormalizeSystemForFingerprint(system))
}

func QoderNormalizeSystemForFingerprint(system string) string {
	if !strings.Contains(system, "x-anthropic-billing-header") || !strings.Contains(system, "cch=") {
		return system
	}
	return QoderClaudeBillingCCHRe.ReplaceAllString(system, "${1}<cch>${2}")
}

func QoderFingerprintAny(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return qoderPayloadFingerprint([]byte(fmt.Sprint(value)))
	}
	return qoderPayloadFingerprint(body)
}

func BuildQoderPayloadWithOptions(request QoderPayloadRequest, sessionID string, messages []QoderMessage, includeSystem bool, includeTools bool) (map[string]any, string) {
	modelInfo := ResolveQoderModelForSite(request.Site, request.Model)
	userType := request.UserType
	if strings.TrimSpace(userType) == "" {
		userType = "personal_standard"
	}

	requestID := uuid.NewString()
	prompt := LatestQoderPayloadPromptText(messages, includeTools)
	if prompt == "" {
		prompt = LatestQoderPayloadPromptText(request.Messages, includeTools)
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = uuid.NewString()
	}
	payload := QoderBasePayload()
	payload["request_id"] = requestID
	payload["chat_record_id"] = requestID
	payload["request_set_id"] = uuid.NewString()
	payload["session_id"] = sessionID
	payload["aliyun_user_type"] = userType
	parameters, _ := payload["parameters"].(map[string]any)
	modelConfig, _ := payload["model_config"].(map[string]any)
	parameters["max_tokens"] = request.MaxTokens
	modelConfig["key"] = modelInfo.Key
	modelConfig["source"] = modelInfo.Source
	chatContext, _ := payload["chat_context"].(map[string]any)
	chatText, _ := chatContext["text"].(map[string]any)
	extra, _ := chatContext["extra"].(map[string]any)
	extraModelConfig, _ := extra["modelConfig"].(map[string]any)
	extraOriginalContent, _ := extra["originalContent"].(map[string]any)
	chatText["text"] = prompt
	extraModelConfig["key"] = modelInfo.Key
	extraModelConfig["source"] = modelInfo.Source
	ApplyQoderContextCapability(payload, request.Site, modelInfo.Key)
	ApplyQoderThinkingDirective(payload, request.Site, modelInfo.Key, request.Thinking)
	extraOriginalContent["text"] = prompt
	payload["business"] = map[string]any{
		"product":  "cli",
		"version":  MustProfileForSite(request.Site).ClientVersion,
		"type":     "agent",
		"stage":    "init",
		"id":       uuid.NewString(),
		"name":     TruncateRunes(prompt, 30),
		"begin_at": time.Now().UnixMilli(),
	}

	outMessages := make([]any, 0, len(messages)+1)
	if includeSystem && strings.TrimSpace(request.System) != "" {
		outMessages = append(outMessages, QoderPayloadMessage("system", request.System))
	}
	for _, msg := range messages {
		outMessages = append(outMessages, QoderPayloadMessageFromMessage(msg))
	}
	AddQoderEphemeralCacheControl(outMessages)
	payload["messages"] = outMessages
	if includeTools {
		payload["tools"] = request.Tools
	} else {
		payload["tools"] = []any{}
	}
	return payload, modelInfo.Key
}

// ApplyQoderContextCapability 按站点和最终 route 选择官方最高上下文档位。
// 只有官方声明 contextConfig 的 route 才写入运行时覆盖，避免为固定上限或未知 route 伪造档位。
func ApplyQoderContextCapability(payload map[string]any, site Site, modelKey string) {
	capability := ContextCapabilityForSite(site, modelKey)
	modelConfig, _ := payload["model_config"].(map[string]any)
	modelConfig["max_input_tokens"] = capability.MaxInputTokens
	if !capability.RuntimeSelectable {
		return
	}

	parameters, _ := payload["parameters"].(map[string]any)
	parameters["context_length"] = capability.MaxInputTokens
	chatContext, _ := payload["chat_context"].(map[string]any)
	extra, _ := chatContext["extra"].(map[string]any)
	runtimeOverride, _ := extra["ideModelConfigOverride"].(map[string]any)
	if runtimeOverride == nil {
		runtimeOverride = map[string]any{}
		extra["ideModelConfigOverride"] = runtimeOverride
	}
	runtimeOverride["max_input_tokens"] = capability.MaxInputTokens
}

// ApplyQoderThinkingDirective 同步 Qoder 请求中重复出现的模型配置。
// reasoning_effort=none 是官方客户端用于关闭可调 Thinking 的运行时覆盖值。
func ApplyQoderThinkingDirective(payload map[string]any, site Site, modelKey string, directive QoderThinkingDirective) {
	capability := ThinkingCapabilityForSite(site, modelKey)
	if capability == ThinkingUnsupported {
		return
	}

	parameters, _ := payload["parameters"].(map[string]any)
	modelConfig, _ := payload["model_config"].(map[string]any)
	chatContext, _ := payload["chat_context"].(map[string]any)
	extra, _ := chatContext["extra"].(map[string]any)
	extraModelConfig, _ := extra["modelConfig"].(map[string]any)
	modelConfig["is_reasoning"] = directive.Enabled
	extraModelConfig["is_reasoning"] = directive.Enabled

	// 仅开关型模型开启时不能携带 High/Max；关闭仍使用 none 明确覆盖上游默认值。
	if capability == ThinkingToggleOnly && directive.Enabled {
		return
	}

	effort := "none"
	if directive.Enabled {
		effort = QoderThinkingEffortForCapability(capability, directive.Effort)
	}
	parameters["reasoning_effort"] = effort
	modelConfig["reasoning_effort"] = effort
	extraModelConfig["reasoning_effort"] = effort
	runtimeOverride, _ := extra["ideModelConfigOverride"].(map[string]any)
	if runtimeOverride == nil {
		runtimeOverride = map[string]any{}
		extra["ideModelConfigOverride"] = runtimeOverride
	}
	runtimeOverride["reasoning_effort"] = effort
}

// QoderThinkingEffortForCapability 把通用请求等级投影到官方模型实际提供的档位。
func QoderThinkingEffortForCapability(capability ThinkingCapability, requested string) string {
	switch capability {
	case ThinkingHighMax:
		if requested == "low" || requested == "medium" {
			return "high"
		}
		return "max"
	case ThinkingLowHighMax:
		switch requested {
		case "low":
			return "low"
		case "medium", "high":
			return "high"
		default:
			return "max"
		}
	default:
		return requested
	}
}

func AddQoderEphemeralCacheControl(messages []any) {
	for i := len(messages) - 1; i >= 0; i-- {
		message, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		contents, _ := message["contents"].([]any)
		for j := len(contents) - 1; j >= 0; j-- {
			block, ok := contents[j].(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			text, _ := block["text"].(string)
			if strings.TrimSpace(text) == "" {
				continue
			}
			if _, exists := block["cache_control"]; exists {
				return
			}
			block["cache_control"] = map[string]any{"type": "ephemeral"}
			return
		}
	}
}

func QoderBasePayload() map[string]any {
	return map[string]any{
		"request_id":       "",
		"request_set_id":   "",
		"chat_record_id":   "",
		"stream":           true,
		"chat_task":        "FREE_INPUT",
		"image_urls":       nil,
		"is_reply":         true,
		"is_retry":         false,
		"session_id":       "",
		"code_language":    "",
		"source":           1,
		"version":          "3",
		"chat_prompt":      "",
		"parameters":       map[string]any{"max_tokens": QoderDefaultMaxTokens},
		"aliyun_user_type": "personal_standard",
		"session_type":     "qodercli",
		"agent_id":         "agent_common",
		"task_id":          "common",
		"chat_context": map[string]any{
			"chatPrompt": "",
			"features":   []any{},
			"imageUrls":  nil,
			"text":       map[string]any{"type": "text", "text": ""},
			"extra": map[string]any{
				"context":         []any{},
				"modelConfig":     map[string]any{"is_reasoning": false, "key": "auto"},
				"originalContent": map[string]any{"type": "text", "text": ""},
			},
		},
		"model_config": map[string]any{
			"key":              "auto",
			"display_name":     "Qoder Auto",
			"model":            "",
			"format":           "openai",
			"is_vl":            false,
			"is_reasoning":     false,
			"api_key":          "",
			"url":              "",
			"source":           "system",
			"max_input_tokens": FallbackMaxInputTokens,
		},
		"messages": []any{},
		"tools":    []any{},
	}
}

var QoderBlankResponseMeta = map[string]any{
	"id": "",
	"usage": map[string]any{
		"prompt_tokens":     0,
		"completion_tokens": 0,
		"total_tokens":      0,
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": 0,
		},
		"prompt_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	},
}

func QoderPayloadMessage(role, text string) map[string]any {
	return QoderPayloadMessageFromMessage(QoderMessage{Role: role, Text: text})
}

func QoderRequestMap(body []byte) map[string]any {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return map[string]any{}
	}
	return req
}

func QoderChatSystemText(messages []protocolopenai.ChatMessage) string {
	parts := make([]string, 0)
	for _, message := range messages {
		// system 和 developer 消息都应该合并到 system prompt
		if message.Role != "system" && message.Role != "developer" {
			continue
		}
		text := QoderChatContentText(message.Content)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func QoderChatContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []protocolopenai.ChatContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type == "text" && part.Text != "" {
				out = append(out, part.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

func QoderMessagesFromChatCompletions(messages []protocolopenai.ChatMessage) ([]QoderMessage, error) {
	out := make([]QoderMessage, 0, len(messages))
	for _, message := range messages {
		converted, err := QoderMessageFromChatCompletionsMessage(message)
		if err != nil {
			return nil, err
		}
		if converted.Role != "" {
			out = append(out, converted)
		}
	}
	return out, nil
}

func QoderMessageFromChatCompletionsMessage(message protocolopenai.ChatMessage) (QoderMessage, error) {
	role := strings.TrimSpace(message.Role)
	switch role {
	case "", "user":
		text := QoderChatContentText(message.Content)
		return QoderMessage{Role: "user", Text: text, Raw: map[string]any{"role": "user", "content": text}}, nil
	case "system", "developer":
		return QoderMessage{}, nil
	case "assistant":
		text := QoderChatContentText(message.Content)
		raw := map[string]any{"role": "assistant", "content": text}
		if toolCalls := QoderChatToolCalls(message.ToolCalls); len(toolCalls) > 0 {
			raw["tool_calls"] = toolCalls
		} else if toolCalls := QoderLegacyChatFunctionCall(message.FunctionCall); len(toolCalls) > 0 {
			raw["tool_calls"] = toolCalls
		}
		return QoderMessage{Role: "assistant", Text: text, Raw: raw}, nil
	case "tool":
		text := QoderChatContentText(message.Content)
		if text == "" {
			text = "(empty)"
		}
		raw := map[string]any{
			"role":         "tool",
			"tool_call_id": message.ToolCallID,
			"content":      text,
		}
		if name := strings.TrimSpace(message.Name); name != "" {
			raw["name"] = name
		}
		return QoderMessage{Role: "tool", Text: text, ToolCallID: message.ToolCallID, Raw: raw}, nil
	case "function":
		text := QoderChatContentText(message.Content)
		if text == "" {
			text = "(empty)"
		}
		callID := strings.TrimSpace(message.Name)
		raw := map[string]any{
			"role":         "tool",
			"tool_call_id": callID,
			"content":      text,
		}
		if callID != "" {
			raw["name"] = callID
		}
		return QoderMessage{Role: "tool", Text: text, ToolCallID: callID, Raw: raw}, nil
	default:
		return QoderMessage{}, fmt.Errorf("unsupported chat message role: %s", role)
	}
}

func QoderChatToolCalls(toolCalls []protocolopenai.ChatToolCall) []any {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]any, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		arguments := toolCall.Function.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		callType := strings.TrimSpace(toolCall.Type)
		if callType == "" {
			callType = "function"
		}
		out = append(out, map[string]any{
			"id":   toolCall.ID,
			"type": callType,
			"function": map[string]any{
				"name":      toolCall.Function.Name,
				"arguments": arguments,
			},
		})
	}
	return out
}

func QoderLegacyChatFunctionCall(functionCall *protocolopenai.ChatFunctionCall) []any {
	if functionCall == nil {
		return nil
	}
	name := strings.TrimSpace(functionCall.Name)
	if name == "" {
		return nil
	}
	// 旧版 function 消息没有 call id，使用函数名作为稳定 id，保证后续 role=function 的结果能关联回来。
	return []any{QoderToolCallMap(name, name, functionCall.Arguments)}
}

func QoderAnthropicSystemText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return QoderStripVolatileSystemText(text), nil
	}
	var blocks []protocolanthropic.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", errors.New("unsupported system field")
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported system block type: %v", block.Type)
		}
		if text := QoderStripVolatileSystemText(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func QoderStripVolatileSystemText(text string) string {
	return QoderClaudeBillingCCHRe.ReplaceAllString(text, "${1}<cch>${2}")
}

func QoderMessagesFromResponsesInput(raw json.RawMessage) ([]QoderMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []QoderMessage{{Role: "user", Text: text, Raw: map[string]any{"role": "user", "content": text}}}, nil
	}
	var items []protocolopenai.ResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parse canonical responses input: %w", err)
	}
	messages := make([]QoderMessage, 0, len(items))
	var pendingToolCalls []any
	flushPendingToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		messages = append(messages, QoderMessage{
			Role: "assistant",
			Raw: map[string]any{
				"role":       "assistant",
				"tool_calls": pendingToolCalls,
			},
		})
		pendingToolCalls = nil
	}
	for _, item := range items {
		if item.Type == "function_call" {
			pendingToolCalls = append(pendingToolCalls, QoderToolCallMap(item.CallID, item.Name, item.Arguments))
			continue
		}
		flushPendingToolCalls()
		converted := QoderMessageFromResponsesInputItem(item)
		if converted.Role != "" {
			messages = append(messages, converted)
		}
	}
	flushPendingToolCalls()
	return NormalizeQoderResponsesToolPairing(messages), nil
}

func QoderMessageFromResponsesInputItem(item protocolopenai.ResponsesInputItem) QoderMessage {
	switch item.Type {
	case "function_call":
		raw := map[string]any{
			"role":       "assistant",
			"tool_calls": []any{QoderToolCallMap(item.CallID, item.Name, item.Arguments)},
		}
		return QoderMessage{Role: "assistant", Raw: raw}
	case "function_call_output":
		output := item.Output
		if output == "" {
			output = "(empty)"
		}
		return QoderMessage{
			Role:       "tool",
			Text:       output,
			ToolCallID: item.CallID,
			Raw: map[string]any{
				"role":         "tool",
				"tool_call_id": item.CallID,
				"content":      output,
			},
		}
	default:
		if item.Type != "" && item.Type != "message" {
			return QoderMessage{}
		}
		role := QoderResponsesRole(item.Role)
		if role == "" {
			return QoderMessage{}
		}
		text := QoderResponsesContentText(item.Content)
		if text == "" && item.Type == "message" && role != "assistant" {
			return QoderMessage{Role: role, Raw: map[string]any{"role": role}}
		}
		return QoderMessage{
			Role: role,
			Text: text,
			Raw:  map[string]any{"role": role, "content": text},
		}
	}
}

func NormalizeQoderResponsesToolPairing(messages []QoderMessage) []QoderMessage {
	// Responses 历史可能包含未返回 output 的 function_call，或重连后遗留的孤儿 output。
	// Qoder 走 Chat 形状上游，同样要求 assistant tool_calls 后面有对应 tool 消息。
	replies := make(map[string]QoderMessage)
	for _, message := range messages {
		if message.Role != "tool" {
			continue
		}
		if id := QoderMessageToolCallID(message); id != "" {
			replies[id] = message
		}
	}

	out := make([]QoderMessage, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.Role == "tool":
			continue
		case QoderMessageHasToolCalls(message):
			tools := NormalizeQoderToolCalls(message.Raw["tool_calls"])
			keptTools := make([]any, 0, len(tools))
			keptReplies := make([]QoderMessage, 0, len(tools))
			for _, rawTool := range tools {
				tool, ok := rawTool.(map[string]any)
				if !ok {
					continue
				}
				id := QoderToolCallIDFromMap(tool)
				if id == "" {
					continue
				}
				reply, ok := replies[id]
				if !ok {
					continue
				}
				keptTools = append(keptTools, tool)
				keptReplies = append(keptReplies, reply)
			}
			if len(keptTools) == 0 {
				if QoderMessageHasText(message) {
					out = append(out, QoderMessageWithoutToolCalls(message))
				}
				continue
			}
			message.Raw = QoderCopyRawMap(message.Raw)
			message.Raw["tool_calls"] = keptTools
			out = append(out, message)
			out = append(out, keptReplies...)
		default:
			out = append(out, message)
		}
	}
	return out
}

func QoderMessageHasText(message QoderMessage) bool {
	return strings.TrimSpace(message.Text) != "" || len(message.TextContent) > 0
}

func QoderMessageWithoutToolCalls(message QoderMessage) QoderMessage {
	message.Raw = QoderCopyRawMap(message.Raw)
	delete(message.Raw, "tool_calls")
	return message
}

func QoderCopyRawMap(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	copied := make(map[string]any, len(raw))
	for key, value := range raw {
		copied[key] = value
	}
	return copied
}

func QoderResponsesContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []protocolopenai.ResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case "input_text", "output_text", "text":
				if part.Text != "" {
					out = append(out, part.Text)
				}
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

func QoderMessagesFromAnthropicMessages(messages []protocolanthropic.AnthropicMessage) ([]QoderMessage, error) {
	out := make([]QoderMessage, 0, len(messages))
	for _, message := range messages {
		converted, err := QoderMessagesFromAnthropicMessage(message)
		if err != nil {
			return nil, err
		}
		out = append(out, converted...)
	}
	return out, nil
}

func QoderMessagesFromAnthropicMessage(message protocolanthropic.AnthropicMessage) ([]QoderMessage, error) {
	if message.Role != "user" {
		text, raw, err := QoderAnthropicMessageTextAndRaw(message)
		if err != nil {
			return nil, err
		}
		return []QoderMessage{{Role: message.Role, Text: text, Raw: raw}}, nil
	}

	blocks, ok, err := QoderAnthropicContentBlocks(message.Content)
	if err != nil {
		return nil, err
	}
	if !ok {
		text := QoderAnthropicContentText(message.Content)
		return []QoderMessage{{Role: message.Role, Text: text, Raw: map[string]any{"role": message.Role, "content": text}}}, nil
	}

	var out []QoderMessage
	var textParts []string
	var textContent []map[string]any
	flushText := func() {
		text := strings.Join(NonEmptyStrings(textParts), "\n")
		contents := textContent
		textParts = nil
		textContent = nil
		if text == "" {
			return
		}
		out = append(out, QoderMessage{
			Role:        "user",
			Text:        text,
			Raw:         map[string]any{"role": "user", "content": text},
			TextContent: contents,
		})
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
			textContent = append(textContent, QoderTextContentBlock(block.Text, block.CacheControl))
		case "tool_result":
			toolText := QoderAnthropicToolResultText(block.Content)
			if strings.TrimSpace(block.ToolUseID) == "" {
				if toolText != "" {
					textParts = append(textParts, toolText)
				}
				continue
			}
			flushText()
			if toolText != "" {
				out = append(out, QoderMessage{
					Role:       "tool",
					Text:       toolText,
					ToolCallID: block.ToolUseID,
					Raw: map[string]any{
						"role":         "tool",
						"tool_call_id": block.ToolUseID,
						"content":      toolText,
					},
				})
			}
		case "tool_use":
			// Qoder payload.py 展开消息文本时会忽略 tool_use。
		case "thinking", "redacted_thinking":
			// Qoder 历史消息不接受 Anthropic thinking signature。
		default:
			return nil, fmt.Errorf("unsupported content block type: %v", block.Type)
		}
	}
	flushText()
	if len(out) == 0 {
		return []QoderMessage{{Role: message.Role, Raw: map[string]any{"role": message.Role}}}, nil
	}
	return out, nil
}

func QoderAnthropicMessageTextAndRaw(message protocolanthropic.AnthropicMessage) (string, map[string]any, error) {
	blocks, ok, err := QoderAnthropicContentBlocks(message.Content)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		text := QoderAnthropicContentText(message.Content)
		return text, map[string]any{"role": message.Role, "content": text}, nil
	}
	textParts := make([]string, 0, len(blocks))
	rawBlocks := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
			rawBlocks = append(rawBlocks, map[string]any{"type": "text", "text": block.Text})
		case "tool_use":
			rawBlocks = append(rawBlocks, map[string]any{
				"type":  "tool_use",
				"id":    block.ID,
				"name":  block.Name,
				"input": QoderRawMessageOrDefault(block.Input, map[string]any{}),
			})
		case "thinking", "redacted_thinking":
			// 不把 thinking 写入 Qoder content/tool 历史。
		default:
			return "", nil, fmt.Errorf("unsupported content block type: %v", block.Type)
		}
	}
	return strings.Join(NonEmptyStrings(textParts), "\n"), map[string]any{"role": message.Role, "content": rawBlocks}, nil
}

func QoderAnthropicContentBlocks(raw json.RawMessage) ([]protocolanthropic.AnthropicContentBlock, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return nil, false, nil
	}
	var blocks []protocolanthropic.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false, err
	}
	return blocks, true, nil
}

func QoderAnthropicContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	blocks, ok, err := QoderAnthropicContentBlocks(raw)
	if err != nil || !ok {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "tool_result":
			parts = append(parts, QoderAnthropicToolResultText(block.Content))
		}
	}
	return strings.Join(NonEmptyStrings(parts), "\n")
}

func QoderAnthropicToolResultText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []protocolanthropic.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

func QoderResponsesRole(role string) string {
	switch strings.TrimSpace(role) {
	case "developer", "system":
		// Responses 的控制角色 input item 已由 ParseQoderResponsesPayload 合并到
		// Qoder system prompt，不能再作为 chat 历史轮次发送。
		return ""
	case "assistant":
		return "assistant"
	case "", "user":
		return "user"
	default:
		return role
	}
}

func QoderToolCallMap(id, name, arguments string) map[string]any {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	}
}

func QoderChatCompletionsTools(req protocolopenai.ChatCompletionsRequest, rawReq map[string]any) []any {
	tools := append([]any(nil), QoderAnySlice(rawReq["tools"])...)
	for _, fn := range req.Functions {
		if tool, ok := QoderChatFunctionToQoderTool(fn); ok {
			tools = append(tools, tool)
		}
	}
	return QoderApplyChatToolChoice(tools, req.ToolChoice, req.FunctionCall)
}

func QoderChatFunctionToQoderTool(fn protocolopenai.ChatFunction) (map[string]any, bool) {
	name := strings.TrimSpace(fn.Name)
	if name == "" {
		return nil, false
	}
	function := map[string]any{
		"name":       name,
		"parameters": QoderRawMessageOrDefault(fn.Parameters, map[string]any{"type": "object", "properties": map[string]any{}}),
	}
	if strings.TrimSpace(fn.Description) != "" {
		function["description"] = fn.Description
	}
	if fn.Strict != nil {
		function["strict"] = *fn.Strict
	}
	return map[string]any{
		"type":     "function",
		"function": function,
	}, true
}

func QoderApplyChatToolChoice(tools []any, toolChoice json.RawMessage, functionCall json.RawMessage) []any {
	if len(tools) == 0 {
		return []any{}
	}
	name, disabled := QoderChatToolChoiceTarget(toolChoice, functionCall)
	if disabled {
		return []any{}
	}
	if name == "" {
		return tools
	}
	filtered := make([]any, 0, len(tools))
	for _, tool := range tools {
		if strings.EqualFold(QoderDeclaredToolName(tool), name) {
			filtered = append(filtered, tool)
		}
	}
	// 如果客户端传了不存在的工具名，保持原始 tools，让上游按正常协议报错或自行处理。
	if len(filtered) == 0 {
		return tools
	}
	return filtered
}

func QoderChatToolChoiceTarget(toolChoice json.RawMessage, functionCall json.RawMessage) (string, bool) {
	if name, disabled, ok := QoderParseChatToolChoice(toolChoice, true); ok {
		return name, disabled
	}
	if name, disabled, ok := QoderParseChatToolChoice(functionCall, false); ok {
		return name, disabled
	}
	return "", false
}

func QoderParseChatToolChoice(raw json.RawMessage, modern bool) (string, bool, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false, false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "none":
			return "", true, true
		case "auto", "required":
			return "", false, true
		default:
			return strings.TrimSpace(value), false, true
		}
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", false, false
	}
	if !modern {
		return QoderStringField(obj, "name"), false, true
	}
	if strings.EqualFold(QoderStringField(obj, "type"), "none") {
		return "", true, true
	}
	if name := QoderStringField(obj, "name"); name != "" {
		return name, false, true
	}
	if fn, ok := obj["function"].(map[string]any); ok {
		return QoderStringField(fn, "name"), false, true
	}
	return "", false, true
}

func QoderResponsesToolsToQoderTools(tools []protocolopenai.ResponsesTool) []any {
	if len(tools) == 0 {
		return []any{}
	}
	converted := make([]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		function := map[string]any{
			"name":       tool.Name,
			"parameters": QoderRawMessageOrDefault(tool.Parameters, map[string]any{"type": "object", "properties": map[string]any{}}),
		}
		if tool.Description != "" {
			function["description"] = tool.Description
		}
		if tool.Strict != nil {
			function["strict"] = *tool.Strict
		}
		converted = append(converted, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return converted
}

func QoderDeclaredToolNameMapper(tools []any) QoderToolNameMapper {
	aliases := QoderDeclaredToolNameAliases(tools)
	if len(aliases) == 0 {
		return nil
	}
	return func(name string) string {
		key := QoderToolNameKey(name)
		if key == "" {
			return name
		}
		if mapped := aliases[key]; mapped != "" {
			return mapped
		}
		return name
	}
}

func QoderDeclaredToolNameAliases(tools []any) map[string]string {
	aliases := map[string]string{}
	for _, raw := range tools {
		name := QoderDeclaredToolName(raw)
		if name == "" {
			continue
		}
		for _, alias := range QoderToolNameAliases(name) {
			key := QoderToolNameKey(alias)
			if key != "" {
				aliases[key] = name
			}
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

func QoderDeclaredToolName(raw any) string {
	tool, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if name := strings.TrimSpace(QoderStringField(tool, "name")); name != "" {
		return name
	}
	function, _ := tool["function"].(map[string]any)
	return strings.TrimSpace(QoderStringField(function, "name"))
}

func QoderToolNameAliases(name string) []string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	aliases := []string{trimmed}
	switch strings.ToLower(trimmed) {
	case "bash":
		aliases = append(aliases, "Bash", "shell", "execute_bash")
	case "read":
		aliases = append(aliases, "Read")
	case "write":
		aliases = append(aliases, "Write")
	case "edit":
		aliases = append(aliases, "Edit")
	case "multiedit", "multi_edit":
		aliases = append(aliases, "MultiEdit", "multi_edit")
	case "grep":
		aliases = append(aliases, "Grep")
	case "glob":
		aliases = append(aliases, "Glob")
	case "ls":
		aliases = append(aliases, "LS", "Ls")
	case "todowrite", "todo_write":
		aliases = append(aliases, "TodoWrite", "todo_write")
	}
	return aliases
}

func QoderToolNameKey(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", ""))
}

func QoderRawMessageOrDefault(raw json.RawMessage, fallback any) any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return fallback
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback
	}
	return value
}

func QoderPayloadMessageFromMessage(message QoderMessage) map[string]any {
	isUser := message.Role == "user"
	content := message.Text
	if isUser {
		content = ""
	}
	msg := map[string]any{
		"role":                        message.Role,
		"content":                     content,
		"contents":                    []any{},
		"reasoning_content_signature": "",
		"response_meta":               QoderBlankResponseMeta,
	}
	if len(message.TextContent) > 0 {
		contents := make([]any, 0, len(message.TextContent))
		for _, block := range message.TextContent {
			contents = append(contents, block)
		}
		msg["contents"] = contents
	} else if message.Text != "" {
		msg["contents"] = []any{map[string]any{"type": "text", "text": message.Text}}
	}
	CopyQoderToolFields(msg, message.Raw)
	if strings.TrimSpace(message.ToolCallID) != "" {
		toolCallID := strings.TrimSpace(message.ToolCallID)
		msg["tool_call_id"] = toolCallID
		msg["tool_call_call_id"] = toolCallID
	}
	return msg
}

func QoderTextContentBlock(text string, cacheControl *protocolanthropic.AnthropicCacheControl) map[string]any {
	block := map[string]any{"type": "text", "text": text}
	if cacheControl != nil {
		block["cache_control"] = map[string]any{"type": cacheControl.Type}
	}
	return block
}

func CopyQoderToolFields(out map[string]any, raw map[string]any) {
	if len(raw) == 0 {
		return
	}
	if toolCalls := NormalizeQoderToolCalls(raw["tool_calls"]); len(toolCalls) > 0 {
		out["tool_calls"] = toolCalls
	} else if toolCalls := AnthropicToolUseBlocksToQoderToolCalls(raw["content"]); len(toolCalls) > 0 {
		out["tool_calls"] = toolCalls
	}
	if toolCallID := FirstNonEmptyQoder(
		QoderStringField(raw, "tool_call_id"),
		QoderStringField(raw, "tool_call_call_id"),
		QoderStringField(raw, "call_id"),
	); toolCallID != "" {
		out["tool_call_id"] = toolCallID
		out["tool_call_call_id"] = toolCallID
	}
	if name, ok := raw["name"].(string); ok && strings.TrimSpace(name) != "" {
		out["name"] = name
	}
}

func EnrichQoderToolResultMessages(messages []QoderMessage) []QoderMessage {
	toolNamesByID := map[string]string{}
	for i := range messages {
		for id, name := range QoderToolCallNamesFromRaw(messages[i].Raw) {
			if id != "" && name != "" {
				toolNamesByID[id] = name
			}
		}
		if messages[i].Role != "tool" {
			continue
		}
		toolCallID := QoderMessageToolCallID(messages[i])
		if toolCallID == "" {
			continue
		}
		if strings.TrimSpace(messages[i].ToolCallID) == "" {
			messages[i].ToolCallID = toolCallID
		}
		if QoderStringField(messages[i].Raw, "name") != "" {
			continue
		}
		name := toolNamesByID[toolCallID]
		if name == "" {
			continue
		}
		if messages[i].Raw == nil {
			messages[i].Raw = map[string]any{}
		}
		messages[i].Raw["name"] = name
	}
	return messages
}

func QoderToolCallIDFromMap(tool map[string]any) string {
	return FirstNonEmptyQoder(
		QoderStringField(tool, "id"),
		QoderStringField(tool, "tool_call_id"),
		QoderStringField(tool, "call_id"),
	)
}

func NormalizeQoderToolCalls(raw any) []any {
	tools := QoderAnySlice(raw)
	if len(tools) == 0 {
		return []any{}
	}
	normalized := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		function, _ := tool["function"].(map[string]any)
		name := QoderStringField(function, "name")
		arguments := QoderToolArgumentsString(function["arguments"])
		if name == "" && strings.TrimSpace(arguments) == "" {
			continue
		}
		id := QoderStringField(tool, "id")
		callType := QoderStringField(tool, "type")
		if callType == "" {
			callType = "function"
		}
		normalized = append(normalized, map[string]any{
			"id":   id,
			"type": callType,
			"function": map[string]any{
				"name":      name,
				"arguments": arguments,
			},
		})
	}
	return normalized
}

func AnthropicToolUseBlocksToQoderToolCalls(raw any) []any {
	blocks := QoderAnySlice(raw)
	if len(blocks) == 0 {
		return []any{}
	}
	toolCalls := make([]any, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok || block["type"] != "tool_use" {
			continue
		}
		name := QoderStringField(block, "name")
		if name == "" {
			continue
		}
		toolCalls = append(toolCalls, map[string]any{
			"id":   QoderStringField(block, "id"),
			"type": "function",
			"function": map[string]any{
				"name":      name,
				"arguments": QoderToolArgumentsString(block["input"]),
			},
		})
	}
	return toolCalls
}

// qoderPayloadFingerprint 保留会话身份的空值及 SHA-256 十六进制格式，不调用资金领域。
func qoderPayloadFingerprint(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
