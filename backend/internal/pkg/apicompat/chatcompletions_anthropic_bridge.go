package apicompat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// 本文件实现 Anthropic Messages 与 OpenAI Chat Completions 的直连桥，跳过 Responses API 中间表示。
//
// 原 chat fallback 会在请求侧执行 Anthropic→Responses→ChatCompletions，响应侧执行
// CC→Responses→Anthropic，使每个流式 token 经过两个状态机。force-chat 账号的上游只支持
// `/v1/chat/completions`，不会接触 Responses 语义；经 `/v1/messages` 到达的客户端也只使用
// 标准 function tools，不包含 custom、tool_search 或 namespace 等 Codex 扩展。
//
// 直连桥把两个方向都压缩为一次转换：
//
//	请求：Anthropic Messages → Chat Completions
//	响应：CC chunk/response → Anthropic events/response
//
// 图片、call ID、tool input、system、reasoning 与 schema 等处理继续复用 Responses 桥的 helper，
// 保证新旧转换语义一致。

// ---------------------------------------------------------------------------
// 请求转换：AnthropicRequest → ChatCompletionsRequest
// ---------------------------------------------------------------------------

// AnthropicToChatCompletionsRequest 直接把 Anthropic Messages 请求转换为 Chat Completions 请求。
// 其语义等价于依次调用 AnthropicToResponses 与 ResponsesToChatCompletionsRequest，
// 但不会创建中间 ResponsesRequest，也省去额外的序列化往返。
func AnthropicToChatCompletionsRequest(req *AnthropicRequest) (*ChatCompletionsRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("anthropic request is nil")
	}

	messages, err := anthropicToChatMessages(req.System, req.Messages)
	if err != nil {
		return nil, err
	}

	out := &ChatCompletionsRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   req.Stream,
	}

	// reasoning 模型（如 gpt-5.x）拒绝 temperature/top_p 采样参数。
	if !isReasoningModel(req.Model) {
		out.Temperature = req.Temperature
		out.TopP = req.TopP
	}

	if req.MaxTokens > 0 {
		v := req.MaxTokens
		if v < minMaxOutputTokens {
			v = minMaxOutputTokens
		}
		out.MaxCompletionTokens = &v
	}

	// Anthropic input_schema 本身就是 JSON Schema，可直接作为 Chat function parameters。
	// web_search_* 等 server tool 没有 Chat Completions 等价表示，因此与旧桥一致地丢弃。
	if len(req.Tools) > 0 {
		tools := anthropicToolsToChatTools(req.Tools)
		if len(tools) > 0 {
			out.Tools = tools
		}
	}

	parallelToolCalls := true
	// 只有转换后仍存在 tools 时才转发 tool_choice；具名选择还必须指向已声明工具，
	// 否则严格 Chat 上游会因未知工具返回 400。Anthropic 的并行禁用标记需要
	// 同时映射到 Chat Completions 顶层 parallel_tool_calls。
	if len(out.Tools) > 0 && len(req.ToolChoice) > 0 {
		declared := make(map[string]bool, len(out.Tools))
		for _, tool := range out.Tools {
			if tool.Function != nil {
				declared[tool.Function.Name] = true
			}
		}
		tc, allowParallel, err := convertAnthropicToolChoiceToChat(req.ToolChoice, declared)
		if err != nil {
			return nil, fmt.Errorf("convert tool_choice: %w", err)
		}
		parallelToolCalls = allowParallel
		if len(tc) > 0 {
			out.ToolChoice = tc
		}
	}

	// output_config.effort 复用当前 Responses 桥的按模型映射；thinking.type 与旧桥一致地忽略。
	effort := "medium"
	if req.OutputConfig != nil && req.OutputConfig.Effort != "" {
		effort = req.OutputConfig.Effort
	}
	if isUltraReasoningEffort(effort) {
		return nil, fmt.Errorf("reasoning effort %q is not supported", strings.TrimSpace(effort))
	}
	out.ReasoningEffort = mapAnthropicEffortToResponsesForModel(req.Model, effort)

	out.ParallelToolCalls = &parallelToolCalls

	return out, nil
}

// anthropicToChatMessages 把 Anthropic system 与消息列表直接转换为 ChatMessage。
func anthropicToChatMessages(system json.RawMessage, msgs []AnthropicMessage) ([]ChatMessage, error) {
	var messages []ChatMessage

	// parseAnthropicSystemContentParts 同时处理字符串和 block 数组，并过滤计费 header。
	if len(system) > 0 {
		sysParts, err := parseAnthropicSystemContentParts(system)
		if err != nil {
			return nil, err
		}
		if len(sysParts) > 0 {
			text := joinResponsesContentPartText(sysParts)
			if text != "" {
				content, _ := json.Marshal(text)
				messages = append(messages, ChatMessage{Role: "system", Content: content})
			}
		}
	}

	for _, m := range msgs {
		converted, err := anthropicMsgToChatMessages(m)
		if err != nil {
			return nil, err
		}
		messages = append(messages, converted...)
	}

	return normalizeChatMessages(messages), nil
}

// anthropicMsgToChatMessages 把一条 Anthropic 消息转换为一条或多条 Chat 消息。
// tool_result 独立成为 tool 角色消息，text/image 留在 user 消息，assistant tool_use 转为 tool_calls。
func anthropicMsgToChatMessages(m AnthropicMessage) ([]ChatMessage, error) {
	switch m.Role {
	case "assistant":
		return anthropicAssistantToChatMessages(m.Content)
	default: // user 与未知角色均按 user 处理。
		return anthropicUserToChatMessages(m.Content)
	}
}

// anthropicUserToChatMessages 处理字符串或 block 数组形式的 Anthropic user 消息。
// tool_result 会拆成独立 tool 消息，其中的图片提升到后续 user 消息的 image_url；
// function_call_output 只能承载字符串，因此图片必须单独传递。
func anthropicUserToChatMessages(raw json.RawMessage) ([]ChatMessage, error) {
	// 纯字符串直接生成单条 user 消息。
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		content, _ := json.Marshal(s)
		return []ChatMessage{{Role: "user", Content: content}}, nil
	}

	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}

	var out []ChatMessage
	var toolResultImageParts []ChatContentPart

	// tool_result 的文本先生成 tool 消息，图片延后并入 user 消息。
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		text, imageParts := convertToolResultOutput(b)
		content, _ := json.Marshal(text)
		out = append(out, ChatMessage{
			Role:       "tool",
			Content:    content,
			ToolCallID: b.ToolUseID,
		})
		for _, ip := range imageParts {
			toolResultImageParts = append(toolResultImageParts, ChatContentPart{
				Type:     "image_url",
				ImageURL: &ChatImageURL{URL: ip.ImageURL},
			})
		}
	}

	// 剩余 text/image 组成 user 消息。纯文本按旧桥用空行拼成字符串；只有存在图片时才使用
	// parts 数组，避免严格 Chat 上游拒绝无图片的数组 content。
	var textParts []string
	var parts []ChatContentPart
	hasImage := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				textParts = append(textParts, b.Text)
				parts = append(parts, ChatContentPart{Type: "text", Text: b.Text})
			}
		case "image":
			if uri := anthropicImageToDataURI(b.Source); uri != "" {
				hasImage = true
				parts = append(parts, ChatContentPart{
					Type:     "image_url",
					ImageURL: &ChatImageURL{URL: uri},
				})
			}
		}
	}
	if len(toolResultImageParts) > 0 {
		hasImage = true
		parts = append(parts, toolResultImageParts...)
	}

	if !hasImage {
		if len(textParts) > 0 {
			content, _ := json.Marshal(strings.Join(textParts, "\n\n"))
			out = append(out, ChatMessage{Role: "user", Content: content})
		}
		return out, nil
	}

	content, err := json.Marshal(parts)
	if err != nil {
		return nil, err
	}
	out = append(out, ChatMessage{Role: "user", Content: content})

	return out, nil
}

// anthropicAssistantToChatMessages 把文本与 tool_use 合并到 assistant 消息的 content/tool_calls；
// Chat Completions 没有入站 thinking 字段，因此与旧桥一致地丢弃 thinking block。
func anthropicAssistantToChatMessages(raw json.RawMessage) ([]ChatMessage, error) {
	// 纯字符串直接生成单条 assistant 消息。
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		content, _ := json.Marshal(s)
		return []ChatMessage{{Role: "assistant", Content: content}}, nil
	}

	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}

	msg := ChatMessage{Role: "assistant"}
	text := extractAnthropicTextFromBlocks(blocks)
	if text != "" {
		content, _ := json.Marshal(text)
		msg.Content = content
	}

	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		args := "{}"
		if len(b.Input) > 0 {
			args = string(b.Input)
		}
		msg.ToolCalls = append(msg.ToolCalls, ChatToolCall{
			ID:   b.ID,
			Type: "function",
			Function: ChatFunctionCall{
				Name:      b.Name,
				Arguments: args,
			},
		})
	}

	return []ChatMessage{msg}, nil
}

// anthropicToolsToChatTools 把 Anthropic 工具映射为 Chat function tools，
// 并丢弃没有 Chat Completions 等价表示的 web_search_* 等 server tool。
func anthropicToolsToChatTools(tools []AnthropicTool) []ChatTool {
	var out []ChatTool
	for _, t := range tools {
		if strings.HasPrefix(t.Type, "web_search") {
			continue
		}
		out = append(out, ChatTool{
			Type: "function",
			Function: &ChatFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  normalizeToolParameters(t.InputSchema),
				Strict:      boolPtr(false),
			},
		})
	}
	return out
}

// convertAnthropicToolChoiceToChat 映射 Anthropic tool_choice；返回 nil 表示丢弃选择。
// 与旧桥一致，未知类型或指向未声明工具的具名选择不向 Chat 上游转发。
//
//	{"type":"auto"}            → "auto"
//	{"type":"any"}             → "required"
//	{"type":"none"}            → "none"
//	{"type":"tool","name":"X"} → {"type":"function","function":{"name":"X"}}（X 已声明）
func convertAnthropicToolChoiceToChat(raw json.RawMessage, declared map[string]bool) (json.RawMessage, bool, error) {
	var tc struct {
		Type                   string `json:"type"`
		Name                   string `json:"name"`
		DisableParallelToolUse bool   `json:"disable_parallel_tool_use"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil, true, err
	}
	allowParallel := !tc.DisableParallelToolUse

	switch tc.Type {
	case "auto":
		converted, err := json.Marshal("auto")
		return converted, allowParallel, err
	case "any":
		converted, err := json.Marshal("required")
		return converted, allowParallel, err
	case "none":
		converted, err := json.Marshal("none")
		return converted, allowParallel, err
	case "tool":
		if tc.Name == "" || !declared[tc.Name] {
			return nil, allowParallel, nil
		}
		converted, err := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]string{"name": tc.Name},
		})
		return converted, allowParallel, err
	default:
		return nil, allowParallel, nil
	}
}

// joinResponsesContentPartText 拼接 system prompt 中的 input_text parts。
func joinResponsesContentPartText(parts []ResponsesContentPart) string {
	var texts []string
	for _, p := range parts {
		if p.Type == "input_text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}

// ---------------------------------------------------------------------------
// 非流式响应：ChatCompletionsResponse → AnthropicResponse
// ---------------------------------------------------------------------------

// ChatCompletionsResponseToAnthropic 直接把 Chat Completions 响应转换为 Anthropic Messages 响应，
// 语义等价于 ChatCompletionsResponseToResponses + ResponsesToAnthropic。
func ChatCompletionsResponseToAnthropic(resp *ChatCompletionsResponse, model string) *AnthropicResponse {
	out := &AnthropicResponse{
		Type:  "message",
		Role:  "assistant",
		Model: model,
	}

	if resp != nil {
		out.ID = resp.ID
		if out.Model == "" {
			out.Model = resp.Model
		}

		if len(resp.Choices) > 0 {
			choice := resp.Choices[0]
			out.Content = chatMessageToAnthropicBlocks(choice.Message)
			out.StopReason = AnthropicStopReasonPtr(chatFinishReasonToAnthropicStopReason(choice.FinishReason, out.Content))
			// length 由 stop_reason 表达为 max_tokens，不需要 incomplete_details 字段。
		}
		if resp.Usage != nil {
			out.Usage = chatUsageToAnthropicUsage(resp.Usage)
		}
	}

	if len(out.Content) == 0 {
		out.Content = []AnthropicContentBlock{{Type: "text", Text: ""}}
	}
	// 空 choices 或 nil 响应也必须与旧桥一致地生成 end_turn；完成态的非流式响应不能使用 null 或空 stop_reason。
	if AnthropicStopReasonString(out.StopReason) == "" {
		out.StopReason = AnthropicStopReasonPtr(chatFinishReasonToAnthropicStopReason("", out.Content))
	}
	// 上游省略响应 ID 时与旧桥一致地补齐，因为客户端把该字段视为必需。
	if out.ID == "" {
		out.ID = generateResponsesID()
	}

	return out
}

// chatMessageToAnthropicBlocks 把 reasoning、文本和 tool_calls 分别转换为
// Anthropic thinking、text 与 tool_use blocks。
func chatMessageToAnthropicBlocks(message ChatMessage) []AnthropicContentBlock {
	var blocks []AnthropicContentBlock
	reasoning := message.ReasoningText()

	if reasoning != "" {
		blocks = append(blocks, AnthropicContentBlock{
			Type:     "thinking",
			Thinking: reasoning,
		})
	}

	text := chatMessageContentText(message.Content)
	// DeepSeek 仅返回 reasoning 且没有 tool call 时，把 reasoning 同时作为可见文本，避免空响应。
	if text == "" && strings.TrimSpace(reasoning) != "" && len(message.ToolCalls) == 0 {
		text = reasoning
	}
	if text != "" || len(message.ToolCalls) == 0 {
		blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: text})
	}

	for _, toolCall := range message.ToolCalls {
		arguments := toolCall.Function.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		blocks = append(blocks, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    fromResponsesCallID(toolCall.ID),
			Name:  toolCall.Function.Name,
			Input: sanitizeAnthropicToolUseInput(toolCall.Function.Name, arguments),
		})
	}

	return blocks
}

// chatFinishReasonToAnthropicStopReason 把 Chat finish_reason 映射为 Anthropic stop_reason。
//
//	"length"     → "max_tokens"
//	"tool_calls" → "tool_use"
//	其它值       → "end_turn"（存在 tool_use block 时为 "tool_use"）
//
// stop、content_filter 和未知原因在旧桥中都视为已完成响应，再根据 blocks 推导 stop_reason。
func chatFinishReasonToAnthropicStopReason(reason string, blocks []AnthropicContentBlock) string {
	switch reason {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		if containsAnthropicToolUseBlock(blocks) {
			return "tool_use"
		}
		return "end_turn"
	}
}

// chatUsageToAnthropicUsage 把 Chat token usage 转换为 Anthropic usage，口径与旧桥一致。
func chatUsageToAnthropicUsage(usage *ChatUsage) AnthropicUsage {
	if usage == nil {
		return AnthropicUsage{}
	}

	cachedTokens := 0
	cacheCreationTokens := 0
	if usage.PromptTokensDetails != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
		// cache_write_tokens 与 cache_creation_tokens 是同一数量的两种字段名，不能相加；
		// 与旧桥一致地优先使用 write，缺失时再使用 creation。
		if usage.PromptTokensDetails.CacheWriteTokens > 0 {
			cacheCreationTokens = usage.PromptTokensDetails.CacheWriteTokens
		} else {
			cacheCreationTokens = usage.PromptTokensDetails.CacheCreationTokens
		}
	}

	inputTokens := usage.PromptTokens - cachedTokens - cacheCreationTokens
	if inputTokens < 0 {
		inputTokens = 0
	}

	return AnthropicUsage{
		InputTokens:              inputTokens,
		OutputTokens:             usage.CompletionTokens,
		CacheReadInputTokens:     cachedTokens,
		CacheCreationInputTokens: cacheCreationTokens,
	}
}

// ---------------------------------------------------------------------------
// 流式响应：ChatCompletionsChunk → []AnthropicStreamEvent
// ---------------------------------------------------------------------------

// ChatCompletionsToAnthropicStreamState 保存 Chat SSE chunk 直转 Anthropic SSE event 的状态，
// 将原来的两个转换状态机合并为一个。
type ChatCompletionsToAnthropicStreamState struct {
	MessageStartSent bool
	MessageStopSent  bool

	// 当前文本或思考内容块的生命周期状态。
	ContentBlockIndex int
	ContentBlockOpen  bool
	CurrentBlockType  string // 内容块类型：text 或 thinking。
	HasToolCall       bool

	// 工具调用按上游 index 聚合。Chat Completions 允许多个工具的参数分片交错到达，
	// Anthropic 内容块则必须在 stop 前收到全部 delta，因此统一在流收尾时顺序输出。
	toolCalls             map[int]*ChatToolCall
	toolArgumentFragments map[int][]string

	// DeepSeek 风格 reasoning_content 先于正文到达；内容块按顺序生成，因此复用同一个 block index 计数器。

	FinishReason string

	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int

	ResponseID string
	Model      string
	Created    int64
}

// NewChatCompletionsToAnthropicStreamState 返回已初始化的直转流状态。
func NewChatCompletionsToAnthropicStreamState(model string) *ChatCompletionsToAnthropicStreamState {
	return &ChatCompletionsToAnthropicStreamState{
		ResponseID:            generateResponsesID(),
		Model:                 model,
		Created:               time.Now().Unix(),
		toolCalls:             make(map[int]*ChatToolCall),
		toolArgumentFragments: make(map[int][]string),
	}
}

// ChatCompletionsChunkToAnthropicEvents 把一个 Chat 流式 chunk 转为零个或多个 Anthropic event 并更新状态。
func ChatCompletionsChunkToAnthropicEvents(
	chunk *ChatCompletionsChunk,
	state *ChatCompletionsToAnthropicStreamState,
) []AnthropicStreamEvent {
	if chunk == nil || state == nil {
		return nil
	}
	if chunk.ID != "" {
		state.ResponseID = chunk.ID
	}
	if state.Model == "" && chunk.Model != "" {
		state.Model = chunk.Model
	}

	// include_usage 通常以空 choices 的独立 chunk 到达，先保存供 finalize 的 message_delta 使用。
	if chunk.Usage != nil {
		u := chatUsageToAnthropicUsage(chunk.Usage)
		state.InputTokens = u.InputTokens
		state.OutputTokens = u.OutputTokens
		state.CacheReadInputTokens = u.CacheReadInputTokens
		state.CacheCreationInputTokens = u.CacheCreationInputTokens
	}

	var events []AnthropicStreamEvent
	events = append(events, ensureCCAnthropicMessageStart(state)...)

	for _, choice := range chunk.Choices {
		// reasoning content 写入 thinking block。
		reasoning := choice.Delta.ReasoningText()
		if reasoning != nil && *reasoning != "" {
			events = append(events, ensureCCAnthropicThinkingBlock(state)...)
			events = append(events, ccAnthropicDelta(state, &AnthropicDelta{
				Type:     "thinking_delta",
				Thinking: *reasoning,
			})...)
		}

		// 正文写入 text block，开始前先关闭 thinking block。
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			events = append(events, closeCCAnthropicBlockIfOpen(state, "thinking")...)
			events = append(events, ensureCCAnthropicTextBlock(state)...)
			events = append(events, ccAnthropicDelta(state, &AnthropicDelta{
				Type: "text_delta",
				Text: *choice.Delta.Content,
			})...)
		}

		// tool calls 仅按 index 聚合；等流结束后再生成完整且不交错的 tool_use blocks。
		for _, toolCall := range choice.Delta.ToolCalls {
			bufferCCAnthropicToolCall(state, &toolCall)
		}

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			state.FinishReason = *choice.FinishReason
		}
	}

	return events
}

// FinalizeChatCompletionsAnthropicStream 在流结束时关闭内容块并发出 message_delta/message_stop。
func FinalizeChatCompletionsAnthropicStream(state *ChatCompletionsToAnthropicStreamState) []AnthropicStreamEvent {
	if state == nil || state.MessageStopSent {
		return nil
	}

	var events []AnthropicStreamEvent
	if !state.MessageStartSent {
		events = append(events, ensureCCAnthropicMessageStart(state)...)
	}

	events = append(events, closeCCAnthropicBlock(state)...)
	events = append(events, finalizeCCAnthropicToolCalls(state)...)

	stopReason := ccFinishReasonToAnthropicStopReason(state.FinishReason, state.HasToolCall)

	events = append(events,
		AnthropicStreamEvent{
			Type: "message_delta",
			Delta: &AnthropicDelta{
				StopReason: stopReason,
			},
			Usage: &AnthropicUsage{
				InputTokens:              state.InputTokens,
				OutputTokens:             state.OutputTokens,
				CacheReadInputTokens:     state.CacheReadInputTokens,
				CacheCreationInputTokens: state.CacheCreationInputTokens,
			},
		},
		AnthropicStreamEvent{Type: "message_stop"},
	)
	state.MessageStopSent = true
	return events
}

// ensureCCAnthropicMessageStart 在首次事件前发出 message_start。
func ensureCCAnthropicMessageStart(state *ChatCompletionsToAnthropicStreamState) []AnthropicStreamEvent {
	if state.MessageStartSent {
		return nil
	}
	state.MessageStartSent = true
	return []AnthropicStreamEvent{{
		Type: "message_start",
		Message: &AnthropicResponse{
			ID:         state.ResponseID,
			Type:       "message",
			Role:       "assistant",
			Content:    []AnthropicContentBlock{},
			Model:      state.Model,
			StopReason: nil, // 序列化为 JSON null，不能是空字符串。
			Usage:      AnthropicUsage{InputTokens: 0, OutputTokens: 0},
		},
	}}
}

// ensureCCAnthropicThinkingBlock 在需要时打开 thinking block。
func ensureCCAnthropicThinkingBlock(state *ChatCompletionsToAnthropicStreamState) []AnthropicStreamEvent {
	if state.ContentBlockOpen && state.CurrentBlockType == "thinking" {
		return nil
	}
	events := closeCCAnthropicBlock(state)
	idx := state.ContentBlockIndex
	state.ContentBlockOpen = true
	state.CurrentBlockType = "thinking"
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_start",
		Index: &idx,
		ContentBlock: &AnthropicContentBlock{
			Type:     "thinking",
			Thinking: "",
		},
	})
	return events
}

// ensureCCAnthropicTextBlock 在需要时打开 text block。
func ensureCCAnthropicTextBlock(state *ChatCompletionsToAnthropicStreamState) []AnthropicStreamEvent {
	if state.ContentBlockOpen && state.CurrentBlockType == "text" {
		return nil
	}
	events := closeCCAnthropicBlock(state)
	idx := state.ContentBlockIndex
	state.ContentBlockOpen = true
	state.CurrentBlockType = "text"
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_start",
		Index: &idx,
		ContentBlock: &AnthropicContentBlock{
			Type: "text",
			Text: "",
		},
	})
	return events
}

// bufferCCAnthropicToolCall 按上游 index 聚合工具 ID、名称和参数分片。
// 首帧可能同时携带完整字段，也可能先到参数、后到名称或 ID。
func bufferCCAnthropicToolCall(state *ChatCompletionsToAnthropicStreamState, toolCall *ChatToolCall) {
	if state == nil || toolCall == nil {
		return
	}
	idx := 0
	if toolCall.Index != nil {
		idx = *toolCall.Index
	}

	stored, ok := state.toolCalls[idx]
	if !ok {
		copyCall := *toolCall
		if copyCall.ID == "" {
			copyCall.ID = generateItemID()
		}
		if copyCall.Type == "" {
			copyCall.Type = "function"
		}
		// 参数由下方共享逻辑累加，避免首帧被重复计入。
		copyCall.Function.Arguments = ""
		state.toolCalls[idx] = &copyCall
		stored = &copyCall
	} else {
		if toolCall.ID != "" {
			stored.ID = toolCall.ID
		}
		if toolCall.Type != "" {
			stored.Type = toolCall.Type
		}
		if toolCall.Function.Name != "" {
			stored.Function.Name = toolCall.Function.Name
		}
	}

	if toolCall.Function.Arguments != "" {
		// 参数分片只暂存，收尾时一次拼接，避免大参数反复复制。
		state.toolArgumentFragments[idx] = append(state.toolArgumentFragments[idx], toolCall.Function.Arguments)
	}
	state.HasToolCall = true
}

// finalizeCCAnthropicToolCalls 按工具 index 生成连续、闭合的 Anthropic tool_use blocks。
// 每个工具只发送一个完整 JSON delta，确保并行参数分片不会跨越 block 生命周期。
func finalizeCCAnthropicToolCalls(state *ChatCompletionsToAnthropicStreamState) []AnthropicStreamEvent {
	if state == nil || len(state.toolCalls) == 0 {
		return nil
	}

	idxs := make([]int, 0, len(state.toolCalls))
	for idx := range state.toolCalls {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)

	var events []AnthropicStreamEvent
	for _, idx := range idxs {
		toolCall := state.toolCalls[idx]
		if toolCall == nil {
			continue
		}
		if toolCall.ID == "" {
			toolCall.ID = generateItemID()
		}
		arguments := strings.Join(state.toolArgumentFragments[idx], "")
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		arguments = string(sanitizeAnthropicToolUseInput(toolCall.Function.Name, arguments))

		blockIdx := state.ContentBlockIndex
		events = append(events,
			AnthropicStreamEvent{
				Type:  "content_block_start",
				Index: &blockIdx,
				ContentBlock: &AnthropicContentBlock{
					Type:  "tool_use",
					ID:    fromResponsesCallID(toolCall.ID),
					Name:  toolCall.Function.Name,
					Input: json.RawMessage("{}"),
				},
			},
			AnthropicStreamEvent{
				Type:  "content_block_delta",
				Index: &blockIdx,
				Delta: &AnthropicDelta{
					Type:        "input_json_delta",
					PartialJSON: arguments,
				},
			},
			AnthropicStreamEvent{
				Type:  "content_block_stop",
				Index: &blockIdx,
			},
		)
		state.ContentBlockIndex++
	}
	return events
}

// ccAnthropicDelta 在当前内容块上发出 content_block_delta。
func ccAnthropicDelta(state *ChatCompletionsToAnthropicStreamState, delta *AnthropicDelta) []AnthropicStreamEvent {
	if !state.ContentBlockOpen {
		return nil
	}
	idx := state.ContentBlockIndex
	return []AnthropicStreamEvent{{
		Type:  "content_block_delta",
		Index: &idx,
		Delta: delta,
	}}
}

// closeCCAnthropicBlockIfOpen 仅在当前块类型匹配时关闭，用于打开 text/tool 前结束 thinking。
func closeCCAnthropicBlockIfOpen(state *ChatCompletionsToAnthropicStreamState, blockType string) []AnthropicStreamEvent {
	if !state.ContentBlockOpen || state.CurrentBlockType != blockType {
		return nil
	}
	return closeCCAnthropicBlock(state)
}

// closeCCAnthropicBlock 关闭当前文本或思考内容块。
func closeCCAnthropicBlock(state *ChatCompletionsToAnthropicStreamState) []AnthropicStreamEvent {
	if !state.ContentBlockOpen {
		return nil
	}
	idx := state.ContentBlockIndex
	state.ContentBlockOpen = false
	state.ContentBlockIndex++
	state.CurrentBlockType = ""
	return []AnthropicStreamEvent{{
		Type:  "content_block_stop",
		Index: &idx,
	}}
}

// ccFinishReasonToAnthropicStopReason 把流中取得的 Chat finish_reason 映射为 message_delta 的 Anthropic stop_reason。
func ccFinishReasonToAnthropicStopReason(reason string, hasToolCall bool) string {
	switch reason {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "stop":
		if hasToolCall {
			return "tool_use"
		}
		return "end_turn"
	default:
		if hasToolCall {
			return "tool_use"
		}
		return "end_turn"
	}
}
