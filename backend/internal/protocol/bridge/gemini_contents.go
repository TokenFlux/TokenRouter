package bridge

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BuildContents 构建 contents
func BuildContents(messages []ClaudeMessage, toolIDToName map[string]string, isThinkingEnabled, allowDummyThought bool) ([]GeminiContent, []GeminiPart, bool, error) {
	var contents []GeminiContent
	var systemParts []GeminiPart
	strippedThinking := false

	for i, msg := range messages {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}

		parts, strippedThisMsg, err := BuildParts(msg.Content, toolIDToName, allowDummyThought)
		if err != nil {
			return nil, nil, false, fmt.Errorf("build parts for message %d: %w", i, err)
		}
		if strippedThisMsg {
			strippedThinking = true
		}

		// Claude Code 可能把 system prompt 放进 messages，需并入 Gemini systemInstruction。
		if role == "system" {
			systemParts = append(systemParts, parts...)
			continue
		}

		// 只有 Gemini 模型支持 dummy thinking block workaround
		// 只对最后一条 assistant 消息添加（Pre-fill 场景）
		// 历史 assistant 消息不能添加没有 signature 的 dummy thinking block
		if allowDummyThought && role == "model" && isThinkingEnabled && i == len(messages)-1 {
			hasThoughtPart := false
			for _, p := range parts {
				if p.Thought {
					hasThoughtPart = true
					break
				}
			}
			if !hasThoughtPart && len(parts) > 0 {
				// 在开头添加 dummy thinking block
				parts = append([]GeminiPart{{
					Text:             "Thinking...",
					Thought:          true,
					ThoughtSignature: DummyThoughtSignature,
				}}, parts...)
			}
		}

		if len(parts) == 0 {
			continue
		}

		contents = append(contents, GeminiContent{
			Role:  role,
			Parts: parts,
		})
	}

	return contents, systemParts, strippedThinking, nil
}

// DummyThoughtSignature 用于跳过 Gemini 3 thought_signature 验证
// 参考: https://ai.google.dev/gemini-api/docs/thought-signatures
// 导出供跨包使用（如 gemini_native_signature_cleaner 跨账号修复）
const DummyThoughtSignature = "skip_thought_signature_validator"

// BuildParts 构建消息的 parts
// allowDummyThought: 只有 Gemini 模型支持 dummy thought signature
func BuildParts(content json.RawMessage, toolIDToName map[string]string, allowDummyThought bool) ([]GeminiPart, bool, error) {
	var parts []GeminiPart
	strippedThinking := false

	// 尝试解析为字符串
	var textContent string
	if err := json.Unmarshal(content, &textContent); err == nil {
		if textContent != "(no content)" && strings.TrimSpace(textContent) != "" {
			parts = append(parts, GeminiPart{Text: strings.TrimSpace(textContent)})
		}
		return parts, false, nil
	}

	// 解析为内容块数组
	var blocks []ContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, false, fmt.Errorf("parse content blocks: %w", err)
	}

	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "(no content)" && strings.TrimSpace(block.Text) != "" {
				parts = append(parts, GeminiPart{Text: block.Text})
			}

		case "thinking":
			part := GeminiPart{
				Text:    block.Thinking,
				Thought: true,
			}
			// signature 处理：
			// - Claude 模型（allowDummyThought=false）：必须是上游返回的真实 signature（dummy 视为缺失）
			// - Gemini 模型（allowDummyThought=true）：优先透传真实 signature，缺失时使用 dummy signature
			if block.Signature != "" && (allowDummyThought || block.Signature != DummyThoughtSignature) {
				part.ThoughtSignature = block.Signature
			} else if !allowDummyThought {
				// Claude 模型需要有效 signature；在缺失时降级为普通文本，并在上层禁用 thinking mode。
				if strings.TrimSpace(block.Thinking) != "" {
					parts = append(parts, GeminiPart{Text: block.Thinking})
				}
				strippedThinking = true
				continue
			} else {
				// Gemini 模型使用 dummy signature
				part.ThoughtSignature = DummyThoughtSignature
			}
			parts = append(parts, part)

		case "image":
			if block.Source != nil && block.Source.Type == "base64" {
				parts = append(parts, GeminiPart{
					InlineData: &GeminiInlineData{
						MimeType: block.Source.MediaType,
						Data:     block.Source.Data,
					},
				})
			}

		case "tool_use":
			// 存储 id -> name 映射
			if block.ID != "" && block.Name != "" {
				toolIDToName[block.ID] = block.Name
			}

			part := GeminiPart{
				FunctionCall: &GeminiFunctionCall{
					Name: block.Name,
					Args: block.Input,
					ID:   block.ID,
				},
			}
			// tool_use 的 signature 处理：
			// - Claude 模型（allowDummyThought=false）：必须是上游返回的真实 signature（dummy 视为缺失）
			// - Gemini 模型（allowDummyThought=true）：优先透传真实 signature，缺失时使用 dummy signature
			if block.Signature != "" && (allowDummyThought || block.Signature != DummyThoughtSignature) {
				part.ThoughtSignature = block.Signature
			} else if allowDummyThought {
				part.ThoughtSignature = DummyThoughtSignature
			}
			parts = append(parts, part)

		case "tool_result":
			// 获取函数名
			funcName := block.Name
			if funcName == "" {
				if name, ok := toolIDToName[block.ToolUseID]; ok {
					funcName = name
				} else {
					funcName = block.ToolUseID
				}
			}

			// 解析 content
			resultContent := ParseToolResultContent(block.Content, block.IsError)

			parts = append(parts, GeminiPart{
				FunctionResponse: &GeminiFunctionResponse{
					Name: funcName,
					Response: map[string]any{
						"result": resultContent,
					},
					ID: block.ToolUseID,
				},
			})
		}
	}

	return parts, strippedThinking, nil
}

// ParseToolResultContent 解析 tool_result 的 content
func ParseToolResultContent(content json.RawMessage, isError bool) string {
	if len(content) == 0 {
		if isError {
			return "Tool execution failed with no output."
		}
		return "Command executed successfully."
	}

	// 尝试解析为字符串
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		if strings.TrimSpace(str) == "" {
			if isError {
				return "Tool execution failed with no output."
			}
			return "Command executed successfully."
		}
		return str
	}

	// 尝试解析为数组
	var arr []map[string]any
	if err := json.Unmarshal(content, &arr); err == nil {
		var texts []string
		for _, item := range arr {
			if text, ok := item["text"].(string); ok {
				texts = append(texts, text)
			}
		}
		result := strings.Join(texts, "\n")
		if strings.TrimSpace(result) == "" {
			if isError {
				return "Tool execution failed with no output."
			}
			return "Command executed successfully."
		}
		return result
	}

	// 返回原始 JSON
	return string(content)
}
