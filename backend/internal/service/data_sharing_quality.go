package service

import "strings"

type dataShareQualityReport struct {
	Errors []string
	Status string
}

// ValidateDataShareSessionQuality 按附件交付规则检查 session 是否可进入正式导出。
func ValidateDataShareSessionQuality(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) []string {
	compact := CompactDataShareMessages(messages)
	errs := validateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage)
	if dataShareHasReplayDuplicateBlock(compact) {
		errs = appendDataShareQualityError(errs, dataShareQualityErrorReplayDuplicateBlock)
	}
	return errs
}

// DataShareSessionQuality 一次性返回质量状态和错误列表，避免采集热路径重复扫描消息。
func DataShareSessionQuality(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) (string, []string) {
	report := evaluateDataShareSessionQuality(model, systemPrompt, messages, tools, usage)
	if report.Status != DataShareQualityInvalid {
		return report.Status, report.Errors
	}
	if !dataShareErrorsAllowNormalizeFallback(report.Errors) {
		return report.Status, report.Errors
	}
	if !dataShareMessagesNeedNormalizeFallback(messages) {
		return report.Status, report.Errors
	}
	normalized := normalizeDataShareMessages(messages)
	if len(normalized) == 0 {
		return report.Status, report.Errors
	}
	// 兼容历史/异构 payload：状态允许走规范化恢复，错误列表仍保留原始快照的具体缺口。
	if normalizedReport := evaluateDataShareSessionQuality(model, systemPrompt, normalized, tools, usage); normalizedReport.Status != DataShareQualityInvalid {
		return DataShareQualityPartial, report.Errors
	}
	return report.Status, report.Errors
}

func evaluateDataShareSessionQuality(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) dataShareQualityReport {
	compact := CompactDataShareMessages(messages)
	return evaluateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage)
}

// evaluateCompactDataShareSessionQuality 只接收已 compact 的消息，避免最终化阶段重复压缩同一份大快照。
func evaluateCompactDataShareSessionQuality(model string, systemPrompt string, compact []map[string]any, tools []map[string]any, usage map[string]any) dataShareQualityReport {
	errs := validateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage)
	if dataShareHasReplayDuplicateBlock(compact) {
		errs = appendDataShareQualityError(errs, dataShareQualityErrorReplayDuplicateBlock)
	}
	status := DataShareQualityInvalid
	if len(errs) == 0 {
		status = DataShareQualityComplete
	} else if dataShareErrorsAllowTailTrim(errs) && dataShareCanTrimTailToComplete(model, systemPrompt, compact, tools, usage) {
		status = DataShareQualityPartial
	}
	return dataShareQualityReport{Errors: errs, Status: status}
}

func validateCompactDataShareSessionQuality(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) []string {
	var errs []string
	seenErrs := map[string]struct{}{}
	addErr := func(code string) {
		if _, ok := seenErrs[code]; ok {
			return
		}
		seenErrs[code] = struct{}{}
		errs = append(errs, code)
	}
	systemPrompt = firstNonBlank(systemPrompt, extractSystemPromptFromMessages(messages))
	if strings.TrimSpace(systemPrompt) == "" {
		addErr("missing_system_prompt")
	}
	if len(messages) < 2 {
		addErr("effective_turns_lt_2")
	}
	toolDefs, invalidToolCount := collectDataShareToolDefinitions(tools)
	if len(toolDefs) == 0 {
		addErr("missing_tool_definitions")
	}
	if invalidToolCount > 0 {
		addErr("invalid_tool_definition")
	}
	toolCalls := collectDataShareToolCalls(messages)
	toolResults := collectDataShareToolResults(messages)
	if len(toolCalls) == 0 {
		addErr("missing_structured_tool_call")
	}
	for _, call := range toolCalls {
		if call.id == "" || call.name == "" {
			addErr("invalid_tool_call")
			continue
		}
		if _, ok := toolDefs[call.name]; !ok {
			addErr("tool_definition_missing")
		}
		if toolResults[call.id] != 1 {
			addErr("tool_call_result_unpaired")
		}
	}
	for id, count := range toolResults {
		if id == "" || count != 1 {
			addErr("tool_result_unpaired")
		}
	}
	if len(toolCalls) > 0 && !hasFinalAssistantMessage(messages) {
		addErr("missing_final_assistant")
	}
	// 交付文档允许 token 用量无法聚合时为空或保留在 meta，因此 usage 不能作为 session 可用性的硬门槛。
	// 模型范围由数据共享采集跳过规则控制，这里只校验已采集 session 的结构质量。
	_ = model
	_ = usage
	return errs
}

func dataShareCanTrimTailToComplete(model string, systemPrompt string, compact []map[string]any, tools []map[string]any, usage map[string]any) bool {
	return dataShareCompleteTrimPrefixLen(model, systemPrompt, compact, tools, usage) > 0
}

// dataShareCompleteTrimPrefixLen 用单次前缀扫描寻找可导出的完整前缀，替代逐候选完整校验的平方级路径。
func dataShareCompleteTrimPrefixLen(model string, systemPrompt string, compact []map[string]any, tools []map[string]any, usage map[string]any) int {
	// usage 当前不是质量硬门槛；保留参数是为了让调用点和完整校验的签名保持一致。
	_ = usage
	state := newDataSharePrefixQualityState(model, systemPrompt, tools)
	completeLen := 0
	for i, msg := range compact {
		state.observe(msg)
		// 尾部裁剪必须至少去掉一条消息，避免把完整快照误当成 partial 修复。
		if i < len(compact)-1 && state.complete() {
			completeLen = i + 1
		}
	}
	return completeLen
}

// dataSharePrefixQualityState 增量维护 validateCompactDataShareSessionQuality 关心的质量条件。
type dataSharePrefixQualityState struct {
	toolDefinitionsReady       bool
	hasSystemPrompt            bool
	messageCount               int
	toolCallCount              int
	invalidToolCallCount       int
	toolDefinitionMissingCount int
	callIDs                    map[string]struct{}
	resultCounts               map[string]int
	callIDsWithBadResultCount  int
	resultIDsWithBadCount      int
	emptyToolResultCount       int
	hasFinalAssistant          bool
	toolDefs                   map[string]struct{}
}

func newDataSharePrefixQualityState(model string, systemPrompt string, tools []map[string]any) *dataSharePrefixQualityState {
	// 模型范围由采集跳过规则控制，前缀质量状态机只维护结构完整性。
	_ = model
	toolDefs, invalidToolCount := collectDataShareToolDefinitions(tools)
	return &dataSharePrefixQualityState{
		toolDefinitionsReady: len(toolDefs) > 0 && invalidToolCount == 0,
		hasSystemPrompt:      strings.TrimSpace(systemPrompt) != "",
		callIDs:              map[string]struct{}{},
		resultCounts:         map[string]int{},
		toolDefs:             toolDefs,
	}
}

func (s *dataSharePrefixQualityState) observe(msg map[string]any) {
	if s == nil {
		return
	}
	s.messageCount++
	role := strings.TrimSpace(stringFromAny(msg["role"]))
	if (role == "system" || role == "developer") && strings.TrimSpace(dataShareContentText(msg["content"])) != "" {
		s.hasSystemPrompt = true
	}
	s.observeToolCalls(msg)
	if role == "tool" {
		s.observeToolResult(strings.TrimSpace(stringFromAny(msg["tool_call_id"])))
	}
	if role != "" {
		s.hasFinalAssistant = role == "assistant" && len(anySlice(msg["tool_calls"])) == 0 && strings.TrimSpace(dataShareContentText(msg["content"])) != ""
	}
}

func (s *dataSharePrefixQualityState) observeToolCalls(msg map[string]any) {
	for _, raw := range anySlice(msg["tool_calls"]) {
		call, ok := mapFromAny(raw)
		if !ok {
			continue
		}
		s.toolCallCount++
		id := strings.TrimSpace(stringFromAny(call["id"]))
		name := strings.TrimSpace(stringFromAny(call["name"]))
		if id == "" || name == "" {
			s.invalidToolCallCount++
			continue
		}
		if _, ok := s.toolDefs[name]; !ok {
			s.toolDefinitionMissingCount++
		}
		s.observeToolCallID(id)
	}
}

func (s *dataSharePrefixQualityState) observeToolCallID(id string) {
	if _, exists := s.callIDs[id]; exists {
		return
	}
	s.callIDs[id] = struct{}{}
	if s.resultCounts[id] != 1 {
		s.callIDsWithBadResultCount++
	}
}

func (s *dataSharePrefixQualityState) observeToolResult(id string) {
	if id == "" {
		s.emptyToolResultCount++
		return
	}
	oldCount := s.resultCounts[id]
	oldBadResult := oldCount > 0 && oldCount != 1
	oldBadCall := s.callIDNeedsResult(id, oldCount)
	newCount := oldCount + 1
	s.resultCounts[id] = newCount
	newBadResult := newCount != 1
	newBadCall := s.callIDNeedsResult(id, newCount)
	if oldBadResult && !newBadResult {
		s.resultIDsWithBadCount--
	} else if !oldBadResult && newBadResult {
		s.resultIDsWithBadCount++
	}
	if oldBadCall && !newBadCall {
		s.callIDsWithBadResultCount--
	} else if !oldBadCall && newBadCall {
		s.callIDsWithBadResultCount++
	}
}

func (s *dataSharePrefixQualityState) callIDNeedsResult(id string, resultCount int) bool {
	if _, ok := s.callIDs[id]; !ok {
		return false
	}
	return resultCount != 1
}

func (s *dataSharePrefixQualityState) complete() bool {
	return s != nil &&
		s.toolDefinitionsReady &&
		s.hasSystemPrompt &&
		s.messageCount >= 2 &&
		s.toolCallCount > 0 &&
		s.invalidToolCallCount == 0 &&
		s.toolDefinitionMissingCount == 0 &&
		s.callIDsWithBadResultCount == 0 &&
		s.resultIDsWithBadCount == 0 &&
		s.emptyToolResultCount == 0 &&
		s.hasFinalAssistant
}

func dataShareErrorsAllowTailTrim(errs []string) bool {
	if len(errs) == 0 {
		return false
	}
	for _, errCode := range errs {
		switch errCode {
		case "invalid_tool_call", "tool_definition_missing", "tool_call_result_unpaired", "tool_result_unpaired", "missing_final_assistant":
			continue
		default:
			return false
		}
	}
	return true
}

func dataShareErrorsAllowNormalizeFallback(errs []string) bool {
	if len(errs) == 0 {
		return false
	}
	for _, errCode := range errs {
		switch errCode {
		case "missing_structured_tool_call", "invalid_tool_call", "tool_call_result_unpaired", "tool_result_unpaired", "missing_final_assistant":
			continue
		default:
			return false
		}
	}
	return true
}

func dataShareMessagesNeedNormalizeFallback(messages []map[string]any) bool {
	for _, msg := range messages {
		for _, raw := range anySlice(msg["content"]) {
			block, ok := mapFromAny(raw)
			if !ok {
				continue
			}
			switch strings.TrimSpace(stringFromAny(block["type"])) {
			case "tool_use", "tool_result":
				return true
			}
		}
	}
	return false
}

type dataShareToolCall struct {
	id   string
	name string
}

func collectDataShareToolDefinitions(tools []map[string]any) (map[string]struct{}, int) {
	defs := make(map[string]struct{}, len(tools))
	invalid := 0
	for _, tool := range tools {
		name := strings.TrimSpace(stringFromAny(tool["name"]))
		description := strings.TrimSpace(stringFromAny(tool["description"]))
		parameters, ok := mapFromAny(tool["parameters"])
		if name == "" || description == "" || !ok || len(parameters) == 0 {
			invalid++
			continue
		}
		defs[name] = struct{}{}
	}
	return defs, invalid
}

func collectDataShareToolCalls(messages []map[string]any) []dataShareToolCall {
	var out []dataShareToolCall
	for _, msg := range messages {
		for _, call := range anySlice(msg["tool_calls"]) {
			m, ok := mapFromAny(call)
			if !ok {
				continue
			}
			out = append(out, dataShareToolCall{
				id:   strings.TrimSpace(stringFromAny(m["id"])),
				name: strings.TrimSpace(stringFromAny(m["name"])),
			})
		}
	}
	return out
}

func collectDataShareToolResults(messages []map[string]any) map[string]int {
	out := map[string]int{}
	for _, msg := range messages {
		if strings.TrimSpace(stringFromAny(msg["role"])) != "tool" {
			continue
		}
		id := strings.TrimSpace(stringFromAny(msg["tool_call_id"]))
		out[id]++
	}
	return out
}

func hasFinalAssistantMessage(messages []map[string]any) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		role := strings.TrimSpace(stringFromAny(msg["role"]))
		if role == "" {
			continue
		}
		if role != "assistant" {
			return false
		}
		if len(anySlice(msg["tool_calls"])) > 0 {
			return false
		}
		return strings.TrimSpace(dataShareContentText(msg["content"])) != ""
	}
	return false
}

func normalizeDataShareMessages(messages []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		for _, expanded := range expandAnthropicDataShareMessage(msg) {
			normalized := normalizeDataShareMessage(expanded)
			if len(normalized) == 0 {
				continue
			}
			out = append(out, normalized)
		}
	}
	return out
}

// expandAnthropicDataShareMessage 将 Anthropic content block 展开成统一的 message/tool 结构。
func expandAnthropicDataShareMessage(msg map[string]any) []map[string]any {
	if msg == nil {
		return nil
	}
	content := anySlice(msg["content"])
	if len(content) == 0 {
		return []map[string]any{msg}
	}
	role := normalizeResponsesInputRole(stringFromAny(msg["role"]), stringFromAny(msg["type"]))
	switch role {
	case "assistant":
		return expandAnthropicAssistantMessage(msg, content)
	case "user":
		return expandAnthropicUserMessage(msg, content)
	default:
		return []map[string]any{msg}
	}
}

// expandAnthropicAssistantMessage 把 assistant 的 tool_use block 转成标准 tool_calls。
func expandAnthropicAssistantMessage(msg map[string]any, content []any) []map[string]any {
	calls := make([]map[string]any, 0)
	textBlocks := make([]any, 0, len(content))
	for _, raw := range content {
		block, ok := mapFromAny(raw)
		if !ok || strings.TrimSpace(stringFromAny(block["type"])) != "tool_use" {
			textBlocks = append(textBlocks, raw)
			continue
		}
		calls = append(calls, map[string]any{
			"id":        firstNonBlank(stringFromAny(block["id"]), stringFromAny(block["tool_use_id"])),
			"name":      stringFromAny(block["name"]),
			"arguments": firstPresentAny(block["input"], block["arguments"]),
		})
	}
	if len(calls) == 0 {
		return []map[string]any{msg}
	}
	out := cloneDataShareMap(msg)
	out["role"] = "assistant"
	out["tool_calls"] = calls
	out["finish_reason"] = "tool_calls"
	out["content"] = contentValueFromAnthropicBlocks(textBlocks)
	return []map[string]any{out}
}

// expandAnthropicUserMessage 把 user 消息里的 tool_result block 转成标准 tool 消息。
func expandAnthropicUserMessage(msg map[string]any, content []any) []map[string]any {
	out := make([]map[string]any, 0, len(content))
	textBlocks := make([]any, 0, len(content))
	sawToolResult := false
	flushText := func() {
		if len(textBlocks) == 0 {
			return
		}
		textMsg := cloneDataShareMap(msg)
		textMsg["role"] = "user"
		textMsg["content"] = contentValueFromAnthropicBlocks(textBlocks)
		out = append(out, textMsg)
		textBlocks = nil
	}
	for _, raw := range content {
		block, ok := mapFromAny(raw)
		if !ok || strings.TrimSpace(stringFromAny(block["type"])) != "tool_result" {
			textBlocks = append(textBlocks, raw)
			continue
		}
		sawToolResult = true
		flushText()
		out = append(out, map[string]any{
			"role":         "tool",
			"tool_call_id": firstNonBlank(stringFromAny(block["tool_use_id"]), stringFromAny(block["tool_call_id"]), stringFromAny(block["id"])),
			"content":      firstPresentAny(block["content"], block["output"]),
			"is_error":     firstPresentAny(block["is_error"], block["error"]),
			"status":       stringFromAny(block["status"]),
		})
	}
	if !sawToolResult {
		return []map[string]any{msg}
	}
	flushText()
	return out
}

// contentValueFromAnthropicBlocks 提取 Anthropic 文本块中的可读内容。
func contentValueFromAnthropicBlocks(blocks []any) any {
	if len(blocks) == 0 {
		return ""
	}
	return normalizeDataShareContentValue(blocks)
}
