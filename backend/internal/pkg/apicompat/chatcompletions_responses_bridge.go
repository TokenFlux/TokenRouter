package apicompat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	toolOutputMediaMarker      = "[Tool output media moved to the following user message]"
	toolOutputMediaAttribution = "[Tool output media for call %s]"
)

type toolOutputMediaByCallID map[string][]ChatContentPart

// ResponsesToChatCompletionsRequest 将 Responses API 请求转换为 Chat Completions 请求，
// 供只实现 `/v1/chat/completions` 的上游使用。
func ResponsesToChatCompletionsRequest(req *ResponsesRequest) (*ChatCompletionsRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("responses request is nil")
	}

	messages, err := responsesInputToChatMessages(req.Instructions, req.Input)
	if err != nil {
		return nil, err
	}

	out := &ChatCompletionsRequest{
		Model:               req.Model,
		Messages:            messages,
		MaxCompletionTokens: req.MaxOutputTokens,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		Stream:              req.Stream,
		ServiceTier:         req.ServiceTier,
		ParallelToolCalls:   req.ParallelToolCalls,
	}
	if req.Reasoning != nil {
		out.ReasoningEffort = req.Reasoning.Effort
	}
	effectiveTools, err := EffectiveResponsesTools(req)
	if err != nil {
		return nil, err
	}
	if len(effectiveTools) > 0 {
		tools, err := responsesToolsToChatTools(effectiveTools)
		if err != nil {
			return nil, err
		}
		out.Tools = tools
	}
	// tools 全部被丢弃（如仅含 web_search/image_generation 等服务端工具）时不再转发
	// tool_choice：上游会拒绝 "'tool_choice' is only allowed when 'tools' are specified"。
	// 指向被丢弃工具的选择项同理（见 responsesToolChoiceToChatToolChoice）。
	if len(out.Tools) > 0 && len(req.ToolChoice) > 0 {
		declared := make(map[string]bool, len(out.Tools))
		for _, tool := range out.Tools {
			if tool.Function != nil {
				declared[tool.Function.Name] = true
			}
			if strings.EqualFold(strings.TrimSpace(tool.Type), "x_search") {
				declared["x_search"] = true
			}
		}
		if tc := responsesToolChoiceToChatToolChoice(req.ToolChoice, declared); len(tc) > 0 {
			out.ToolChoice = tc
		}
	}
	if req.Text != nil {
		out.ResponseFormat = responsesTextFormatToChatResponseFormat(req.Text.Format)
	}

	return out, nil
}

// EffectiveResponsesTools 汇总 Responses 请求声明的所有客户端可执行工具。
// 新版 Codex 会把运行时工具放在 input 内的 additional_tools 项中，
// 只支持 Chat Completions 的上游必须同时收到顶层和 input 内的工具。
func EffectiveResponsesTools(req *ResponsesRequest) ([]ResponsesTool, error) {
	if req == nil {
		return nil, nil
	}

	tools := append([]ResponsesTool(nil), req.Tools...)
	inputRaw := bytesTrimSpace(req.Input)
	if len(inputRaw) == 0 || string(inputRaw) == "null" || inputRaw[0] != '[' {
		return tools, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(inputRaw, &items); err != nil {
		return nil, fmt.Errorf("parse responses input for additional tools: %w", err)
	}
	for _, raw := range items {
		raw = bytesTrimSpace(raw)
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var discriminator struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &discriminator); err != nil {
			return nil, fmt.Errorf("parse responses additional tools item: %w", err)
		}
		if discriminator.Type != "additional_tools" {
			continue
		}
		var item struct {
			Tools []ResponsesTool `json:"tools"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("parse responses additional tools item: %w", err)
		}
		tools = append(tools, item.Tools...)
	}
	return tools, nil
}

// CustomToolNames 收集 Responses 请求中 custom/freeform 工具的名字。Chat 桥回程时
// 需要据此把模型对这些工具的调用还原为 custom_tool_call 项，Codex 只按该类型路由。
func CustomToolNames(tools []ResponsesTool) map[string]bool {
	var out map[string]bool
	for _, tool := range tools {
		if tool.Type == "custom" && tool.Name != "" {
			if out == nil {
				out = make(map[string]bool)
			}
			out[tool.Name] = true
		}
	}
	return out
}

// NamespacedToolName 记录 namespace 子工具的原始归属。
type NamespacedToolName struct {
	Namespace string
	Name      string
}

// NamespaceToolNames 收集 namespace 子工具摊平名到原始归属的映射。Chat 桥回程时
// 需要恢复 namespace 与裸工具名；超长摊平名包含截断哈希，不能靠字符串切分还原。
// 摊平名撞名的请求已在转换阶段被显式拒绝（见 namespaceChildrenToChatTools），
// 此处映射不存在歧义。
func NamespaceToolNames(tools []ResponsesTool) map[string]NamespacedToolName {
	var out map[string]NamespacedToolName
	for _, tool := range tools {
		if tool.Type != "namespace" || tool.Name == "" {
			continue
		}
		children := tool.Tools
		if len(children) == 0 {
			children = tool.Children
		}
		for _, child := range children {
			if child.Type != "function" || child.Name == "" {
				continue
			}
			if out == nil {
				out = make(map[string]NamespacedToolName)
			}
			out[flattenNamespaceToolName(tool.Name, child.Name)] = NamespacedToolName{
				Namespace: tool.Name,
				Name:      child.Name,
			}
		}
	}
	return out
}

// HasToolSearchTool 判断 Responses 请求是否声明了 tool_search 服务端工具。Chat 桥
// 回程时需据此把代理调用还原为 tool_search_call；Codex 只会执行 execution=client
// 的该类条目，同名 function_call 会因载荷不匹配而中止当前回合。
func HasToolSearchTool(tools []ResponsesTool) bool {
	for _, tool := range tools {
		if tool.Type == "tool_search" {
			return true
		}
	}
	return false
}

// responsesInputToChatMessages 将 Responses 请求里的 instructions 和 input[]
// 转成 Chat Completions messages，并分成三段处理：
//
//	parse     —— instructions 转 system message，input[] 拆成逐项输入
//	build     —— buildChatMessagesFromItems 挂载 reasoning、合并并行工具调用，
//	             并跳过没有 Chat 等价物的 Responses item
//	normalize —— normalizeChatMessages 统一收口 DeepSeek 需要的消息不变量
//
// build + normalize 的拆分把协议规则集中在少数入口里，避免未来 Codex 新增
// item type 时被泛化路径误传给上游。
func responsesInputToChatMessages(instructions string, inputRaw json.RawMessage) ([]ChatMessage, error) {
	var messages []ChatMessage
	if strings.TrimSpace(instructions) != "" {
		content, _ := json.Marshal(instructions)
		messages = append(messages, ChatMessage{Role: "system", Content: content})
	}

	inputRaw = bytesTrimSpace(inputRaw)
	if len(inputRaw) == 0 || string(inputRaw) == "null" {
		return messages, nil
	}

	// 裸字符串输入表示一个普通用户回合。
	var inputText string
	if err := json.Unmarshal(inputRaw, &inputText); err == nil {
		content, _ := json.Marshal(inputText)
		messages = append(messages, ChatMessage{Role: "user", Content: content})
		return messages, nil
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal(inputRaw, &rawItems); err != nil {
		return nil, fmt.Errorf("parse responses input: %w", err)
	}

	built, mediaByCallID, err := buildChatMessagesFromItems(messages, rawItems)
	if err != nil {
		return nil, err
	}
	return normalizeChatMessagesWithToolOutputMedia(built, mediaByCallID), nil
}

// buildChatMessagesFromItems 遍历 Responses input items，并追加对应的 Chat message。
func buildChatMessagesFromItems(messages []ChatMessage, rawItems []json.RawMessage) ([]ChatMessage, toolOutputMediaByCallID, error) {
	// pendingReasoning 暂存 reasoning item 的文本，直到写出它归属的 assistant
	// message。DeepSeek thinking 模式要求产生工具调用的 reasoning_content 随同
	// assistant tool_calls 回传；丢失后上游会返回 400。它只允许跨过同回合的
	// assistant message，其它角色会结束当前 thinking 片段。
	var pendingReasoning string
	mediaByCallID := make(toolOutputMediaByCallID)

	for _, raw := range rawItems {
		raw = bytesTrimSpace(raw)
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}

		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			var text string
			if textErr := json.Unmarshal(raw, &text); textErr == nil {
				content, _ := json.Marshal(text)
				messages = append(messages, ChatMessage{Role: "user", Content: content})
				pendingReasoning = ""
				continue
			}
			return nil, nil, fmt.Errorf("parse responses input item: %w", err)
		}

		role := chatCompletionsBridgeRole(rawString(item["role"]))
		itemType := rawString(item["type"])
		switch itemType {
		case "reasoning":
			if txt := extractResponsesReasoningText(item); txt != "" {
				pendingReasoning = txt
			}
			continue
		case "function_call":
			arguments := rawString(item["arguments"])
			if strings.TrimSpace(arguments) == "" {
				arguments = "{}"
			}
			name := rawString(item["name"])
			// namespace 子工具的历史调用带 namespace 字段，需与请求方向的摊平
			// 命名（namespaceChildrenToChatTools）保持一致。
			if ns := rawString(item["namespace"]); ns != "" {
				name = flattenNamespaceToolName(ns, name)
			}
			toolCall := ChatToolCall{
				ID:   rawString(item["call_id"]),
				Type: "function",
				Function: ChatFunctionCall{
					Name:      name,
					Arguments: arguments,
				},
			}
			messages = appendAssistantToolCall(messages, toolCall, pendingReasoning)
			pendingReasoning = ""
			continue
		case "tool_search_call":
			// tool_search 调用的 arguments 是 JSON 对象（如 {"query": ...}），
			// 原文即为降级 function 调用的 arguments 字符串。
			arguments := strings.TrimSpace(string(bytesTrimSpace(item["arguments"])))
			if s := rawString(item["arguments"]); s != "" {
				arguments = s
			}
			if arguments == "" || arguments == "null" {
				arguments = "{}"
			}
			toolCall := ChatToolCall{
				ID:   rawString(item["call_id"]),
				Type: "function",
				Function: ChatFunctionCall{
					Name:      toolSearchProxyName,
					Arguments: arguments,
				},
			}
			messages = appendAssistantToolCall(messages, toolCall, pendingReasoning)
			pendingReasoning = ""
			continue
		case "custom_tool_call":
			// custom/freeform 工具的历史调用：input 自由文本包进降级 function 工具
			// 的 {"input": ...} 参数，与请求方向的工具降级（customToolInputSchema）
			// 保持一致，模型才能把历史与当前工具定义对上。
			arguments, _ := json.Marshal(map[string]string{"input": rawString(item["input"])})
			toolCall := ChatToolCall{
				ID:   rawString(item["call_id"]),
				Type: "function",
				Function: ChatFunctionCall{
					Name:      rawString(item["name"]),
					Arguments: string(arguments),
				},
			}
			messages = appendAssistantToolCall(messages, toolCall, pendingReasoning)
			pendingReasoning = ""
			continue
		case "function_call_output", "custom_tool_call_output", "tool_search_output":
			outputRaw := bytesTrimSpace(item["output"])
			callID := rawString(item["call_id"])
			delete(mediaByCallID, callID)

			outputText, media, rewritten := extractToolOutputMedia(outputRaw)
			if rewritten {
				if callID != "" {
					mediaByCallID[callID] = media
				}
			} else {
				outputText = rawString(outputRaw)
				if outputText == "" && len(outputRaw) > 0 && string(outputRaw) != "null" && string(outputRaw) != `""` {
					// 对象/数组形式的输出（如 tool_search 的结果列表）整体字符串化。
					outputText = string(outputRaw)
				}
			}
			content, _ := json.Marshal(outputText)
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: callID,
				Content:    content,
			})
			pendingReasoning = ""
			continue
		case "input_text", "text":
			content, _ := json.Marshal(rawString(item["text"]))
			messages = append(messages, ChatMessage{Role: "user", Content: content})
			pendingReasoning = ""
			continue
		case "input_image":
			content, err := chatContentFromSingleResponsesPart(itemType, item)
			if err != nil {
				return nil, nil, err
			}
			messages = append(messages, ChatMessage{Role: "user", Content: content})
			pendingReasoning = ""
			continue
		}

		// 只有真正的 message item 会转成 chat message。Codex 还会发出没有
		// Chat 等价物的 Responses item（web_search_call、local_shell_call、
		// file_search_call 等）。如果走泛化路径，它们会插到
		// assistant tool_calls 和 tool reply 中间，导致 DeepSeek 拒绝请求。
		if itemType != "" && itemType != "message" {
			pendingReasoning = ""
			continue
		}

		content := item["content"]
		if len(bytesTrimSpace(content)) == 0 {
			if text := rawString(item["text"]); text != "" {
				content, _ = json.Marshal(text)
			}
		}
		chatContent, err := responsesContentToChatContent(content, role)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, ChatMessage{Role: role, Content: chatContent})
		// reasoning 只允许跨过 assistant 文本消息。
		if role != "assistant" {
			pendingReasoning = ""
		}
	}

	return messages, mediaByCallID, nil
}

// extractToolOutputMedia 只改写可识别的图片节点。无媒体输出返回 rewritten=false，
// 让调用方保留原始字节与提示缓存前缀。
func extractToolOutputMedia(outputRaw json.RawMessage) (string, []ChatContentPart, bool) {
	outputRaw = bytesTrimSpace(outputRaw)
	if len(outputRaw) == 0 || string(outputRaw) == "null" {
		return "", nil, false
	}

	var outputString string
	if err := json.Unmarshal(outputRaw, &outputString); err == nil {
		if isToolOutputImageDataURL(outputString) {
			return toolOutputMediaMarker, []ChatContentPart{toolOutputImagePart(outputString)}, true
		}

		nested, ok := decodeToolOutputJSON([]byte(outputString))
		if !ok {
			return "", nil, false
		}
		rewritten, media, changed := rewriteToolOutputMediaValue(nested)
		if !changed {
			return "", nil, false
		}
		encoded, err := json.Marshal(rewritten)
		if err != nil {
			return "", nil, false
		}
		return string(encoded), media, true
	}

	value, ok := decodeToolOutputJSON(outputRaw)
	if !ok {
		return "", nil, false
	}
	rewritten, media, changed := rewriteToolOutputMediaValue(value)
	if !changed {
		return "", nil, false
	}
	encoded, err := json.Marshal(rewritten)
	if err != nil {
		return "", nil, false
	}
	return string(encoded), media, true
}

func decodeToolOutputJSON(raw []byte) (any, bool) {
	if !json.Valid(raw) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	return value, true
}

func rewriteToolOutputMediaValue(value any) (any, []ChatContentPart, bool) {
	switch typed := value.(type) {
	case []any:
		var media []ChatContentPart
		changed := false
		for i, item := range typed {
			rewritten, itemMedia, itemChanged := rewriteToolOutputMediaValue(item)
			if !itemChanged {
				continue
			}
			typed[i] = rewritten
			media = append(media, itemMedia...)
			changed = true
		}
		return typed, media, changed
	case map[string]any:
		if imageURL, ok := recognizedToolOutputImageURL(typed); ok {
			return map[string]any{
				"type": "input_text",
				"text": toolOutputMediaMarker,
			}, []ChatContentPart{toolOutputImagePart(imageURL)}, true
		}

		content, ok := typed["content"]
		if !ok {
			return typed, nil, false
		}
		rewritten, media, changed := rewriteToolOutputMediaValue(content)
		if !changed {
			return typed, nil, false
		}
		typed["content"] = rewritten
		return typed, media, true
	default:
		return value, nil, false
	}
}

func recognizedToolOutputImageURL(value map[string]any) (string, bool) {
	partType, _ := value["type"].(string)
	if partType != "input_image" && partType != "image_url" {
		return "", false
	}

	switch imageURL := value["image_url"].(type) {
	case string:
		return imageURL, strings.TrimSpace(imageURL) != ""
	case map[string]any:
		url, _ := imageURL["url"].(string)
		return url, strings.TrimSpace(url) != ""
	default:
		return "", false
	}
}

func isToolOutputImageDataURL(value string) bool {
	const prefix = "data:image/"
	const separator = ";base64,"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	separatorIndex := strings.Index(value[len(prefix):], separator)
	if separatorIndex <= 0 {
		return false
	}
	payloadIndex := len(prefix) + separatorIndex + len(separator)
	return payloadIndex < len(value)
}

func toolOutputImagePart(imageURL string) ChatContentPart {
	return ChatContentPart{
		Type:     "image_url",
		ImageURL: &ChatImageURL{URL: imageURL},
	}
}

// appendAssistantToolCall 把工具调用合并进 Chat 消息列表。连续的工具调用必须
// 共用一条 assistant 消息，随后再紧跟对应的工具回复。
func appendAssistantToolCall(messages []ChatMessage, toolCall ChatToolCall, pendingReasoning string) []ChatMessage {
	if n := len(messages); n > 0 && messages[n-1].Role == "assistant" {
		messages[n-1].ToolCalls = append(messages[n-1].ToolCalls, toolCall)
		if messages[n-1].ReasoningContent == "" {
			messages[n-1].ReasoningContent = pendingReasoning
		}
		return messages
	}
	return append(messages, ChatMessage{
		Role:             "assistant",
		ToolCalls:        []ChatToolCall{toolCall},
		ReasoningContent: pendingReasoning,
	})
}

// normalizeChatMessages 集中保证 DeepSeek / OpenAI Chat Completions schema
// 对工具调用的要求：带 tool_calls 的 assistant message 后面必须按顺序紧跟
// 每个 tool_call_id 对应的一条 tool message，中间不能夹其它消息。
//
// Codex 历史里常见的破坏方式包括：
//   - 非 tool 消息落在 assistant tool_calls 和 tool replies 中间；
//   - 并行工具调用的部分输出缺失，或重连后留下未回答的 tool_call；
//   - tool reply 没有对应的 assistant tool_call。
//
// 这里会重建消息序列，让每个已回答的 tool_call 后面直接跟着对应回复；
// 未回答的 tool_call 会被丢弃，只剩空内容的 assistant 也会被丢弃。
func normalizeChatMessages(messages []ChatMessage) []ChatMessage {
	return normalizeChatMessagesWithToolOutputMedia(messages, nil)
}

func normalizeChatMessagesWithToolOutputMedia(messages []ChatMessage, mediaByCallID toolOutputMediaByCallID) []ChatMessage {
	// 按 tool_call_id 索引所有工具回复，重复 id 时保留最后一条。
	replies := make(map[string]ChatMessage)
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallID != "" {
			replies[m.ToolCallID] = m
		}
	}

	out := make([]ChatMessage, 0, len(messages))
	for _, m := range messages {
		switch {
		case m.Role == "tool":
			// 没有 tool_call_id 的裸 tool message 属于 Chat Completions 直通，
			// 保留原位；有 assistant 声明的工具回复会在 assistant 后统一输出，
			// 其它孤儿回复直接丢弃。
			if m.ToolCallID == "" {
				out = append(out, m)
			}
			continue
		case len(m.ToolCalls) > 0:
			kept := make([]ChatToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				if tc.ID == "" {
					continue
				}
				if _, ok := replies[tc.ID]; ok {
					kept = append(kept, tc)
				}
			}
			if len(kept) == 0 {
				// 没有已回答的 tool_call 时，如果有内容就降级成普通消息，否则丢弃。
				if isBlankChatContent(m.Content) {
					continue
				}
				m.ToolCalls = nil
				out = append(out, m)
				continue
			}
			m.ToolCalls = kept
			out = append(out, m)
			for _, tc := range kept {
				out = append(out, replies[tc.ID])
			}

			var mediaParts []ChatContentPart
			for _, tc := range kept {
				media := mediaByCallID[tc.ID]
				if len(media) == 0 {
					continue
				}
				mediaParts = append(mediaParts, ChatContentPart{
					Type: "text",
					Text: fmt.Sprintf(toolOutputMediaAttribution, tc.ID),
				})
				mediaParts = append(mediaParts, media...)
			}
			if len(mediaParts) > 0 {
				content, _ := json.Marshal(mediaParts)
				out = append(out, ChatMessage{Role: "user", Content: content})
			}
		default:
			out = append(out, m)
		}
	}
	return out
}

// isBlankChatContent 判断 chat message content 是否没有可用文本。
func isBlankChatContent(raw json.RawMessage) bool {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return true
	}
	return chatMessageContentText(raw) == ""
}

// extractResponsesReasoningText 从 Responses reasoning item 中提取 reasoning
// 文本。Chat→Responses 桥会把上游 reasoning_content 写进 summary_text，因此
// 这里优先读取 summary[].text，再回退到 content。
func extractResponsesReasoningText(item map[string]json.RawMessage) string {
	var parts []string
	collect := func(raw json.RawMessage) {
		raw = bytesTrimSpace(raw)
		if len(raw) == 0 || string(raw) == "null" {
			return
		}
		var arr []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &arr); err == nil {
			for _, p := range arr {
				if t := rawString(p["text"]); t != "" {
					parts = append(parts, t)
				}
			}
			return
		}
		if t := rawString(raw); t != "" {
			parts = append(parts, t)
		}
	}
	collect(item["summary"])
	if len(parts) == 0 {
		collect(item["content"])
	}
	return strings.Join(parts, "\n")
}

func chatCompletionsBridgeRole(role string) string {
	trimmed := strings.TrimSpace(role)
	if trimmed == "" {
		return "user"
	}
	if strings.EqualFold(trimmed, "developer") {
		return "system"
	}
	return role
}

func responsesContentToChatContent(raw json.RawMessage, role string) (json.RawMessage, error) {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		empty, _ := json.Marshal("")
		return empty, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return raw, nil
	}

	var rawParts []json.RawMessage
	if err := json.Unmarshal(raw, &rawParts); err == nil {
		return responsesContentPartsToChatContent(rawParts, role)
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		return chatContentFromSingleResponsesPart(rawString(obj["type"]), obj)
	}

	return raw, nil
}

func responsesContentPartsToChatContent(rawParts []json.RawMessage, role string) (json.RawMessage, error) {
	var textParts []string
	var chatParts []ChatContentPart
	hasNonText := false

	for _, rawPart := range rawParts {
		var part map[string]json.RawMessage
		if err := json.Unmarshal(rawPart, &part); err != nil {
			continue
		}
		partType := rawString(part["type"])
		switch partType {
		case "input_text", "output_text", "text", "":
			text := rawString(part["text"])
			if text == "" {
				continue
			}
			textParts = append(textParts, text)
			chatParts = append(chatParts, ChatContentPart{Type: "text", Text: text})
		case "input_image", "image_url":
			imageURL := rawString(part["image_url"])
			if imageURL == "" {
				imageURL = rawNestedString(part["image_url"], "url")
			}
			if imageURL == "" {
				continue
			}
			hasNonText = true
			chatParts = append(chatParts, ChatContentPart{
				Type:     "image_url",
				ImageURL: &ChatImageURL{URL: imageURL},
			})
		}
	}

	if !hasNonText {
		joined, _ := json.Marshal(strings.Join(textParts, "\n\n"))
		return joined, nil
	}
	if role != "user" {
		joined, _ := json.Marshal(strings.Join(textParts, "\n\n"))
		return joined, nil
	}
	if len(chatParts) == 0 {
		empty, _ := json.Marshal("")
		return empty, nil
	}
	return json.Marshal(chatParts)
}

func chatContentFromSingleResponsesPart(partType string, part map[string]json.RawMessage) (json.RawMessage, error) {
	switch partType {
	case "input_image", "image_url":
		imageURL := rawString(part["image_url"])
		if imageURL == "" {
			imageURL = rawNestedString(part["image_url"], "url")
		}
		return json.Marshal([]ChatContentPart{{
			Type:     "image_url",
			ImageURL: &ChatImageURL{URL: imageURL},
		}})
	default:
		return json.Marshal(rawString(part["text"]))
	}
}

// customToolInputSchema 是 custom/freeform 工具降级为 function 工具时的参数 schema。
// chat 协议无法表达 custom 工具的自由文本输入（及其 grammar 约束），退化为单一
// input 字符串参数；回程时再从 arguments 的 input 字段还原（见
// extractCustomToolCallInput）。
const customToolInputSchema = `{"type":"object","properties":{"input":{"type":"string","description":"The raw input for this tool, passed through verbatim."}},"required":["input"]}`

func responsesToolsToChatTools(tools []ResponsesTool) ([]ChatTool, error) {
	// 顶层 function/custom 工具名集合：namespace 子工具摊平后与其撞名时，chat
	// 上游无法按 namespace 区分调用归属。这类请求在原生 Responses 上游是合法的
	// （按 namespace+name 路由），歧义由摊平转换制造且无法消除，必须显式拒绝，
	// 不能静默降级（重复声明发给上游、回程还原到错误工具）。
	topLevel := make(map[string]bool)
	for _, tool := range tools {
		if (tool.Type == "function" || tool.Type == "custom") && tool.Name != "" {
			topLevel[tool.Name] = true
		}
	}
	flatOwner := make(map[string]NamespacedToolName)
	toolSearchDeclared := false
	out := make([]ChatTool, 0, len(tools))
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			out = append(out, ChatTool{
				Type: "function",
				Function: &ChatFunction{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Parameters,
					Strict:      tool.Strict,
				},
			})
		case "custom":
			// codex 0.14x 的核心执行工具 exec 即为 custom 类型；丢弃它会让模型
			// 无法执行任何命令，必须降级为 function 工具透传。
			out = append(out, ChatTool{
				Type: "function",
				Function: &ChatFunction{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  json.RawMessage(customToolInputSchema),
				},
			})
		case "tool_search":
			// 代理不能改名（codex 的模型侧按 tool_search 这个名字调用），与客户端
			// 声明的同名工具无法区分——回程会把普通工具的调用劫持成 tool_search_call，
			// 必须显式拒绝；重复声明 type=tool_search 去重即可。
			if topLevel[toolSearchProxyName] {
				return nil, fmt.Errorf("built-in tool_search conflicts with a declared tool named %q; this upstream cannot disambiguate them, rename the tool", toolSearchProxyName)
			}
			if toolSearchDeclared {
				continue
			}
			toolSearchDeclared = true
			out = append(out, toolSearchProxyChatTool())
		case "namespace":
			flattened, err := namespaceChildrenToChatTools(tool, topLevel, flatOwner)
			if err != nil {
				return nil, err
			}
			out = append(out, flattened...)
		case "x_search":
			out = append(out, ChatTool{
				Type:                     "x_search",
				AllowedXHandles:          tool.AllowedXHandles,
				ExcludedXHandles:         tool.ExcludedXHandles,
				FromDate:                 tool.FromDate,
				ToDate:                   tool.ToDate,
				EnableImageUnderstanding: tool.EnableImageUnderstanding,
				EnableVideoUnderstanding: tool.EnableVideoUnderstanding,
			})
		}
		// 其余类型（web_search、image_generation 等服务端工具）在 chat 上游没有
		// 对应能力，维持丢弃。
	}
	return out, nil
}

// toolSearchProxyName 是 tool_search 服务端工具降级后的 function 工具名。模型对
// 它的调用以同名 function_call 原样回传，由 codex 端路由。
const toolSearchProxyName = "tool_search"

const toolSearchProxySchema = `{"type":"object","properties":{"query":{"type":"string","description":"Search query for tools or connectors to load."},"limit":{"type":"integer","description":"Maximum number of tool groups to return."}},"required":["query"]}`

func toolSearchProxyChatTool() ChatTool {
	return ChatTool{
		Type: "function",
		Function: &ChatFunction{
			Name:        toolSearchProxyName,
			Description: "Search and load Codex tools, plugins, connectors, and MCP namespaces for the current task.",
			Parameters:  json.RawMessage(toolSearchProxySchema),
		},
	}
}

// namespaceChildrenToChatTools 将 namespace 工具的子 function 工具摊平为顶层
// function 工具，名字加 "<namespace>__" 前缀。摊平名与顶层工具或其他 namespace
// 撞名时返回错误（歧义不可消除，显式拒绝）；同一 (namespace, 子工具) 的重复声明
// 去重后不算冲突。
func namespaceChildrenToChatTools(tool ResponsesTool, topLevel map[string]bool, flatOwner map[string]NamespacedToolName) ([]ChatTool, error) {
	if tool.Name == "" {
		return nil, nil
	}
	children := tool.Tools
	if len(children) == 0 {
		children = tool.Children
	}
	var out []ChatTool
	for _, child := range children {
		if child.Type != "function" || child.Name == "" {
			continue
		}
		flat := flattenNamespaceToolName(tool.Name, child.Name)
		entry := NamespacedToolName{Namespace: tool.Name, Name: child.Name}
		if topLevel[flat] {
			return nil, fmt.Errorf("namespace tool %q/%q flattens to %q which conflicts with a top-level tool of the same name; this upstream cannot disambiguate them, rename one of the tools", tool.Name, child.Name, flat)
		}
		if prev, ok := flatOwner[flat]; ok {
			if prev == entry {
				continue
			}
			return nil, fmt.Errorf("namespace tools %q/%q and %q/%q both flatten to %q; this upstream cannot disambiguate them, rename one of the tools", prev.Namespace, prev.Name, tool.Name, child.Name, flat)
		}
		flatOwner[flat] = entry
		out = append(out, ChatTool{
			Type: "function",
			Function: &ChatFunction{
				Name:        flat,
				Description: child.Description,
				Parameters:  child.Parameters,
				Strict:      child.Strict,
			},
		})
	}
	return out, nil
}

// chatToolNameMaxLen 是 Chat Completions function 工具名的通用长度上限。
const chatToolNameMaxLen = 64

// flattenNamespaceToolName 生成 namespace 子工具的摊平名；超长时截断并追加
// sha256 短哈希保证唯一性。
func flattenNamespaceToolName(namespace, name string) string {
	full := namespace + "__" + name
	if len(full) <= chatToolNameMaxLen {
		return full
	}
	sum := sha256.Sum256([]byte(full))
	suffix := "__" + hex.EncodeToString(sum[:4])
	prefixLen := chatToolNameMaxLen - len(suffix)
	var prefix strings.Builder
	for _, ch := range full {
		if prefix.Len()+len(string(ch)) > prefixLen {
			break
		}
		_, _ = prefix.WriteRune(ch)
	}
	return prefix.String() + suffix
}

// responsesToolChoiceToChatToolChoice 把 Responses 的 tool_choice 转为 chat 形态。
// declared 是转换后实际声明的 chat 工具名集合：具名选择项仅在目标工具幸存时转发，
// 服务端工具（web_search 等）的选择项随工具本身丢弃——指向未声明工具的 tool_choice
// 会被 chat 上游 400 拒绝。返回 nil 表示丢弃 tool_choice。
func responsesToolChoiceToChatToolChoice(raw json.RawMessage, declared map[string]bool) json.RawMessage {
	var choice map[string]json.RawMessage
	if err := json.Unmarshal(raw, &choice); err != nil {
		// "auto"/"none"/"required" 等字符串形式原样转发。
		return raw
	}
	var name string
	switch rawString(choice["type"]) {
	case "x_search":
		if !declared["x_search"] {
			return nil
		}
		out, err := json.Marshal(map[string]any{"type": "x_search"})
		if err != nil {
			return raw
		}
		return out
	case "tool_search":
		// tool_search 未被丢弃而是降级为同名 function 代理（见
		// responsesToolsToChatTools），强制选择它同样降级为 function 选择，
		// 静默丢弃会把强制搜索退化为自动选择。
		name = toolSearchProxyName
	case "function", "custom":
		// custom 工具已降级为 function 工具，指向它的 tool_choice 同样按 function 转换。
		name = rawString(choice["name"])
		if name == "" {
			name = rawNestedString(choice["function"], "name")
		}
		if name == "" {
			return raw
		}
	default:
		return nil
	}
	if !declared[name] {
		return nil
	}
	out, err := json.Marshal(map[string]any{
		"type": "function",
		"function": map[string]string{
			"name": name,
		},
	})
	if err != nil {
		return raw
	}
	return out
}

// extractCustomToolCallInput 从降级 function 调用的 arguments 中还原 custom 工具的
// 自由文本输入：优先取 {"input": "..."} 的 input 字段；模型未按 schema 输出时原样
// 回传，交由客户端校验、模型重试。
func extractCustomToolCallInput(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return trimmed
	}
	if raw, ok := obj["input"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return trimmed
	}
	if len(obj) == 0 {
		return ""
	}
	return trimmed
}

// ChatCompletionsResponseToResponses 将非流式 Chat Completions 响应转换为
// Responses API 响应。customTools 是客户端请求中 custom 工具的名字集合，
// 命中的调用会还原为 custom_tool_call；toolSearch 表示客户端声明了 tool_search，
// 对应代理调用会还原为 tool_search_call；namespaceTools 用于恢复子工具的原始
// namespace 与裸名称。
func ChatCompletionsResponseToResponses(resp *ChatCompletionsResponse, model string, customTools map[string]bool, toolSearch bool, namespaceTools map[string]NamespacedToolName) *ResponsesResponse {
	id := ""
	if resp != nil {
		id = resp.ID
	}
	if id == "" {
		id = generateResponsesID()
	}

	out := &ResponsesResponse{
		ID:     id,
		Object: "response",
		Model:  model,
		Status: "completed",
	}
	if resp == nil {
		out.Output = []ResponsesOutput{emptyResponsesMessageOutput()}
		return out
	}
	if out.Model == "" {
		out.Model = resp.Model
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		out.Output = chatMessageToResponsesOutput(choice.Message, customTools, toolSearch, namespaceTools)
		if choice.FinishReason == "length" {
			out.Status = "incomplete"
			out.IncompleteDetails = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
		}
	}
	if len(out.Output) == 0 {
		out.Output = []ResponsesOutput{emptyResponsesMessageOutput()}
	}
	if resp.Usage != nil {
		out.Usage = ChatUsageToResponsesUsage(resp.Usage)
	}
	return out
}

func chatMessageToResponsesOutput(message ChatMessage, customTools map[string]bool, toolSearch bool, namespaceTools map[string]NamespacedToolName) []ResponsesOutput {
	var outputs []ResponsesOutput
	reasoning := message.ReasoningText()
	if reasoning != "" {
		outputs = append(outputs, ResponsesOutput{
			Type: "reasoning",
			ID:   generateItemID(),
			Summary: []ResponsesSummary{{
				Type: "summary_text",
				Text: reasoning,
			}},
		})
	}

	text := chatMessageContentText(message.Content)
	if text == "" && strings.TrimSpace(reasoning) != "" && len(message.ToolCalls) == 0 {
		text = reasoning
	}
	if text != "" || len(message.ToolCalls) == 0 {
		outputs = append(outputs, ResponsesOutput{
			Type: "message",
			ID:   generateItemID(),
			Role: "assistant",
			Content: []ResponsesContentPart{{
				Type: "output_text",
				Text: text,
			}},
			Status: "completed",
		})
	}

	for _, toolCall := range message.ToolCalls {
		arguments := toolCall.Function.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		if customTools[toolCall.Function.Name] {
			outputs = append(outputs, ResponsesOutput{
				Type:   "custom_tool_call",
				ID:     generateItemID(),
				CallID: toolCall.ID,
				Name:   toolCall.Function.Name,
				Input:  extractCustomToolCallInput(arguments),
				Status: "completed",
			})
			continue
		}
		if toolSearch && toolCall.Function.Name == toolSearchProxyName {
			outputs = append(outputs, ResponsesOutput{
				Type:      "tool_search_call",
				ID:        generateItemID(),
				CallID:    toolCall.ID,
				Arguments: arguments,
				Status:    "completed",
			})
			continue
		}
		if ns, ok := namespaceTools[toolCall.Function.Name]; ok {
			outputs = append(outputs, ResponsesOutput{
				Type:      "function_call",
				ID:        generateItemID(),
				CallID:    toolCall.ID,
				Name:      ns.Name,
				Namespace: ns.Namespace,
				Arguments: arguments,
				Status:    "completed",
			})
			continue
		}
		outputs = append(outputs, ResponsesOutput{
			Type:      "function_call",
			ID:        generateItemID(),
			CallID:    toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: arguments,
			Status:    "completed",
		})
	}

	return outputs
}

// toolSearchCallArgumentsJSON 把降级 function 调用累积的 arguments 字符串还原为
// tool_search_call 线上要求的 JSON 对象；模型未按 schema 输出（非法 JSON）时按
// 字符串值兜底，交由 codex 解析报错后让模型重试。
func toolSearchCallArgumentsJSON(arguments string) json.RawMessage {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	fallback, _ := json.Marshal(arguments)
	return fallback
}

func emptyResponsesMessageOutput() ResponsesOutput {
	return ResponsesOutput{
		Type:    "message",
		ID:      generateItemID(),
		Role:    "assistant",
		Content: []ResponsesContentPart{{Type: "output_text", Text: ""}},
		Status:  "completed",
	}
}

func chatMessageContentText(raw json.RawMessage) string {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []ChatContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var texts []string
		for _, part := range parts {
			if part.Type == "text" && part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
		return strings.Join(texts, "\n\n")
	}
	return ""
}

// ChatUsageToResponsesUsage 将 Chat Completions token 用量转换为 Responses
// usage 结构。
func ChatUsageToResponsesUsage(usage *ChatUsage) *ResponsesUsage {
	if usage == nil {
		return nil
	}
	out := &ResponsesUsage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	if usage.PromptTokensDetails != nil && (usage.PromptTokensDetails.CachedTokens > 0 ||
		usage.PromptTokensDetails.CacheCreationTokens > 0 || usage.PromptTokensDetails.CacheWriteTokens > 0) {
		out.InputTokensDetails = &ResponsesInputTokensDetails{
			CachedTokens:        usage.PromptTokensDetails.CachedTokens,
			CacheCreationTokens: usage.PromptTokensDetails.CacheCreationTokens,
			CacheWriteTokens:    usage.PromptTokensDetails.CacheWriteTokens,
		}
		if usage.PromptTokensDetails.CacheWriteTokens > 0 {
			out.CacheCreationInputTokens = usage.PromptTokensDetails.CacheWriteTokens
		} else {
			out.CacheCreationInputTokens = usage.PromptTokensDetails.CacheCreationTokens
		}
	}
	return out
}

// ChatCompletionsToResponsesStreamState 记录 Chat Completions SSE chunk 转换为
// Responses SSE 事件时的中间状态。
type ChatCompletionsToResponsesStreamState struct {
	ResponseID     string
	Model          string
	Created        int64
	SequenceNumber int
	CreatedSent    bool
	CompletedSent  bool

	// nextOutputIndex 按 item 打开顺序分配 output_index，保证流式索引与最终
	// response.output 数组顺序一致。
	nextOutputIndex int

	// reasoning item 生命周期。DeepSeek 类上游会先流出 reasoning_content，再
	// 流出正文，因此 reasoning 必须作为独立 output item，在 delta 前打开，并在
	// message/tool item 打开前关闭。
	ReasoningItemID string
	ReasoningIndex  int
	ReasoningOpen   bool
	ReasoningDone   bool

	// message item 与 output_text content part 生命周期。
	MessageItemID string
	MessageIndex  int
	TextPartOpen  bool

	Text      strings.Builder
	Reasoning strings.Builder

	// 工具调用生命周期，按上游 tool_call index 归档。
	ToolCalls       map[int]*ChatToolCall
	ToolItemIDs     map[int]string
	ToolOutputIndex map[int]int

	// CustomTools 是客户端请求中 custom/freeform 工具的名字集合（见
	// CustomToolNames）。命中的调用按 custom_tool_call 生命周期下发，codex 才能
	// 路由回它注册的 custom 工具。
	CustomTools map[string]bool

	// ToolSearchDeclared 表示客户端请求声明了 tool_search 工具（见
	// HasToolSearchTool）。命中的代理调用按 tool_search_call 项还原，codex 只按
	// 该项类型（且 execution=client）执行 tool search。
	ToolSearchDeclared bool

	// NamespaceTools 是 namespace 子工具的摊平名 → 原始归属映射（见
	// NamespaceToolNames）。命中的调用还原为带 namespace 字段的 function_call 项，
	// codex 按 namespace+name 路由。
	NamespaceTools map[string]NamespacedToolName

	// toolIsCustom 记录每个工具调用宣告时的类型判定，保证 added/done 事件的
	// 项类型一致。
	toolIsCustom map[int]bool

	// toolIsToolSearch 记录工具调用是否判定为 tool_search 代理调用。
	toolIsToolSearch map[int]bool

	// toolNamespace 记录工具调用宣告时命中的 namespace 归属（见 NamespaceTools）。
	toolNamespace map[int]NamespacedToolName

	// toolAnnounced 记录 output_item.added 是否已发出。存在 custom 工具且名字
	// 尚未到达时延迟宣告，待名字可判定类型后再补发（见 announceChatToolItem）。
	toolAnnounced map[int]bool

	FinishReason string
	Usage        *ResponsesUsage
}

// NewChatCompletionsToResponsesStreamState 返回初始化后的流式转换状态。
func NewChatCompletionsToResponsesStreamState(model string) *ChatCompletionsToResponsesStreamState {
	return &ChatCompletionsToResponsesStreamState{
		ResponseID:       generateResponsesID(),
		Model:            model,
		Created:          time.Now().Unix(),
		ToolCalls:        make(map[int]*ChatToolCall),
		ToolItemIDs:      make(map[int]string),
		ToolOutputIndex:  make(map[int]int),
		toolIsCustom:     make(map[int]bool),
		toolIsToolSearch: make(map[int]bool),
		toolNamespace:    make(map[int]NamespacedToolName),
		toolAnnounced:    make(map[int]bool),
	}
}

func (state *ChatCompletionsToResponsesStreamState) allocOutputIndex() int {
	idx := state.nextOutputIndex
	state.nextOutputIndex++
	return idx
}

// ChatCompletionsChunkToResponsesEvents 将单个 Chat Completions 流式 chunk
// 转换为零个或多个 Responses 流式事件。
func ChatCompletionsChunkToResponsesEvents(
	chunk *ChatCompletionsChunk,
	state *ChatCompletionsToResponsesStreamState,
) []ResponsesStreamEvent {
	if chunk == nil || state == nil {
		return nil
	}
	if chunk.ID != "" {
		state.ResponseID = chunk.ID
	}
	if state.Model == "" && chunk.Model != "" {
		state.Model = chunk.Model
	}
	if chunk.Usage != nil {
		state.Usage = ChatUsageToResponsesUsage(chunk.Usage)
	}

	var events []ResponsesStreamEvent
	events = append(events, ensureChatToResponsesCreated(state)...)

	for _, choice := range chunk.Choices {
		// reasoning 作为独立 output item 发出，首个 delta 前必须先打开
		// output_item 与 summary part；同时过滤上游常见的空字符串起始 delta。
		reasoning := choice.Delta.ReasoningText()
		if reasoning != nil && *reasoning != "" {
			events = append(events, ensureChatReasoningItem(state)...)
			_, _ = state.Reasoning.WriteString(*reasoning)
			events = append(events, chatToResponsesEvent(state, "response.reasoning_summary_text.delta", &ResponsesStreamEvent{
				OutputIndex:  state.ReasoningIndex,
				SummaryIndex: 0,
				Delta:        *reasoning,
				ItemID:       state.ReasoningItemID,
			}))
		}
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			// 首个正文 delta 会先关闭 reasoning item，再打开 message item 和
			// output_text content part。
			events = append(events, closeChatReasoningItem(state)...)
			events = append(events, ensureChatToResponsesMessageItem(state)...)
			events = append(events, ensureChatToResponsesTextPart(state)...)
			_, _ = state.Text.WriteString(*choice.Delta.Content)
			events = append(events, chatToResponsesEvent(state, "response.output_text.delta", &ResponsesStreamEvent{
				OutputIndex:  state.MessageIndex,
				ContentIndex: 0,
				Delta:        *choice.Delta.Content,
				ItemID:       state.MessageItemID,
			}))
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			idx := 0
			if toolCall.Index != nil {
				idx = *toolCall.Index
			}
			stored, ok := state.ToolCalls[idx]
			if !ok {
				// 工具调用开始前需要先关闭仍打开的 reasoning item。
				events = append(events, closeChatReasoningItem(state)...)
				copyCall := toolCall
				if copyCall.ID == "" {
					copyCall.ID = generateItemID()
				}
				copyCall.Type = "function"
				// arguments 由下面的共享累加逻辑统一处理，避免 GLM/Zhipu 这类
				// 首帧同时携带 id/name/arguments 的上游把首帧参数计入两次。
				copyCall.Function.Arguments = ""
				state.ToolCalls[idx] = &copyCall
				stored = &copyCall
				state.ToolItemIDs[idx] = generateItemID()
				state.ToolOutputIndex[idx] = state.allocOutputIndex()
			} else {
				if toolCall.ID != "" {
					stored.ID = toolCall.ID
				}
				if toolCall.Function.Name != "" {
					stored.Function.Name = toolCall.Function.Name
				}
			}
			events = append(events, announceChatToolItem(state, idx, stored, false)...)
			if toolCall.Function.Arguments != "" {
				stored.Function.Arguments += toolCall.Function.Arguments
				// 未宣告（名字未到）时仅累积，宣告时统一补发；custom 调用的
				// arguments 是包裹 input 的 JSON 片段，无法增量还原为自由文本
				// 输入，缓冲整份 arguments 收尾时一次性下发（见 closeChatToolItems）；
				// tool_search 调用同样收尾时随 output_item.done 全量下发。
				if state.toolAnnounced[idx] && !state.toolIsCustom[idx] && !state.toolIsToolSearch[idx] {
					events = append(events, chatToResponsesEvent(state, "response.function_call_arguments.delta", &ResponsesStreamEvent{
						OutputIndex: state.ToolOutputIndex[idx],
						ItemID:      state.ToolItemIDs[idx],
						Delta:       toolCall.Function.Arguments,
						CallID:      stored.ID,
						Name:        stored.Function.Name,
					}))
				}
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			state.FinishReason = *choice.FinishReason
		}
	}

	return events
}

// FinalizeChatCompletionsResponsesStream 生成 Responses 流的终止事件。
func FinalizeChatCompletionsResponsesStream(state *ChatCompletionsToResponsesStreamState) []ResponsesStreamEvent {
	if state == nil || state.CompletedSent {
		return nil
	}
	var events []ResponsesStreamEvent
	events = append(events, ensureChatToResponsesCreated(state)...)

	// 关闭没有进入正文阶段的 reasoning item（仅 reasoning 或空 completion）。
	events = append(events, closeChatReasoningItem(state)...)
	events = append(events, synthesizeChatReasoningFallbackMessage(state)...)

	if state.MessageItemID != "" {
		if state.TextPartOpen {
			events = append(events, chatToResponsesEvent(state, "response.output_text.done", &ResponsesStreamEvent{
				OutputIndex:  state.MessageIndex,
				ContentIndex: 0,
				Text:         state.Text.String(),
				ItemID:       state.MessageItemID,
			}))
			events = append(events, chatToResponsesEvent(state, "response.content_part.done", &ResponsesStreamEvent{
				OutputIndex:  state.MessageIndex,
				ContentIndex: 0,
				ItemID:       state.MessageItemID,
				Part:         &ResponsesContentPart{Type: "output_text", Text: state.Text.String()},
			}))
		}
		events = append(events, chatToResponsesEvent(state, "response.output_item.done", &ResponsesStreamEvent{
			OutputIndex: state.MessageIndex,
			Item: &ResponsesOutput{
				Type:    "message",
				ID:      state.MessageItemID,
				Role:    "assistant",
				Content: []ResponsesContentPart{{Type: "output_text", Text: state.Text.String()}},
				Status:  "completed",
			},
		}))
	}

	// 关闭流里打开过的所有 function_call item。Codex 只有收到
	// function_call_arguments.done 与 output_item.done 后才会认为工具调用完成。
	events = append(events, closeChatToolItems(state)...)

	status := "completed"
	var incompleteDetails *ResponsesIncompleteDetails
	if state.FinishReason == "length" {
		status = "incomplete"
		incompleteDetails = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
	}

	state.CompletedSent = true
	events = append(events, chatToResponsesEvent(state, "response.completed", &ResponsesStreamEvent{
		Response: &ResponsesResponse{
			ID:                state.ResponseID,
			Object:            "response",
			Model:             state.Model,
			Status:            status,
			Output:            state.chatOutput(),
			Usage:             state.Usage,
			IncompleteDetails: incompleteDetails,
		},
	}))
	return events
}

func ensureChatToResponsesCreated(state *ChatCompletionsToResponsesStreamState) []ResponsesStreamEvent {
	if state.CreatedSent {
		return nil
	}
	state.CreatedSent = true
	return []ResponsesStreamEvent{chatToResponsesEvent(state, "response.created", &ResponsesStreamEvent{
		Response: &ResponsesResponse{
			ID:     state.ResponseID,
			Object: "response",
			Model:  state.Model,
			Status: "in_progress",
			Output: []ResponsesOutput{},
		},
	})}
}

// ensureChatReasoningItem 在首个 reasoning delta 前打开 reasoning output item
// 与 summary part；Codex 依赖这段生命周期展示流式思考内容。
func ensureChatReasoningItem(state *ChatCompletionsToResponsesStreamState) []ResponsesStreamEvent {
	if state.ReasoningOpen || state.ReasoningDone {
		return nil
	}
	state.ReasoningOpen = true
	state.ReasoningItemID = generateItemID()
	state.ReasoningIndex = state.allocOutputIndex()
	return []ResponsesStreamEvent{
		chatToResponsesEvent(state, "response.output_item.added", &ResponsesStreamEvent{
			OutputIndex: state.ReasoningIndex,
			Item:        &ResponsesOutput{Type: "reasoning", ID: state.ReasoningItemID, Status: "in_progress"},
		}),
		chatToResponsesEvent(state, "response.reasoning_summary_part.added", &ResponsesStreamEvent{
			OutputIndex:  state.ReasoningIndex,
			SummaryIndex: 0,
			ItemID:       state.ReasoningItemID,
			Part:         &ResponsesContentPart{Type: "summary_text"},
		}),
	}
}

// closeChatReasoningItem 发出 reasoning item 的终止事件：
// reasoning_summary_text.done、reasoning_summary_part.done 与 output_item.done。
func closeChatReasoningItem(state *ChatCompletionsToResponsesStreamState) []ResponsesStreamEvent {
	if !state.ReasoningOpen {
		return nil
	}
	state.ReasoningOpen = false
	state.ReasoningDone = true
	reasoning := state.Reasoning.String()
	return []ResponsesStreamEvent{
		chatToResponsesEvent(state, "response.reasoning_summary_text.done", &ResponsesStreamEvent{
			OutputIndex:  state.ReasoningIndex,
			SummaryIndex: 0,
			Text:         reasoning,
			ItemID:       state.ReasoningItemID,
		}),
		chatToResponsesEvent(state, "response.reasoning_summary_part.done", &ResponsesStreamEvent{
			OutputIndex:  state.ReasoningIndex,
			SummaryIndex: 0,
			ItemID:       state.ReasoningItemID,
			Part:         &ResponsesContentPart{Type: "summary_text", Text: reasoning},
		}),
		chatToResponsesEvent(state, "response.output_item.done", &ResponsesStreamEvent{
			OutputIndex: state.ReasoningIndex,
			Item: &ResponsesOutput{
				Type:    "reasoning",
				ID:      state.ReasoningItemID,
				Status:  "completed",
				Summary: []ResponsesSummary{{Type: "summary_text", Text: reasoning}},
			},
		}),
	}
}

func synthesizeChatReasoningFallbackMessage(state *ChatCompletionsToResponsesStreamState) []ResponsesStreamEvent {
	if state == nil ||
		state.MessageItemID != "" ||
		state.Text.Len() > 0 ||
		state.Reasoning.Len() == 0 ||
		len(state.ToolCalls) > 0 {
		return nil
	}

	text := state.Reasoning.String()
	if strings.TrimSpace(text) == "" {
		return nil
	}

	var events []ResponsesStreamEvent
	events = append(events, ensureChatToResponsesMessageItem(state)...)
	events = append(events, ensureChatToResponsesTextPart(state)...)
	_, _ = state.Text.WriteString(text)
	events = append(events, chatToResponsesEvent(state, "response.output_text.delta", &ResponsesStreamEvent{
		OutputIndex:  state.MessageIndex,
		ContentIndex: 0,
		Delta:        text,
		ItemID:       state.MessageItemID,
	}))
	return events
}

func ensureChatToResponsesMessageItem(state *ChatCompletionsToResponsesStreamState) []ResponsesStreamEvent {
	if state.MessageItemID != "" {
		return nil
	}
	state.MessageItemID = generateItemID()
	state.MessageIndex = state.allocOutputIndex()
	return []ResponsesStreamEvent{chatToResponsesEvent(state, "response.output_item.added", &ResponsesStreamEvent{
		OutputIndex: state.MessageIndex,
		Item: &ResponsesOutput{
			Type:    "message",
			ID:      state.MessageItemID,
			Role:    "assistant",
			Status:  "in_progress",
			Content: []ResponsesContentPart{{Type: "output_text"}},
		},
	})}
}

func ensureChatToResponsesTextPart(state *ChatCompletionsToResponsesStreamState) []ResponsesStreamEvent {
	if state.TextPartOpen {
		return nil
	}
	state.TextPartOpen = true
	return []ResponsesStreamEvent{chatToResponsesEvent(state, "response.content_part.added", &ResponsesStreamEvent{
		OutputIndex:  state.MessageIndex,
		ContentIndex: 0,
		ItemID:       state.MessageItemID,
		Part:         &ResponsesContentPart{Type: "output_text", Text: ""},
	})}
}

// announceChatToolItem 在类型可判定时发出工具调用的 output_item.added。custom
// 工具的判定依赖名字：名字未到且请求里存在 custom 工具时延迟宣告，避免 added/done
// 的项类型不一致；force 用于流收尾，名字始终未到时按 function_call 兜底。
func announceChatToolItem(
	state *ChatCompletionsToResponsesStreamState,
	idx int,
	stored *ChatToolCall,
	force bool,
) []ResponsesStreamEvent {
	if state.toolAnnounced[idx] {
		return nil
	}
	if !force && stored.Function.Name == "" && (len(state.CustomTools) > 0 || state.ToolSearchDeclared || len(state.NamespaceTools) > 0) {
		return nil
	}
	state.toolAnnounced[idx] = true
	isCustom := state.CustomTools[stored.Function.Name]
	isToolSearch := !isCustom && state.ToolSearchDeclared && stored.Function.Name == toolSearchProxyName
	state.toolIsCustom[idx] = isCustom
	state.toolIsToolSearch[idx] = isToolSearch
	itemType := "function_call"
	if isCustom {
		itemType = "custom_tool_call"
	}
	if isToolSearch {
		itemType = "tool_search_call"
	}
	// namespace 子工具的调用仍按 function_call 生命周期下发，但 added/done 项要
	// 还原为裸子工具名 + namespace 字段（codex 按 namespace+name 路由）。
	itemName, itemNamespace := stored.Function.Name, ""
	if ns, ok := state.NamespaceTools[stored.Function.Name]; ok && !isCustom && !isToolSearch {
		state.toolNamespace[idx] = ns
		itemName, itemNamespace = ns.Name, ns.Namespace
	}
	events := []ResponsesStreamEvent{chatToResponsesEvent(state, "response.output_item.added", &ResponsesStreamEvent{
		OutputIndex: state.ToolOutputIndex[idx],
		Item: &ResponsesOutput{
			Type:      itemType,
			ID:        state.ToolItemIDs[idx],
			CallID:    stored.ID,
			Name:      itemName,
			Namespace: itemNamespace,
			Status:    "in_progress",
		},
	})}
	// 迟到宣告时补发已累积的参数增量（custom/tool_search 的输入收尾统一下发，不补发）。
	if !isCustom && !isToolSearch && stored.Function.Arguments != "" {
		events = append(events, chatToResponsesEvent(state, "response.function_call_arguments.delta", &ResponsesStreamEvent{
			OutputIndex: state.ToolOutputIndex[idx],
			ItemID:      state.ToolItemIDs[idx],
			Delta:       stored.Function.Arguments,
			CallID:      stored.ID,
			Name:        stored.Function.Name,
		}))
	}
	return events
}

// closeChatToolItems 为流里打开过的每个工具调用发出对应的参数完成事件与
// output_item.done，并带上完整 call_id、name 和 arguments，保证 Codex 能执行调用。
func closeChatToolItems(state *ChatCompletionsToResponsesStreamState) []ResponsesStreamEvent {
	if len(state.ToolCalls) == 0 {
		return nil
	}
	var events []ResponsesStreamEvent
	for i := 0; i < len(state.ToolCalls); i++ {
		toolCall, ok := state.ToolCalls[i]
		if !ok || toolCall == nil {
			continue
		}
		itemID, opened := state.ToolItemIDs[i]
		if !opened {
			continue
		}
		// 名字始终未到导致尚未宣告的调用，收尾前按最终名字兜底宣告。
		events = append(events, announceChatToolItem(state, i, toolCall, true)...)
		arguments := toolCall.Function.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		outputIndex := state.ToolOutputIndex[i]
		if state.toolIsCustom[i] {
			// custom 调用按 custom_tool_call 生命周期收尾：input 在此处一次性下发
			// （流中不产出增量，见 ChatCompletionsChunkToResponsesEvents）。
			input := extractCustomToolCallInput(arguments)
			if input != "" {
				events = append(events, chatToResponsesEvent(state, "response.custom_tool_call_input.delta", &ResponsesStreamEvent{
					OutputIndex: outputIndex,
					ItemID:      itemID,
					Delta:       input,
				}))
			}
			events = append(events,
				chatToResponsesEvent(state, "response.custom_tool_call_input.done", &ResponsesStreamEvent{
					OutputIndex: outputIndex,
					ItemID:      itemID,
					CallID:      toolCall.ID,
					Name:        toolCall.Function.Name,
					Input:       input,
				}),
				chatToResponsesEvent(state, "response.output_item.done", &ResponsesStreamEvent{
					OutputIndex: outputIndex,
					Item: &ResponsesOutput{
						Type:   "custom_tool_call",
						ID:     itemID,
						CallID: toolCall.ID,
						Name:   toolCall.Function.Name,
						Input:  input,
						Status: "completed",
					},
				}),
			)
			continue
		}
		if state.toolIsToolSearch[i] {
			// tool_search 调用按 tool_search_call 项收尾：codex 从 output_item.done
			// 物化该调用（无参数增量事件），arguments 全量随项下发。
			events = append(events, chatToResponsesEvent(state, "response.output_item.done", &ResponsesStreamEvent{
				OutputIndex: outputIndex,
				Item: &ResponsesOutput{
					Type:      "tool_search_call",
					ID:        itemID,
					CallID:    toolCall.ID,
					Arguments: arguments,
					Status:    "completed",
				},
			}))
			continue
		}
		// namespace 子工具调用在宣告时已记录归属，收尾项同样带还原名与 namespace。
		name, namespace := toolCall.Function.Name, ""
		if ns, ok := state.toolNamespace[i]; ok {
			name, namespace = ns.Name, ns.Namespace
		}
		events = append(events,
			chatToResponsesEvent(state, "response.function_call_arguments.done", &ResponsesStreamEvent{
				OutputIndex: outputIndex,
				ItemID:      itemID,
				CallID:      toolCall.ID,
				Name:        name,
				Arguments:   arguments,
			}),
			chatToResponsesEvent(state, "response.output_item.done", &ResponsesStreamEvent{
				OutputIndex: outputIndex,
				Item: &ResponsesOutput{
					Type:      "function_call",
					ID:        itemID,
					CallID:    toolCall.ID,
					Name:      name,
					Namespace: namespace,
					Arguments: arguments,
					Status:    "completed",
				},
			}),
		)
	}
	return events
}

func (state *ChatCompletionsToResponsesStreamState) chatOutput() []ResponsesOutput {
	var outputs []ResponsesOutput
	if state.Reasoning.Len() > 0 {
		outputs = append(outputs, ResponsesOutput{
			Type: "reasoning",
			ID:   generateItemID(),
			Summary: []ResponsesSummary{{
				Type: "summary_text",
				Text: state.Reasoning.String(),
			}},
		})
	}
	if state.MessageItemID != "" || len(state.ToolCalls) == 0 {
		outputs = append(outputs, ResponsesOutput{
			Type: "message",
			ID:   nonEmpty(state.MessageItemID, generateItemID()),
			Role: "assistant",
			Content: []ResponsesContentPart{{
				Type: "output_text",
				Text: state.Text.String(),
			}},
			Status: "completed",
		})
	}
	for i := 0; i < len(state.ToolCalls); i++ {
		toolCall, ok := state.ToolCalls[i]
		if !ok || toolCall == nil {
			continue
		}
		arguments := toolCall.Function.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		if state.toolIsCustom[i] {
			outputs = append(outputs, ResponsesOutput{
				Type:   "custom_tool_call",
				ID:     generateItemID(),
				CallID: toolCall.ID,
				Name:   toolCall.Function.Name,
				Input:  extractCustomToolCallInput(arguments),
				Status: "completed",
			})
			continue
		}
		if state.toolIsToolSearch[i] {
			outputs = append(outputs, ResponsesOutput{
				Type:      "tool_search_call",
				ID:        generateItemID(),
				CallID:    toolCall.ID,
				Arguments: arguments,
				Status:    "completed",
			})
			continue
		}
		name, namespace := toolCall.Function.Name, ""
		if ns, ok := state.toolNamespace[i]; ok {
			name, namespace = ns.Name, ns.Namespace
		}
		outputs = append(outputs, ResponsesOutput{
			Type:      "function_call",
			ID:        generateItemID(),
			CallID:    toolCall.ID,
			Name:      name,
			Namespace: namespace,
			Arguments: arguments,
			Status:    "completed",
		})
	}
	return outputs
}

func chatToResponsesEvent(
	state *ChatCompletionsToResponsesStreamState,
	eventType string,
	template *ResponsesStreamEvent,
) ResponsesStreamEvent {
	seq := state.SequenceNumber
	state.SequenceNumber++
	evt := *template
	evt.Type = eventType
	evt.SequenceNumber = seq
	return evt
}

func rawString(raw json.RawMessage) string {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func rawNestedString(raw json.RawMessage, key string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return rawString(obj[key])
}

func bytesTrimSpace(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
