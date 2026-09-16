// 本文件维护纯 Anthropic 字节修复；平台资格、设置和重试次数由调用方决定。
package anthropic

import (
	"bytes"
	"encoding/json"
	"unsafe"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var patternTypeThinking = []byte(`"type":"thinking"`)
var patternTypeThinkingSpaced = []byte(`"type": "thinking"`)
var patternTypeRedactedThinking = []byte(`"type":"redacted_thinking"`)
var patternTypeRedactedSpaced = []byte(`"type": "redacted_thinking"`)
var patternThinkingField = []byte(`"thinking":`)
var patternThinkingFieldSpaced = []byte(`"thinking" :`)
var patternEmptyContent = []byte(`"content":[]`)
var patternEmptyContentSpaced = []byte(`"content": []`)
var patternEmptyContentSp1 = []byte(`"content" : []`)
var patternEmptyContentSp2 = []byte(`"content" :[]`)
var patternEmptyText = []byte(`"text":""`)
var patternEmptyTextSpaced = []byte(`"text": ""`)
var patternEmptyTextSp1 = []byte(`"text" : ""`)
var patternEmptyTextSp2 = []byte(`"text" :""`)

// SliceRawFromBody 返回 Result.Raw 对应的原始字节切片。
// 优先使用 Result.Index 直接从 body 切片，避免对大字段（如 messages）产生额外拷贝。
// 当 Index 不可用时，退化为复制（理论上极少发生）。
func SliceRawFromBody(body []byte, r gjson.Result) []byte {
	if r.Index > 0 {
		end := r.Index + len(r.Raw)
		if end <= len(body) {
			return body[r.Index:end]
		}
	}
	// fallback: 不影响正确性，但会产生一次拷贝
	return []byte(r.Raw)
}

// StripEmptyTextBlocksFromSlice removes empty text blocks from a content slice (including nested tool_result content).
// Returns (cleaned slice, true) if any blocks were removed, or (original, false) if unchanged.
func StripEmptyTextBlocksFromSlice(blocks []any) ([]any, bool) {
	var result []any
	changed := false
	for i, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			if result != nil {
				result = append(result, block)
			}
			continue
		}
		blockType, _ := blockMap["type"].(string)

		// Strip empty text blocks
		if blockType == "text" {
			if txt, _ := blockMap["text"].(string); txt == "" {
				if result == nil {
					result = make([]any, 0, len(blocks))
					result = append(result, blocks[:i]...)
				}
				changed = true
				continue
			}
		}

		// Recurse into tool_result nested content
		if blockType == "tool_result" {
			if nestedContent, ok := blockMap["content"].([]any); ok {
				if cleaned, nestedChanged := StripEmptyTextBlocksFromSlice(nestedContent); nestedChanged {
					if result == nil {
						result = make([]any, 0, len(blocks))
						result = append(result, blocks[:i]...)
					}
					changed = true
					blockCopy := make(map[string]any, len(blockMap))
					for k, v := range blockMap {
						blockCopy[k] = v
					}
					blockCopy["content"] = cleaned
					result = append(result, blockCopy)
					continue
				}
			}
		}

		if result != nil {
			result = append(result, block)
		}
	}
	if !changed {
		return blocks, false
	}
	return result, true
}

// StripEmptyTextBlocks removes empty text blocks from the request body (including nested tool_result content).
// This is a lightweight pre-filter for the initial request path to prevent upstream 400 errors.
// Returns the original body unchanged if no empty text blocks are found.
func StripEmptyTextBlocks(body []byte) []byte {
	// Fast path: check if body contains empty text patterns
	hasEmptyTextBlock := bytes.Contains(body, patternEmptyText) ||
		bytes.Contains(body, patternEmptyTextSpaced) ||
		bytes.Contains(body, patternEmptyTextSp1) ||
		bytes.Contains(body, patternEmptyTextSp2)
	if !hasEmptyTextBlock {
		return body
	}

	jsonStr := *(*string)(unsafe.Pointer(&body))
	msgsRes := gjson.Get(jsonStr, "messages")
	if !msgsRes.Exists() || !msgsRes.IsArray() {
		return body
	}

	var messages []any
	if err := json.Unmarshal(SliceRawFromBody(body, msgsRes), &messages); err != nil {
		return body
	}

	modified := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		content, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		if cleaned, changed := StripEmptyTextBlocksFromSlice(content); changed {
			modified = true
			msgMap["content"] = cleaned
		}
	}

	if !modified {
		return body
	}

	msgsBytes, err := json.Marshal(messages)
	if err != nil {
		return body
	}
	out, err := sjson.SetRawBytes(body, "messages", msgsBytes)
	if err != nil {
		return body
	}
	return out
}

// FilterThinkingBlocksForRetry 在 retry 场景中移除或降级 thinking 相关结构。
//
// 原因：
//   - 上游可能因为历史 `thinking`/`redacted_thinking` 缺失或非法 signature 而拒收。
//   - Anthropic extended thinking 有结构约束：顶层 `thinking` 开启且最后一条 assistant
//     是 prefill 时，assistant content 必须以 thinking block 开头。
//   - 如果只移除 thinking block 却保留顶层 `thinking`，可能触发
//     "Expected `thinking` or `redacted_thinking`, but found `text`"。
//
// 策略：尽量保留内容语义。
//   - 禁用顶层 `thinking` 字段。
//   - 将 `thinking` block 转成 `text` block，保留 thinking 文本。
//   - 删除无法转成明文的 `redacted_thinking` block。
//   - 确保消息 content 不会变成空数组。
//
// 调用方传入 mappedModel 时，仅 Anthropic 官方语义执行 retry 变形；
// passback-required/unknown 上游返回原 body，避免破坏原样回传契约。
func FilterThinkingBlocksForRetry(body []byte) []byte {

	hasThinkingContent := bytes.Contains(body, patternTypeThinking) ||
		bytes.Contains(body, patternTypeThinkingSpaced) ||
		bytes.Contains(body, patternTypeRedactedThinking) ||
		bytes.Contains(body, patternTypeRedactedSpaced) ||
		bytes.Contains(body, patternThinkingField) ||
		bytes.Contains(body, patternThinkingFieldSpaced)

	// Also check for empty content arrays and empty text blocks that need fixing.
	// Note: This is a heuristic check; the actual empty content handling is done below.
	hasEmptyContent := bytes.Contains(body, patternEmptyContent) ||
		bytes.Contains(body, patternEmptyContentSpaced) ||
		bytes.Contains(body, patternEmptyContentSp1) ||
		bytes.Contains(body, patternEmptyContentSp2)

	// Check for empty text blocks: {"type":"text","text":""}
	// These cause upstream 400: "text content blocks must be non-empty"
	hasEmptyTextBlock := bytes.Contains(body, patternEmptyText) ||
		bytes.Contains(body, patternEmptyTextSpaced) ||
		bytes.Contains(body, patternEmptyTextSp1) ||
		bytes.Contains(body, patternEmptyTextSp2)

	// Fast path: nothing to process
	if !hasThinkingContent && !hasEmptyContent && !hasEmptyTextBlock {
		return body
	}

	// 尽量避免把整个 body Unmarshal 成 map（会产生大量 map/接口分配）。
	// 这里先用 gjson 把 messages 子树摘出来，后续只对 messages 做 Unmarshal/Marshal。
	jsonStr := *(*string)(unsafe.Pointer(&body))
	msgsRes := gjson.Get(jsonStr, "messages")
	if !msgsRes.Exists() || !msgsRes.IsArray() {
		return body
	}

	// Fast path：只需要删除顶层 thinking，不需要改 messages。
	// 注意：patternThinkingField 可能来自嵌套字段（如 tool_use.input.thinking），因此必须用 gjson 判断顶层字段是否存在。
	containsThinkingBlocks := bytes.Contains(body, patternTypeThinking) ||
		bytes.Contains(body, patternTypeThinkingSpaced) ||
		bytes.Contains(body, patternTypeRedactedThinking) ||
		bytes.Contains(body, patternTypeRedactedSpaced) ||
		bytes.Contains(body, patternThinkingFieldSpaced)
	if !hasEmptyContent && !hasEmptyTextBlock && !containsThinkingBlocks {
		if topThinking := gjson.Get(jsonStr, "thinking"); topThinking.Exists() {
			if out, err := sjson.DeleteBytes(body, "thinking"); err == nil {
				out = RemoveThinkingDependentContextStrategies(out)
				return out
			}
			return body
		}
		return body
	}

	var messages []any
	if err := json.Unmarshal(SliceRawFromBody(body, msgsRes), &messages); err != nil {
		return body
	}

	modified := false

	// Disable top-level thinking mode for retry to avoid structural/signature constraints upstream.
	deleteTopLevelThinking := gjson.Get(jsonStr, "thinking").Exists()

	for i := 0; i < len(messages); i++ {
		msgMap, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		content, ok := msgMap["content"].([]any)
		if !ok {
			// String content or other format - keep as is
			continue
		}

		// 延迟分配：只有检测到需要修改的块，才构建新 slice。
		var newContent []any
		modifiedThisMsg := false

		ensureNewContent := func(prefixLen int) {
			if newContent != nil {
				return
			}
			newContent = make([]any, 0, len(content))
			if prefixLen > 0 {
				newContent = append(newContent, content[:prefixLen]...)
			}
		}

		for bi := 0; bi < len(content); bi++ {
			block := content[bi]
			blockMap, ok := block.(map[string]any)
			if !ok {
				if newContent != nil {
					newContent = append(newContent, block)
				}
				continue
			}

			blockType, _ := blockMap["type"].(string)

			// Strip empty text blocks: {"type":"text","text":""}
			// Upstream rejects these with 400: "text content blocks must be non-empty"
			if blockType == "text" {
				if txt, _ := blockMap["text"].(string); txt == "" {
					modifiedThisMsg = true
					ensureNewContent(bi)
					continue
				}
			}

			// Convert thinking blocks to text (preserve content) and drop redacted_thinking.
			switch blockType {
			case "thinking":
				modifiedThisMsg = true
				ensureNewContent(bi)
				thinkingText, _ := blockMap["thinking"].(string)
				if thinkingText != "" {
					newContent = append(newContent, map[string]any{"type": "text", "text": thinkingText})
				}
				continue
			case "redacted_thinking":
				modifiedThisMsg = true
				ensureNewContent(bi)
				continue
			}

			// Handle blocks without type discriminator but with a "thinking" field.
			if blockType == "" {
				if rawThinking, hasThinking := blockMap["thinking"]; hasThinking {
					modifiedThisMsg = true
					ensureNewContent(bi)
					switch v := rawThinking.(type) {
					case string:
						if v != "" {
							newContent = append(newContent, map[string]any{"type": "text", "text": v})
						}
					default:
						if b, err := json.Marshal(v); err == nil && len(b) > 0 {
							newContent = append(newContent, map[string]any{"type": "text", "text": string(b)})
						}
					}
					continue
				}
			}

			// Recursively strip empty text blocks from tool_result nested content.
			if blockType == "tool_result" {
				if nestedContent, ok := blockMap["content"].([]any); ok {
					if cleaned, changed := StripEmptyTextBlocksFromSlice(nestedContent); changed {
						modifiedThisMsg = true
						ensureNewContent(bi)
						blockCopy := make(map[string]any, len(blockMap))
						for k, v := range blockMap {
							blockCopy[k] = v
						}
						blockCopy["content"] = cleaned
						newContent = append(newContent, blockCopy)
						continue
					}
				}
			}

			if newContent != nil {
				newContent = append(newContent, block)
			}
		}

		// Handle empty content: either from filtering or originally empty
		if newContent == nil {
			if len(content) == 0 {
				modified = true
				placeholder := "(content removed)"
				if role == "assistant" {
					placeholder = "(assistant content removed)"
				}
				msgMap["content"] = []any{map[string]any{"type": "text", "text": placeholder}}
			}
			continue
		}

		if len(newContent) == 0 {
			modified = true
			placeholder := "(content removed)"
			if role == "assistant" {
				placeholder = "(assistant content removed)"
			}
			msgMap["content"] = []any{map[string]any{"type": "text", "text": placeholder}}
			continue
		}

		if modifiedThisMsg {
			modified = true
			msgMap["content"] = newContent
		}
	}

	if !modified && !deleteTopLevelThinking {
		// Avoid rewriting JSON when no changes are needed.
		return body
	}

	out := body
	if deleteTopLevelThinking {
		if b, err := sjson.DeleteBytes(out, "thinking"); err == nil {
			out = b
		} else {
			return body
		}
		// Removing "thinking" makes any context_management strategy that requires it invalid
		// (e.g. clear_thinking_20251015).  Strip those entries so the retry request does not
		// receive a 400 "strategy requires thinking to be enabled or adaptive".
		out = RemoveThinkingDependentContextStrategies(out)
	}
	if modified {
		msgsBytes, err := json.Marshal(messages)
		if err != nil {
			return body
		}
		out, err = sjson.SetRawBytes(out, "messages", msgsBytes)
		if err != nil {
			return body
		}
	}
	return out
}

// RemoveThinkingDependentContextStrategies 从 context_management.edits 中移除
// 需要 thinking 启用的策略（如 clear_thinking_20251015）。
// 当顶层 "thinking" 字段被禁用时必须调用，否则上游会返回
// "strategy requires thinking to be enabled or adaptive"。
func RemoveThinkingDependentContextStrategies(body []byte) []byte {
	jsonStr := *(*string)(unsafe.Pointer(&body))
	editsRes := gjson.Get(jsonStr, "context_management.edits")
	if !editsRes.Exists() || !editsRes.IsArray() {
		return body
	}

	var filtered []json.RawMessage
	hasRemoved := false
	editsRes.ForEach(func(_, v gjson.Result) bool {
		if v.Get("type").String() == "clear_thinking_20251015" {
			hasRemoved = true
			return true
		}
		filtered = append(filtered, json.RawMessage(v.Raw))
		return true
	})

	if !hasRemoved {
		return body
	}

	if len(filtered) == 0 {
		if b, err := sjson.DeleteBytes(body, "context_management.edits"); err == nil {
			return b
		}
		return body
	}

	filteredBytes, err := json.Marshal(filtered)
	if err != nil {
		return body
	}
	if b, err := sjson.SetRawBytes(body, "context_management.edits", filteredBytes); err == nil {
		return b
	}
	return body
}

// FilterSignatureSensitiveBlocksForRetry 是更强的 retry 过滤器，用于上游错误明确指向
// tool block 的 signature/thought_signature 校验问题。
//
// 它包含 FilterThinkingBlocksForRetry 的处理，并额外执行：
//   - 将 `tool_use` block 转成 text，停止发送结构化工具调用。
//   - 将 `tool_result` block 转成 text，在不保留工具语义的情况下保留结果内容。
//
// 只能在确有必要时使用：把工具块转成纯文本会改变模型行为，也可能增加提示注入风险。
//
// 传入 mappedModel 时，仅 Anthropic 官方语义执行变形；passback-required/unknown
// 上游返回原 body，避免破坏原样回传契约。
func FilterSignatureSensitiveBlocksForRetry(body []byte) []byte {

	// Fast path: only run when we see likely relevant constructs.
	if !bytes.Contains(body, []byte(`"type":"thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "thinking"`)) &&
		!bytes.Contains(body, []byte(`"type":"redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"type":"tool_use"`)) &&
		!bytes.Contains(body, []byte(`"type": "tool_use"`)) &&
		!bytes.Contains(body, []byte(`"type":"tool_result"`)) &&
		!bytes.Contains(body, []byte(`"type": "tool_result"`)) &&
		!bytes.Contains(body, []byte(`"thinking":`)) &&
		!bytes.Contains(body, []byte(`"thinking" :`)) {
		return body
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	modified := false

	// Disable top-level thinking for retry to avoid structural/signature constraints upstream.
	if _, exists := req["thinking"]; exists {
		delete(req, "thinking")
		modified = true
		// Remove context_management strategies that require thinking to be enabled
		// (e.g. clear_thinking_20251015), otherwise upstream returns 400.
		if cm, ok := req["context_management"].(map[string]any); ok {
			if edits, ok := cm["edits"].([]any); ok {
				filtered := make([]any, 0, len(edits))
				for _, edit := range edits {
					if editMap, ok := edit.(map[string]any); ok {
						if editMap["type"] == "clear_thinking_20251015" {
							continue
						}
					}
					filtered = append(filtered, edit)
				}
				if len(filtered) != len(edits) {
					if len(filtered) == 0 {
						delete(cm, "edits")
					} else {
						cm["edits"] = filtered
					}
				}
			}
		}
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		return body
	}

	newMessages := make([]any, 0, len(messages))

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			newMessages = append(newMessages, msg)
			continue
		}

		role, _ := msgMap["role"].(string)
		content, ok := msgMap["content"].([]any)
		if !ok {
			newMessages = append(newMessages, msg)
			continue
		}

		newContent := make([]any, 0, len(content))
		modifiedThisMsg := false

		for _, block := range content {
			blockMap, ok := block.(map[string]any)
			if !ok {
				newContent = append(newContent, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)
			switch blockType {
			case "thinking":
				modifiedThisMsg = true
				thinkingText, _ := blockMap["thinking"].(string)
				if thinkingText == "" {
					continue
				}
				newContent = append(newContent, map[string]any{"type": "text", "text": thinkingText})
				continue
			case "redacted_thinking":
				modifiedThisMsg = true
				continue
			case "tool_use":
				modifiedThisMsg = true
				name, _ := blockMap["name"].(string)
				id, _ := blockMap["id"].(string)
				input := blockMap["input"]
				inputJSON, _ := json.Marshal(input)
				text := "(tool_use)"
				if name != "" {
					text += " name=" + name
				}
				if id != "" {
					text += " id=" + id
				}
				if len(inputJSON) > 0 && string(inputJSON) != "null" {
					text += " input=" + string(inputJSON)
				}
				newContent = append(newContent, map[string]any{"type": "text", "text": text})
				continue
			case "tool_result":
				modifiedThisMsg = true
				toolUseID, _ := blockMap["tool_use_id"].(string)
				isError, _ := blockMap["is_error"].(bool)
				content := blockMap["content"]
				contentJSON, _ := json.Marshal(content)
				text := "(tool_result)"
				if toolUseID != "" {
					text += " tool_use_id=" + toolUseID
				}
				if isError {
					text += " is_error=true"
				}
				if len(contentJSON) > 0 && string(contentJSON) != "null" {
					text += "\n" + string(contentJSON)
				}
				newContent = append(newContent, map[string]any{"type": "text", "text": text})
				continue
			}

			if blockType == "" {
				if rawThinking, hasThinking := blockMap["thinking"]; hasThinking {
					modifiedThisMsg = true
					switch v := rawThinking.(type) {
					case string:
						if v != "" {
							newContent = append(newContent, map[string]any{"type": "text", "text": v})
						}
					default:
						if b, err := json.Marshal(v); err == nil && len(b) > 0 {
							newContent = append(newContent, map[string]any{"type": "text", "text": string(b)})
						}
					}
					continue
				}
			}

			newContent = append(newContent, block)
		}

		if modifiedThisMsg {
			modified = true
			if len(newContent) == 0 {
				placeholder := "(content removed)"
				if role == "assistant" {
					placeholder = "(assistant content removed)"
				}
				newContent = append(newContent, map[string]any{"type": "text", "text": placeholder})
			}
			msgMap["content"] = newContent
		}

		newMessages = append(newMessages, msgMap)
	}

	if !modified {
		return body
	}

	req["messages"] = newMessages
	newBody, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return newBody
}

// FilterThinkingBlocksInternal removes invalid thinking blocks from request
// 策略：
//   - 当 thinking.type 不是 "enabled"/"adaptive"：移除所有 thinking 相关块
//   - 当 thinking.type 是 "enabled"/"adaptive"：仅移除缺失/无效 signature 的 thinking 块
func FilterThinkingBlocksInternal(body []byte, rejectedSignature string) []byte {
	// Fast path: if body doesn't contain "thinking", skip parsing
	if !bytes.Contains(body, []byte(`"type":"thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "thinking"`)) &&
		!bytes.Contains(body, []byte(`"type":"redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"type": "redacted_thinking"`)) &&
		!bytes.Contains(body, []byte(`"thinking":`)) &&
		!bytes.Contains(body, []byte(`"thinking" :`)) {
		return body
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	// Check if thinking is enabled
	thinkingEnabled := false
	if thinking, ok := req["thinking"].(map[string]any); ok {
		if thinkType, ok := thinking["type"].(string); ok && (thinkType == "enabled" || thinkType == "adaptive") {
			thinkingEnabled = true
		}
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		return body
	}

	filtered := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		content, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}

		newContent := make([]any, 0, len(content))
		filteredThisMessage := false

		for _, block := range content {
			blockMap, ok := block.(map[string]any)
			if !ok {
				newContent = append(newContent, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)

			if blockType == "thinking" || blockType == "redacted_thinking" {
				// When thinking is enabled and this is an assistant message,
				// only keep thinking blocks with valid signatures
				if thinkingEnabled && role == "assistant" {
					signature, _ := blockMap["signature"].(string)
					if signature != "" && signature != rejectedSignature {
						newContent = append(newContent, block)
						continue
					}
				}
				filtered = true
				filteredThisMessage = true
				continue
			}

			// Handle blocks without type discriminator but with "thinking" key
			if blockType == "" {
				if _, hasThinking := blockMap["thinking"]; hasThinking {
					filtered = true
					filteredThisMessage = true
					continue
				}
			}

			newContent = append(newContent, block)
		}

		if filteredThisMessage {
			msgMap["content"] = newContent
		}
	}

	if !filtered {
		return body
	}

	newBody, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return newBody
}
