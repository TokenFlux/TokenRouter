// 保留原生、兼容与静态上游各自的输出循环；实际写入由同步 sink 承担。
package antigravity

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type antigravityCompatStreamAdapter interface {
	Emit(*protocolanthropic.AnthropicStreamEvent, *ClientWriter)
	Finalize(*ClientWriter)
	WriteError(*ClientWriter, string)
}
type antigravityChatStreamAdapter struct {
	c              *upstream.OutputContext
	reverseTools   func([]byte) []byte
	anthropicState *bridge.AnthropicEventToResponsesState
	chatState      *bridge.ResponsesEventToChatState
}

func NewAntigravityChatStreamAdapter(c *upstream.OutputContext, model string, includeUsage bool, reverse func([]byte) []byte) *antigravityChatStreamAdapter {
	anthropicState := bridge.NewAnthropicEventToResponsesState(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read})
	anthropicState.Model = model
	chatState := bridge.NewResponsesEventToChatState(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read})
	chatState.Model = model
	chatState.IncludeUsage = includeUsage
	return &antigravityChatStreamAdapter{
		c:              c,
		reverseTools:   reverse,
		anthropicState: anthropicState,
		chatState:      chatState,
	}
}
func (a *antigravityChatStreamAdapter) Emit(event *protocolanthropic.AnthropicStreamEvent, writer *ClientWriter) {
	for _, responseEvent := range bridge.AnthropicEventToResponsesEvents(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, event, a.anthropicState) {
		a.emitResponseEvent(&responseEvent, writer)
	}
}
func (a *antigravityChatStreamAdapter) Finalize(writer *ClientWriter) {
	for _, responseEvent := range bridge.FinalizeAnthropicResponsesStream(a.anthropicState) {
		a.emitResponseEvent(&responseEvent, writer)
	}
	for _, chunk := range bridge.FinalizeResponsesChatStream(a.chatState) {
		a.writeChunk(chunk, writer)
	}
	writer.Write([]byte("data: [DONE]\n\n"))
}
func (a *antigravityChatStreamAdapter) WriteError(writer *ClientWriter, reason string) {
	writer.Fprintf("data: {\"error\":{\"message\":%q,\"type\":\"upstream_error\"}}\n\n", reason)
}
func (a *antigravityChatStreamAdapter) emitResponseEvent(event *protocolopenai.ResponsesStreamEvent, writer *ClientWriter) {
	for _, chunk := range bridge.ResponsesEventToChatChunks(event, a.chatState) {
		a.writeChunk(chunk, writer)
	}
}

// writeChunk 在输出前恢复工具名。
func (a *antigravityChatStreamAdapter) writeChunk(chunk protocolopenai.ChatCompletionsChunk, writer *ClientWriter) {
	payload, err := json.Marshal(chunk)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "Failed to marshal Antigravity chat chunk: %v", err)
		return
	}
	payload = a.reverseTools(payload)
	writer.Fprintf("data: %s\n\n", payload)
}

type antigravityResponsesStreamAdapter struct {
	c                  *upstream.OutputContext
	reverseTools       func([]byte) []byte
	anthropicState     *bridge.AnthropicEventToResponsesState
	clientToolRestorer *bridge.ResponsesClientToolStreamRestorer
}

func NewAntigravityResponsesStreamAdapter(
	c *upstream.OutputContext,
	model string,
	clientToolMapping bridge.ResponsesClientToolMapping, reverse func([]byte) []byte,
) *antigravityResponsesStreamAdapter {
	state := bridge.NewAnthropicEventToResponsesState(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read})
	state.Model = model
	return &antigravityResponsesStreamAdapter{
		c:                  c,
		reverseTools:       reverse,
		anthropicState:     state,
		clientToolRestorer: bridge.NewResponsesClientToolStreamRestorer(clientToolMapping),
	}
}
func (a *antigravityResponsesStreamAdapter) Emit(event *protocolanthropic.AnthropicStreamEvent, writer *ClientWriter) {
	for _, responseEvent := range bridge.AnthropicEventToResponsesEvents(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, event, a.anthropicState) {
		a.emitResponseEvent(responseEvent, writer)
	}
}
func (a *antigravityResponsesStreamAdapter) Finalize(writer *ClientWriter) {
	for _, responseEvent := range bridge.FinalizeAnthropicResponsesStream(a.anthropicState) {
		a.emitResponseEvent(responseEvent, writer)
	}
}
func (a *antigravityResponsesStreamAdapter) WriteError(writer *ClientWriter, reason string) {
	writer.Fprintf("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"upstream_error\",\"message\":%q}}\n\n", reason)
}
func (a *antigravityResponsesStreamAdapter) emitResponseEvent(event protocolopenai.ResponsesStreamEvent, writer *ClientWriter) {
	payload, err := json.Marshal(event)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "Failed to marshal Antigravity Responses event: %v", err)
		return
	}
	payload = a.reverseTools(payload)
	restoredPayloads, _, err := a.clientToolRestorer.RestoreEvent(payload)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "Failed to restore Antigravity Responses client tools: %v", err)
		return
	}
	for _, restored := range restoredPayloads {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(restored, &envelope); err != nil || envelope.Type == "" {
			logger.LegacyPrintf("service.antigravity_gateway", "Failed to read Antigravity Responses event type: %v", err)
			continue
		}
		writer.Fprintf("event: %s\ndata: %s\n\n", envelope.Type, restored)
	}
}

type antigravityCompatScanEvent struct {
	line string
	err  error
}
type antigravityCompatStreamSession struct {
	processor      *StreamingProcessor
	adapter        antigravityCompatStreamAdapter
	writer         *ClientWriter
	usage          *upstream.TokenUsage
	pendingEvents  []protocolanthropic.AnthropicStreamEvent
	firstTokenMs   *int
	startTime      time.Time
	meaningfulData bool
}

func NewAntigravityCompatStreamSession(
	model string,
	startTime time.Time,
	adapter antigravityCompatStreamAdapter,
	writer *ClientWriter,
) *antigravityCompatStreamSession {
	return &antigravityCompatStreamSession{
		processor: NewStreamingProcessor(model),
		adapter:   adapter,
		writer:    writer,
		usage:     &upstream.TokenUsage{},
		startTime: startTime,
	}
}
func (s *antigravityCompatStreamSession) consume(line string) {
	claudeEvents := s.processor.ProcessLine(strings.TrimRight(line, "\r\n"))
	if len(claudeEvents) == 0 {
		return
	}
	s.consumeClaudeEvents(claudeEvents)
}
func (s *antigravityCompatStreamSession) hasMeaningfulData() bool {
	return s.meaningfulData
}
func (s *antigravityCompatStreamSession) finish() *StreamResult {
	finalEvents, usage := s.processor.Finish()
	MergeAntigravityCompatUsage(s.usage, usage)
	s.consumeClaudeEvents(finalEvents)
	s.adapter.Finalize(s.writer)
	return s.result(s.writer.Disconnected())
}
func (s *antigravityCompatStreamSession) collectResult(clientDisconnect bool) *StreamResult {
	_, usage := s.processor.Finish()
	MergeAntigravityCompatUsage(s.usage, usage)
	return s.result(clientDisconnect)
}
func (s *antigravityCompatStreamSession) result(clientDisconnect bool) *StreamResult {
	return &StreamResult{
		Usage:            s.usage,
		FirstTokenMs:     s.firstTokenMs,
		ClientDisconnect: clientDisconnect,
	}
}
func (s *antigravityCompatStreamSession) consumeClaudeEvents(data []byte) {
	var eventType string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			s.consumeClaudeData(eventType, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}
func (s *antigravityCompatStreamSession) consumeClaudeData(eventType, payload string) {
	var event protocolanthropic.AnthropicStreamEvent
	if json.Unmarshal([]byte(payload), &event) != nil {
		return
	}
	if event.Type == "" {
		event.Type = eventType
	}
	if event.Usage != nil {
		protocolanthropic.MergeAnthropicUsage(s.usage, *event.Usage)
	}
	if event.Message != nil {
		protocolanthropic.MergeAnthropicUsage(s.usage, event.Message.Usage)
	}
	s.emitOrBuffer(event)
}
func (s *antigravityCompatStreamSession) emitOrBuffer(event protocolanthropic.AnthropicStreamEvent) {
	if s.meaningfulData {
		s.adapter.Emit(&event, s.writer)
		return
	}

	s.pendingEvents = append(s.pendingEvents, event)
	if !IsMeaningfulAntigravityCompatEvent(&event) {
		return
	}

	s.meaningfulData = true
	ms := int(time.Since(s.startTime).Milliseconds())
	s.firstTokenMs = &ms
	for i := range s.pendingEvents {
		s.adapter.Emit(&s.pendingEvents[i], s.writer)
	}
	s.pendingEvents = nil
}
func IsMeaningfulAntigravityCompatEvent(event *protocolanthropic.AnthropicStreamEvent) bool {
	if event == nil {
		return false
	}
	if event.Type == "message_stop" {
		return true
	}
	if event.ContentBlock != nil {
		block := event.ContentBlock
		return block.Type == "tool_use" ||
			block.Text != "" ||
			block.Thinking != "" ||
			block.Signature != "" ||
			block.Source != nil
	}
	if event.Delta != nil {
		delta := event.Delta
		return delta.Text != "" ||
			delta.PartialJSON != "" ||
			delta.Thinking != "" ||
			delta.Signature != "" ||
			delta.StopReason != ""
	}
	return false
}
func MergeAntigravityCompatUsage(dst *upstream.TokenUsage, src *protocolanthropic.ClaudeUsage) {
	if dst == nil || src == nil {
		return
	}
	dst.InputTokens = src.InputTokens
	dst.OutputTokens = src.OutputTokens
	dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	dst.CacheReadInputTokens = src.CacheReadInputTokens
	dst.ImageOutputTokens = src.ImageOutputTokens
}
func (s *ResponseAdapter) HandleAntigravityCompatStream(
	c *upstream.OutputContext,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	adapter antigravityCompatStreamAdapter,
	prefix string,
) (*StreamResult, error) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	writer := NewClientWriter(c.Writer, flusher, prefix)
	writer.beforeFirstWrite = func() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
	}
	session := NewAntigravityCompatStreamSession(originalModel, startTime, adapter, writer)
	events, stopScanner, maxLineSize := s.StartAntigravityCompatScanner(resp.Body)
	defer stopScanner()

	timeout := s.AntigravityCompatStreamTimeout()
	timeoutTimer, timeoutCh := NewAntigravityCompatTimer(timeout)
	if timeoutTimer != nil {
		defer timeoutTimer.Stop()
	}
	keepaliveTicker, keepaliveCh := s.NewAntigravityCompatKeepaliveTicker()
	if keepaliveTicker != nil {
		defer keepaliveTicker.Stop()
	}

	for {
		select {
		case event, open := <-events:
			if !open {
				if !session.hasMeaningfulData() && !writer.Disconnected() {
					return nil, AntigravityCompatEmptyStreamError(s.Options.Failover)
				}
				return session.finish(), nil
			}
			if event.err != nil {
				return s.HandleAntigravityCompatReadError(c, session, event.err, maxLineSize, prefix)
			}
			ResetAntigravityCompatTimer(timeoutTimer, timeout)
			s.observeRaw([]byte(event.line))
			session.consume(event.line)

		case <-timeoutCh:
			if writer.Disconnected() {
				return session.collectResult(true), nil
			}
			if !session.hasMeaningfulData() {
				return nil, AntigravityCompatEmptyStreamError(s.Options.Failover)
			}
			logger.LegacyPrintf("service.antigravity_gateway", "Stream data interval timeout (%s)", prefix)
			WriteAntigravityCompatStreamError(c, adapter, writer, "stream_timeout", s.Options.MarkCommitted)
			return session.collectResult(false), fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if session.hasMeaningfulData() && !writer.Disconnected() {
				writer.Write([]byte(": ping\n\n"))
			}
		}
	}
}
func (s *ResponseAdapter) StartAntigravityCompatScanner(
	body io.Reader,
) (<-chan antigravityCompatScanEvent, func(), int) {
	maxLineSize := upstream.DefaultSSELineLimit
	if s.Options.MaxLineSize > 0 {
		maxLineSize = s.Options.MaxLineSize
	}
	scanner := bufio.NewScanner(body)
	scanBuf := httpclient.GetSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)

	events := make(chan antigravityCompatScanEvent, 16)
	done := make(chan struct{})
	go func() {
		defer httpclient.PutSSEScannerBuf64K(scanBuf)
		defer close(events)
		send := func(event antigravityCompatScanEvent) bool {
			select {
			case events <- event:
				return true
			case <-done:
				return false
			}
		}
		for scanner.Scan() {
			if !send(antigravityCompatScanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			send(antigravityCompatScanEvent{err: err})
		}
	}()
	return events, func() { close(done) }, maxLineSize
}
func (s *ResponseAdapter) AntigravityCompatStreamTimeout() time.Duration {
	if false {
		return 0
	}
	return time.Duration(s.Options.StreamDataIntervalTimeout) * time.Second
}
func (s *ResponseAdapter) NewAntigravityCompatKeepaliveTicker() (*time.Ticker, <-chan time.Time) {
	if false {
		return nil, nil
	}
	interval := time.Duration(s.Options.StreamKeepaliveInterval) * time.Second
	if interval <= 0 {
		return nil, nil
	}
	ticker := time.NewTicker(interval)
	return ticker, ticker.C
}
func NewAntigravityCompatTimer(timeout time.Duration) (*time.Timer, <-chan time.Time) {
	if timeout <= 0 {
		return nil, nil
	}
	timer := time.NewTimer(timeout)
	return timer, timer.C
}
func ResetAntigravityCompatTimer(timer *time.Timer, timeout time.Duration) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
}
func (s *ResponseAdapter) HandleAntigravityCompatReadError(
	c *upstream.OutputContext,
	session *antigravityCompatStreamSession,
	err error,
	maxLineSize int,
	prefix string,
) (*StreamResult, error) {
	if !session.hasMeaningfulData() && !session.writer.Disconnected() {
		return nil, AntigravityCompatEmptyStreamError(s.Options.Failover)
	}
	if disconnect, handled := HandleStreamReadError(err, session.writer.Disconnected(), prefix); handled {
		return session.collectResult(disconnect), nil
	}
	if errors.Is(err, bufio.ErrTooLong) {
		logger.LegacyPrintf("service.antigravity_gateway", "SSE line too long (%s): max_size=%d error=%v", prefix, maxLineSize, err)
		WriteAntigravityCompatStreamError(c, session.adapter, session.writer, "response_too_large", s.Options.MarkCommitted)
		return session.result(false), err
	}
	WriteAntigravityCompatStreamError(c, session.adapter, session.writer, "stream_read_error", s.Options.MarkCommitted)
	return nil, fmt.Errorf("stream read error: %w", err)
}
func WriteAntigravityCompatStreamError(
	c *upstream.OutputContext,
	adapter antigravityCompatStreamAdapter,
	writer *ClientWriter,
	reason string,
	mark func(),
) {
	adapter.WriteError(writer, reason)
	mark()
}
func AntigravityCompatEmptyStreamError(failover func([]byte) error) error {
	logger.LegacyPrintf("service.antigravity_gateway", "Empty Antigravity compatibility stream, triggering failover")
	return failover([]byte(`{"error":"empty stream response from upstream"}`))
}
func (s *ResponseAdapter) HandleChatCompletionsStreamingFromAntigravity(
	c *upstream.OutputContext,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	includeUsage bool,
) (*StreamResult, error) {
	return s.HandleAntigravityCompatStream(
		c,
		resp,
		startTime,
		originalModel,
		NewAntigravityChatStreamAdapter(c, originalModel, includeUsage, s.Options.ReverseTools),
		"antigravity chat completions stream",
	)
}
func (s *ResponseAdapter) HandleResponsesStreamingFromAntigravity(
	c *upstream.OutputContext,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	clientToolMapping bridge.ResponsesClientToolMapping,
) (*StreamResult, error) {
	return s.HandleAntigravityCompatStream(
		c,
		resp,
		startTime,
		originalModel,
		NewAntigravityResponsesStreamAdapter(c, originalModel, clientToolMapping, s.Options.ReverseTools),
		"antigravity responses stream",
	)
}
