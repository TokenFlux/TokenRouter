package bridge

import (
	"encoding/json"
	"iter"
	"strings"
)

// NativeGeminiCompatStream 拥有 Gemini 经 Anthropic wire 转换的思考/文本/工具状态。
// 下游 Responses/Chat 编排仍由调用方组合已有 bridge，写失败由迭代消费者决定退出。
type NativeGeminiCompatStream struct {
	runtime        NativeGeminiRuntime
	finishReason   string
	sawToolUse     bool
	nextBlockIndex int
	openBlockIndex int
	openToolIndex  int
	openBlockType  string
	seenText       string
	seenThinking   string
	openToolName   string
	seenToolJSON   string
	usage          NativeGeminiUsage
}

// 构造仅创建本次流的状态；负索引表示尚未打开任何内容块。
func NewNativeGeminiCompatStream(runtime NativeGeminiRuntime) *NativeGeminiCompatStream {
	return &NativeGeminiCompatStream{runtime: runtime, openBlockIndex: -1, openToolIndex: -1}
}
func (s *NativeGeminiCompatStream) Usage() *NativeGeminiUsage {
	value := s.usage
	return &value
}

// Begin 保留空 content、nil stop_reason 与初始零用量字段。
func (s *NativeGeminiCompatStream) Begin(messageID, originalModel string) iter.Seq[*AnthropicStreamEvent] {
	return func(yield func(*AnthropicStreamEvent) bool) {
		emit := func(event *AnthropicStreamEvent) bool { return !yield(event) }
		if emit(&AnthropicStreamEvent{
			Type: "message_start",
			Message: &AnthropicResponse{
				ID:         messageID,
				Type:       "message",
				Role:       "assistant",
				Model:      originalModel,
				Content:    []AnthropicContentBlock{},
				StopReason: nil, // 序列化为 JSON null。
				Usage:      AnthropicUsage{},
			},
		}) {
			return
		}

	}
}

// Process 由消费方逐事件推进，不提前处理尚未写出的后续分片。
func (s *NativeGeminiCompatStream) Process(geminiResp map[string]any, raw []byte) iter.Seq[*AnthropicStreamEvent] {
	return func(yield func(*AnthropicStreamEvent) bool) {
		emit := func(event *AnthropicStreamEvent) bool { return !yield(event) }
		if fr := NativeExtractGeminiFinishReason(geminiResp); fr != "" {
			s.finishReason = fr
		}
		if u := NativeExtractGeminiUsage(raw); u != nil {
			s.usage = *u
		}

		for _, part := range NativeExtractGeminiParts(geminiResp) {
			if text, ok := part["text"].(string); ok && text != "" {
				if s.openToolIndex >= 0 {
					if s.closeOpenTool(emit) {
						return
					}
				}
				thought, _ := part["thought"].(bool)
				blockType := "text"
				deltaType := "text_delta"
				seen := s.seenText
				if thought {
					blockType = "thinking"
					deltaType = "thinking_delta"
					seen = s.seenThinking
				}
				delta, newSeen := NativeComputeGeminiTextDelta(seen, text)
				if thought {
					s.seenThinking = newSeen
				} else {
					s.seenText = newSeen
				}
				if delta == "" {
					continue
				}
				if s.openBlockType != blockType {
					if s.closeOpenBlock(emit) {
						return
					}
					idx := s.nextBlockIndex
					s.nextBlockIndex++
					s.openBlockIndex = idx
					s.openBlockType = blockType
					contentBlock := &AnthropicContentBlock{Type: "text", Text: ""}
					if thought {
						contentBlock = &AnthropicContentBlock{Type: "thinking", Thinking: ""}
					}
					if emit(&AnthropicStreamEvent{
						Type:         "content_block_start",
						Index:        &idx,
						ContentBlock: contentBlock,
					}) {
						return
					}
				}
				deltaEvent := &AnthropicDelta{Type: deltaType, Text: delta}
				if thought {
					deltaEvent.Text = ""
					deltaEvent.Thinking = delta
				}
				if emit(&AnthropicStreamEvent{
					Type:  "content_block_delta",
					Delta: deltaEvent,
				}) {
					return
				}
				continue
			}

			if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
				name, _ := fc["name"].(string)
				if strings.TrimSpace(name) == "" {
					name = "tool"
				}
				if s.closeOpenBlock(emit) {
					return
				}
				if s.openToolIndex >= 0 && s.openToolName != name {
					if s.closeOpenTool(emit) {
						return
					}
				}
				if s.openToolIndex < 0 {
					idx := s.nextBlockIndex
					s.nextBlockIndex++
					s.openToolIndex = idx
					s.openToolName = name
					s.sawToolUse = true
					if emit(&AnthropicStreamEvent{
						Type:  "content_block_start",
						Index: &idx,
						ContentBlock: &AnthropicContentBlock{
							Type:  "tool_use",
							ID:    "toolu_" + s.runtime.RandomHex(8),
							Name:  name,
							Input: json.RawMessage(`{}`),
						},
					}) {
						return
					}
				}

				argsJSONText := "{}"
				switch v := fc["args"].(type) {
				case nil:
				case string:
					if strings.TrimSpace(v) != "" {
						argsJSONText = v
					}
				default:
					if b, err := json.Marshal(v); err == nil && len(b) > 0 {
						argsJSONText = string(b)
					}
				}
				delta, newSeen := NativeComputeGeminiTextDelta(s.seenToolJSON, argsJSONText)
				s.seenToolJSON = newSeen
				if delta != "" {
					if emit(&AnthropicStreamEvent{
						Type: "content_block_delta",
						Delta: &AnthropicDelta{
							Type:        "input_json_delta",
							PartialJSON: delta,
						},
					}) {
						return
					}
				}
			}
		}
	}
}

// Finish 保留块关闭、末尾 usage、message_delta 和 message_stop 的顺序。
func (s *NativeGeminiCompatStream) Finish() iter.Seq[*AnthropicStreamEvent] {
	return func(yield func(*AnthropicStreamEvent) bool) {
		emit := func(event *AnthropicStreamEvent) bool { return !yield(event) }

		if s.closeOpenBlock(emit) {
			return
		}
		if s.closeOpenTool(emit) {
			return
		}

		stopReason := NativeMapGeminiFinishReasonToClaudeStopReason(s.finishReason)
		if s.sawToolUse {
			stopReason = "tool_use"
		}
		if emit(&AnthropicStreamEvent{
			Type: "message_delta",
			Delta: &AnthropicDelta{
				Type:       "message_delta",
				StopReason: stopReason,
			},
			Usage: &AnthropicUsage{
				InputTokens:          s.usage.InputTokens,
				OutputTokens:         s.usage.OutputTokens,
				CacheReadInputTokens: s.usage.CacheReadInputTokens,
			},
		}) {
			return
		}
		if emit(&AnthropicStreamEvent{Type: "message_stop"}) {
			return
		}

	}
}
func (s *NativeGeminiCompatStream) closeOpenBlock(emit func(*AnthropicStreamEvent) bool) bool {
	if s.openBlockIndex < 0 {
		return false
	}
	disconnected := emit(&AnthropicStreamEvent{Type: "content_block_stop"})
	s.openBlockIndex = -1
	s.openBlockType = ""
	return disconnected
}

func (s *NativeGeminiCompatStream) closeOpenTool(emit func(*AnthropicStreamEvent) bool) bool {
	if s.openToolIndex < 0 {
		return false
	}
	disconnected := emit(&AnthropicStreamEvent{Type: "content_block_stop"})
	s.openToolIndex = -1
	s.openToolName = ""
	s.seenToolJSON = ""
	return disconnected
}
