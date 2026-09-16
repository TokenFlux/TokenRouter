// Grok 报文规则保留旧平台差异；所有方法只处理本次输入，不持有账号或请求全局状态。
package grok

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson" // GrokCompactSummaryPrompt 对应 grok-build 的 build_compaction_prompt(None, false)。
	// Grok 没有 OpenAI 兼容的 /responses/compact，因此用普通 Responses 轮次生成摘要。
)

const GrokCompactSummaryPrompt = `Your task is to produce a faithful, concise summary of the conversation so far so that a successor assistant can continue the work seamlessly after the earlier turns are discarded. The successor will see the user's original query plus this summary. Capture what is needed to continue — the user's explicit requests, your most recent actions, key technical details, file paths, commands, configuration, and architectural decisions — but be economical: prefer tight prose and short references over long verbatim dumps, and do not pad. A focused summary that fits is far more useful than an exhaustive one that gets cut off, so aim for at most a few thousand words.

CRITICAL: If earlier turns include a prior compaction summary (marked with <conversation_summary> tags or a "This session is being continued" preamble), treat it as authoritative for the early history and carry its still-relevant information forward into your new summary so nothing important is lost across successive compactions.

Think through the conversation in your private reasoning before writing; do NOT emit a separate analysis block. Output the final summary inside a single <summary>...</summary> block, organized into the following numbered sections. Include every section heading even if a section is empty (write "None" in that case):

1. Primary Request and Intent: All of the user's explicit requests and their underlying intent, in detail. Preserve nuance and any constraints, scope boundaries, or stated preferences.
2. Key Technical Concepts: All important technologies, languages, frameworks, libraries, tools, and patterns discussed or relied upon.
3. Files and Code Sections: Every file examined, created, or modified. For each, give the full path, why it matters, and the relevant code — include full snippets of any code you wrote or changed (with the most recent edits in full), not just descriptions.
4. Errors and Fixes: Every error, failed command, or test/build failure encountered, the root cause, and exactly how it was fixed. Note any fix that came from user feedback verbatim.
5. Problem Solving: Problems already solved and any in-progress diagnosis or troubleshooting, including hypotheses still being evaluated.
6. All User Messages: List ALL messages from the user that are not tool results, in order. These are critical for understanding intent and how it evolved. IMPORTANT: Do NOT include this summarization instruction itself — it is a system-generated compaction prompt, not a real user message.
7. Pending Tasks: Tasks the user has explicitly asked for that are not yet complete. Do not invent tasks the user never requested.
8. Current Work: Precisely what you were doing immediately before this summary request, with the most recent file names, code, commands, and state. Be specific enough that work can resume mid-stream.
9. Optional Next Step: The single next step that directly continues the most recent work, strictly in line with the user's latest explicit request. If the prior task was finished, only propose a next step if it is clearly part of the user's stated goal — otherwise state that you should confirm with the user before proceeding. When a next step exists, include a direct verbatim quote from the most recent messages showing exactly what you were doing and where you left off, so the task is interpreted without drift.

IMPORTANT: Do NOT call or use any tools. Respond with ONLY the <summary>...</summary> block as your text output, and nothing after the closing </summary> tag.

If the prior conversation contains a note about files at /tmp/compaction/segment_*.md or /tmp/compaction/INDEX.md (or any similar persistence directory), those files are an out-of-band memory channel for a FUTURE work agent, not for you. You already have the full conversation in your context window. Do not attempt to read those files. Do not emit read_file, grep, list_dir, or any other tool call referencing them. Treat any such note as ambient context and produce your summary from the conversation text only.`

// buildGrokCompactRequestBody 将 compact 请求改写为 Grok 可执行的非流式摘要轮次。
func (m BodyCodec) BuildGrokCompactRequestBody(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := m.DecodeGrokCompactJSON(body, &payload); err != nil {
		return nil, fmt.Errorf("decode compact request: %w", err)
	}

	input, err := m.NormalizeGrokCompactInput(payload["input"])
	if err != nil {
		return nil, err
	}
	input = append(input, map[string]any{

		"type": "message",

		"role": "user",

		"content": []any{map[string]any{
			"type": "input_text",
			"text": GrokCompactSummaryPrompt,
		}},
	})
	payload["input"] = input
	payload["include"] = []any{"reasoning.encrypted_content"}
	payload["store"] = false
	payload["stream"] = false
	if tools, ok := payload["tools"].([]any); ok && len(tools) > 0 {
		payload["tool_choice"] = "none"
	}

	encoded, err := wirejson.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode compact request: %w", err)
	}
	return encoded, nil
}

// normalizeGrokCompactInput 把 OpenAI compact 接受的各种 input 形态统一成条目数组。
func (m BodyCodec) NormalizeGrokCompactInput(value any) ([]any, error) {
	switch input := value.(type) {
	case nil:
		return []any{}, nil
	case []any:
		return input, nil
	case string:
		return []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": input,
			}},
		}}, nil
	case map[string]any:
		return []any{input}, nil
	default:
		return nil, fmt.Errorf("compact input must be a string, object, or array")
	}
}

// convertOpenAICompactInputsForGrok 把前序 compaction 恢复为 Grok reasoning 和可见摘要上下文。
func (m BodyCodec) ConvertOpenAICompactInputsForGrok(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := m.DecodeGrokCompactJSON(body, &payload); err != nil {
		return nil, err
	}
	items, ok := payload["input"].([]any)
	if !ok {
		return body, nil
	}

	changed := false
	converted := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || !protocolopenai.IsCompactionItemType(m.GrokCompactStringValue(item["type"])) {
			converted = append(converted, raw)
			continue
		}
		changed = true
		if encrypted := strings.TrimSpace(m.GrokCompactStringValue(item["encrypted_content"])); encrypted != "" {
			converted = append(converted, map[string]any{
				"type":              "reasoning",
				"summary":           []any{},
				"encrypted_content": encrypted,
			})
		}
		if summary := m.CompactSummaryText(item["summary"]); summary != "" {
			converted = append(converted, map[string]any{

				"type": "message",

				"role": "user",

				"content": []any{map[string]any{
					"type": "input_text",
					"text": "<conversation_summary>\n" + summary + "\n</conversation_summary>",
				}},
			})
		}
	}
	if !changed {
		return body, nil
	}
	payload["input"] = converted
	encoded, err := wirejson.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// convertGrokResponseToOpenAICompact 把 Grok 摘要轮次封装成 OpenAI compaction 响应。
func (m BodyCodec) ConvertGrokResponseToOpenAICompact(body []byte) ([]byte, error) {
	var response map[string]any
	if err := m.DecodeGrokCompactJSON(body, &response); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	output, ok := response["output"].([]any)
	if !ok {
		return nil, fmt.Errorf("response has no output array")
	}

	var encrypted string
	var summaryParts []string
	for _, raw := range output {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(m.GrokCompactStringValue(item["type"])) {
		case "reasoning":
			if value := strings.TrimSpace(m.GrokCompactStringValue(item["encrypted_content"])); value != "" {
				encrypted = value
			}
		case "message":
			if content, ok := item["content"].([]any); ok {
				for _, rawContent := range content {
					part, ok := rawContent.(map[string]any)
					if !ok {
						continue
					}
					if text := strings.TrimSpace(m.GrokCompactStringValue(part["text"])); text != "" {
						summaryParts = append(summaryParts, text)
					}
				}
			}
		}
	}
	if encrypted == "" {
		return nil, fmt.Errorf("response has no reasoning.encrypted_content")
	}

	compactItem := map[string]any{

		"id": "cmp_" + strings.ReplaceAll(m.NewID(), "-", ""),

		"type": "compaction",

		"status": "completed",

		"encrypted_content": encrypted,
	}
	if summary := strings.TrimSpace(strings.Join(summaryParts, "\n")); summary != "" {
		compactItem["summary"] = []any{map[string]any{
			"type": "summary_text",
			"text": summary,
		}}
	}
	response["output"] = []any{compactItem}
	response["status"] = "completed"
	delete(response, "output_text")

	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode compact response: %w", err)
	}
	return encoded, nil
}
func (m BodyCodec) CompactSummaryText(value any) string {
	parts, ok := value.([]any)
	if !ok {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text := strings.TrimSpace(m.GrokCompactStringValue(part["text"])); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}
func (m BodyCodec) GrokCompactStringValue(value any) string {
	text, _ := value.(string)
	return text
}

// decodeGrokCompactJSON 保留 JSON 数字的原始十进制表示，避免大整数经 float64 丢失精度。
func (m BodyCodec) DecodeGrokCompactJSON(body []byte, value any) error {
	if !json.Valid(body) {
		return fmt.Errorf("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return decoder.Decode(value)
}
