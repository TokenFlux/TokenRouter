// 原生流转换保持唯一实现，输入输出通过显式适配器传递。
package qoder

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	QoderDefaultMaxTokens          = 32768
	QoderStreamTimeout             = 15 * time.Minute
	QoderKeepaliveEvery            = 10 * time.Second
	QoderConversationTTL           = 2 * time.Hour
	QoderRefreshLockPoll           = 100 * time.Millisecond
	QoderRefreshLockWait           = 3 * time.Second
	QoderAccountStateUpdateTimeout = 5 * time.Second

	QoderTextToolCallStart = "<tool_call>"
	QoderTextToolCallEnd   = "</tool_call>"
	QoderTextArgKeyStart   = "<arg_key>"
	QoderTextArgKeyEnd     = "</arg_key>"
	QoderTextArgValueStart = "<arg_value>"
	QoderTextArgValueEnd   = "</arg_value>"

	QoderDSMLToolCallsStart = "<｜｜DSML｜｜tool_calls>"
	QoderDSMLToolCallsEnd   = "</｜｜DSML｜｜tool_calls>"
	QoderDSMLInvokeStart    = "<｜｜DSML｜｜invoke"
	QoderDSMLInvokeEnd      = "</｜｜DSML｜｜invoke>"
	QoderDSMLParameterStart = "<｜｜DSML｜｜parameter"
	QoderDSMLParameterEnd   = "</｜｜DSML｜｜parameter>"
)

// DefaultQoderModelAliases 将兜底的 TokenRouter 请求侧 alias 映射到 Qoder API key。
// 已配置 model_mapping 的 Qoder 账号以账号配置为准，此表仅作为兜底路由和默认展示面。
var DefaultQoderModelAliases = map[string]QoderModelInfo{
	// 通过加密 reasoning metadata 确认该路由为 Claude Opus 4.6。
	"claude-opus-4-6": {Key: "ultimate", Source: "system", Provider: "Claude", Notes: "Confirmed Claude Opus 4.6 via encrypted reasoning metadata.", DisplayName: "Claude Opus 4.6"},
	// Qoder 自动选择路由，具体上游模型动态变化且未确认。
	"auto": {Key: "auto", Source: "system", Provider: "Qoder", Notes: "Qoder-selected route; exact upstream model is dynamic and unconfirmed.", DisplayName: "Qoder Auto"},
	// Qoder performance/efficient/lite tier 目前没有确认到具体供应商模型。
	"performance": {Key: "performance", Source: "system", Provider: "Qoder", Notes: "Qoder performance tier; exact upstream model is unconfirmed.", DisplayName: "Qoder Performance"},
	"efficient":   {Key: "efficient", Source: "system", Provider: "Qoder", Notes: "Qoder efficient tier; exact upstream model is unconfirmed.", DisplayName: "Qoder Efficient"},
	// Qoder lite tier 尚未验证，观测结果不完全一致。
	"lite": {Key: "lite", Source: "system", Provider: "Qoder", Notes: "Unverified Qoder lite tier; observations are mixed.", DisplayName: "Qoder Lite"},
	// Qoder UI 暴露的是这些供应商模型名，这里把可读公开 alias 映射到内部 route key。
	"qwen3.8-max":       {Key: "qmodel_38max", Source: "system", Provider: "Qwen", Notes: "Qoder UI model name Qwen3.8-Max.", DisplayName: "Qwen3.8-Max"},
	"qwen3.7-max":       {Key: "qmodel_latest", Source: "system", Provider: "Qwen", Notes: "Qoder UI model name Qwen3.7-Max.", DisplayName: "Qwen3.7-Max"},
	"qwen3.7-plus":      {Key: "qmodel", Source: "system", Provider: "Qwen", Notes: "Qoder UI model name Qwen3.7-Plus.", DisplayName: "Qwen3.7-Plus"},
	"qwen3.6-flash":     {Key: "q36fmodel", Source: "system", Provider: "Qwen", Notes: "Qoder CN UI model name Qwen3.6-Flash.", DisplayName: "Qwen3.6-Flash"},
	"deepseek-v4-pro":   {Key: "dmodel", Source: "system", Provider: "DeepSeek", Notes: "Qoder UI model name DeepSeek-V4-Pro.", DisplayName: "DeepSeek-V4-Pro"},
	"deepseek-v4-flash": {Key: "dfmodel", Source: "system", Provider: "DeepSeek", Notes: "Qoder UI model name DeepSeek-V4-Flash.", DisplayName: "DeepSeek-V4-Flash"},
	"glm-5.3":           {Key: "gmodel", Source: "system", Provider: "GLM", Notes: "Qoder UI model name GLM-5.3.", DisplayName: "GLM-5.3"},
	"glm-5.2":           {Key: "gm51model", Source: "system", Provider: "GLM", Notes: "Qoder UI model name GLM-5.2.", DisplayName: "GLM-5.2"},
	// Qoder 1.15.0 起同时展示 Kimi-K3 与 Kimi-K2.7-Code，两者使用不同路由 key。
	"kimi-k3":        {Key: "kmodel_latest", Source: "system", Provider: "Kimi", Notes: "Qoder UI model name Kimi-K3.", DisplayName: "Kimi-K3"},
	"kimi-k2.7-code": {Key: "kmodel", Source: "system", Provider: "Kimi", Notes: "Qoder UI model name Kimi-K2.7-Code.", DisplayName: "Kimi-K2.7-Code"},
	"minimax-m3":     {Key: "mmodel", Source: "system", Provider: "MiniMax", Notes: "Qoder UI model name MiniMax-M3.", DisplayName: "MiniMax-M3"},
	"minimax-m2.7":   {Key: "mmodel", Source: "system", Provider: "MiniMax", Notes: "Qoder CN UI model name MiniMax-M2.7.", DisplayName: "MiniMax-M2.7"},
}

type QoderModelInfo struct {
	Key         string `json:"key"`
	Source      string `json:"source"`
	Provider    string `json:"provider,omitempty"`
	Notes       string `json:"notes,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type QoderMessage struct {
	Role        string
	Text        string
	ToolCallID  string
	Raw         map[string]any
	TextContent []map[string]any
}

func QoderMessageHasToolCalls(message QoderMessage) bool {
	return len(QoderToolCallNamesFromRaw(message.Raw)) > 0
}

func QoderMessageToolCallID(message QoderMessage) string {
	return FirstNonEmptyQoder(
		message.ToolCallID,
		QoderStringField(message.Raw, "tool_call_id"),
		QoderStringField(message.Raw, "tool_call_call_id"),
		QoderStringField(message.Raw, "call_id"),
	)
}

func QoderToolCallNamesFromRaw(raw map[string]any) map[string]string {
	names := map[string]string{}
	if len(raw) == 0 {
		return names
	}
	for _, rawTool := range QoderAnySlice(raw["tool_calls"]) {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		function, _ := tool["function"].(map[string]any)
		id := FirstNonEmptyQoder(
			QoderStringField(tool, "id"),
			QoderStringField(tool, "tool_call_id"),
			QoderStringField(tool, "call_id"),
		)
		name := FirstNonEmptyQoder(
			QoderStringField(function, "name"),
			QoderStringField(tool, "name"),
			QoderStringField(tool, "tool_name"),
		)
		if id != "" && name != "" {
			names[id] = name
		}
	}
	for _, rawBlock := range QoderAnySlice(raw["content"]) {
		block, ok := rawBlock.(map[string]any)
		if !ok || block["type"] != "tool_use" {
			continue
		}
		id := QoderStringField(block, "id")
		name := QoderStringField(block, "name")
		if id != "" && name != "" {
			names[id] = name
		}
	}
	return names
}

func QoderToolArgumentsString(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.RawMessage:
		return string(v)
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(body)
	}
}

func ReadQoderSSEEvents(resp *http.Response) ([]SSEEvent, error) {
	return ReadQoderSSEEventsContext(context.Background(), resp, nil)
}

func ReadQoderSSEEventsContext(ctx context.Context, resp *http.Response, keepalive func() error) ([]SSEEvent, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("qoder response body is nil")
	}
	events := make([]SSEEvent, 0)
	if err := StreamQoderEvents(ctx, resp, func(event SSEEvent) error {
		events = append(events, event)
		return nil
	}, keepalive); err != nil {
		return nil, err
	}
	return events, nil
}

func QoderNonStreamingKeepalive(c *upstream.OutputContext) func() error {
	if c == nil || c.Writer == nil {
		return nil
	}
	header := c.Writer.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	return func() error {
		if !c.Writer.Written() {
			c.Writer.WriteHeader(http.StatusOK)
		}
		// 非流式客户端会把最终 body 当作 JSON 解析；空白字符可以保持连接活跃，
		// 同时不会破坏 JSON 文档。
		_, err := io.WriteString(c.Writer, "\n")
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return err
	}
}

func WriteQoderStreamKeepalive(c *upstream.OutputContext, started bool) error {
	if c == nil || c.Writer == nil || !started {
		return nil
	}
	_, err := io.WriteString(c.Writer, ": keep-alive\n\n")
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return err
}

func WriteQoderOpenAIStream(c *upstream.OutputContext, model string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) error {
	events = NormalizeQoderTextToolCallEvents(events)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	completionID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	if err := WriteSSEData(c.Writer, OpenAIChunk(completionID, model, map[string]any{"role": "assistant"}, nil)); err != nil {
		return err
	}
	usage := upstream.TokenUsage{}
	totalTokens := 0
	toolCalls := NewQoderOpenAIToolCallAccumulator(toolNameMappers...)
	for _, event := range events {
		if event.HasUsage {
			MergeQoderUsageEvent(&usage, event)
			totalTokens = event.TotalTokens
			if err := WriteSSEData(c.Writer, OpenAIUsageChunk(completionID, model, usage, totalTokens, event.UsageDetails)); err != nil {
				return err
			}
			continue
		}
		if event.IsDone {
			finishReason := "stop"
			if toolCalls.HasToolCalls() {
				finishReason = "tool_calls"
			}
			if err := WriteSSEData(c.Writer, OpenAIChunk(completionID, model, map[string]any{}, finishReason)); err != nil {
				return err
			}
			_, err := io.WriteString(c.Writer, "data: [DONE]\n\n")
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			return err
		}
		if event.Type == "text_delta" && event.Text != "" {
			if err := WriteSSEData(c.Writer, OpenAIChunk(completionID, model, map[string]any{"content": event.Text}, nil)); err != nil {
				return err
			}
		}
		if event.Type == "tool_call_delta" {
			deltas := toolCalls.AppendDelta(event)
			if len(deltas) == 0 {
				continue
			}
			if err := WriteSSEData(c.Writer, OpenAIChunk(completionID, model, map[string]any{"tool_calls": deltas}, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}

type QoderStreamResult struct {
	// HasUsage 标记上游已明确提供计量，显式零值与缺失分开。
	HasUsage     bool
	Usage        upstream.TokenUsage
	UsageDetails UsageDetails
	TotalTokens  int
	HasOutput    bool
}

type QoderStreamWriteTracker struct {
	disconnected bool
}

type QoderDisconnectAwareWriter struct {
	writer  io.Writer
	tracker *QoderStreamWriteTracker
}

func (w QoderDisconnectAwareWriter) Write(p []byte) (int, error) {
	if w.tracker != nil && w.tracker.disconnected {
		return len(p), nil
	}
	if w.writer == nil {
		if w.tracker != nil {
			w.tracker.disconnected = true
		}
		return len(p), nil
	}
	n, err := w.writer.Write(p)
	if err != nil {
		if w.tracker != nil {
			w.tracker.disconnected = true
		}
		return len(p), nil
	}
	return n, nil
}

func (w QoderDisconnectAwareWriter) Flush() {
	if w.tracker != nil && w.tracker.disconnected {
		return
	}
	if flusher, ok := w.writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

type QoderToolNameMapper func(string) string

type QoderOpenAIStreamResponseOption struct {
	mapUsage     func(upstream.TokenUsage) upstream.TokenUsage
	mapToolName  QoderToolNameMapper
	includeUsage bool
}

type QoderAnthropicStreamResponseOption struct {
	mapUsage    func(upstream.TokenUsage) upstream.TokenUsage
	mapToolName QoderToolNameMapper
}

type QoderResponsesStreamResponseOption struct {
	mapUsage    func(upstream.TokenUsage) upstream.TokenUsage
	mapToolName QoderToolNameMapper
	responseID  string
}

func QoderOpenAIStreamUsageMapper(mapper func(upstream.TokenUsage) upstream.TokenUsage) QoderOpenAIStreamResponseOption {
	return QoderOpenAIStreamResponseOption{mapUsage: mapper}
}

func QoderOpenAIStreamToolNameMapper(mapper QoderToolNameMapper) QoderOpenAIStreamResponseOption {
	return QoderOpenAIStreamResponseOption{mapToolName: mapper}
}

func QoderOpenAIStreamIncludeUsage(include bool) QoderOpenAIStreamResponseOption {
	return QoderOpenAIStreamResponseOption{includeUsage: include}
}

func QoderAnthropicStreamUsageMapper(mapper func(upstream.TokenUsage) upstream.TokenUsage) QoderAnthropicStreamResponseOption {
	return QoderAnthropicStreamResponseOption{mapUsage: mapper}
}

func QoderAnthropicStreamToolNameMapper(mapper QoderToolNameMapper) QoderAnthropicStreamResponseOption {
	return QoderAnthropicStreamResponseOption{mapToolName: mapper}
}

func QoderResponsesStreamUsageMapper(mapper func(upstream.TokenUsage) upstream.TokenUsage) QoderResponsesStreamResponseOption {
	return QoderResponsesStreamResponseOption{mapUsage: mapper}
}

func QoderResponsesStreamToolNameMapper(mapper QoderToolNameMapper) QoderResponsesStreamResponseOption {
	return QoderResponsesStreamResponseOption{mapToolName: mapper}
}

func QoderResponsesStreamResponseID(responseID string) QoderResponsesStreamResponseOption {
	return QoderResponsesStreamResponseOption{responseID: responseID}
}

func QoderOpenAIStreamResponseOptions(options []QoderOpenAIStreamResponseOption) (func(upstream.TokenUsage) upstream.TokenUsage, QoderToolNameMapper, bool) {
	var usageMapper func(upstream.TokenUsage) upstream.TokenUsage
	var toolNameMapper QoderToolNameMapper
	includeUsage := false
	for _, option := range options {
		if option.mapUsage != nil && usageMapper == nil {
			usageMapper = option.mapUsage
		}
		if option.mapToolName != nil && toolNameMapper == nil {
			toolNameMapper = option.mapToolName
		}
		includeUsage = includeUsage || option.includeUsage
	}
	return usageMapper, toolNameMapper, includeUsage
}

func QoderAnthropicStreamResponseOptions(options []QoderAnthropicStreamResponseOption) (func(upstream.TokenUsage) upstream.TokenUsage, QoderToolNameMapper) {
	var usageMapper func(upstream.TokenUsage) upstream.TokenUsage
	var toolNameMapper QoderToolNameMapper
	for _, option := range options {
		if option.mapUsage != nil && usageMapper == nil {
			usageMapper = option.mapUsage
		}
		if option.mapToolName != nil && toolNameMapper == nil {
			toolNameMapper = option.mapToolName
		}
	}
	return usageMapper, toolNameMapper
}

func QoderResponsesStreamResponseOptions(options []QoderResponsesStreamResponseOption) (func(upstream.TokenUsage) upstream.TokenUsage, QoderToolNameMapper, string) {
	var usageMapper func(upstream.TokenUsage) upstream.TokenUsage
	var toolNameMapper QoderToolNameMapper
	var responseID string
	for _, option := range options {
		if option.mapUsage != nil && usageMapper == nil {
			usageMapper = option.mapUsage
		}
		if option.mapToolName != nil && toolNameMapper == nil {
			toolNameMapper = option.mapToolName
		}
		if option.responseID != "" && responseID == "" {
			responseID = option.responseID
		}
	}
	return usageMapper, toolNameMapper, responseID
}

type QoderTextToolCallTransformer struct {
	buffer       string
	nextToolCall int
}

func NewQoderTextToolCallTransformer() *QoderTextToolCallTransformer {
	return &QoderTextToolCallTransformer{}
}

func NormalizeQoderTextToolCallEvents(events []SSEEvent) []SSEEvent {
	if len(events) == 0 {
		return events
	}
	transformer := NewQoderTextToolCallTransformer()
	out := make([]SSEEvent, 0, len(events))
	for _, event := range events {
		out = append(out, transformer.Append(event)...)
	}
	out = append(out, transformer.Flush()...)
	return out
}

func (t *QoderTextToolCallTransformer) Append(event SSEEvent) []SSEEvent {
	if t == nil {
		return []SSEEvent{event}
	}
	if event.Type == "text_delta" {
		if event.Text == "" {
			return nil
		}
		t.buffer += event.Text
		return t.drain(false)
	}
	out := t.drain(true)
	out = append(out, event)
	return out
}

func (t *QoderTextToolCallTransformer) Flush() []SSEEvent {
	if t == nil {
		return nil
	}
	return t.drain(true)
}

func (t *QoderTextToolCallTransformer) drain(final bool) []SSEEvent {
	out := make([]SSEEvent, 0)
	for t.buffer != "" {
		start, marker := QoderTextToolCallMarkerStart(t.buffer)
		if start < 0 {
			if final {
				out = append(out, SSEEvent{Type: "text_delta", Text: t.buffer})
				t.buffer = ""
				return out
			}
			suffixLen := QoderToolCallStartSuffixLen(t.buffer)
			text := t.buffer
			if suffixLen > 0 {
				text = t.buffer[:len(t.buffer)-suffixLen]
				t.buffer = t.buffer[len(t.buffer)-suffixLen:]
			} else {
				t.buffer = ""
			}
			if text != "" {
				out = append(out, SSEEvent{Type: "text_delta", Text: text})
			}
			return out
		}
		if start > 0 {
			out = append(out, SSEEvent{Type: "text_delta", Text: t.buffer[:start]})
			t.buffer = t.buffer[start:]
			continue
		}

		startTag, endTag := QoderTextToolCallMarkerTags(marker)
		end := strings.Index(t.buffer[len(startTag):], endTag)
		if end < 0 {
			if final {
				out = append(out, SSEEvent{Type: "text_delta", Text: t.buffer})
				t.buffer = ""
			}
			return out
		}
		segmentEnd := len(startTag) + end + len(endTag)
		segment := t.buffer[:segmentEnd]
		if events, ok := t.parseToolCallSegment(segment, marker); ok {
			out = append(out, events...)
		} else {
			out = append(out, SSEEvent{Type: "text_delta", Text: segment})
		}
		t.buffer = t.buffer[segmentEnd:]
	}
	return out
}

func QoderTextToolCallMarkerStart(text string) (int, string) {
	xmlStart := strings.Index(text, QoderTextToolCallStart)
	dsmlStart := strings.Index(text, QoderDSMLToolCallsStart)
	if xmlStart < 0 {
		return dsmlStart, QoderDSMLToolCallsStart
	}
	if dsmlStart < 0 || xmlStart < dsmlStart {
		return xmlStart, QoderTextToolCallStart
	}
	return dsmlStart, QoderDSMLToolCallsStart
}

func QoderTextToolCallMarkerTags(marker string) (string, string) {
	if marker == QoderDSMLToolCallsStart {
		return QoderDSMLToolCallsStart, QoderDSMLToolCallsEnd
	}
	return QoderTextToolCallStart, QoderTextToolCallEnd
}

func QoderToolCallStartSuffixLen(text string) int {
	maxSuffix := 0
	for _, marker := range []string{QoderTextToolCallStart, QoderDSMLToolCallsStart} {
		limit := min(len(marker)-1, len(text))
		for i := limit; i > 0; i-- {
			if strings.HasPrefix(marker, text[len(text)-i:]) && i > maxSuffix {
				maxSuffix = i
			}
		}
	}
	return maxSuffix
}

func (t *QoderTextToolCallTransformer) parseToolCallSegment(segment string, marker string) ([]SSEEvent, bool) {
	if marker == QoderDSMLToolCallsStart {
		return t.parseDSMLToolCalls(segment)
	}
	event, ok := t.parseToolCall(segment)
	if !ok {
		return nil, false
	}
	return []SSEEvent{event}, true
}

func (t *QoderTextToolCallTransformer) parseToolCall(segment string) (SSEEvent, bool) {
	inner := strings.TrimPrefix(segment, QoderTextToolCallStart)
	inner = strings.TrimSuffix(inner, QoderTextToolCallEnd)
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return SSEEvent{}, false
	}
	if event, ok := t.parseJSONToolCall(inner, segment); ok {
		return event, true
	}

	firstArgTag := len(inner)
	for _, tag := range []string{QoderTextArgValueStart, QoderTextArgKeyStart} {
		if idx := strings.Index(inner, tag); idx >= 0 && idx < firstArgTag {
			firstArgTag = idx
		}
	}
	name := strings.TrimSpace(html.UnescapeString(inner[:firstArgTag]))
	if name == "" {
		return SSEEvent{}, false
	}

	args := map[string]any{}
	rest := inner[firstArgTag:]
	for {
		keyOpen := strings.Index(rest, QoderTextArgKeyStart)
		if keyOpen < 0 {
			break
		}
		keyStart := keyOpen + len(QoderTextArgKeyStart)
		keyEnd := strings.Index(rest[keyStart:], QoderTextArgKeyEnd)
		if keyEnd < 0 {
			return SSEEvent{}, false
		}
		key := strings.TrimSpace(html.UnescapeString(rest[keyStart : keyStart+keyEnd]))
		afterKey := rest[keyStart+keyEnd+len(QoderTextArgKeyEnd):]

		valueOpen := strings.Index(afterKey, QoderTextArgValueStart)
		if valueOpen < 0 {
			return SSEEvent{}, false
		}
		valueStart := valueOpen + len(QoderTextArgValueStart)
		valueEnd := strings.Index(afterKey[valueStart:], QoderTextArgValueEnd)
		if valueEnd < 0 {
			return SSEEvent{}, false
		}
		if key != "" {
			args[key] = html.UnescapeString(afterKey[valueStart : valueStart+valueEnd])
		}
		rest = afterKey[valueStart+valueEnd+len(QoderTextArgValueEnd):]
	}

	arguments, err := json.Marshal(args)
	if err != nil {
		return SSEEvent{}, false
	}
	index := t.nextToolCall
	id := QoderTextToolCallID(index, segment)
	t.nextToolCall++
	return SSEEvent{
		Type:             "tool_call_delta",
		ToolCallID:       id,
		ToolCallIndex:    index,
		HasToolCallIndex: true,
		ToolType:         "function",
		ToolName:         name,
		Arguments:        string(arguments),
	}, true
}

func (t *QoderTextToolCallTransformer) parseJSONToolCall(inner string, segment string) (SSEEvent, bool) {
	decodedText := strings.TrimSpace(html.UnescapeString(inner))
	if !strings.HasPrefix(decodedText, "{") {
		return SSEEvent{}, false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(decodedText), &payload); err != nil {
		return SSEEvent{}, false
	}
	function, _ := payload["function"].(map[string]any)
	name := FirstNonEmptyQoder(
		QoderStringField(function, "name"),
		QoderStringField(payload, "name"),
		QoderStringField(payload, "tool"),
		QoderStringField(payload, "tool_name"),
	)
	if name == "" {
		return SSEEvent{}, false
	}
	arguments := FirstPresentQoderToolArguments(
		function["arguments"],
		payload["arguments"],
		payload["input"],
		payload["parameters"],
		QoderInlineToolArguments(payload),
	)
	name, arguments = NormalizeQoderTextToolCallNameAndArguments(name, arguments)
	index := t.nextToolCall
	id := FirstNonEmptyQoder(
		QoderStringField(payload, "id"),
		QoderStringField(payload, "tool_call_id"),
		QoderStringField(payload, "call_id"),
		QoderTextToolCallID(index, segment),
	)
	t.nextToolCall++
	return SSEEvent{
		Type:             "tool_call_delta",
		ToolCallID:       id,
		ToolCallIndex:    index,
		HasToolCallIndex: true,
		ToolType:         FirstNonEmptyQoder(QoderStringField(payload, "type"), "function"),
		ToolName:         name,
		Arguments:        arguments,
	}, true
}

func FirstPresentQoderToolArguments(values ...any) string {
	for _, value := range values {
		if value == nil {
			continue
		}
		arguments := QoderToolArgumentsString(value)
		if strings.TrimSpace(arguments) != "" {
			return arguments
		}
	}
	return "{}"
}

func QoderInlineToolArguments(payload map[string]any) any {
	if len(payload) == 0 {
		return nil
	}
	args := map[string]any{}
	for _, key := range []string{"command", "cmd", "description"} {
		if value, ok := payload[key]; ok {
			args[key] = value
		}
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

func NormalizeQoderTextToolCallNameAndArguments(name string, arguments string) (string, string) {
	originalName := strings.TrimSpace(name)
	normalizedName := originalName
	if strings.EqualFold(originalName, "shell") || strings.EqualFold(originalName, "execute_bash") {
		normalizedName = "Bash"
	}
	if normalizedName != "Bash" {
		return normalizedName, arguments
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		command := strings.TrimSpace(arguments)
		if command == "" || command == "{}" {
			return normalizedName, "{}"
		}
		if strings.HasPrefix(command, "{") || strings.HasPrefix(command, "[") {
			return normalizedName, arguments
		}
		body, _ := json.Marshal(map[string]any{"command": command})
		return normalizedName, string(body)
	}
	if _, ok := args["command"]; !ok {
		if cmd, ok := args["cmd"]; ok {
			args["command"] = cmd
			delete(args, "cmd")
		}
	}
	body, err := json.Marshal(args)
	if err != nil {
		return normalizedName, arguments
	}
	return normalizedName, string(body)
}

func (t *QoderTextToolCallTransformer) parseDSMLToolCalls(segment string) ([]SSEEvent, bool) {
	inner := strings.TrimPrefix(segment, QoderDSMLToolCallsStart)
	inner = strings.TrimSuffix(inner, QoderDSMLToolCallsEnd)
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return nil, false
	}
	events := make([]SSEEvent, 0)
	rest := inner
	for {
		start := strings.Index(rest, QoderDSMLInvokeStart)
		if start < 0 {
			break
		}
		rest = rest[start:]
		openEnd := strings.Index(rest, ">")
		if openEnd < 0 {
			return nil, false
		}
		closeStart := strings.Index(rest[openEnd+1:], QoderDSMLInvokeEnd)
		if closeStart < 0 {
			return nil, false
		}
		openTag := rest[:openEnd+1]
		body := rest[openEnd+1 : openEnd+1+closeStart]
		if event, ok := t.parseDSMLInvoke(openTag, body, segment); ok {
			events = append(events, event)
		}
		rest = rest[openEnd+1+closeStart+len(QoderDSMLInvokeEnd):]
	}
	return events, len(events) > 0
}

func (t *QoderTextToolCallTransformer) parseDSMLInvoke(openTag string, body string, segment string) (SSEEvent, bool) {
	name := QoderTagAttribute(openTag, "name")
	if name == "" {
		return SSEEvent{}, false
	}
	args := map[string]any{}
	rest := body
	for {
		start := strings.Index(rest, QoderDSMLParameterStart)
		if start < 0 {
			break
		}
		rest = rest[start:]
		openEnd := strings.Index(rest, ">")
		if openEnd < 0 {
			return SSEEvent{}, false
		}
		closeStart := strings.Index(rest[openEnd+1:], QoderDSMLParameterEnd)
		if closeStart < 0 {
			return SSEEvent{}, false
		}
		openParam := rest[:openEnd+1]
		paramName := QoderTagAttribute(openParam, "name")
		if paramName != "" {
			args[paramName] = html.UnescapeString(rest[openEnd+1 : openEnd+1+closeStart])
		}
		rest = rest[openEnd+1+closeStart+len(QoderDSMLParameterEnd):]
	}
	if len(args) == 0 {
		return SSEEvent{}, false
	}
	arguments, err := json.Marshal(args)
	if err != nil {
		return SSEEvent{}, false
	}
	name, normalizedArguments := NormalizeQoderTextToolCallNameAndArguments(name, string(arguments))
	index := t.nextToolCall
	t.nextToolCall++
	return SSEEvent{
		Type:             "tool_call_delta",
		ToolCallID:       QoderTextToolCallID(index, segment),
		ToolCallIndex:    index,
		HasToolCallIndex: true,
		ToolType:         "function",
		ToolName:         name,
		Arguments:        normalizedArguments,
	}, true
}

func QoderTagAttribute(tag string, name string) string {
	pattern := name + `="`
	start := strings.Index(tag, pattern)
	if start < 0 {
		return ""
	}
	start += len(pattern)
	end := strings.Index(tag[start:], `"`)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(tag[start : start+end]))
}

func NormalizeQoderOutboundToolCallEvent(event SSEEvent) SSEEvent {
	if strings.TrimSpace(event.ToolName) == "" {
		return event
	}
	event.ToolName, event.Arguments = NormalizeQoderTextToolCallNameAndArguments(event.ToolName, event.Arguments)
	return event
}

func QoderTextToolCallID(index int, segment string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%d\n%s", index, segment)))
	return fmt.Sprintf("call_%x", sum[:12])
}

type QoderOpenAIToolCallAccumulator struct {
	calls               []QoderOpenAIToolCallState
	slotByUpstreamIndex map[int]int
	mapToolName         QoderToolNameMapper
}

type QoderOpenAIToolCallState struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

func NewQoderOpenAIToolCallAccumulator(toolNameMappers ...QoderToolNameMapper) *QoderOpenAIToolCallAccumulator {
	var mapper QoderToolNameMapper
	for _, candidate := range toolNameMappers {
		if candidate != nil {
			mapper = candidate
			break
		}
	}
	return &QoderOpenAIToolCallAccumulator{mapToolName: mapper}
}

func (a *QoderOpenAIToolCallAccumulator) AppendDelta(event SSEEvent) []any {
	if a == nil {
		return []any{}
	}
	event = NormalizeQoderOutboundToolCallEvent(event)
	if QoderToolCallDeltaIsEmptyPlaceholder(event) {
		return []any{}
	}
	if event.ToolCallID == "" && !event.HasToolCallIndex && event.ToolName == "" && event.ToolType == "" && event.Arguments != "" && len(a.calls) > 1 {
		return []any{}
	}
	index := a.resolveIndex(event)
	for len(a.calls) <= index {
		a.calls = append(a.calls, QoderOpenAIToolCallState{Type: "function"})
	}
	state := &a.calls[index]
	if event.ToolCallID != "" {
		state.ID = event.ToolCallID
	}
	if event.ToolType != "" {
		state.Type = event.ToolType
	} else if state.Type == "" {
		state.Type = "function"
	}
	if event.ToolName != "" {
		state.Name = a.toolName(event.ToolName)
	}
	if event.Arguments != "" {
		state.Arguments = MergeQoderToolArguments(state.Arguments, event.Arguments)
	}
	a.bindUpstreamIndex(event, index)
	return []any{QoderOpenAIToolCallDelta(index, a.mapEventToolName(event))}
}

func (a *QoderOpenAIToolCallAccumulator) toolName(name string) string {
	if a == nil || a.mapToolName == nil || strings.TrimSpace(name) == "" {
		return name
	}
	mapped := strings.TrimSpace(a.mapToolName(name))
	if mapped == "" {
		return name
	}
	return mapped
}

func (a *QoderOpenAIToolCallAccumulator) mapEventToolName(event SSEEvent) SSEEvent {
	if event.ToolName != "" {
		event.ToolName = a.toolName(event.ToolName)
	}
	return event
}

func (a *QoderOpenAIToolCallAccumulator) Calls() []any {
	if a == nil || len(a.calls) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(a.calls))
	for _, call := range a.calls {
		if call.ID == "" && call.Name == "" && call.Arguments == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":   call.ID,
			"type": FirstNonEmptyQoder(call.Type, "function"),
			"function": map[string]any{
				"name":      call.Name,
				"arguments": call.Arguments,
			},
		})
	}
	return out
}

func (a *QoderOpenAIToolCallAccumulator) HasToolCalls() bool {
	return len(a.Calls()) > 0
}

func (a *QoderOpenAIToolCallAccumulator) resolveIndex(event SSEEvent) int {
	if event.ToolCallID != "" {
		for i := range a.calls {
			if a.calls[i].ID == event.ToolCallID {
				return i
			}
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 {
		if index, ok := a.slotByUpstreamIndex[event.ToolCallIndex]; ok && index >= 0 && index < len(a.calls) {
			if event.ToolCallID == "" || a.calls[index].ID == "" || a.calls[index].ID == event.ToolCallID {
				if a.shouldStartNewToolCallInSlot(event, index) {
					return len(a.calls)
				}
				return index
			}
			if event.ToolCallID != "" && a.calls[index].ID != "" && a.calls[index].ID != event.ToolCallID {
				return len(a.calls)
			}
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 && event.ToolCallID == "" && (event.ToolName != "" || event.ToolType != "") {
		if event.ToolCallIndex < len(a.calls) && a.shouldStartNewToolCallInSlot(event, event.ToolCallIndex) {
			return len(a.calls)
		}
		return event.ToolCallIndex
	}
	if a.shouldStartImplicitToolCall(event) {
		return len(a.calls)
	}
	if len(a.calls) > 0 {
		last := &a.calls[len(a.calls)-1]
		if event.ToolCallID == "" || last.ID == "" || last.ID == event.ToolCallID {
			return len(a.calls) - 1
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 {
		return event.ToolCallIndex
	}
	if len(a.calls) == 0 {
		return 0
	}
	return len(a.calls)
}

func (a *QoderOpenAIToolCallAccumulator) shouldStartImplicitToolCall(event SSEEvent) bool {
	if a == nil || len(a.calls) == 0 || event.ToolCallID != "" || event.HasToolCallIndex {
		return false
	}
	return a.shouldStartNewToolCallInSlot(event, len(a.calls)-1)
}

func (a *QoderOpenAIToolCallAccumulator) shouldStartNewToolCallInSlot(event SSEEvent, index int) bool {
	if a == nil || index < 0 || index >= len(a.calls) || event.ToolCallID != "" {
		return false
	}
	if event.ToolName == "" && event.ToolType == "" {
		return false
	}
	existing := a.calls[index]
	if existing.ID == "" && existing.Name == "" && existing.Arguments == "" {
		return false
	}
	if QoderToolArgumentsAreCompleteJSON(existing.Arguments) && !QoderToolArgumentStringIsEmptyPlaceholder(existing.Arguments) {
		return true
	}
	return existing.Name != "" && existing.Arguments == "" && event.Arguments == ""
}

func QoderToolArgumentsAreCompleteJSON(arguments string) bool {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return false
	}
	return json.Valid([]byte(trimmed))
}

func MergeQoderToolArguments(existing string, delta string) string {
	if delta == "" {
		return existing
	}
	if existing == "" {
		return delta
	}
	if strings.TrimSpace(delta) == "{}" {
		return existing
	}
	if strings.TrimSpace(existing) == "{}" {
		return delta
	}
	return existing + delta
}

func QoderToolCallDeltaIsEmptyPlaceholder(event SSEEvent) bool {
	return event.ToolCallID == "" && event.ToolName == "" && event.Arguments == "" && event.ToolType != ""
}

func (a *QoderOpenAIToolCallAccumulator) bindUpstreamIndex(event SSEEvent, slot int) {
	if a == nil || !event.HasToolCallIndex || event.ToolCallIndex < 0 || slot < 0 || slot >= len(a.calls) {
		return
	}
	if a.slotByUpstreamIndex == nil {
		a.slotByUpstreamIndex = make(map[int]int)
	}
	if existingSlot, ok := a.slotByUpstreamIndex[event.ToolCallIndex]; ok && existingSlot >= 0 && existingSlot < len(a.calls) && existingSlot != slot {
		existingID := a.calls[existingSlot].ID
		slotID := a.calls[slot].ID
		if existingID != "" && slotID != "" && existingID != slotID {
			a.slotByUpstreamIndex[event.ToolCallIndex] = slot
			return
		}
	}
	a.slotByUpstreamIndex[event.ToolCallIndex] = slot
}

func QoderOpenAIToolCallDelta(index int, event SSEEvent) map[string]any {
	function := map[string]any{}
	if event.ToolName != "" {
		function["name"] = event.ToolName
	}
	if event.Arguments != "" && !QoderToolArgumentDeltaIsEmptyPlaceholder(event) {
		function["arguments"] = event.Arguments
	}
	callType := event.ToolType
	if callType == "" && (event.ToolCallID != "" || event.ToolName != "") {
		callType = "function"
	}
	delta := map[string]any{
		"index":    index,
		"function": function,
	}
	if event.ToolCallID != "" {
		delta["id"] = event.ToolCallID
	}
	if callType != "" {
		delta["type"] = callType
	}
	return delta
}

func QoderToolArgumentDeltaIsEmptyPlaceholder(event SSEEvent) bool {
	return QoderToolArgumentStringIsEmptyPlaceholder(event.Arguments)
}

func QoderToolArgumentStringIsEmptyPlaceholder(arguments string) bool {
	return strings.TrimSpace(arguments) == "{}"
}

func WriteQoderOpenAIStreamResponse(ctx context.Context, c *upstream.OutputContext, model string, resp *http.Response, options ...QoderOpenAIStreamResponseOption) (*QoderStreamResult, error) {
	usageMapper, toolNameMapper, includeUsage := QoderOpenAIStreamResponseOptions(options)
	// 确保无论成功或失败都关闭响应
	defer CloseQoderResponse(resp)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	completionID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	started := false
	writeTracker := &QoderStreamWriteTracker{}
	streamWriter := QoderDisconnectAwareWriter{writer: c.Writer, tracker: writeTracker}
	writeData := func(data map[string]any) error {
		if writeTracker.disconnected {
			return nil
		}
		return WriteSSEData(streamWriter, data)
	}
	ensureStarted := func() error {
		if started || writeTracker.disconnected {
			return nil
		}
		started = true
		c.Writer.WriteHeader(http.StatusOK)
		return writeData(OpenAIChunk(completionID, model, map[string]any{"role": "assistant"}, nil))
	}
	result := &QoderStreamResult{}
	toolCalls := NewQoderOpenAIToolCallAccumulator(toolNameMapper)
	finalized := false
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if writeTracker.disconnected {
			return nil
		}
		finishReason := "stop"
		if toolCalls.HasToolCalls() {
			finishReason = "tool_calls"
		}
		if err := ensureStarted(); err != nil {
			return err
		}
		if err := writeData(OpenAIChunk(completionID, model, map[string]any{}, finishReason)); err != nil {
			return err
		}
		if writeTracker.disconnected {
			return nil
		}
		_, err := io.WriteString(streamWriter, "data: [DONE]\n\n")
		if err != nil {
			return err
		}
		streamWriter.Flush()
		return err
	}
	if err := StreamQoderEvents(ctx, resp, func(event SSEEvent) error {
		if event.HasUsage {
			result.HasUsage = true
			MergeQoderUsageEvent(&result.Usage, event)
			result.TotalTokens = event.TotalTokens
			result.UsageDetails = event.UsageDetails
			chunkUsage := result.Usage
			if usageMapper != nil {
				chunkUsage = usageMapper(chunkUsage)
			}
			totalTokens := result.TotalTokens
			if totalTokens == 0 {
				totalTokens = chunkUsage.InputTokens + chunkUsage.CacheReadInputTokens + chunkUsage.OutputTokens
			}
			if includeUsage {
				if err := ensureStarted(); err != nil {
					return err
				}
				return writeData(OpenAIUsageChunk(completionID, model, chunkUsage, totalTokens, event.UsageDetails))
			}
			return nil
		}
		if event.IsDone {
			return finish()
		}
		if event.Type == "text_delta" && event.Text != "" {
			result.HasOutput = true
			if err := ensureStarted(); err != nil {
				return err
			}
			return writeData(OpenAIChunk(completionID, model, map[string]any{"content": event.Text}, nil))
		}
		if event.Type == "tool_call_delta" {
			result.HasOutput = true
			deltas := toolCalls.AppendDelta(event)
			if len(deltas) == 0 {
				return nil
			}
			if err := ensureStarted(); err != nil {
				return err
			}
			return writeData(OpenAIChunk(completionID, model, map[string]any{"tool_calls": deltas}, nil))
		}
		return nil
	}, func() error {
		if writeTracker.disconnected {
			return nil
		}
		if err := WriteQoderStreamKeepalive(c, started); err != nil {
			writeTracker.disconnected = true
		}
		return nil
	}); err != nil {
		return qoderPartialStreamResult(result), err
	}
	if err := finish(); err != nil {
		return qoderPartialStreamResult(result), err
	}
	return result, nil
}

func WriteQoderAnthropicStream(c *upstream.OutputContext, model string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) error {
	events = NormalizeQoderTextToolCallEvents(events)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := WriteAnthropicSSE(c.Writer, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	}); err != nil {
		return err
	}
	usage := upstream.TokenUsage{}
	writer := NewQoderAnthropicContentWriter(c.Writer, toolNameMappers...)
	finalized := false
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if err := writer.closeOpenBlock(); err != nil {
			return err
		}
		if err := writer.ensureContentBlock(); err != nil {
			return err
		}
		if err := WriteAnthropicSSE(c.Writer, "message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   writer.stopReason(),
				"stop_sequence": nil,
			},
			"usage": QoderAnthropicUsage(usage, QoderUsageDetailsFromEvents(events)),
		}); err != nil {
			return err
		}
		return WriteAnthropicSSE(c.Writer, "message_stop", map[string]any{"type": "message_stop"})
	}
	for _, event := range events {
		if event.HasUsage {
			MergeQoderUsageEvent(&usage, event)
			continue
		}
		if event.IsDone {
			return finish()
		}
		if event.Type == "text_delta" && event.Text != "" {
			if err := writer.writeTextDelta(event.Text); err != nil {
				return err
			}
			continue
		}
		if event.Type == "reasoning_delta" {
			if err := writer.writeThinkingDelta(event.Text); err != nil {
				return err
			}
			continue
		}
		if event.Type == "tool_call_delta" {
			if err := writer.writeToolCall(event); err != nil {
				return err
			}
		}
	}
	if err := finish(); err != nil {
		return err
	}
	return nil
}

func WriteQoderAnthropicStreamResponse(ctx context.Context, c *upstream.OutputContext, model string, resp *http.Response, options ...QoderAnthropicStreamResponseOption) (*QoderStreamResult, error) {
	usageMapper, toolNameMapper := QoderAnthropicStreamResponseOptions(options)
	// 确保无论成功或失败都关闭响应
	defer CloseQoderResponse(resp)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	started := false
	writeTracker := &QoderStreamWriteTracker{}
	streamWriter := QoderDisconnectAwareWriter{writer: c.Writer, tracker: writeTracker}
	writeEvent := func(event string, data map[string]any) error {
		if writeTracker.disconnected {
			return nil
		}
		return WriteAnthropicSSE(streamWriter, event, data)
	}
	ensureStarted := func() error {
		if started || writeTracker.disconnected {
			return nil
		}
		started = true
		c.Writer.WriteHeader(http.StatusOK)
		return writeEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            messageID,
				"type":          "message",
				"role":          "assistant",
				"model":         model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		})
	}
	result := &QoderStreamResult{}
	writer := NewQoderAnthropicContentWriter(streamWriter, toolNameMapper)
	finalized := false
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if err := ensureStarted(); err != nil {
			return err
		}
		if err := writer.closeOpenBlock(); err != nil {
			return err
		}
		if err := writer.ensureContentBlock(); err != nil {
			return err
		}
		finalUsage := result.Usage
		if usageMapper != nil {
			finalUsage = usageMapper(finalUsage)
		}
		if err := writeEvent("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   writer.stopReason(),
				"stop_sequence": nil,
			},
			"usage": QoderAnthropicUsage(finalUsage, result.UsageDetails),
		}); err != nil {
			return err
		}
		return writeEvent("message_stop", map[string]any{"type": "message_stop"})
	}
	if err := StreamQoderEvents(ctx, resp, func(event SSEEvent) error {
		if event.HasUsage {
			result.HasUsage = true
			MergeQoderUsageEvent(&result.Usage, event)
			result.UsageDetails = event.UsageDetails
			return nil
		}
		if event.IsDone {
			return finish()
		}
		if event.Type == "text_delta" && event.Text != "" {
			result.HasOutput = true
			if err := ensureStarted(); err != nil {
				return err
			}
			return writer.writeTextDelta(event.Text)
		}
		if event.Type == "reasoning_delta" {
			if event.Text == "" {
				return nil
			}
			result.HasOutput = true
			if err := ensureStarted(); err != nil {
				return err
			}
			return writer.writeThinkingDelta(event.Text)
		}
		if event.Type == "tool_call_delta" {
			result.HasOutput = true
			if err := ensureStarted(); err != nil {
				return err
			}
			return writer.writeToolCall(event)
		}
		return nil
	}, func() error {
		if writeTracker.disconnected {
			return nil
		}
		if err := WriteQoderStreamKeepalive(c, started); err != nil {
			writeTracker.disconnected = true
		}
		return nil
	}); err != nil {
		return qoderPartialStreamResult(result), err
	}
	if err := finish(); err != nil {
		return qoderPartialStreamResult(result), err
	}
	return result, nil
}

func BuildQoderResponsesResponse(model string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]byte, error) {
	return BuildQoderResponsesResponseWithID(model, "", events, toolNameMappers...)
}

func BuildQoderResponsesResponseWithID(model, responseID string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]byte, error) {
	anthropicBody, err := BuildQoderAnthropicMessage(model, events, toolNameMappers...)
	if err != nil {
		return nil, err
	}
	var anthropicResp protocolanthropic.AnthropicResponse
	if err := json.Unmarshal(anthropicBody, &anthropicResp); err != nil {
		return nil, fmt.Errorf("parse qoder anthropic response: %w", err)
	}
	responsesResp := bridge.AnthropicToResponsesResponse(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, &anthropicResp)
	if strings.TrimSpace(responseID) != "" {
		responsesResp.ID = strings.TrimSpace(responseID)
	}
	responsesResp.Model = model
	return json.Marshal(responsesResp)
}

func WriteQoderResponsesStreamResponse(ctx context.Context, c *upstream.OutputContext, model string, resp *http.Response, options ...QoderResponsesStreamResponseOption) (*QoderStreamResult, error) {
	usageMapper, toolNameMapper, responseID := QoderResponsesStreamResponseOptions(options)
	defer CloseQoderResponse(resp)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	result := &QoderStreamResult{}
	if responseID == "" {
		responseID = QoderResponsesID()
	}
	sequence := 0
	clientDisconnected := false
	writeEventFrame := func(evt protocolopenai.ResponsesStreamEvent) error {
		if clientDisconnected {
			return nil
		}
		evt.SequenceNumber = sequence
		sequence++
		sse, err := bridge.ResponsesEventToSSE(evt)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(c.Writer, sse); err != nil {
			clientDisconnected = true
			return nil
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}
	started := false
	ensureStarted := func() error {
		if started || clientDisconnected {
			return nil
		}
		started = true
		c.Writer.WriteHeader(http.StatusOK)
		return writeEventFrame(protocolopenai.ResponsesStreamEvent{
			Type: "response.created",
			Response: &protocolopenai.ResponsesResponse{
				ID:     responseID,
				Object: "response",
				Model:  model,
				Status: "in_progress",
				Output: []protocolopenai.ResponsesOutput{},
			},
		})
	}
	writeEvent := func(evt protocolopenai.ResponsesStreamEvent) error {
		if err := ensureStarted(); err != nil {
			return err
		}
		return writeEventFrame(evt)
	}

	nextOutputIndex := 0
	var messageItemID string
	messageOutputIndex := -1
	messageOpen := false
	messageDone := false
	var messageText strings.Builder
	completedOutputs := map[int]protocolopenai.ResponsesOutput{}
	completedOutputList := func() []protocolopenai.ResponsesOutput {
		if len(completedOutputs) == 0 {
			return []protocolopenai.ResponsesOutput{}
		}
		indexes := make([]int, 0, len(completedOutputs))
		for index := range completedOutputs {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		outputs := make([]protocolopenai.ResponsesOutput, 0, len(indexes))
		for _, index := range indexes {
			outputs = append(outputs, completedOutputs[index])
		}
		return outputs
	}

	var reasoningItemID string
	reasoningOutputIndex := -1
	reasoningOpen := false
	var reasoningText strings.Builder
	openReasoning := func() error {
		if reasoningOpen {
			return nil
		}
		reasoningText.Reset()
		reasoningItemID = "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		reasoningOutputIndex = nextOutputIndex
		nextOutputIndex++
		reasoningOpen = true
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: reasoningOutputIndex,
			Item: &protocolopenai.ResponsesOutput{
				Type:   "reasoning",
				ID:     reasoningItemID,
				Status: "in_progress",
			},
		}); err != nil {
			return err
		}
		return writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:         "response.reasoning_summary_part.added",
			OutputIndex:  reasoningOutputIndex,
			SummaryIndex: 0,
			ItemID:       reasoningItemID,
			Part:         &protocolopenai.ResponsesContentPart{Type: "summary_text"},
		})
	}
	closeReasoning := func() error {
		if !reasoningOpen {
			return nil
		}
		text := reasoningText.String()
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:         "response.reasoning_summary_text.done",
			OutputIndex:  reasoningOutputIndex,
			SummaryIndex: 0,
			Text:         text,
			ItemID:       reasoningItemID,
		}); err != nil {
			return err
		}
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:         "response.reasoning_summary_part.done",
			OutputIndex:  reasoningOutputIndex,
			SummaryIndex: 0,
			ItemID:       reasoningItemID,
			Part:         &protocolopenai.ResponsesContentPart{Type: "summary_text", Text: text},
		}); err != nil {
			return err
		}
		item := protocolopenai.ResponsesOutput{
			Type:    "reasoning",
			ID:      reasoningItemID,
			Status:  "completed",
			Summary: []protocolopenai.ResponsesSummary{{Type: "summary_text", Text: text}},
		}
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.done",
			OutputIndex: reasoningOutputIndex,
			Item:        &item,
		}); err != nil {
			return err
		}
		completedOutputs[reasoningOutputIndex] = item
		reasoningOpen = false
		return nil
	}
	openMessage := func() error {
		if messageOpen {
			return nil
		}
		if err := closeReasoning(); err != nil {
			return err
		}
		messageText.Reset()
		messageItemID = "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		messageOutputIndex = nextOutputIndex
		nextOutputIndex++
		messageOpen = true
		messageDone = false
		return writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: messageOutputIndex,
			Item: &protocolopenai.ResponsesOutput{
				Type:   "message",
				ID:     messageItemID,
				Role:   "assistant",
				Status: "in_progress",
			},
		})
	}
	closeMessage := func() error {
		if !messageOpen || messageDone {
			return nil
		}
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:         "response.output_text.done",
			OutputIndex:  messageOutputIndex,
			ContentIndex: 0,
			ItemID:       messageItemID,
			Text:         messageText.String(),
		}); err != nil {
			return err
		}
		item := protocolopenai.ResponsesOutput{
			Type:    "message",
			ID:      messageItemID,
			Role:    "assistant",
			Content: []protocolopenai.ResponsesContentPart{{Type: "output_text", Text: messageText.String()}},
			Status:  "completed",
		}
		if err := writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.done",
			OutputIndex: messageOutputIndex,
			Item:        &item,
		}); err != nil {
			return err
		}
		completedOutputs[messageOutputIndex] = item
		messageOpen = false
		messageDone = true
		return nil
	}

	toolAccumulator := NewQoderOpenAIToolCallAccumulator(toolNameMapper)
	toolStates := map[int]*QoderResponsesStreamToolState{}
	toolIndexForEvent := func(event SSEEvent) int {
		normalized := NormalizeQoderOutboundToolCallEvent(event)
		return toolAccumulator.resolveIndex(normalized)
	}
	ensureToolAdded := func(index int) (*QoderResponsesStreamToolState, error) {
		for len(toolAccumulator.calls) <= index {
			toolAccumulator.calls = append(toolAccumulator.calls, QoderOpenAIToolCallState{Type: "function"})
		}
		call := toolAccumulator.calls[index]
		state := toolStates[index]
		if state == nil {
			state = &QoderResponsesStreamToolState{
				itemID:      "item_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				outputIndex: nextOutputIndex,
			}
			nextOutputIndex++
			toolStates[index] = state
		}
		if call.ID != "" {
			state.callID = ToResponsesCallIDForQoder(call.ID)
		}
		if state.callID == "" {
			state.callID = fmt.Sprintf("call_%d", index)
		}
		if call.Name != "" {
			state.name = call.Name
		}
		if call.Arguments != "" {
			state.arguments = call.Arguments
		}
		if state.added || state.name == "" {
			return state, nil
		}
		if err := closeReasoning(); err != nil {
			return state, err
		}
		if err := closeMessage(); err != nil {
			return state, err
		}
		state.added = true
		return state, writeEvent(protocolopenai.ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: state.outputIndex,
			Item: &protocolopenai.ResponsesOutput{
				Type:   "function_call",
				ID:     state.itemID,
				CallID: state.callID,
				Name:   state.name,
				Status: "in_progress",
			},
		})
	}
	writeToolDelta := func(event SSEEvent) error {
		if QoderToolCallDeltaIsEmptyPlaceholder(NormalizeQoderOutboundToolCallEvent(event)) {
			return nil
		}
		index := toolIndexForEvent(event)
		deltas := toolAccumulator.AppendDelta(event)
		if len(deltas) == 0 {
			return nil
		}
		state, err := ensureToolAdded(index)
		if err != nil {
			return err
		}
		for _, rawDelta := range deltas {
			delta, _ := rawDelta.(map[string]any)
			function, _ := delta["function"].(map[string]any)
			arguments, _ := function["arguments"].(string)
			if arguments == "" || QoderToolArgumentDeltaIsEmptyPlaceholder(event) || !state.added {
				continue
			}
			if err := writeEvent(protocolopenai.ResponsesStreamEvent{
				Type:        "response.function_call_arguments.delta",
				OutputIndex: state.outputIndex,
				ItemID:      state.itemID,
				CallID:      state.callID,
				Name:        state.name,
				Delta:       arguments,
			}); err != nil {
				return err
			}
		}
		return nil
	}
	closeTools := func() error {
		indexes := make([]int, 0, len(toolStates))
		for index := range toolStates {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			state := toolStates[index]
			if state == nil {
				continue
			}
			if _, err := ensureToolAdded(index); err != nil {
				return err
			}
			if !state.added || state.done {
				continue
			}
			if err := writeEvent(protocolopenai.ResponsesStreamEvent{
				Type:        "response.function_call_arguments.done",
				OutputIndex: state.outputIndex,
				ItemID:      state.itemID,
				CallID:      state.callID,
				Name:        state.name,
				Arguments:   FirstNonEmptyQoder(state.arguments, "{}"),
			}); err != nil {
				return err
			}
			item := protocolopenai.ResponsesOutput{
				Type:      "function_call",
				ID:        state.itemID,
				CallID:    state.callID,
				Name:      state.name,
				Arguments: FirstNonEmptyQoder(state.arguments, "{}"),
				Status:    "completed",
			}
			if err := writeEvent(protocolopenai.ResponsesStreamEvent{
				Type:        "response.output_item.done",
				OutputIndex: state.outputIndex,
				Item:        &item,
			}); err != nil {
				return err
			}
			completedOutputs[state.outputIndex] = item
			state.done = true
		}
		return nil
	}
	finalized := false
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if err := closeReasoning(); err != nil {
			return err
		}
		if err := closeMessage(); err != nil {
			return err
		}
		if err := closeTools(); err != nil {
			return err
		}
		finalUsage := result.Usage
		if usageMapper != nil {
			finalUsage = usageMapper(finalUsage)
		}
		return writeEvent(protocolopenai.ResponsesStreamEvent{
			Type: "response.completed",
			Response: &protocolopenai.ResponsesResponse{
				ID:     responseID,
				Object: "response",
				Model:  model,
				Status: "completed",
				Output: completedOutputList(),
				Usage:  QoderResponsesUsage(finalUsage),
			},
		})
	}

	if err := StreamQoderEvents(ctx, resp, func(event SSEEvent) error {
		if event.HasUsage {
			result.HasUsage = true
			MergeQoderUsageEvent(&result.Usage, event)
			result.UsageDetails = event.UsageDetails
			return nil
		}
		if event.IsDone {
			return finish()
		}
		switch event.Type {
		case "text_delta":
			if event.Text != "" {
				result.HasOutput = true
				if err := openMessage(); err != nil {
					return err
				}
				_, _ = messageText.WriteString(event.Text)
				return writeEvent(protocolopenai.ResponsesStreamEvent{
					Type:         "response.output_text.delta",
					OutputIndex:  messageOutputIndex,
					ContentIndex: 0,
					ItemID:       messageItemID,
					Delta:        event.Text,
				})
			}
		case "reasoning_delta":
			if event.Text != "" {
				result.HasOutput = true
				if err := closeMessage(); err != nil {
					return err
				}
				if err := openReasoning(); err != nil {
					return err
				}
				_, _ = reasoningText.WriteString(event.Text)
				return writeEvent(protocolopenai.ResponsesStreamEvent{
					Type:         "response.reasoning_summary_text.delta",
					OutputIndex:  reasoningOutputIndex,
					SummaryIndex: 0,
					ItemID:       reasoningItemID,
					Delta:        event.Text,
				})
			}
		case "tool_call_delta":
			result.HasOutput = true
			return writeToolDelta(event)
		}
		return nil
	}, func() error {
		if clientDisconnected {
			return nil
		}
		if err := WriteQoderStreamKeepalive(c, started); err != nil {
			clientDisconnected = true
		}
		return nil
	}); err != nil {
		return qoderPartialStreamResult(result), err
	}
	if err := finish(); err != nil {
		return qoderPartialStreamResult(result), err
	}
	return result, nil
}

type QoderResponsesStreamToolState struct {
	itemID      string
	callID      string
	name        string
	arguments   string
	outputIndex int
	added       bool
	done        bool
}

func ToResponsesCallIDForQoder(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "call_") {
		return trimmed
	}
	return "call_" + trimmed
}

func QoderResponsesID() string {
	return "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func QoderResponsesUsage(usage upstream.TokenUsage) *protocolopenai.ResponsesUsage {
	inputTokens := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	out := &protocolopenai.ResponsesUsage{
		InputTokens:  inputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  inputTokens + usage.OutputTokens,
	}
	if usage.CacheReadInputTokens > 0 {
		out.InputTokensDetails = &protocolopenai.ResponsesInputTokensDetails{CachedTokens: usage.CacheReadInputTokens}
	}
	return out
}

type QoderAnthropicContentWriter struct {
	w                 io.Writer
	nextIndex         int
	openTextIndex     *int
	openThinkingIndex *int
	pendingToolCalls  *QoderOpenAIToolCallAccumulator
	mapToolName       QoderToolNameMapper
	sawToolCall       bool
}

func NewQoderAnthropicContentWriter(w io.Writer, toolNameMapper ...QoderToolNameMapper) *QoderAnthropicContentWriter {
	writer := &QoderAnthropicContentWriter{w: w}
	if len(toolNameMapper) > 0 {
		writer.mapToolName = toolNameMapper[0]
	}
	return writer
}

func (w *QoderAnthropicContentWriter) writeTextDelta(text string) error {
	if strings.TrimSpace(text) == "" && text == "" {
		return nil
	}
	if err := w.flushPendingToolCalls(); err != nil {
		return err
	}
	if err := w.closeOpenThinkingBlock(); err != nil {
		return err
	}
	if w.openTextIndex == nil {
		index := w.nextIndex
		w.nextIndex++
		if err := WriteAnthropicSSE(w.w, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": map[string]any{"type": "text", "text": ""},
		}); err != nil {
			return err
		}
		w.openTextIndex = &index
	}
	return WriteAnthropicSSE(w.w, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": *w.openTextIndex,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

func (w *QoderAnthropicContentWriter) writeThinkingDelta(text string) error {
	if text == "" {
		return nil
	}
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	if err := w.flushPendingToolCalls(); err != nil {
		return err
	}
	if w.openThinkingIndex == nil {
		index := w.nextIndex
		w.nextIndex++
		if err := WriteAnthropicSSE(w.w, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": map[string]any{"type": "thinking", "thinking": ""},
		}); err != nil {
			return err
		}
		w.openThinkingIndex = &index
	}
	return WriteAnthropicSSE(w.w, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": *w.openThinkingIndex,
		"delta": map[string]any{"type": "thinking_delta", "thinking": text},
	})
}

func (w *QoderAnthropicContentWriter) writeToolCall(event SSEEvent) error {
	normalized := NormalizeQoderOutboundToolCallEvent(event)
	if QoderToolCallDeltaIsEmptyPlaceholder(normalized) {
		return nil
	}
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	if err := w.closeOpenThinkingBlock(); err != nil {
		return err
	}
	if w.pendingToolCalls == nil {
		w.pendingToolCalls = NewQoderOpenAIToolCallAccumulator(w.mapToolName)
	}
	if len(w.pendingToolCalls.AppendDelta(normalized)) > 0 {
		w.sawToolCall = true
	}
	return nil
}

func (w *QoderAnthropicContentWriter) closeOpenBlock() error {
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	if err := w.closeOpenThinkingBlock(); err != nil {
		return err
	}
	return w.flushPendingToolCalls()
}

func (w *QoderAnthropicContentWriter) closeOpenTextBlock() error {
	if w.openTextIndex == nil {
		return nil
	}
	index := *w.openTextIndex
	w.openTextIndex = nil
	return WriteAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func (w *QoderAnthropicContentWriter) closeOpenThinkingBlock() error {
	if w.openThinkingIndex == nil {
		return nil
	}
	index := *w.openThinkingIndex
	w.openThinkingIndex = nil
	return WriteAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func (w *QoderAnthropicContentWriter) flushPendingToolCalls() error {
	if w.pendingToolCalls == nil {
		return nil
	}
	calls := w.pendingToolCalls.Calls()
	w.pendingToolCalls = nil
	for _, rawCall := range calls {
		call, ok := rawCall.(map[string]any)
		if !ok {
			continue
		}
		function, _ := call["function"].(map[string]any)
		index := w.nextIndex
		w.nextIndex++
		if err := WriteAnthropicSSE(w.w, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    QoderStringField(call, "id"),
				"name":  QoderStringField(function, "name"),
				"input": map[string]any{},
			},
		}); err != nil {
			return err
		}
		arguments := QoderToolArgumentsString(function["arguments"])
		if arguments != "" {
			if _, err := QoderAnthropicToolInput(arguments); err != nil {
				return err
			}
			if err := WriteAnthropicSSE(w.w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": arguments,
				},
			}); err != nil {
				return err
			}
		}
		if err := WriteAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index}); err != nil {
			return err
		}
	}
	return nil
}

func (w *QoderAnthropicContentWriter) ensureContentBlock() error {
	if w == nil || w.nextIndex > 0 {
		return nil
	}
	index := w.nextIndex
	w.nextIndex++
	if err := WriteAnthropicSSE(w.w, "content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         index,
		"content_block": map[string]any{"type": "text", "text": ""},
	}); err != nil {
		return err
	}
	return WriteAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func (w *QoderAnthropicContentWriter) stopReason() string {
	if w != nil && w.sawToolCall {
		return "tool_use"
	}
	return "end_turn"
}

type QoderEventResult struct {
	events []SSEEvent
	err    error
}

func QoderSendEventResult(ctx context.Context, results chan<- QoderEventResult, result QoderEventResult) bool {
	select {
	case results <- result:
		return true
	case <-ctx.Done():
		return false
	}
}

func ScanQoderEvents(ctx context.Context, resp *http.Response, results chan<- QoderEventResult) {
	defer close(results)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), defaultMaxLineSize)
	for scanner.Scan() {
		parsed, err := ParseSSELine(scanner.Text())
		if err != nil {
			QoderSendEventResult(ctx, results, QoderEventResult{err: err})
			return
		}
		if len(parsed) > 0 && !QoderSendEventResult(ctx, results, QoderEventResult{events: parsed}) {
			return
		}
		for _, event := range parsed {
			if event.IsDone {
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// 显式处理 bufio.ErrTooLong：SSE event 超过 defaultMaxLineSize
		if errors.Is(err, bufio.ErrTooLong) {
			QoderSendEventResult(ctx, results, QoderEventResult{
				err: fmt.Errorf("SSE event exceeds max line size (%d bytes, buffer limit %d): %w", defaultMaxLineSize, 64*1024, err),
			})
			return
		}
		QoderSendEventResult(ctx, results, QoderEventResult{err: err})
	}
}

func StreamQoderEvents(ctx context.Context, resp *http.Response, handle func(SSEEvent) error, keepalive func() error) error {
	if resp == nil || resp.Body == nil {
		return errors.New("qoder response body is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer func() { _ = resp.Body.Close() }()
	defer cancel()

	ticker := time.NewTicker(QoderKeepaliveEvery)
	defer ticker.Stop()
	results := make(chan QoderEventResult, 1)
	transformer := NewQoderTextToolCallTransformer()
	go ScanQoderEvents(ctx, resp, results)
	for {
		select {
		case result, ok := <-results:
			if !ok {
				for _, event := range transformer.Flush() {
					if err := handle(event); err != nil {
						return err
					}
				}
				return nil
			}
			if result.err != nil {
				return result.err
			}
			for _, event := range result.events {
				for _, transformed := range transformer.Append(event) {
					if err := handle(transformed); err != nil {
						return err
					}
					if transformed.IsDone {
						return nil
					}
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if keepalive != nil {
				if err := keepalive(); err != nil {
					return err
				}
			}
		}
	}
}

func CloseQoderResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func BuildQoderOpenAICompletion(model string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]byte, error) {
	events = NormalizeQoderTextToolCallEvents(events)
	content := QoderTextFromEvents(events)
	usage := QoderUsageFromEvents(events)
	totalTokens := QoderTotalTokensFromEvents(events, usage)
	usageDetails := QoderUsageDetailsFromEvents(events)
	toolCalls := QoderOpenAIToolCallsFromEvents(events, toolNameMappers...)
	message := map[string]any{"role": "assistant", "content": content}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		if content == "" {
			message["content"] = nil
		}
		message["tool_calls"] = toolCalls
		finishReason = "tool_calls"
	}
	return json.Marshal(map[string]any{
		"id":      "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24],
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
		"usage": QoderOpenAIUsage(usage, totalTokens, usageDetails),
	})
}

func BuildQoderAnthropicMessage(model string, events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]byte, error) {
	events = NormalizeQoderTextToolCallEvents(events)
	contentBlocks, err := QoderAnthropicContentBlocksFromEvents(events, toolNameMappers...)
	if err != nil {
		return nil, err
	}
	usage := QoderUsageFromEvents(events)
	usageDetails := QoderUsageDetailsFromEvents(events)
	stopReason := "end_turn"
	if QoderEventsHaveToolCalls(events) {
		stopReason = "tool_use"
	}
	return json.Marshal(map[string]any{
		"id":            "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       contentBlocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         QoderAnthropicUsage(usage, usageDetails),
	})
}

func QoderOpenAIToolCallsFromEvents(events []SSEEvent, toolNameMappers ...QoderToolNameMapper) []any {
	acc := NewQoderOpenAIToolCallAccumulator(toolNameMappers...)
	for _, event := range events {
		if event.Type == "tool_call_delta" {
			acc.AppendDelta(event)
		}
	}
	return acc.Calls()
}

func QoderEventsHaveToolCalls(events []SSEEvent) bool {
	acc := NewQoderOpenAIToolCallAccumulator()
	for _, event := range events {
		if event.Type == "tool_call_delta" {
			acc.AppendDelta(event)
		}
	}
	return acc.HasToolCalls()
}

func QoderAnthropicContentBlocksFromEvents(events []SSEEvent, toolNameMappers ...QoderToolNameMapper) ([]any, error) {
	blocks := make([]any, 0)
	var text bytes.Buffer
	var thinking bytes.Buffer
	var toolAccumulator *QoderOpenAIToolCallAccumulator
	flushText := func() {
		if text.Len() == 0 {
			return
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": text.String()})
		text.Reset()
	}
	flushThinking := func() {
		if thinking.Len() == 0 {
			return
		}
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": thinking.String()})
		thinking.Reset()
	}
	flushTools := func() error {
		if toolAccumulator == nil {
			return nil
		}
		for _, rawCall := range toolAccumulator.Calls() {
			call, ok := rawCall.(map[string]any)
			if !ok {
				continue
			}
			function, _ := call["function"].(map[string]any)
			input, err := QoderAnthropicToolInput(function["arguments"])
			if err != nil {
				return err
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    call["id"],
				"name":  function["name"],
				"input": input,
			})
		}
		toolAccumulator = nil
		return nil
	}
	for _, event := range events {
		switch event.Type {
		case "text_delta":
			if event.Text != "" {
				if err := flushTools(); err != nil {
					return nil, err
				}
				flushThinking()
				_, _ = text.WriteString(event.Text)
			}
		case "reasoning_delta":
			if event.Text != "" {
				flushText()
				if err := flushTools(); err != nil {
					return nil, err
				}
				_, _ = thinking.WriteString(event.Text)
			}
		case "tool_call_delta":
			if QoderToolCallDeltaIsEmptyPlaceholder(NormalizeQoderOutboundToolCallEvent(event)) {
				continue
			}
			if toolAccumulator == nil {
				toolAccumulator = NewQoderOpenAIToolCallAccumulator(toolNameMappers...)
			}
			if len(toolAccumulator.AppendDelta(event)) > 0 {
				flushText()
				flushThinking()
			}
		}
	}
	flushText()
	flushThinking()
	if err := flushTools(); err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return []any{map[string]any{"type": "text", "text": ""}}, nil
	}
	return blocks, nil
}

func QoderAnthropicToolInput(raw any) (map[string]any, error) {
	args := QoderToolArgumentsString(raw)
	if strings.TrimSpace(args) == "" {
		return map[string]any{}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(args), &decoded); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("malformed qoder tool arguments: %s", args)
}

func QoderUsageFromEvents(events []SSEEvent) upstream.TokenUsage {
	usage := upstream.TokenUsage{}
	for _, event := range events {
		if event.HasUsage {
			MergeQoderUsageEvent(&usage, event)
		}
	}
	return usage
}

func QoderUsageDetailsFromEvents(events []SSEEvent) UsageDetails {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].HasUsage {
			return events[i].UsageDetails
		}
	}
	return UsageDetails{}
}

func MergeQoderUsageEvent(usage *upstream.TokenUsage, event SSEEvent) {
	if usage == nil || !event.HasUsage {
		return
	}
	cachedTokens := 0
	if event.UsageDetails.PromptTokensDetails != nil {
		cachedTokens = event.UsageDetails.PromptTokensDetails.CachedTokens
		usage.InputTokens = max(event.PromptTokens-cachedTokens, 0)
		usage.CacheReadInputTokens = cachedTokens
		usage.CacheCreationInputTokens = 0
	} else {
		usage.InputTokens = event.PromptTokens
		usage.CacheReadInputTokens = 0
	}
	usage.OutputTokens = event.CompletionTokens
}

func QoderEventsWithUsage(events []SSEEvent, usage upstream.TokenUsage) []SSEEvent {
	if len(events) == 0 {
		return events
	}
	out := append([]SSEEvent(nil), events...)
	for i := range out {
		if !out[i].HasUsage {
			continue
		}
		if out[i].PromptTokens == 0 {
			out[i].PromptTokens = usage.InputTokens + usage.CacheReadInputTokens
		}
		if out[i].CompletionTokens == 0 {
			out[i].CompletionTokens = usage.OutputTokens
		}
		if out[i].TotalTokens == 0 {
			out[i].TotalTokens = out[i].PromptTokens + out[i].CompletionTokens
		}
	}
	return out
}

func QoderTotalTokensFromEvents(events []SSEEvent, usage upstream.TokenUsage) int {
	var lastPromptTokens int
	var lastCompletionTokens int
	for i := len(events) - 1; i >= 0; i-- {
		if !events[i].HasUsage {
			continue
		}
		if events[i].TotalTokens > 0 {
			return events[i].TotalTokens
		}
		lastPromptTokens = events[i].PromptTokens
		lastCompletionTokens = events[i].CompletionTokens
		break
	}
	if lastPromptTokens > 0 || lastCompletionTokens > 0 {
		return lastPromptTokens + lastCompletionTokens
	}
	return usage.InputTokens + usage.OutputTokens
}

func QoderOpenAIUsage(usage upstream.TokenUsage, totalTokens int, usageDetails ...UsageDetails) map[string]any {
	promptTokens := usage.InputTokens + usage.CacheReadInputTokens
	completionTokens := usage.OutputTokens
	if totalTokens > 0 && totalTokens >= completionTokens {
		promptTokens = totalTokens - completionTokens
	}
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	out := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
	}
	details := QoderFirstUsageDetails(usageDetails)
	if details.PromptTokensDetails != nil {
		out["prompt_tokens_details"] = map[string]any{
			"cached_tokens":    details.PromptTokensDetails.CachedTokens,
			"cacheable_tokens": details.PromptTokensDetails.CacheableTokens,
		}
	} else if usage.CacheReadInputTokens > 0 {
		out["prompt_tokens_details"] = map[string]any{"cached_tokens": usage.CacheReadInputTokens}
	}
	if details.CompletionTokensDetails != nil {
		out["completion_tokens_details"] = map[string]any{"reasoning_tokens": details.CompletionTokensDetails.ReasoningTokens}
	}
	return out
}

func QoderAnthropicUsage(usage upstream.TokenUsage, usageDetails ...UsageDetails) map[string]any {
	out := map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
	}
	details := QoderFirstUsageDetails(usageDetails)
	if usage.CacheReadInputTokens > 0 || details.PromptTokensDetails != nil {
		out["cache_read_input_tokens"] = usage.CacheReadInputTokens
	}
	if usage.CacheCreationInputTokens > 0 {
		out["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	}
	return out
}

func QoderFirstUsageDetails(details []UsageDetails) UsageDetails {
	if len(details) == 0 {
		return UsageDetails{}
	}
	return details[0]
}

func WriteSSEData(w io.Writer, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func WriteAnthropicSSE(w io.Writer, event string, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func OpenAIChunk(id, model string, delta map[string]any, finishReason any) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
}

func OpenAIUsageChunk(id, model string, usage upstream.TokenUsage, totalTokens int, usageDetails ...UsageDetails) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{},
		"usage":   QoderOpenAIUsage(usage, totalTokens, usageDetails...),
	}
}

func ResolveQoderModel(model string) QoderModelInfo {
	return ResolveQoderModelForSite(SiteGlobal, model)
}

func ResolveQoderModelForSite(site Site, model string) QoderModelInfo {
	if info, ok := LookupQoderModelAliasForSite(site, strings.TrimSpace(model)); ok {
		return info
	}
	return QoderModelInfo{Key: strings.TrimSpace(model), Source: "system"}
}

func LatestQoderUserText(messages []QoderMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Text) != "" {
			return messages[i].Text
		}
	}
	return ""
}

func LatestQoderPromptText(messages []QoderMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if text := QoderPromptTextForMessage(messages[i]); text != "" {
			return text
		}
	}
	return ""
}

func QoderPromptTextForMessage(message QoderMessage) string {
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return ""
	}
	if message.Role != "tool" {
		return message.Text
	}
	toolCallID := QoderMessageToolCallID(message)
	if toolCallID == "" {
		return message.Text
	}
	return fmt.Sprintf("<tool_result id=\"%s\">\n%s\n</tool_result>", html.EscapeString(toolCallID), message.Text)
}

func LatestQoderPayloadPromptText(messages []QoderMessage, includeTools bool) string {
	if text := QoderToolContinuationPromptText(messages); text != "" {
		return text
	}
	if text := LatestQoderUserText(messages); text != "" {
		return text
	}
	return LatestQoderPromptText(messages)
}

func QoderToolContinuationPromptText(messages []QoderMessage) string {
	toolStart := len(messages)
	for toolStart > 0 && messages[toolStart-1].Role == "tool" {
		toolStart--
	}
	if toolStart == len(messages) {
		return ""
	}

	// 当 previous_response_id 承载前一次 assistant tool call 时，只有 tool 的
	// Responses 续写是合法的。否则要求当前回放中存在紧邻的 assistant tool-call
	// 轮次，避免普通历史 tool 消息被误提升为当前 prompt。
	if toolStart > 0 && !QoderMessageHasToolCalls(messages[toolStart-1]) {
		return ""
	}

	parts := make([]string, 0, len(messages)-toolStart)
	for _, message := range messages[toolStart:] {
		if text := QoderPromptTextForMessage(message); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func QoderAnySlice(raw any) []any {
	if raw == nil {
		return []any{}
	}
	if values, ok := raw.([]any); ok {
		return values
	}
	return []any{}
}

func QoderStringField(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func QoderTextFromEvents(events []SSEEvent) string {
	var buf bytes.Buffer
	for _, event := range events {
		if event.Type == "text_delta" && event.Text != "" {
			_, _ = buf.WriteString(event.Text)
		}
	}
	return buf.String()
}

func NonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func TruncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func GjsonString(body []byte, path string) string {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	value, _ := decoded[path].(string)
	return value
}

func GjsonBool(body []byte, path string) bool {
	return gjson.GetBytes(body, path).Bool()
}

// LookupQoderModelAlias 仅解析当前公开 alias，未命中的模型由调用方原样透传。
func LookupQoderModelAlias(model string) (QoderModelInfo, bool) {
	model = NormalizeQoderAliasModel(model)
	info, ok := DefaultQoderModelAliases[model]
	return info, ok
}

// LookupQoderModelAliasForSite 只解析账号站点实际支持的公开 alias。
func LookupQoderModelAliasForSite(site Site, model string) (QoderModelInfo, bool) {
	model = NormalizeQoderAliasModel(model)
	if _, ok := AliasForSite(site, model); !ok {
		return QoderModelInfo{}, false
	}
	info, ok := DefaultQoderModelAliases[model]
	return info, ok
}
func NormalizeQoderAliasModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}
func FirstNonEmptyQoder(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

const defaultMaxLineSize = upstream.DefaultSSELineLimit

// qoderPartialStreamResult 保留已服务且已观测用量的失败结果，不推断缺失计量。
func qoderPartialStreamResult(result *QoderStreamResult) *QoderStreamResult {
	if result != nil && result.HasOutput && result.HasUsage {
		return result
	}
	return nil
}
