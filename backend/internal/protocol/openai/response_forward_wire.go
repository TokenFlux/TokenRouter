// 原流报文解析与重建保持逐次调用和原未知字段；不执行 I/O 或读取平台账号。
package openai

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type OpenAICompatSSEFrame struct {
	EventType string
	Data      string
}

type OpenAICompatSSEFrameParser struct {
	eventType string
	dataLines []string
}

func (p *OpenAICompatSSEFrameParser) AddLine(line string) (OpenAICompatSSEFrame, bool) {
	if line == "" {
		return p.dispatch()
	}
	if strings.HasPrefix(line, ":") {
		return OpenAICompatSSEFrame{}, false
	}
	if eventType, ok := ExtractSSEEventLine(line); ok {
		p.eventType = eventType
		return OpenAICompatSSEFrame{}, false
	}
	if data, ok := ExtractSSEDataLine(line); ok {
		p.dataLines = append(p.dataLines, data)
	}
	return OpenAICompatSSEFrame{}, false
}

func (p *OpenAICompatSSEFrameParser) Finish() (OpenAICompatSSEFrame, bool) {
	return p.dispatch()
}

func (p *OpenAICompatSSEFrameParser) dispatch() (OpenAICompatSSEFrame, bool) {
	frame := OpenAICompatSSEFrame{
		EventType: p.eventType,
		Data:      strings.Join(p.dataLines, "\n"),
	}
	p.eventType = ""
	p.dataLines = nil
	return frame, frame.Data != ""
}

func OpenAICompatPayloadWithEventType(payload, eventType string) string {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || strings.TrimSpace(payload) == "" || strings.TrimSpace(payload) == "[DONE]" {
		return payload
	}
	if strings.TrimSpace(gjson.Get(payload, "type").String()) != "" {
		return payload
	}
	patched, err := sjson.Set(payload, "type", eventType)
	if err != nil {
		return payload
	}
	return patched
}

func ReplaceModelInSSELine(line, fromModel, toModel string) string {
	data, ok := ExtractSSEDataLine(line)
	if !ok {
		return line
	}
	if data == "" || data == "[DONE]" {
		return line
	}

	// 使用 gjson 精确检查 model 字段，避免全量 JSON 反序列化
	if m := gjson.Get(data, "model"); m.Exists() && m.Str == fromModel {
		newData, err := sjson.Set(data, "model", toModel)
		if err != nil {
			return line
		}
		return "data: " + newData
	}

	// 检查嵌套的 response.model 字段
	if m := gjson.Get(data, "response.model"); m.Exists() && m.Str == fromModel {
		newData, err := sjson.Set(data, "response.model", toModel)
		if err != nil {
			return line
		}
		return "data: " + newData
	}

	return line
}

// NormalizeOpenAIResponsesFunctionCallArguments 修正部分上游会把完整 arguments
// 字符串重复拼接两次的坏事件，避免 Codex 客户端收到无法解析的工具参数。
func NormalizeOpenAIResponsesFunctionCallArguments(data []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(data)) == 0 || !bytes.Contains(data, []byte(`"arguments"`)) {
		return data, false
	}
	if !gjson.ValidBytes(data) {
		return data, false
	}

	updated := data
	changed := false
	setDedupedArgument := func(path string) {
		arg := gjson.GetBytes(updated, path)
		if !arg.Exists() || arg.Type != gjson.String {
			return
		}
		deduped, ok := DedupeRepeatedJSONArgumentString(arg.Str)
		if !ok {
			return
		}
		next, err := sjson.SetBytes(updated, path, deduped)
		if err != nil {
			return
		}
		updated = next
		changed = true
	}

	eventType := strings.TrimSpace(gjson.GetBytes(updated, "type").String())
	if eventType == "response.function_call_arguments.done" {
		setDedupedArgument("arguments")
	}
	if itemType := strings.TrimSpace(gjson.GetBytes(updated, "item.type").String()); IsResponsesFunctionCallItemType(itemType) {
		setDedupedArgument("item.arguments")
	}
	DedupeResponsesFunctionCallOutputArguments(updated, "response.output", setDedupedArgument)
	DedupeResponsesFunctionCallOutputArguments(updated, "output", setDedupedArgument)

	return updated, changed
}

func DedupeResponsesFunctionCallOutputArguments(data []byte, outputPath string, setDedupedArgument func(string)) {
	output := gjson.GetBytes(data, outputPath)
	if !output.Exists() || !output.IsArray() {
		return
	}
	for i, item := range output.Array() {
		if !IsResponsesFunctionCallItemType(strings.TrimSpace(item.Get("type").String())) {
			continue
		}
		setDedupedArgument(outputPath + "." + strconv.Itoa(i) + ".arguments")
	}
}

func IsResponsesFunctionCallItemType(itemType string) bool {
	return itemType == "function_call" || itemType == "custom_tool_call"
}

func DedupeRepeatedJSONArgumentString(arguments string) (string, bool) {
	if len(arguments) == 0 || len(arguments)%2 != 0 {
		return "", false
	}
	halfLen := len(arguments) / 2
	first := arguments[:halfLen]
	if first != arguments[halfLen:] {
		return "", false
	}
	trimmed := strings.TrimSpace(first)
	if trimmed == "" || (!strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[")) {
		return "", false
	}
	if !json.Valid([]byte(first)) {
		return "", false
	}
	return first, true
}

func ParseSSEUsage(data string, usage *ForwardUsage) {
	ParseSSEUsageBytes([]byte(data), usage)
}

func ParseSSEUsageBytes(data []byte, usage *ForwardUsage) {
	if usage == nil || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return
	}
	// Usage is absent from nearly every delta event. Avoid full JSON validation
	// and four gjson path scans on that hot path while retaining progressive
	// usage from compatible upstreams on any event type.
	if !bytes.Contains(data, []byte(`"usage"`)) {
		return
	}
	parsedUsage, ok := ExtractOpenAIUsageFromJSONBytes(data)
	if !ok {
		return
	}
	if OpenAIStreamEventTypeIsTerminal(strings.TrimSpace(gjson.GetBytes(data, "type").String())) {
		*usage = parsedUsage
		return
	}
	MergeOpenAIUsageNonZero(usage, parsedUsage)
}

// Compatible Responses upstreams may report usage before the terminal event.
// Retain those non-zero fields as a fallback; terminal usage remains authoritative.
func MergeOpenAIUsageNonZero(dst *ForwardUsage, src ForwardUsage) {
	if dst == nil {
		return
	}
	if src.InputTokens > 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.ImageInputTokens > 0 {
		dst.ImageInputTokens = src.ImageInputTokens
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheCreationInputTokens > 0 {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	}
	if src.CacheReadInputTokens > 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.ImageOutputTokens > 0 {
		dst.ImageOutputTokens = src.ImageOutputTokens
	}
}

func ExtractOpenAIUsageFromJSONBytes(body []byte) (ForwardUsage, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ForwardUsage{}, false
	}
	// 部分 OpenAI 兼容上游（例如 Cline API）会将标准响应包在 data 字段中：
	// {"data":{"choices": [...], "usage": {...}}, "success":true}。
	// 按优先级先保留原有路径，再尝试兼容层 data 包装，
	// 避免同步请求能正常返回但用量被静默记录为 0。
	candidates := []struct {
		usagePath      string
		imageUsagePath string
	}{
		{usagePath: "usage", imageUsagePath: "tool_usage.image_gen"},
		{usagePath: "response.usage", imageUsagePath: "response.tool_usage.image_gen"},
		{usagePath: "data.usage", imageUsagePath: "data.tool_usage.image_gen"},
		{usagePath: "data.response.usage", imageUsagePath: "data.response.tool_usage.image_gen"},
	}
	for _, candidate := range candidates {
		if usage, ok := OpenAIUsageFromGJSON(gjson.GetBytes(body, candidate.usagePath)); ok {
			MergeHostedImageGenToolUsage(gjson.GetBytes(body, candidate.imageUsagePath), &usage)
			return usage, true
		}
	}
	return ForwardUsage{}, false
}

// OpenAIResponsesCompletedEventIsEmpty 判断 Responses 终态是否既无用量、错误，也无输出项。
// 同时检查此前累计的用量，避免把 usage 位于更早事件的合法响应误判为静默拒绝。
func OpenAIResponsesCompletedEventIsEmpty(data []byte, usage *ForwardUsage) bool {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return false
	}
	if usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.ImageInputTokens > 0 || usage.ImageOutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0) {
		return false
	}
	if gjson.GetBytes(data, "usage").Exists() || gjson.GetBytes(data, "response.usage").Exists() {
		return false
	}
	if gjson.GetBytes(data, "error").Exists() || gjson.GetBytes(data, "response.error").Exists() {
		return false
	}
	if output := gjson.GetBytes(data, "response.output"); output.Exists() && output.IsArray() && len(output.Array()) > 0 {
		return false
	}
	return true
}

// MergeHostedImageGenToolUsage 补齐 hosted image_generation 工具独立返回的图片 token。
func MergeHostedImageGenToolUsage(imageGen gjson.Result, usage *ForwardUsage) {
	if usage == nil || !imageGen.Exists() || !imageGen.IsObject() {
		return
	}
	if usage.ImageOutputTokens == 0 {
		if value, ok := BoundedJSONNonNegativeInt(imageGen.Get("output_tokens_details.image_tokens")); ok && value > 0 {
			usage.ImageOutputTokens = value
		}
	}
	if usage.ImageInputTokens == 0 {
		if value, ok := BoundedJSONNonNegativeInt(imageGen.Get("input_tokens_details.image_tokens")); ok && value > 0 {
			usage.ImageInputTokens = value
		}
	}
}

func ExtractOpenAIResponseIDFromJSONBytes(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	if id := strings.TrimSpace(gjson.GetBytes(body, "id").String()); id != "" {
		return id
	}
	return strings.TrimSpace(gjson.GetBytes(body, "response.id").String())
}

func OpenAIUsageFromGJSON(value gjson.Result) (ForwardUsage, bool) {
	if !value.Exists() || !value.IsObject() {
		return ForwardUsage{}, false
	}
	// OpenAI Responses 使用 input/output，Chat Completions 使用 prompt/completion，两种都要计费。
	inputTokens := value.Get("input_tokens").Int()
	if inputTokens == 0 {
		inputTokens = value.Get("prompt_tokens").Int()
	}
	outputTokens := value.Get("output_tokens").Int()
	if outputTokens == 0 {
		outputTokens = value.Get("completion_tokens").Int()
	}
	// xAI 可能将 reasoning_tokens 与可见输出 token 分开返回；按 total_tokens
	// 与推理字段的差额判断独立部分，OpenAI 标准 completion_tokens 已包含推理细节。
	reasoningTokens := max(int(FirstPositiveGJSONInt(
		value.Get("completion_tokens_details.reasoning_tokens"),
		value.Get("output_tokens_details.reasoning_tokens"),
	)), 0)
	if reasoningTokens > 0 {
		outputTokens = protocol.IncludeIndependentReasoningTokens(
			inputTokens, outputTokens, value.Get("total_tokens").Int(), int64(reasoningTokens),
		)
	}
	cacheReadTokens := OpenAICacheReadTokensFromUsage(value)
	cacheCreationTokens := OpenAICacheCreationTokensFromUsage(value)
	imageOutputTokens := value.Get("output_tokens_details.image_tokens").Int()
	if imageOutputTokens == 0 {
		imageOutputTokens = value.Get("completion_tokens_details.image_tokens").Int()
	}
	// 图片输入 token（如 gpt-image-2 的 /v1/images/edits 带图请求），
	// 上游在 input_tokens_details.image_tokens 单独回传，用于图/文输入分价计费。
	// 普通文本请求该字段为 0，走原路径行为不变。
	imageInputTokens := FirstPositiveGJSONInt(
		value.Get("input_tokens_details.image_tokens"),
		value.Get("prompt_tokens_details.image_tokens"),
	)
	return ForwardUsage{
		InputTokens:              int(inputTokens),
		ImageInputTokens:         imageInputTokens,
		OutputTokens:             int(outputTokens),
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
		ImageOutputTokens:        int(imageOutputTokens),
	}, true
}

func OpenAICacheReadTokensFromUsage(value gjson.Result) int {
	for _, nested := range []gjson.Result{
		value.Get("input_tokens_details.cached_tokens"),
		value.Get("prompt_tokens_details.cached_tokens"),
	} {
		if nested.Exists() {
			return max(int(nested.Int()), 0)
		}
	}

	return FirstPositiveGJSONInt(
		value.Get("cache_read_input_tokens"),
		value.Get("cache_read_tokens"),
		value.Get("cached_tokens"),
	)
}

func OpenAICacheCreationTokensFromUsage(value gjson.Result) int {
	for _, nested := range []gjson.Result{
		value.Get("input_tokens_details.cache_write_tokens"),
		value.Get("prompt_tokens_details.cache_write_tokens"),
		value.Get("input_tokens_details.cache_creation_tokens"),
		value.Get("prompt_tokens_details.cache_creation_tokens"),
	} {
		if nested.Exists() {
			return max(int(nested.Int()), 0)
		}
	}

	return FirstPositiveGJSONInt(
		value.Get("cache_write_tokens"),
		value.Get("cache_creation_input_tokens"),
		value.Get("cache_write_input_tokens"),
		value.Get("cache_creation_tokens"),
	)
}

// BodyHasSSEFraming 按 SSE 规范检查是否存在以 data: 或 event: 开头的物理行。
// 有效 JSON 字符串中的同名文本不会出现在物理行首，因此不会被误判。
func BodyHasSSEFraming(body []byte) bool {
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if bytes.HasPrefix(line, []byte("data:")) || bytes.HasPrefix(line, []byte("event:")) {
			return true
		}
	}
	return false
}

func ExtractOpenAISSETerminalEvent(body string) (string, []byte, bool) {
	var terminalType string
	var terminalPayload []byte
	ForEachOpenAISSEFrame(body, func(eventType string, data []byte) {
		eventType = EffectiveOpenAISSEEventType(data, eventType)
		switch eventType {
		case "error", "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			terminalType = eventType
			terminalPayload = append([]byte(nil), data...)
		}
	})
	if terminalPayload != nil {
		return terminalType, terminalPayload, true
	}
	return "", nil, false
}

func ExtractCodexFinalResponse(body string) ([]byte, bool) {
	var finalResponse []byte
	ForEachSSEDataPayload(body, func(data []byte) {
		if finalResponse != nil {
			return
		}
		if normalized, changed := NormalizeCompletedImageGenerationStatus(data); changed {
			data = normalized
		}
		eventType := gjson.GetBytes(data, "type").String()
		if eventType == "response.done" || eventType == "response.completed" {
			if response := gjson.GetBytes(data, "response"); response.Exists() && response.Type == gjson.JSON && response.Raw != "" {
				finalResponse = []byte(response.Raw)
			}
		}
	})
	if finalResponse != nil {
		return finalResponse, true
	}
	return nil, false
}

func NormalizeCompletedImageGenerationStatus(data []byte) ([]byte, bool) {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return data, false
	}

	shouldNormalize := func(item gjson.Result) bool {
		if !item.Exists() || !item.IsObject() ||
			strings.TrimSpace(item.Get("type").String()) != "image_generation_call" {
			return false
		}
		switch strings.TrimSpace(item.Get("status").String()) {
		case "generating", "in_progress":
			return strings.TrimSpace(item.Get("result").String()) != ""
		default:
			return false
		}
	}

	eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
	switch eventType {
	case "response.output_item.done":
		if !shouldNormalize(gjson.GetBytes(data, "item")) {
			return data, false
		}
		updated, err := sjson.SetBytes(data, "item.status", "completed")
		if err != nil {
			return data, false
		}
		return updated, true
	case "response.completed", "response.done":
		output := gjson.GetBytes(data, "response.output")
		if !output.Exists() || !output.IsArray() {
			return data, false
		}
		updated := data
		changed := false
		for i, item := range output.Array() {
			if !shouldNormalize(item) {
				continue
			}
			next, err := sjson.SetBytes(updated, "response.output."+strconv.Itoa(i)+".status", "completed")
			if err != nil {
				return data, false
			}
			updated = next
			changed = true
		}
		return updated, changed
	default:
		return data, false
	}
}

func ResponsesStreamEventMayContributeToOutput(eventType string) bool {
	switch eventType {
	case "response.output_text.delta",
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.reasoning_summary_text.delta":
		return true
	default:
		return false
	}
}

func IsResponsesCompactionItemType(itemType string) bool {
	return IsCompactionItemType(itemType)
}

// ResponsesOutputHasCompactionItem 判断响应 JSON 的 output 数组是否已包含
// compaction 条目。
func ResponsesOutputHasCompactionItem(response []byte) bool {
	for _, item := range gjson.GetBytes(response, "output").Array() {
		if IsResponsesCompactionItemType(item.Get("type").String()) {
			return true
		}
	}
	return false
}

// FindRawCompactionItemFromSSE 从原始 SSE 事件流中提取第一个 compaction 类
// item 的 raw JSON：output_item.done 优先，output_item.added 兜底。
func FindRawCompactionItemFromSSE(bodyText string) (json.RawMessage, bool) {
	var found json.RawMessage
	pick := func(eventType string) {
		ForEachSSEDataPayload(bodyText, func(data []byte) {
			if found != nil {
				return
			}
			if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != eventType {
				return
			}
			item := gjson.GetBytes(data, "item")
			if !item.IsObject() || !IsResponsesCompactionItemType(item.Get("type").String()) {
				return
			}
			found = json.RawMessage(item.Raw)
		})
	}
	pick("response.output_item.done")
	if found == nil {
		pick("response.output_item.added")
	}
	return found, found != nil
}

func ExtractImageGenerationOutputFromSSEData(data []byte, seen map[string]struct{}) (json.RawMessage, bool) {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return nil, false
	}
	if gjson.GetBytes(data, "type").String() != "response.output_item.done" {
		return nil, false
	}
	item := gjson.GetBytes(data, "item")
	if !item.Exists() || !item.IsObject() || item.Get("type").String() != "image_generation_call" {
		return nil, false
	}
	if strings.TrimSpace(item.Get("result").String()) == "" {
		return nil, false
	}
	key := strings.TrimSpace(item.Get("id").String())
	if key == "" {
		key = strings.TrimSpace(item.Get("output_format").String()) + "|" + strings.TrimSpace(item.Get("result").String())
	}
	if key != "" && seen != nil {
		if _, exists := seen[key]; exists {
			return nil, false
		}
		seen[key] = struct{}{}
	}
	return json.RawMessage(item.Raw), true
}

func ParseSSEUsageFromBody(body string) *ForwardUsage {
	usage := &ForwardUsage{}
	ForEachSSEDataPayload(body, func(data []byte) {
		ParseSSEUsageBytes(data, usage)
	})
	return usage
}

func ReplaceModelInSSEBody(body, fromModel, toModel string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if _, ok := ExtractSSEDataLine(line); !ok {
			continue
		}
		lines[i] = ReplaceModelInSSELine(line, fromModel, toModel)
	}
	return strings.Join(lines, "\n")
}

func FirstPositiveGJSONInt(values ...gjson.Result) int {
	for _, value := range values {
		if !value.Exists() {
			continue
		}
		n := int(value.Int())
		if n > 0 {
			return n
		}
	}
	return 0
}

// BoundedJSONNonNegativeInt 解析整数形式的 JSON 指数记法，同时避免对上游可控指数使用任意精度解析器。
func BoundedJSONNonNegativeInt(value gjson.Result) (int, bool) {
	if !value.Exists() || value.Type != gjson.Number {
		return 0, false
	}
	raw := value.Raw
	if len(raw) == 0 || len(raw) > 64 || raw[0] == '-' {
		return 0, false
	}

	mantissaEnd := len(raw)
	for i, c := range raw {
		if c != 'e' && c != 'E' {
			continue
		}
		mantissaEnd = i
		break
	}

	digits := raw[:mantissaEnd]
	fractionDigits := 0
	digitCount := 0
	dotSeen := false
	mantissaIsZero := true
	for _, c := range digits {
		switch {
		case c == '.' && !dotSeen:
			dotSeen = true
		case c >= '0' && c <= '9':
			digitCount++
			mantissaIsZero = mantissaIsZero && c == '0'
			if dotSeen {
				fractionDigits++
			}
		default:
			return 0, false
		}
	}

	exponent := 0
	if mantissaEnd < len(raw) {
		exponentRaw := raw[mantissaEnd+1:]
		negative := false
		if len(exponentRaw) > 0 && (exponentRaw[0] == '+' || exponentRaw[0] == '-') {
			negative = exponentRaw[0] == '-'
			exponentRaw = exponentRaw[1:]
		}
		if len(exponentRaw) == 0 {
			return 0, false
		}
		for len(exponentRaw) > 1 && exponentRaw[0] == '0' {
			exponentRaw = exponentRaw[1:]
		}
		for _, digit := range exponentRaw {
			if digit < '0' || digit > '9' {
				return 0, false
			}
		}
		if mantissaIsZero {
			return 0, true
		}
		if len(exponentRaw) > 3 {
			return 0, false
		}
		for _, digit := range exponentRaw {
			exponent = exponent*10 + int(digit-'0')
		}
		if exponent > 100 {
			return 0, false
		}
		if negative {
			exponent = -exponent
		}
	}

	trailingZeros := exponent - fractionDigits
	scaleReduction := 0
	if trailingZeros < 0 {
		scaleReduction = -trailingZeros
		remaining := scaleReduction
		allZeros := true
		for i := len(digits) - 1; i >= 0; i-- {
			if digits[i] == '.' {
				continue
			}
			if digits[i] != '0' {
				allZeros = false
				if remaining > 0 {
					return 0, false
				}
			}
			if remaining > 0 {
				remaining--
			}
		}
		if remaining > 0 {
			if allZeros {
				return 0, true
			}
			return 0, false
		}
	}

	maxInt := int(^uint(0) >> 1)
	parsed := 0
	digitsToAccumulate := digitCount - scaleReduction
	for _, c := range digits {
		if c == '.' {
			continue
		}
		if digitsToAccumulate <= 0 {
			break
		}
		if parsed > (maxInt-int(c-'0'))/10 {
			return 0, false
		}
		parsed = parsed*10 + int(c-'0')
		digitsToAccumulate--
	}
	if trailingZeros < 0 {
		return parsed, true
	}
	for ; trailingZeros > 0; trailingZeros-- {
		if parsed > maxInt/10 {
			return 0, false
		}
		parsed *= 10
	}
	return parsed, true
}

func OpenAIStreamEventTypeIsTerminal(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
		return true
	default:
		return false
	}
}

func ForEachOpenAISSEFrame(body string, fn func(string, []byte)) {
	if fn == nil || strings.TrimSpace(body) == "" {
		return
	}
	var parser OpenAICompatSSEFrameParser
	emit := func(frame OpenAICompatSSEFrame, ok bool) {
		if !ok {
			return
		}
		emitData := func(value string) {
			value = strings.TrimSpace(value)
			if value == "" || value == "[DONE]" {
				return
			}
			data := []byte(value)
			fn(EffectiveOpenAISSEEventType(data, frame.EventType), data)
		}
		if gjson.Valid(frame.Data) {
			emitData(frame.Data)
			return
		}
		for _, value := range strings.Split(frame.Data, "\n") {
			emitData(value)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		emit(parser.AddLine(strings.TrimRight(line, "\r")))
	}
	emit(parser.Finish())
}

// EffectiveOpenAISSEEventType 优先使用 payload 内的 type，兼容 event 行与 data 行分离的 SSE。
func EffectiveOpenAISSEEventType(payload []byte, eventType string) string {
	if value := strings.TrimSpace(gjson.GetBytes(payload, "type").String()); value != "" {
		return value
	}
	return strings.TrimSpace(eventType)
}

// ParseSSEUsageBytesWithType 兼容带 event 类型的用量解析入口。
// 旧实现没有 event 参数，因此先复用原有解析器，保持已有字段合并语义。
func ParseSSEUsageBytesWithType(data []byte, eventType string, usage *ForwardUsage) bool {
	if usage == nil || len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return false
	}
	parsedUsage, ok := ExtractOpenAIUsageFromJSONBytes(data)
	if !ok {
		return false
	}
	if OpenAIStreamEventTypeIsTerminal(EffectiveOpenAISSEEventType(data, eventType)) {
		// 某些兼容上游会在 completed 事件附带全零占位 usage；该占位不能
		// 覆盖此前已收到的真实用量。终态只在至少有一个非零字段时生效。
		if OpenAIUsageHasTokens(&parsedUsage) {
			*usage = parsedUsage
		}
		return true
	}
	MergeOpenAIUsageNonZero(usage, parsedUsage)
	return true
}

func OpenAIUsageHasTokens(usage *ForwardUsage) bool {
	return usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.ImageOutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0 || usage.ImageInputTokens > 0)
}

func ReplaceModelInResponseBody(body []byte, fromModel, toModel string) []byte {
	// 使用 gjson/sjson 精确替换 model 字段，避免全量 JSON 反序列化
	if m := gjson.GetBytes(body, "model"); m.Exists() && m.Str == fromModel {
		newBody, err := sjson.SetBytes(body, "model", toModel)
		if err != nil {
			return body
		}
		return newBody
	}
	return body
}
