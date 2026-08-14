package service

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/antigravity"
	"github.com/BrandonVee/TokenRouter/internal/pkg/apicompat"
	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

type antigravityCompatStreamAdapter interface {
	Emit(*apicompat.AnthropicStreamEvent, *antigravityClientWriter)
	Finalize(*antigravityClientWriter)
	WriteError(*antigravityClientWriter, string)
	ResponseBody(*ClaudeUsage) []byte
}

type antigravityChatStreamAdapter struct {
	c                 *gin.Context
	anthropicState    *apicompat.AnthropicEventToResponsesState
	chatState         *apicompat.ResponsesEventToChatState
	streamAccumulator *openAIChatCompletionsStreamAccumulator
}

func newAntigravityChatStreamAdapter(c *gin.Context, model string, includeUsage bool) *antigravityChatStreamAdapter {
	anthropicState := apicompat.NewAnthropicEventToResponsesState()
	anthropicState.Model = model
	chatState := apicompat.NewResponsesEventToChatState()
	chatState.Model = model
	chatState.IncludeUsage = includeUsage
	return &antigravityChatStreamAdapter{
		c:                 c,
		anthropicState:    anthropicState,
		chatState:         chatState,
		streamAccumulator: newOpenAIChatCompletionsStreamAccumulator(model),
	}
}

func (a *antigravityChatStreamAdapter) Emit(event *apicompat.AnthropicStreamEvent, writer *antigravityClientWriter) {
	for _, responseEvent := range apicompat.AnthropicEventToResponsesEvents(event, a.anthropicState) {
		a.emitResponseEvent(&responseEvent, writer)
	}
}

func (a *antigravityChatStreamAdapter) Finalize(writer *antigravityClientWriter) {
	for _, responseEvent := range apicompat.FinalizeAnthropicResponsesStream(a.anthropicState) {
		a.emitResponseEvent(&responseEvent, writer)
	}
	for _, chunk := range apicompat.FinalizeResponsesChatStream(a.chatState) {
		a.writeChunk(chunk, writer)
	}
	writer.Write([]byte("data: [DONE]\n\n"))
}

func (a *antigravityChatStreamAdapter) WriteError(writer *antigravityClientWriter, reason string) {
	writer.Fprintf("data: {\"error\":{\"message\":%q,\"type\":\"upstream_error\"}}\n\n", reason)
}

func (a *antigravityChatStreamAdapter) emitResponseEvent(event *apicompat.ResponsesStreamEvent, writer *antigravityClientWriter) {
	for _, chunk := range apicompat.ResponsesEventToChatChunks(event, a.chatState) {
		a.writeChunk(chunk, writer)
	}
}

// writeChunk 在输出前恢复工具名，并同步累积用于数据共享的非流式快照。
func (a *antigravityChatStreamAdapter) writeChunk(chunk apicompat.ChatCompletionsChunk, writer *antigravityClientWriter) {
	payload, err := json.Marshal(chunk)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "Failed to marshal Antigravity chat chunk: %v", err)
		return
	}
	payload = reverseToolNamesIfPresent(a.c, payload)
	observeOpenAIChatStreamPayload(a.streamAccumulator, payload, nil)
	writer.Fprintf("data: %s\n\n", payload)
}

// ResponseBody 返回已写给客户端的 Chat Completions 聚合快照。
func (a *antigravityChatStreamAdapter) ResponseBody(usage *ClaudeUsage) []byte {
	if usage == nil {
		return a.streamAccumulator.ResponseBody(nil)
	}
	return a.streamAccumulator.ResponseBody(openAIUsageFromClaudeUsage(*usage))
}

type antigravityResponsesStreamAdapter struct {
	c                  *gin.Context
	anthropicState     *apicompat.AnthropicEventToResponsesState
	clientToolRestorer *apicompat.ResponsesClientToolStreamRestorer
}

func newAntigravityResponsesStreamAdapter(
	c *gin.Context,
	model string,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) *antigravityResponsesStreamAdapter {
	state := apicompat.NewAnthropicEventToResponsesState()
	state.Model = model
	return &antigravityResponsesStreamAdapter{
		c:                  c,
		anthropicState:     state,
		clientToolRestorer: apicompat.NewResponsesClientToolStreamRestorer(clientToolMapping),
	}
}

func (a *antigravityResponsesStreamAdapter) Emit(event *apicompat.AnthropicStreamEvent, writer *antigravityClientWriter) {
	for _, responseEvent := range apicompat.AnthropicEventToResponsesEvents(event, a.anthropicState) {
		a.emitResponseEvent(responseEvent, writer)
	}
}

func (a *antigravityResponsesStreamAdapter) Finalize(writer *antigravityClientWriter) {
	for _, responseEvent := range apicompat.FinalizeAnthropicResponsesStream(a.anthropicState) {
		a.emitResponseEvent(responseEvent, writer)
	}
}

func (a *antigravityResponsesStreamAdapter) WriteError(writer *antigravityClientWriter, reason string) {
	writer.Fprintf("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"upstream_error\",\"message\":%q}}\n\n", reason)
}

func (a *antigravityResponsesStreamAdapter) emitResponseEvent(event apicompat.ResponsesStreamEvent, writer *antigravityClientWriter) {
	payload, err := json.Marshal(event)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "Failed to marshal Antigravity Responses event: %v", err)
		return
	}
	payload = reverseToolNamesIfPresent(a.c, payload)
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

// ResponseBody 当前不为 Responses 流生成数据共享快照。
func (a *antigravityResponsesStreamAdapter) ResponseBody(*ClaudeUsage) []byte { return nil }

type antigravityCompatScanEvent struct {
	line string
	err  error
}

type antigravityCompatStreamSession struct {
	processor      *antigravity.StreamingProcessor
	adapter        antigravityCompatStreamAdapter
	writer         *antigravityClientWriter
	usage          *ClaudeUsage
	pendingEvents  []apicompat.AnthropicStreamEvent
	firstTokenMs   *int
	startTime      time.Time
	meaningfulData bool
}

func newAntigravityCompatStreamSession(
	model string,
	startTime time.Time,
	adapter antigravityCompatStreamAdapter,
	writer *antigravityClientWriter,
) *antigravityCompatStreamSession {
	return &antigravityCompatStreamSession{
		processor: antigravity.NewStreamingProcessor(model),
		adapter:   adapter,
		writer:    writer,
		usage:     &ClaudeUsage{},
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

func (s *antigravityCompatStreamSession) finish() *antigravityStreamResult {
	finalEvents, usage := s.processor.Finish()
	mergeAntigravityCompatUsage(s.usage, usage)
	s.consumeClaudeEvents(finalEvents)
	s.adapter.Finalize(s.writer)
	return s.result(s.writer.Disconnected())
}

func (s *antigravityCompatStreamSession) collectResult(clientDisconnect bool) *antigravityStreamResult {
	_, usage := s.processor.Finish()
	mergeAntigravityCompatUsage(s.usage, usage)
	return s.result(clientDisconnect)
}

func (s *antigravityCompatStreamSession) result(clientDisconnect bool) *antigravityStreamResult {
	return &antigravityStreamResult{
		usage:            s.usage,
		firstTokenMs:     s.firstTokenMs,
		responseBody:     cloneDataSharingRequestBody(s.adapter.ResponseBody(s.usage)),
		clientDisconnect: clientDisconnect,
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
	var event apicompat.AnthropicStreamEvent
	if json.Unmarshal([]byte(payload), &event) != nil {
		return
	}
	if event.Type == "" {
		event.Type = eventType
	}
	if event.Usage != nil {
		mergeAnthropicUsage(s.usage, *event.Usage)
	}
	if event.Message != nil {
		mergeAnthropicUsage(s.usage, event.Message.Usage)
	}
	s.emitOrBuffer(event)
}

func (s *antigravityCompatStreamSession) emitOrBuffer(event apicompat.AnthropicStreamEvent) {
	if s.meaningfulData {
		s.adapter.Emit(&event, s.writer)
		return
	}

	s.pendingEvents = append(s.pendingEvents, event)
	if !isMeaningfulAntigravityCompatEvent(&event) {
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

func isMeaningfulAntigravityCompatEvent(event *apicompat.AnthropicStreamEvent) bool {
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

func mergeAntigravityCompatUsage(dst *ClaudeUsage, src *antigravity.ClaudeUsage) {
	if dst == nil || src == nil {
		return
	}
	dst.InputTokens = src.InputTokens
	dst.OutputTokens = src.OutputTokens
	dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	dst.CacheReadInputTokens = src.CacheReadInputTokens
	dst.ImageOutputTokens = src.ImageOutputTokens
}

func (s *AntigravityGatewayService) handleAntigravityCompatStream(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	adapter antigravityCompatStreamAdapter,
	prefix string,
) (*antigravityStreamResult, error) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	writer := newAntigravityClientWriter(c.Writer, flusher, prefix)
	writer.beforeFirstWrite = func() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
	}
	session := newAntigravityCompatStreamSession(originalModel, startTime, adapter, writer)
	events, stopScanner, maxLineSize := s.startAntigravityCompatScanner(resp.Body)
	defer stopScanner()

	timeout := s.antigravityCompatStreamTimeout()
	timeoutTimer, timeoutCh := newAntigravityCompatTimer(timeout)
	if timeoutTimer != nil {
		defer timeoutTimer.Stop()
	}
	keepaliveTicker, keepaliveCh := s.newAntigravityCompatKeepaliveTicker()
	if keepaliveTicker != nil {
		defer keepaliveTicker.Stop()
	}

	for {
		select {
		case event, open := <-events:
			if !open {
				if !session.hasMeaningfulData() && !writer.Disconnected() {
					return nil, antigravityCompatEmptyStreamError()
				}
				return session.finish(), nil
			}
			if event.err != nil {
				return s.handleAntigravityCompatReadError(c, session, event.err, maxLineSize, prefix)
			}
			resetAntigravityCompatTimer(timeoutTimer, timeout)
			session.consume(event.line)

		case <-timeoutCh:
			if writer.Disconnected() {
				return session.collectResult(true), nil
			}
			if !session.hasMeaningfulData() {
				return nil, antigravityCompatEmptyStreamError()
			}
			logger.LegacyPrintf("service.antigravity_gateway", "Stream data interval timeout (%s)", prefix)
			writeAntigravityCompatStreamError(c, adapter, writer, "stream_timeout")
			return session.collectResult(false), fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if session.hasMeaningfulData() && !writer.Disconnected() {
				writer.Write([]byte(": ping\n\n"))
			}
		}
	}
}

func (s *AntigravityGatewayService) startAntigravityCompatScanner(
	body io.Reader,
) (<-chan antigravityCompatScanEvent, func(), int) {
	maxLineSize := defaultMaxLineSize
	if s.settingService != nil && s.settingService.cfg != nil && s.settingService.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.settingService.cfg.Gateway.MaxLineSize
	}
	scanner := bufio.NewScanner(body)
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)

	events := make(chan antigravityCompatScanEvent, 16)
	done := make(chan struct{})
	go func() {
		defer putSSEScannerBuf64K(scanBuf)
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

func (s *AntigravityGatewayService) antigravityCompatStreamTimeout() time.Duration {
	if s.settingService == nil || s.settingService.cfg == nil {
		return 0
	}
	return time.Duration(s.settingService.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
}

func (s *AntigravityGatewayService) newAntigravityCompatKeepaliveTicker() (*time.Ticker, <-chan time.Time) {
	if s.settingService == nil || s.settingService.cfg == nil {
		return nil, nil
	}
	interval := time.Duration(s.settingService.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	if interval <= 0 {
		return nil, nil
	}
	ticker := time.NewTicker(interval)
	return ticker, ticker.C
}

func newAntigravityCompatTimer(timeout time.Duration) (*time.Timer, <-chan time.Time) {
	if timeout <= 0 {
		return nil, nil
	}
	timer := time.NewTimer(timeout)
	return timer, timer.C
}

func resetAntigravityCompatTimer(timer *time.Timer, timeout time.Duration) {
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

func (s *AntigravityGatewayService) handleAntigravityCompatReadError(
	c *gin.Context,
	session *antigravityCompatStreamSession,
	err error,
	maxLineSize int,
	prefix string,
) (*antigravityStreamResult, error) {
	if !session.hasMeaningfulData() && !session.writer.Disconnected() {
		return nil, antigravityCompatEmptyStreamError()
	}
	if disconnect, handled := handleStreamReadError(err, session.writer.Disconnected(), prefix); handled {
		return session.collectResult(disconnect), nil
	}
	if errors.Is(err, bufio.ErrTooLong) {
		logger.LegacyPrintf("service.antigravity_gateway", "SSE line too long (%s): max_size=%d error=%v", prefix, maxLineSize, err)
		writeAntigravityCompatStreamError(c, session.adapter, session.writer, "response_too_large")
		return session.result(false), err
	}
	writeAntigravityCompatStreamError(c, session.adapter, session.writer, "stream_read_error")
	return nil, fmt.Errorf("stream read error: %w", err)
}

func writeAntigravityCompatStreamError(
	c *gin.Context,
	adapter antigravityCompatStreamAdapter,
	writer *antigravityClientWriter,
	reason string,
) {
	adapter.WriteError(writer, reason)
	MarkResponseCommitted(c)
}

func antigravityCompatEmptyStreamError() error {
	logger.LegacyPrintf("service.antigravity_gateway", "Empty Antigravity compatibility stream, triggering failover")
	return &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		ResponseBody:           []byte(`{"error":"empty stream response from upstream"}`),
		RetryableOnSameAccount: true,
	}
}

func (s *AntigravityGatewayService) handleChatCompletionsStreamingFromAntigravity(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	includeUsage bool,
) (*antigravityStreamResult, error) {
	return s.handleAntigravityCompatStream(
		c,
		resp,
		startTime,
		originalModel,
		newAntigravityChatStreamAdapter(c, originalModel, includeUsage),
		"antigravity chat completions stream",
	)
}

func (s *AntigravityGatewayService) handleResponsesStreamingFromAntigravity(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*antigravityStreamResult, error) {
	return s.handleAntigravityCompatStream(
		c,
		resp,
		startTime,
		originalModel,
		newAntigravityResponsesStreamAdapter(c, originalModel, clientToolMapping),
		"antigravity responses stream",
	)
}
