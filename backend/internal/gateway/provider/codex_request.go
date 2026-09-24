// Codex 请求选项与 Messages 桥接提示在本次尝试构造，协议算法由 upstream 唯一执行。
package provider

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/tidwall/gjson"
)

func ApplyCodexOAuthTransform(reqBody map[string]any, isCodexCLI bool, isCompact bool) openai.CodexTransformResult {
	return ApplyCodexOAuthTransformWithOptions(reqBody, openai.CodexOAuthTransformOptions{IsCodexCLI: isCodexCLI, IsCompact: isCompact})
}

func ApplyCodexOAuthTransformWithOptions(reqBody map[string]any, opts openai.CodexOAuthTransformOptions) openai.CodexTransformResult {
	opts.ModelRules = CodexModelRules()
	opts.IsMessagesBridge = IsOpenAICompatMessagesBridgeRequestBody
	return openai.ApplyCodexOAuthTransformWithOptions(reqBody, opts)
}

func IsCodexSparkModel(model string) bool {
	return openai.IsCodexSparkModel(model, CodexModelRules())
}

func StripOpenAIImageGenerationToolsFromRawPayload(payload []byte) ([]byte, bool, error) {
	return openai.StripOpenAIImageGenerationToolsFromRawPayload(payload, ImageIntent().OpenAIRequestBodyHasImageGenerationDeclaration(payload))
}

func ValidateCodexSparkInput(reqBody map[string]any, model string) error {
	return openai.ValidateCodexSparkInput(reqBody, model, IsCodexSparkModel(model))
}

func EnsureOpenAIResponsesImageGenerationTool(reqBody map[string]any) bool {
	return openai.EnsureOpenAIResponsesImageGenerationTool(reqBody, IsCodexSparkModel(openai.FirstNonEmptyString(reqBody["model"])))
}

func EnsureOpenAIResponsesImageGenerationToolChoiceAuto(reqBody map[string]any) bool {
	return openai.EnsureOpenAIResponsesImageGenerationToolChoiceAuto(reqBody, IsCodexSparkModel(openai.FirstNonEmptyString(reqBody["model"])))
}

func ApplyCodexImageGenerationBridgeInstructions(reqBody map[string]any) bool {
	return openai.ApplyCodexImageGenerationBridgeInstructions(reqBody, IsCodexSparkModel(openai.FirstNonEmptyString(reqBody["model"])))
}

func ValidateOpenAIResponsesImageModel(reqBody map[string]any, model string) error {
	return openai.ValidateOpenAIResponsesImageModel(reqBody, model, media.IsImageGenerationModel(strings.TrimSpace(model)))
}

func NormalizeOpenAIResponsesImageOnlyModel(reqBody map[string]any) bool {
	return openai.NormalizeOpenAIResponsesImageOnlyModel(reqBody, media.IsImageGenerationModel(openai.FirstNonEmptyString(reqBody["model"])))
}

func ApplyCodexClientMetadata(reqBody map[string]any, account *ExecutionAccount) bool {
	if account == nil {
		return false
	}
	return openai.ApplyCodexClientMetadata(reqBody, account.View().GetOpenAIDeviceID())
}

func IsOpenAICompatMessagesBridgeBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if bytes.Contains(body, []byte(OpenAICompatClaudeCodeTodoGuardMarker)) {
		return true
	}
	return IsOpenAICompatMessagesBridgePromptCacheKey(gjson.GetBytes(body, "prompt_cache_key").String())
}

func IsOpenAICompatMessagesBridgeRequestBody(reqBody map[string]any) bool {
	if reqBody == nil {
		return false
	}
	if input, ok := reqBody["input"].([]any); ok && InputContainsText(input, OpenAICompatClaudeCodeTodoGuardMarker) {
		return true
	}
	return IsOpenAICompatMessagesBridgePromptCacheKey(openai.FirstNonEmptyString(reqBody["prompt_cache_key"]))
}

func IsOpenAICompatMessagesBridgePromptCacheKey(key string) bool {
	key = strings.TrimSpace(key)
	return strings.HasPrefix(key, "anthropic-metadata-") ||
		strings.HasPrefix(key, "anthropic-cache-") ||
		strings.HasPrefix(key, "anthropic-digest-")
}

const (
	OpenAICompatClaudeCodeTodoGuardMarker = "<sub2api-claude-code-todo-guard>"
	OpenAICompatClaudeCodeTodoGuardText   = OpenAICompatClaudeCodeTodoGuardMarker + "\nWhen using Claude Code todo or task tracking tools, keep the visible task list consistent. Do not send final or summary text while any item remains in_progress. Before finishing, asking the user to choose, or reporting a blocker, update the todo list so completed work is completed and deferred work is pending/open; leave an item in_progress only when active work will continue in the same turn.\n</sub2api-claude-code-todo-guard>"
)

func AppendOpenAICompatClaudeCodeTodoGuard(req *protocolopenai.ResponsesRequest) bool {
	if req == nil || len(req.Input) == 0 {
		return false
	}

	var items []protocolopenai.ResponsesInputItem
	if err := json.Unmarshal(req.Input, &items); err != nil {
		return false
	}
	if len(items) == 0 || ResponsesInputItemsContainText(items, OpenAICompatClaudeCodeTodoGuardMarker) {
		return false
	}

	content, err := json.Marshal([]protocolopenai.ResponsesContentPart{{
		Type: "input_text",
		Text: OpenAICompatClaudeCodeTodoGuardText,
	}})
	if err != nil {
		return false
	}

	guard := protocolopenai.ResponsesInputItem{
		Type:    "message",
		Role:    "developer",
		Content: content,
	}

	insertAt := 0
	for insertAt < len(items) && items[insertAt].Type == "message" && items[insertAt].Role == "developer" {
		insertAt++
	}

	items = append(items, protocolopenai.ResponsesInputItem{})
	copy(items[insertAt+1:], items[insertAt:])
	items[insertAt] = guard

	input, err := json.Marshal(items)
	if err != nil {
		return false
	}
	req.Input = input
	return true
}

func AppendOpenAICompatClaudeCodeTodoGuardToRequestBody(reqBody map[string]any) bool {
	if reqBody == nil {
		return false
	}

	input, ok := reqBody["input"].([]any)
	if !ok || len(input) == 0 || InputContainsText(input, OpenAICompatClaudeCodeTodoGuardMarker) {
		return false
	}

	guard := map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{
				"type": "input_text",
				"text": OpenAICompatClaudeCodeTodoGuardText,
			},
		},
	}

	insertAt := 0
	for insertAt < len(input) {
		item, ok := input[insertAt].(map[string]any)
		if !ok || strings.TrimSpace(openai.FirstNonEmptyString(item["type"])) != "message" || strings.TrimSpace(openai.FirstNonEmptyString(item["role"])) != "developer" {
			break
		}
		insertAt++
	}

	input = append(input, nil)
	copy(input[insertAt+1:], input[insertAt:])
	input[insertAt] = guard
	reqBody["input"] = input
	return true
}

func ResponsesInputItemsContainText(items []protocolopenai.ResponsesInputItem, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, item := range items {
		if strings.Contains(string(item.Content), needle) {
			return true
		}
	}
	return false
}

func InputContainsText(input []any, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, item := range input {
		b, err := json.Marshal(item)
		if err == nil && strings.Contains(string(b), needle) {
			return true
		}
	}
	return false
}
