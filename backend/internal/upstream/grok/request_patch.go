// Grok 报文规则保留旧平台差异；所有方法只处理本次输入，不持有账号或请求全局状态。
package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func (m BodyCodec) IsGrokInvalidEncryptedContentResponse(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusUnprocessableEntity {
		return false
	}

	// xAI 同时使用过扁平和嵌套两种错误封装：
	// 扁平形式：{"code":"invalid-argument","error":"Could not decrypt the provided encrypted_content."}
	// 嵌套形式：{"error":{"message":"Could not decrypt the provided encrypted_content."}}
	code := strings.TrimSpace(gjson.GetBytes(body, "code").String())
	errNode := gjson.GetBytes(body, "error")
	if code == "" && errNode.IsObject() {
		code = strings.TrimSpace(errNode.Get("code").String())
	}

	if strings.EqualFold(code, "invalid_encrypted_content") || strings.EqualFold(code, "invalid_compaction") || strings.EqualFold(code, "compaction_decode_error") {
		return true
	}
	// 保留 xAI 官方扁平错误码校验，避免重试无关的 400 响应。
	if !strings.EqualFold(code, "invalid-argument") && code != "" {
		return false
	}
	for _, candidate := range GrokStructuredErrorMessageCandidates(body) {
		normalizedMessage := strings.ToLower(candidate)
		// Nested OpenAI-style envelopes may omit top-level code; require decrypt text.
		if code == "" && !strings.Contains(normalizedMessage, "decrypt") {
			continue
		}
		if strings.Contains(normalizedMessage, "encrypted_content") &&
			(strings.Contains(normalizedMessage, "decrypt") || strings.Contains(normalizedMessage, "unmodified")) {
			return true
		}
	}
	return false
}
func (m BodyCodec) IsGrokCompactionReplayDecodeError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest || len(body) == 0 {
		return false
	}
	for _, candidate := range GrokStructuredErrorMessageCandidates(body) {
		message := strings.ToLower(candidate)
		decodeSignal := strings.Contains(message, "decode") ||
			strings.Contains(message, "deserialize") ||
			strings.Contains(message, "decoder")
		replaySignal := strings.Contains(message, "compaction") ||
			strings.Contains(message, "summary") ||
			strings.Contains(message, "encrypted_content") ||
			strings.Contains(message, "response history")
		if decodeSignal && replaySignal {
			return true
		}
	}
	return false
}
func (m BodyCodec) SanitizeGrokCompactionReplayBody(body []byte) ([]byte, bool, error) {
	converted, err := m.ConvertOpenAICompactInputsForGrok(body)
	if err != nil {
		return nil, false, fmt.Errorf("convert Grok compaction replay: %w", err)
	}
	var requestBody map[string]any
	decoder := json.NewDecoder(bytes.NewReader(converted))
	decoder.UseNumber()
	if err := decoder.Decode(&requestBody); err != nil {
		return nil, false, err
	}

	changed := !bytes.Equal(converted, body)
	if protocolopenai.TrimEncryptedReasoningItems(requestBody) {
		changed = true
	}
	if m.DropEmptyGrokReplayReasoning(requestBody) {
		changed = true
	}
	if previousID, _ := requestBody["previous_response_id"].(string); strings.TrimSpace(previousID) != "" && !protocolopenai.HasFunctionCallOutput(requestBody) {
		delete(requestBody, "previous_response_id")
		if _, exists := requestBody["store"]; !exists {
			requestBody["store"] = false
		}
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	retryBody, err := wirejson.Marshal(requestBody)
	if err != nil {
		return nil, false, err
	}
	return retryBody, true, nil
}
func (m BodyCodec) DropEmptyGrokReplayReasoning(requestBody map[string]any) bool {
	items, ok := requestBody["input"].([]any)
	if !ok {
		return false
	}
	filtered := items[:0]
	changed := false
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || strings.TrimSpace(m.GrokStringValue(item["type"])) != "reasoning" {
			filtered = append(filtered, rawItem)
			continue
		}
		summary, _ := item["summary"].([]any)
		content, hasContent := item["content"]
		_, hasEncrypted := item["encrypted_content"]
		if hasEncrypted || len(summary) > 0 || (hasContent && content != nil) {
			filtered = append(filtered, rawItem)
			continue
		}
		changed = true
	}
	if changed {
		requestBody["input"] = filtered
	}
	return changed
}

// requestHasGrokEncryptedReasoning 判断出站 Responses 请求体是否仍包含可在重试前
// 剥离的 reasoning.encrypted_content。
func (m BodyCodec) RequestHasGrokEncryptedReasoning(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return false
	}
	items := input.Array()
	if input.IsObject() {
		items = []gjson.Result{input}
	}
	for _, item := range items {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			continue
		}
		enc := item.Get("encrypted_content")
		if enc.Exists() && enc.Type != gjson.Null && strings.TrimSpace(enc.String()) != "" {
			return true
		}
	}
	return false
}

type GrokEncryptedContentStripRetriedKey struct{}

func (m BodyCodec) MarkGrokEncryptedContentStripRetried(ctx context.Context) context.Context {
	return context.WithValue(ctx, GrokEncryptedContentStripRetriedKey{}, true)
}
func (m BodyCodec) GrokEncryptedContentStripRetried(ctx context.Context) bool {
	v, _ := ctx.Value(GrokEncryptedContentStripRetriedKey{}).(bool)
	return v
}
func (m BodyCodec) TrimGrokInvalidEncryptedContentRetryBody(body []byte) ([]byte, bool, error) {
	input := gjson.GetBytes(body, "input")
	items := input.Array()
	if input.IsObject() {
		items = []gjson.Result{input}
	}

	hasEncryptedReasoning := false
	for _, item := range items {
		if (strings.TrimSpace(item.Get("type").String()) == "reasoning" && item.Get("encrypted_content").Exists()) ||
			(protocolopenai.IsCompactionItemType(strings.TrimSpace(item.Get("type").String())) && item.Get("encrypted_content").Exists()) {
			hasEncryptedReasoning = true
			break
		}
	}
	if !hasEncryptedReasoning {
		return body, false, nil
	}

	var requestBody map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&requestBody); err != nil {
		return nil, false, err
	}
	if !protocolopenai.TrimEncryptedReasoningItems(requestBody) {
		return body, false, nil
	}

	retryBody, err := wirejson.Marshal(requestBody)
	if err != nil {
		return nil, false, err
	}
	return retryBody, true, nil
}
func (m BodyCodec) PatchGrokResponsesBody(body []byte, upstreamModel string) ([]byte, error) {
	return m.PatchGrokResponsesBodyBase(body, upstreamModel)
}
func (m BodyCodec) PatchGrokResponsesBodyWithClientTools(body []byte, upstreamModel string) ([]byte, protocolbridge.ResponsesClientToolMapping, error) {
	if !json.Valid(body) {
		return nil, protocolbridge.ResponsesClientToolMapping{}, fmt.Errorf("invalid json request body")
	}
	promoted, err := m.SanitizeGrokResponsesInput(body)
	if err != nil {
		return nil, protocolbridge.ResponsesClientToolMapping{}, err
	}
	adapted, mapping, err := protocolbridge.AdaptResponsesClientToolsJSON(promoted, "Grok")
	if err != nil {
		return nil, protocolbridge.ResponsesClientToolMapping{}, err
	}
	patched, err := m.PatchGrokResponsesBodyBase(adapted, upstreamModel)
	if err != nil {
		return nil, protocolbridge.ResponsesClientToolMapping{}, err
	}
	return patched, mapping, nil
}
func (m BodyCodec) PatchGrokResponsesBodyBase(body []byte, upstreamModel string) ([]byte, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid json request body")
	}
	// sjson 可能复用输入底层数组，因此保留调用方请求字节不变，
	// 同一请求体还可能被计费或重试路径读取。
	out, err := sjson.SetBytes(append([]byte(nil), body...), "model", upstreamModel)
	if err != nil {
		return nil, err
	}
	out, err = m.NormalizeGrokResponsesReasoningEffort(out, upstreamModel)
	if err != nil {
		return nil, err
	}
	out, err = m.SanitizeGrokResponsesModelCapabilities(out, upstreamModel)
	if err != nil {
		return nil, err
	}
	for _, unsupportedField := range []string{"prompt_cache_retention", "safety_identifier", "metadata"} {
		if gjson.GetBytes(out, unsupportedField).Exists() {
			out, err = sjson.DeleteBytes(out, unsupportedField)
			if err != nil {
				return nil, err
			}
		}
	}
	if strings.EqualFold(upstreamModel, "grok-4.5") {
		for _, unsupportedField := range []string{"presence_penalty", "presencePenalty", "frequency_penalty", "frequencyPenalty", "stop"} {
			if gjson.GetBytes(out, unsupportedField).Exists() {
				out, err = sjson.DeleteBytes(out, unsupportedField)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if m.GrokModelRejectsLogprobs(upstreamModel) {
		for _, unsupportedField := range []string{"logprobs", "top_logprobs"} {
			if gjson.GetBytes(out, unsupportedField).Exists() {
				out, err = sjson.DeleteBytes(out, unsupportedField)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	out, err = m.SanitizeGrokResponsesUnsupportedFields(out)
	if err != nil {
		return nil, err
	}
	out, err = m.ConvertOpenAICompactInputsForGrok(out)
	if err != nil {
		return nil, err
	}
	out, err = m.SanitizeGrokResponsesInput(out)
	if err != nil {
		return nil, err
	}
	out, err = m.SanitizeGrokResponsesModelInput(out)
	if err != nil {
		return nil, err
	}
	out, err = m.StripRedundantGrokViewImageTool(out)
	if err != nil {
		return nil, err
	}
	out, err = m.SanitizeGrokReasoningNullContent(out)
	if err != nil {
		return nil, err
	}
	out, err = m.SanitizeGrokResponsesTools(out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// grokModelRejectsLogprobs 识别不接受 OpenAI logprobs 字段的 Grok 4.20 模型族。
func (m BodyCodec) GrokModelRejectsLogprobs(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndex(model, "/"); slash >= 0 {
		model = strings.TrimSpace(model[slash+1:])
	}
	return strings.HasPrefix(model, "grok-4.20")
}

// sanitizeGrokResponsesModelCapabilities 移除目标模型明确不支持的 Responses 参数。
func (m BodyCodec) SanitizeGrokResponsesModelCapabilities(body []byte, upstreamModel string) ([]byte, error) {
	if !m.GrokModelRejectsReasoningEffort(upstreamModel) {
		return body, nil
	}

	out := body
	for _, field := range []string{"reasoning", "reasoning_effort", "reasoningEffort"} {
		if !gjson.GetBytes(out, field).Exists() {
			continue
		}
		var err error
		out, err = sjson.DeleteBytes(out, field)
		if err != nil {
			return nil, fmt.Errorf("remove unsupported Grok Composer %s: %w", field, err)
		}
	}
	return out, nil
}

// grokModelRejectsReasoningEffort 判断 Composer 别名是否拒绝 reasoning 参数。
func (m BodyCodec) GrokModelRejectsReasoningEffort(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if slash := strings.LastIndex(model, "/"); slash >= 0 {
		model = strings.TrimSpace(model[slash+1:])
	}
	switch model {
	case "grok-composer", "grok-composer-2.5-fast", "composer-2.5":
		return true
	default:
		return false
	}
}
func (m BodyCodec) NormalizeGrokResponsesReasoningEffort(body []byte, upstreamModel string) ([]byte, error) {
	supportsEffort := m.GrokSupportsReasoningEffort(upstreamModel)
	out := body
	var err error
	for _, field := range []string{"reasoning.effort", "reasoning_effort"} {
		value := gjson.GetBytes(out, field)
		if !value.Exists() {
			continue
		}
		normalized, keep := m.NormalizeGrokReasoningEffortValue(value.String(), upstreamModel)
		if !supportsEffort || !keep {
			out, err = sjson.DeleteBytes(out, field)
		} else {
			out, err = sjson.SetBytes(out, field, normalized)
		}
		if err != nil {
			return nil, fmt.Errorf("normalize Grok reasoning field %s: %w", field, err)
		}
	}
	if camel := gjson.GetBytes(out, "reasoningEffort"); camel.Exists() {
		normalized, keep := m.NormalizeGrokReasoningEffortValue(camel.String(), upstreamModel)
		out, err = sjson.DeleteBytes(out, "reasoningEffort")
		if err != nil {
			return nil, fmt.Errorf("remove Grok reasoningEffort: %w", err)
		}
		if supportsEffort && keep && !gjson.GetBytes(out, "reasoning_effort").Exists() {
			out, err = sjson.SetBytes(out, "reasoning_effort", normalized)
			if err != nil {
				return nil, fmt.Errorf("set Grok reasoning_effort: %w", err)
			}
		}
	}
	if reasoning := gjson.GetBytes(out, "reasoning"); reasoning.Exists() && reasoning.IsObject() && len(reasoning.Map()) == 0 {
		out, err = sjson.DeleteBytes(out, "reasoning")
		if err != nil {
			return nil, fmt.Errorf("remove empty Grok reasoning: %w", err)
		}
	}
	return out, nil
}
func (m BodyCodec) NormalizeGrokChatReasoningEffort(body []byte, upstreamModel string) ([]byte, error) {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoningEffort").String())
	}
	normalized, keep := m.NormalizeGrokReasoningEffortValue(raw, upstreamModel)
	keep = keep && m.GrokSupportsReasoningEffort(upstreamModel)
	out := body
	var err error
	if gjson.GetBytes(out, "reasoningEffort").Exists() {
		out, err = sjson.DeleteBytes(out, "reasoningEffort")
		if err != nil {
			return nil, err
		}
	}
	if !keep {
		if gjson.GetBytes(out, "reasoning_effort").Exists() {
			out, err = sjson.DeleteBytes(out, "reasoning_effort")
		}
		return out, err
	}
	out, err = sjson.SetBytes(out, "reasoning_effort", normalized)
	return out, err
}
func (m BodyCodec) NormalizeGrokReasoningEffortValue(raw, model string) (string, bool) {
	value := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(raw)))
	switch value {
	case "none", "low", "medium", "high":
		return value, true
	case "minimal":
		return "low", true
	case "xhigh", "extrahigh":
		if m.GrokSupportsXHighReasoningEffort(model) {
			return "xhigh", true
		}
		return "high", true
	case "max", "ultra":
		return "high", true
	default:
		return "", false
	}
}

// GrokSupportsXHighReasoningEffort 判断模型是否声明并透传 xhigh 推理档位。
// 当前仅 Grok 4.6 及其无日期别名支持。
func (m BodyCodec) GrokSupportsXHighReasoningEffort(model string) bool {
	model = strings.ToLower(StripGrokProviderPrefix(strings.TrimSpace(model)))
	return model == "grok-4.6" || model == "grok-4.6-latest"
}
func (m BodyCodec) GrokSupportsReasoningEffort(model string) bool {
	model = strings.ToLower(StripGrokProviderPrefix(strings.TrimSpace(model)))
	switch model {
	case "grok-4.5", "grok-4.5-latest", "grok-4.6", "grok-4.6-latest",
		"grok-4.3", "grok-4.3-latest",
		"grok-3-mini", "grok-3-mini-fast", "grok-4.20-0309-reasoning",
		"grok-4.20-reasoning", "grok-4.20-multi-agent-0309":
		return true
	default:
		return false
	}
}

var grokResponsesUnsupportedRecursiveFields = map[string]struct{}{
	"external_web_access": {},
}

func (m BodyCodec) SanitizeGrokResponsesUnsupportedFields(body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"external_web_access"`)) {
		return body, nil
	}

	var payload any
	if err := wirejson.DecodeUseNumber(body, &payload); err != nil {
		return nil, err
	}
	if !m.DeleteJSONFields(payload, grokResponsesUnsupportedRecursiveFields) {
		return body, nil
	}
	return wirejson.Marshal(payload)
}
func (m BodyCodec) DeleteJSONFields(value any, fields map[string]struct{}) bool {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for field := range fields {
			if _, ok := typed[field]; ok {
				delete(typed, field)
				changed = true
			}
		}
		for _, child := range typed {
			if m.DeleteJSONFields(child, fields) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range typed {
			if m.DeleteJSONFields(child, fields) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// sanitizeGrokResponsesInput 移除 Codex/Responses Lite 私有的 additional_tools 载体，
// 同时按载体顺序把其中受支持的工具提升到顶层并保留原顶层顺序；最终由
// sanitizeGrokResponsesTools 过滤 xAI 不支持的类型。
func (m BodyCodec) SanitizeGrokResponsesInput(body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"additional_tools"`)) {
		return body, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || !input.IsArray() {
		return body, nil
	}

	rawItems := input.Array()
	filtered := make([]json.RawMessage, 0, len(rawItems))
	topLevelTools := gjson.GetBytes(body, "tools")
	mergedTools := make([]json.RawMessage, 0)
	seenTools := make(map[string]struct{})
	appendTool := func(tool gjson.Result) bool {
		key := m.GrokResponsesToolDedupKey(tool)
		if _, exists := seenTools[key]; exists {
			return false
		}
		seenTools[key] = struct{}{}
		mergedTools = append(mergedTools, json.RawMessage(tool.Raw))
		return true
	}
	if topLevelTools.IsArray() {
		for _, tool := range topLevelTools.Array() {
			seenTools[m.GrokResponsesToolDedupKey(tool)] = struct{}{}
			mergedTools = append(mergedTools, json.RawMessage(tool.Raw))
		}
	}

	promoted := false
	for _, item := range rawItems {
		if strings.TrimSpace(item.Get("type").String()) == "additional_tools" {
			tools := item.Get("tools")
			if tools.IsArray() {
				for _, tool := range tools.Array() {
					if appendTool(tool) {
						promoted = true
					}
				}
			}
			continue
		}
		filtered = append(filtered, json.RawMessage(item.Raw))
	}
	if len(filtered) == len(rawItems) {
		return body, nil
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	body, err = sjson.SetRawBytes(body, "input", encoded)
	if err != nil || !promoted {
		return body, err
	}
	encodedTools, err := json.Marshal(mergedTools)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "tools", encodedTools)
}

// 当前轮的内联 input_image 已由 Grok 直接读取；同时保留本地 view_image
// 可能让 Grok 只宣告工具调用而不继续作答，因此只移除该冗余自动工具。
func (m BodyCodec) StripRedundantGrokViewImageTool(body []byte) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, nil
	}
	items := input.Array()
	if len(items) == 0 {
		return body, nil
	}
	current := items[len(items)-1]
	if strings.TrimSpace(current.Get("role").String()) != "user" ||
		!protocolopenai.JSONValueMayContainImageInput(current) {
		return body, nil
	}

	toolChoice := gjson.GetBytes(body, "tool_choice")
	if toolChoice.IsObject() && strings.TrimSpace(toolChoice.Get("type").String()) == "function" {
		choiceName := strings.TrimSpace(toolChoice.Get("name").String())
		if choiceName == "" {
			choiceName = strings.TrimSpace(toolChoice.Get("function.name").String())
		}
		if choiceName == "view_image" {
			return body, nil
		}
	}

	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return body, nil
	}
	filtered := make([]json.RawMessage, 0, len(tools.Array()))
	changed := false
	for _, tool := range tools.Array() {
		if strings.TrimSpace(tool.Get("type").String()) == "function" &&
			strings.TrimSpace(tool.Get("name").String()) == "view_image" {
			changed = true
			continue
		}
		filtered = append(filtered, json.RawMessage(tool.Raw))
	}
	if !changed {
		return body, nil
	}
	if len(filtered) == 0 && strings.TrimSpace(toolChoice.String()) == "required" {
		return body, nil
	}

	if len(filtered) == 0 {
		out, err := sjson.DeleteBytes(body, "tools")
		if err != nil {
			return nil, err
		}
		return sjson.DeleteBytes(out, "parallel_tool_calls")
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "tools", encoded)
}
func (m BodyCodec) GrokResponsesToolDedupKey(tool gjson.Result) string {
	toolType := strings.TrimSpace(tool.Get("type").String())
	if toolType != "" {
		if name := strings.TrimSpace(tool.Get("name").String()); name != "" {
			return "type:" + toolType + "\x00name:" + name
		}
		if toolType == "mcp" {
			if label := strings.TrimSpace(tool.Get("server_label").String()); label != "" {
				return "type:mcp\x00server_label:" + label
			}
		}
	}
	return "json:" + protocolopenai.NormalizeCompatSeedJSON(json.RawMessage(tool.Raw))
}

// sanitizeGrokReasoningNullContent 清理 Responses input 中显式的 JSON null；
// xAI 的 untagged ModelInput 反序列化器会拒收这些字段，compaction 项保持原样。
func (m BodyCodec) SanitizeGrokReasoningNullContent(body []byte) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || (!input.IsArray() && !input.IsObject()) {
		return body, nil
	}
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return body, nil
	}
	rawInput, ok := decoded["input"]
	if !ok {
		return body, nil
	}
	cleaned, changed := m.StripExplicitNullsFromGrokInput(rawInput)
	if !changed {
		return body, nil
	}
	decoded["input"] = cleaned
	return wirejson.Marshal(decoded)
}
func (m BodyCodec) StripExplicitNullsFromGrokInput(value any) (any, bool) {
	switch node := value.(type) {
	case []any:
		changed := false
		for i, item := range node {
			itemMap, ok := item.(map[string]any)
			if !ok {
				next, childChanged := m.StripExplicitNullsFromGrokInput(item)
				if childChanged {
					node[i] = next
					changed = true
				}
				continue
			}
			if protocolopenai.IsCompactionItemType(m.GrokCompactStringValue(itemMap["type"])) {
				continue
			}
			next, childChanged := m.StripExplicitNullsFromGrokObject(itemMap)
			if childChanged {
				node[i] = next
				changed = true
			}
		}
		return node, changed
	case map[string]any:
		if protocolopenai.IsCompactionItemType(m.GrokCompactStringValue(node["type"])) {
			return node, false
		}
		return m.StripExplicitNullsFromGrokObject(node)
	default:
		return value, false
	}
}
func (m BodyCodec) StripExplicitNullsFromGrokObject(node map[string]any) (map[string]any, bool) {
	if node == nil {
		return node, false
	}
	changed := false
	for key, child := range node {
		if child == nil {
			delete(node, key)
			changed = true
			continue
		}
		switch typed := child.(type) {
		case map[string]any:
			next, childChanged := m.StripExplicitNullsFromGrokObject(typed)
			if childChanged {
				node[key] = next
				changed = true
			}
		case []any:
			next, childChanged := m.StripExplicitNullsFromGrokInput(typed)
			if childChanged {
				node[key] = next
				changed = true
			}
		}
	}
	return node, changed
}

var grokResponsesSupportedToolTypes = map[string]struct{}{

	"code_execution": {},

	"code_interpreter": {},

	"collections_search": {},

	"file_search": {},

	"function": {},

	"mcp": {},

	"shell": {},

	"web_search": {},

	"x_search": {},
}

const GrokSafeFunctionParameters = `{"type":"object","properties":{},"additionalProperties":true}`

func (m BodyCodec) SanitizeGrokResponsesTools(body []byte) ([]byte, error) {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() {
		return m.DeleteGrokOrphanToolControls(body)
	}
	if !tools.IsArray() {
		return m.DeleteGrokOrphanToolControls(body)
	}

	rawTools := tools.Array()
	filteredTools := make([]json.RawMessage, 0, len(rawTools))
	toolsChanged := false
	for _, tool := range rawTools {
		toolType := strings.TrimSpace(tool.Get("type").String())
		if _, ok := grokResponsesSupportedToolTypes[toolType]; ok {
			raw := json.RawMessage(tool.Raw)
			if toolType == "function" && (!tool.Get("parameters").Exists() || tool.Get("parameters").Type == gjson.Null) {
				var payload map[string]any
				if err := wirejson.DecodeUseNumber(raw, &payload); err != nil {
					return nil, err
				}
				payload["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
				encoded, err := wirejson.Marshal(payload)
				if err != nil {
					return nil, err
				}
				raw = encoded
				toolsChanged = true
			} else if toolType == "function" && m.GrokFunctionParametersHaveInvalidUnionRoot(tool.Get("parameters")) {
				var err error
				raw, err = sjson.SetRawBytes(raw, "parameters", []byte(GrokSafeFunctionParameters))
				if err != nil {
					return nil, err
				}
				if strict := tool.Get("strict"); strict.Exists() && strict.Bool() {
					raw, err = sjson.SetBytes(raw, "strict", false)
					if err != nil {
						return nil, err
					}
				}
				toolsChanged = true
			}
			filteredTools = append(filteredTools, raw)
		}
	}
	if !m.GrokRawToolsContainType(filteredTools, "tool_search") {
		for index, raw := range filteredTools {
			if !gjson.GetBytes(raw, "defer_loading").Exists() {
				continue
			}
			cleaned, deleteErr := sjson.DeleteBytes(raw, "defer_loading")
			if deleteErr != nil {
				return nil, deleteErr
			}
			filteredTools[index] = cleaned
			toolsChanged = true
		}
	}

	var err error
	if len(filteredTools) != len(rawTools) || toolsChanged {
		if len(filteredTools) == 0 {
			body, err = sjson.DeleteBytes(body, "tools")
		} else {
			var encoded []byte
			encoded, err = json.Marshal(filteredTools)
			if err != nil {
				return nil, err
			}
			body, err = sjson.SetRawBytes(body, "tools", encoded)
		}
		if err != nil {
			return nil, err
		}
	}
	if len(filteredTools) == 0 {
		return m.DeleteGrokOrphanToolControls(body)
	}

	toolChoice := gjson.GetBytes(body, "tool_choice")
	if !toolChoice.Exists() {
		return body, nil
	}
	if m.ShouldDropGrokToolChoice(toolChoice, filteredTools) {
		body, err = sjson.DeleteBytes(body, "tool_choice")
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}
func (m BodyCodec) GrokFunctionParametersHaveInvalidUnionRoot(parameters gjson.Result) bool {
	if !parameters.Exists() || !parameters.IsObject() {
		return false
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		branches := parameters.Get(keyword)
		if !branches.IsArray() {
			continue
		}
		values := branches.Array()
		if len(values) == 0 {
			continue
		}
		for _, branch := range values {
			if !strings.EqualFold(strings.TrimSpace(branch.Get("type").String()), "object") {
				return true
			}
		}
	}
	return false
}
func (m BodyCodec) GrokRawToolsContainType(tools []json.RawMessage, want string) bool {
	for _, tool := range tools {
		if strings.TrimSpace(gjson.GetBytes(tool, "type").String()) == want {
			return true
		}
	}
	return false
}
func (m BodyCodec) DeleteGrokOrphanToolControls(body []byte) ([]byte, error) {
	var err error
	for _, field := range []string{"tool_choice", "parallel_tool_calls"} {
		if !gjson.GetBytes(body, field).Exists() {
			continue
		}
		body, err = sjson.DeleteBytes(body, field)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}
func (m BodyCodec) ShouldDropGrokToolChoice(toolChoice gjson.Result, tools []json.RawMessage) bool {
	if len(tools) == 0 {
		return true
	}
	if !toolChoice.IsObject() {
		return false
	}
	choiceType := strings.TrimSpace(toolChoice.Get("type").String())
	if choiceType == "" {
		return false
	}
	if _, ok := grokResponsesSupportedToolTypes[choiceType]; !ok {
		return true
	}
	if choiceType == "function" {
		choiceName := strings.TrimSpace(toolChoice.Get("name").String())
		if choiceName == "" {
			choiceName = strings.TrimSpace(toolChoice.Get("function.name").String())
		}
		if choiceName == "" {
			return false
		}
		for _, tool := range tools {
			var item struct {
				Type     string `json:"type"`
				Name     string `json:"name"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if err := json.Unmarshal(tool, &item); err != nil {
				continue
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = strings.TrimSpace(item.Function.Name)
			}
			if strings.TrimSpace(item.Type) == "function" && name == choiceName {
				return false
			}
		}
		return true
	}
	return false
}
func (m BodyCodec) ShouldBridgeGrokComposerImageInputs(body []byte) bool {
	if len(body) == 0 || !m.IsGrokComposerModel(gjson.GetBytes(body, "model").String()) {
		return false
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() {
		return false
	}
	return protocolopenai.JSONValueMayContainImageInput(messages)
}
func (m BodyCodec) IsGrokComposerModel(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if model == "" {
		return false
	}
	if strings.Contains(model, "/") {
		parts := strings.Split(model, "/")
		model = strings.TrimSpace(parts[len(parts)-1])
	}
	return strings.Contains(model, "composer")
}
func (m BodyCodec) CollectGrokComposerImageURLs(reqBody map[string]any) []string {
	messages, ok := reqBody["messages"].([]any)
	if !ok {
		return nil
	}

	var imageURLs []string
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			if imageURL := m.GrokComposerImageURLFromPart(part); imageURL != "" {
				imageURLs = append(imageURLs, imageURL)
			}
		}
	}
	return imageURLs
}
func (m BodyCodec) GrokComposerImageURLFromPart(part any) string {
	partMap, ok := part.(map[string]any)
	if !ok {
		return ""
	}
	if strings.TrimSpace(strings.ToLower(fmt.Sprint(partMap["type"]))) != "image_url" {
		return ""
	}
	switch imageURL := partMap["image_url"].(type) {
	case string:
		return m.NormalizeGrokComposerImageURL(imageURL)
	case map[string]any:
		raw, _ := imageURL["url"].(string)
		return m.NormalizeGrokComposerImageURL(raw)
	default:
		return ""
	}
}
func (m BodyCodec) NormalizeGrokComposerImageURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || protocolopenai.IsEmptyBase64DataURI(trimmed) {
		return ""
	}
	return trimmed
}
func (m BodyCodec) BuildGrokComposerImageDescriptionBody(imageURL string, index int) ([]byte, error) {
	prompt := fmt.Sprintf("Describe image %d in concise, factual text for a downstream coding/composer model. Include visible text, UI elements, diagrams, errors, and spatial relationships. Do not mention that you are an image analysis bridge.", index)
	req := map[string]any{

		"model": ComposerImageBridgeVisionModel,

		"stream": false,

		"store": false,

		"max_output_tokens": ComposerImageBridgeMaxOutputTokens,

		"input": []any{
			map[string]any{

				"type": "message",

				"role": "user",

				"content": []any{
					map[string]any{"type": "input_text", "text": prompt},
					map[string]any{"type": "input_image", "image_url": imageURL},
				},
			},
		},
	}
	return wirejson.Marshal(req)
}
func (m BodyCodec) GrokResponsesOutputText(resp *protocolopenai.ResponsesResponse) string {
	if resp == nil {
		return ""
	}
	var parts []string
	for _, output := range resp.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" || content.Type == "text" || content.Type == "input_text" {
				if text := strings.TrimSpace(content.Text); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// rewriteGrokComposerImagesAsText 按图片出现顺序替换消息内容，同时保留原文本块。
func (m BodyCodec) RewriteGrokComposerImagesAsText(reqBody map[string]any, descriptions []string) bool {
	messages, ok := reqBody["messages"].([]any)
	if !ok {
		return false
	}

	imageIndex := 0
	changed := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		var textParts []string
		messageChanged := false
		for _, part := range parts {
			if imageURL := m.GrokComposerImageURLFromPart(part); imageURL != "" {
				if imageIndex < len(descriptions) {
					textParts = append(textParts, fmt.Sprintf("Image %d description: %s", imageIndex+1, strings.TrimSpace(descriptions[imageIndex])))
				}
				imageIndex++
				messageChanged = true
				continue
			}
			if text := m.GrokComposerTextFromPart(part); text != "" {
				textParts = append(textParts, text)
			}
		}
		if messageChanged {
			msgMap["content"] = strings.Join(textParts, "\n\n")
			changed = true
		}
	}
	return changed
}
func (m BodyCodec) GrokComposerTextFromPart(part any) string {
	partMap, ok := part.(map[string]any)
	if !ok {
		return ""
	}
	partType := strings.TrimSpace(strings.ToLower(fmt.Sprint(partMap["type"])))
	switch partType {
	case "text", "input_text":
		text, _ := partMap["text"].(string)
		return strings.TrimSpace(text)
	default:
		return ""
	}
}
