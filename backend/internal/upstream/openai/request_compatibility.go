// OpenAI 请求兼容只处理显式字节与值，不判断账号、路由或动态设置。
package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// SanitizeOpenAICrossModeFailoverReasoning 从 canonical 请求体派生跨模式重试请求，
// 完整删除带 provider 私有 encrypted_content 的 reasoning 项及其关联的 id/summary。
// 使用 UseNumber 保留请求中大整数的精确 JSON 表示，且不会修改传入的字节切片。
func SanitizeOpenAICrossModeFailoverReasoning(body []byte) (sanitized []byte, changed bool, err error) {
	if len(body) == 0 {
		return body, false, nil
	}
	if !gjson.GetBytes(body, "input").Exists() {
		return body, false, nil
	}

	var decoded map[string]any
	if err := wirejson.DecodeUseNumber(body, &decoded); err != nil {
		return body, false, fmt.Errorf("decode cross-mode failover body: %w", err)
	}
	if !DropOpenAIEncryptedReasoningInputItems(decoded) {
		return body, false, nil
	}
	out, marshalErr := wirejson.Marshal(decoded)
	if marshalErr != nil {
		return body, false, fmt.Errorf("serialize cross-mode failover body: %w", marshalErr)
	}
	return out, true, nil
}

// DropOpenAIEncryptedReasoningInputItems 删除带 encrypted_content 的 reasoning 项，
// 并返回请求体是否发生变化。没有加密字段的普通 reasoning 必须原样保留。
func DropOpenAIEncryptedReasoningInputItems(reqBody map[string]any) bool {
	if len(reqBody) == 0 {
		return false
	}
	inputValue, has := reqBody["input"]
	if !has {
		return false
	}

	switch input := inputValue.(type) {
	case []any:
		filtered := input[:0]
		changed := false
		for _, item := range input {
			if IsOpenAIEncryptedReasoningInputItem(item) {
				changed = true
				continue
			}
			filtered = append(filtered, item)
		}
		if !changed {
			return false
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
			return true
		}
		reqBody["input"] = filtered
		return true
	case []map[string]any:
		filtered := input[:0]
		changed := false
		for _, item := range input {
			if IsOpenAIEncryptedReasoningInputItem(item) {
				changed = true
				continue
			}
			filtered = append(filtered, item)
		}
		if !changed {
			return false
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
			return true
		}
		reqBody["input"] = filtered
		return true
	case map[string]any:
		if IsOpenAIEncryptedReasoningInputItem(input) {
			delete(reqBody, "input")
			return true
		}
		return false
	default:
		return false
	}
}

func IsOpenAIEncryptedReasoningInputItem(item any) bool {
	inputItem, ok := item.(map[string]any)
	if !ok {
		return false
	}
	itemType, _ := inputItem["type"].(string)
	if strings.TrimSpace(itemType) != "reasoning" {
		return false
	}
	_, has := inputItem["encrypted_content"]
	return has
}

func NormalizeOpenAICompactRequestBody(body []byte) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}
	normalized := []byte(`{}`)
	// Keep the current Codex /compact schema while still dropping request-scoped
	// fields such as prompt_cache_key, store, and stream.
	for _, field := range []string{
		"model",
		"input",
		"instructions",
		"tools",
		"parallel_tool_calls",
		"reasoning",
		"service_tier",
		"text",
		"previous_response_id",
	} {
		value := gjson.GetBytes(body, field)
		if !value.Exists() {
			continue
		}
		next, err := sjson.SetRawBytes(normalized, field, []byte(value.Raw))
		if err != nil {
			return body, false, fmt.Errorf("normalize compact body %s: %w", field, err)
		}
		normalized = next
	}
	if next, removed, err := NormalizeOpenAIParallelToolCallsWithoutTools(normalized, false); err != nil {
		return body, false, err
	} else if removed {
		normalized = next
	}

	if bytes.Equal(bytes.TrimSpace(body), bytes.TrimSpace(normalized)) {
		return body, false, nil
	}
	return normalized, true, nil
}

func NormalizeOpenAIParallelToolCallsWithoutTools(body []byte, responsesLite bool) ([]byte, bool, error) {
	if responsesLite {
		return body, false, nil
	}
	parallel := gjson.GetBytes(body, "parallel_tool_calls")
	if !parallel.Exists() {
		return body, false, nil
	}
	if OpenAIRequestBodyHasTools(body) {
		return body, false, nil
	}
	normalized, err := sjson.DeleteBytes(body, "parallel_tool_calls")
	if err != nil {
		return body, false, fmt.Errorf("normalize parallel_tool_calls without tools: %w", err)
	}
	return normalized, true, nil
}

// OpenAIRequestBodyHasTools 同时识别顶层 tools 和 input[].additional_tools。
func OpenAIRequestBodyHasTools(body []byte) bool {
	if tools := gjson.GetBytes(body, "tools"); tools.IsArray() && len(tools.Array()) > 0 {
		return true
	}
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if strings.TrimSpace(item.Get("type").String()) != "additional_tools" {
			continue
		}
		if tools := item.Get("tools"); tools.IsArray() && len(tools.Array()) > 0 {
			return true
		}
	}
	return false
}

// NormalizeOpenAIResponsesReasoningContentReplay 在历史记录发送到真实 OpenAI Responses
// 端点前移除不可移植的 reasoning.content 数组。兼容供应商可能返回可见推理块，
// 而 OpenAI 在回放该项时只接受空数组。
//
// 保留 reasoning 项及其可移植字段（summary、encrypted_content、id 和不透明扩展字段）。
// 调用方仅对 OpenAI 目标启用此归一化，兼容供应商仍可消费自身的 content。
func NormalizeOpenAIResponsesReasoningContentReplay(body []byte) ([]byte, bool, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	needsNormalization := false
	input.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			return true
		}
		content := item.Get("content")
		if content.IsArray() && len(content.Array()) > 0 {
			needsNormalization = true
			return false
		}
		return true
	})
	if !needsNormalization {
		return body, false, nil
	}

	var reqBody map[string]any
	if err := wirejson.DecodeUseNumber(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("normalize OpenAI reasoning content replay: %w", err)
	}
	items, ok := reqBody["input"].([]any)
	if !ok {
		return body, false, nil
	}
	changed := false
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || strings.TrimSpace(FirstNonEmptyString(item["type"])) != "reasoning" {
			continue
		}
		content, ok := item["content"].([]any)
		if !ok || len(content) == 0 {
			continue
		}
		delete(item, "content")
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	normalized, err := wirejson.Marshal(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize normalized OpenAI reasoning content replay: %w", err)
	}
	return normalized, true, nil
}

func NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(body []byte, knownStoreFalse bool) ([]byte, bool, error) {
	if !knownStoreFalse && gjson.GetBytes(body, "store").Type != gjson.False {
		return body, false, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	var reqBody map[string]any
	if err := wirejson.DecodeUseNumber(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("normalize API-key store=false reasoning replay: %w", err)
	}
	items, ok := reqBody["input"].([]any)
	if !ok {
		return body, false, nil
	}
	filtered := make([]any, 0, len(items))
	changed := false
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			filtered = append(filtered, rawItem)
			continue
		}
		typ := strings.TrimSpace(FirstNonEmptyString(item["type"]))
		id := strings.TrimSpace(FirstNonEmptyString(item["id"]))
		switch typ {
		case "reasoning":
			encryptedContent, hasEncryptedContent := item["encrypted_content"].(string)
			if !hasEncryptedContent || strings.TrimSpace(encryptedContent) == "" {
				changed = true
				continue
			}
			if strings.HasPrefix(id, "rs_") {
				delete(item, "id")
				changed = true
			}
			if summary, ok := item["summary"]; !ok || summary == nil {
				item["summary"] = []any{}
				changed = true
			}
		case "item_reference":
			if strings.HasPrefix(id, "rs_") {
				changed = true
				continue
			}
		}
		if wire.ShouldStripNonPairCallID(typ) {
			if _, hasCallID := item["call_id"]; hasCallID {
				delete(item, "call_id")
				changed = true
			}
		}
		filtered = append(filtered, item)
	}
	if !changed {
		return body, false, nil
	}
	reqBody["input"] = filtered
	normalized, err := wirejson.Marshal(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize API-key store=false reasoning replay: %w", err)
	}
	return normalized, true, nil
}

func SetOpenAIRequestMapPath(reqBody map[string]any, path string, value any) {
	path = strings.TrimSpace(path)
	if reqBody == nil || path == "" {
		return
	}
	parts := strings.Split(path, ".")
	current := reqBody
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		next, _ := current[part].(map[string]any)
		if next == nil {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last != "" {
		current[last] = value
	}
}

func DeleteOpenAIRequestMapPath(reqBody map[string]any, path string) {
	path = strings.TrimSpace(path)
	if reqBody == nil || path == "" {
		return
	}
	parts := strings.Split(path, ".")
	current := reqBody
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		next, _ := current[part].(map[string]any)
		if next == nil {
			return
		}
		current = next
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last != "" {
		delete(current, last)
	}
}

func NormalizeOpenAIResponsesReasoningMode(body []byte) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}
	mode := gjson.GetBytes(body, "reasoning.mode")
	if !mode.Exists() || mode.Type != gjson.String {
		return body, false, nil
	}
	updated := body
	effort := gjson.GetBytes(body, "reasoning.effort")
	if (!effort.Exists() || effort.Type == gjson.Null || strings.TrimSpace(effort.String()) == "") &&
		strings.EqualFold(strings.TrimSpace(mode.String()), "pro") {
		var err error
		updated, err = sjson.SetBytes(updated, "reasoning.effort", "max")
		if err != nil {
			return body, false, fmt.Errorf("set reasoning effort for mode=pro: %w", err)
		}
	}
	updated, err := sjson.DeleteBytes(updated, "reasoning.mode")
	if err != nil {
		return body, false, fmt.Errorf("delete unsupported reasoning.mode: %w", err)
	}
	if reasoning := gjson.GetBytes(updated, "reasoning"); reasoning.Exists() && reasoning.IsObject() && len(reasoning.Map()) == 0 {
		updated, err = sjson.DeleteBytes(updated, "reasoning")
		if err != nil {
			return body, false, fmt.Errorf("delete empty reasoning object: %w", err)
		}
	}
	return updated, true, nil
}

func NormalizeOpenAIResponseFormatSchemasBody(body []byte) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}
	textFormat := strings.TrimSpace(gjson.GetBytes(body, "text.format.type").String())
	responseFormat := strings.TrimSpace(gjson.GetBytes(body, "response_format.type").String())
	if textFormat != "json_schema" && responseFormat != "json_schema" {
		return body, false, nil
	}
	var reqBody map[string]any
	if err := wirejson.DecodeUseNumber(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("normalize responses schema body: %w", err)
	}
	if !NormalizeOpenAIResponseFormatSchemas(reqBody) {
		return body, false, nil
	}
	normalized, err := json.Marshal(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize normalized responses schema body: %w", err)
	}
	return normalized, true, nil
}

// NormalizeOpenAIPassthroughOAuthBody 将透传 OAuth 请求体收敛为旧链路关键行为：
// 1) 删除 ChatGPT internal API 不支持的顶层 Responses 参数
// 2) store=false 3) 非 compact 保持 stream=true；compact 强制 stream=false
func NormalizeOpenAIPassthroughOAuthBody(body []byte, compact bool) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}

	normalized, changed, err := wire.NormalizeOpenAIOAuthResponsesCompatibilityBody(body)
	if err != nil {
		return body, false, err
	}
	if reasoningBody, reasoningChanged, reasoningErr := NormalizeOpenAIResponsesReasoningMode(normalized); reasoningErr != nil {
		return body, false, reasoningErr
	} else if reasoningChanged {
		normalized = reasoningBody
		changed = true
	}

	for _, field := range OpenAIChatGPTInternalUnsupportedFields {
		if value := gjson.GetBytes(normalized, field); !value.Exists() {
			continue
		}
		next, err := sjson.DeleteBytes(normalized, field)
		if err != nil {
			return body, false, fmt.Errorf("normalize passthrough body delete %s: %w", field, err)
		}
		normalized = next
		changed = true
	}
	if schemaBody, schemaChanged, schemaErr := NormalizeOpenAIResponseFormatSchemasBody(normalized); schemaErr != nil {
		return body, false, schemaErr
	} else if schemaChanged {
		normalized = schemaBody
		changed = true
	}

	// ChatGPT internal API 要求 input 必须是条目数组。
	if inputResult := gjson.GetBytes(normalized, "input"); inputResult.Exists() {
		switch {
		case inputResult.Type == gjson.String:
			text := inputResult.String()
			var inputValue any
			if strings.TrimSpace(text) != "" {
				inputValue = []any{map[string]any{
					"type": "message", "role": "user", "content": text,
				}}
			} else {
				inputValue = []any{}
			}
			next, err := sjson.SetBytes(normalized, "input", inputValue)
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body input string: %w", err)
			}
			normalized = next
			changed = true
		case inputResult.Type == gjson.JSON && !inputResult.IsArray():
			next, err := sjson.SetRawBytes(normalized, "input", []byte("["+inputResult.Raw+"]"))
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body input object: %w", err)
			}
			normalized = next
			changed = true
		}
	}

	if compact {
		if store := gjson.GetBytes(normalized, "store"); store.Exists() {
			next, err := sjson.DeleteBytes(normalized, "store")
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body delete store: %w", err)
			}
			normalized = next
			changed = true
		}
		if stream := gjson.GetBytes(normalized, "stream"); stream.Exists() {
			next, err := sjson.DeleteBytes(normalized, "stream")
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body delete stream: %w", err)
			}
			normalized = next
			changed = true
		}
	} else {
		if store := gjson.GetBytes(normalized, "store"); !store.Exists() || store.Type != gjson.False {
			next, err := sjson.SetBytes(normalized, "store", false)
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body store=false: %w", err)
			}
			normalized = next
			changed = true
		}
		if stream := gjson.GetBytes(normalized, "stream"); !stream.Exists() || stream.Type != gjson.True {
			next, err := sjson.SetBytes(normalized, "stream", true)
			if err != nil {
				return body, false, fmt.Errorf("normalize passthrough body stream=true: %w", err)
			}
			normalized = next
			changed = true
		}
	}

	return normalized, changed, nil
}

func DetectOpenAIPassthroughInstructionsRejectReason(reqModel string, body []byte) string {
	if !IsOpenAICodexModel(reqModel) {
		return ""
	}

	instructions := gjson.GetBytes(body, "instructions")
	if !instructions.Exists() {
		return ""
	}
	if instructions.Type != gjson.String {
		return "instructions_not_string"
	}
	if strings.TrimSpace(instructions.String()) == "" {
		return "instructions_empty"
	}
	return ""
}

// IsOpenAICodexModel 判断请求模型是否属于需要 Codex 指令语义的模型族。
func IsOpenAICodexModel(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "codex")
}

func OpenAIRequestBodyMayContainImageInput(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	input := gjson.GetBytes(body, "input")
	messages := gjson.GetBytes(body, "messages.#-1")
	return wire.JSONValueMayContainImageInput(input) || wire.JSONValueMayContainImageInput(messages)
}

func OpenAIRequestBodyMayContainEmptyBase64InputImage(body []byte) bool {
	if len(body) == 0 || !OpenAIRequestBodyMayContainInputImageToken(body) {
		return false
	}
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return false
	}
	return OpenAIJSONValueMayContainEmptyBase64InputImage(input)
}

func OpenAIRequestBodyMayContainInputImageToken(body []byte) bool {
	if bytes.Contains(body, []byte("input_image")) {
		return true
	}
	// JSON 字符串任意字符都可能被 unicode escape，遇到 \u 时交给 gjson 解码后的结构扫描兜底。
	return bytes.Contains(body, []byte("\\u"))
}

func OpenAIJSONValueMayContainEmptyBase64InputImage(value gjson.Result) bool {
	if !value.Exists() {
		return false
	}
	if value.IsArray() {
		found := false
		value.ForEach(func(_, item gjson.Result) bool {
			if OpenAIJSONValueMayContainEmptyBase64InputImage(item) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	if value.IsObject() {
		if strings.TrimSpace(value.Get("type").String()) == "input_image" && wire.IsEmptyBase64DataURI(value.Get("image_url").String()) {
			return true
		}
		return OpenAIJSONValueMayContainEmptyBase64InputImage(value.Get("content"))
	}
	return false
}

func SanitizeEmptyBase64InputImagesInOpenAIBody(body []byte) ([]byte, bool, error) {
	if !OpenAIRequestBodyMayContainEmptyBase64InputImage(body) {
		return body, false, nil
	}

	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("sanitize request body: %w", err)
	}
	if !SanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(reqBody) {
		return body, false, nil
	}
	normalized, err := wirejson.Marshal(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("serialize sanitized request body: %w", err)
	}
	return normalized, true, nil
}

func SanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(reqBody map[string]any) bool {
	if reqBody == nil {
		return false
	}
	input, ok := reqBody["input"]
	if !ok {
		return false
	}
	normalizedInput, changed := SanitizeEmptyBase64InputImagesInOpenAIInput(input)
	if !changed {
		return false
	}
	reqBody["input"] = normalizedInput
	return true
}

func SanitizeEmptyBase64InputImagesInOpenAIInput(input any) (any, bool) {
	items, ok := input.([]any)
	if !ok {
		return input, false
	}

	normalizedItems := make([]any, 0, len(items))
	changed := false
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			normalizedItems = append(normalizedItems, item)
			continue
		}
		if ShouldDropEmptyBase64InputImagePart(itemMap) {
			changed = true
			continue
		}
		content, ok := itemMap["content"]
		if !ok {
			normalizedItems = append(normalizedItems, itemMap)
			continue
		}
		parts, ok := content.([]any)
		if !ok {
			normalizedItems = append(normalizedItems, itemMap)
			continue
		}

		normalizedParts := make([]any, 0, len(parts))
		itemChanged := false
		for _, part := range parts {
			if ShouldDropEmptyBase64InputImagePart(part) {
				changed = true
				itemChanged = true
				continue
			}
			normalizedParts = append(normalizedParts, part)
		}
		if itemChanged {
			if len(normalizedParts) == 0 {
				continue
			}
			itemMap["content"] = normalizedParts
		}
		normalizedItems = append(normalizedItems, itemMap)
	}
	if !changed {
		return input, false
	}
	return normalizedItems, true
}

func ShouldDropEmptyBase64InputImagePart(part any) bool {
	partMap, ok := part.(map[string]any)
	if !ok {
		return false
	}
	typeValue, _ := partMap["type"].(string)
	if strings.TrimSpace(typeValue) != "input_image" {
		return false
	}
	imageURL, _ := partMap["image_url"].(string)
	return wire.IsEmptyBase64DataURI(imageURL)
}
