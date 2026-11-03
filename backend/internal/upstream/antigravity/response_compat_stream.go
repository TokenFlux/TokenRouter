package antigravity

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// response_compat_stream.go converts Antigravity streams to OpenAI protocols
// while buffering output until meaningful data arrives.
// OpenAI compatibility streams pass Antigravity data through Claude events
// before Responses or Chat conversion. Prelude events stay buffered until
// content or a completion observation qualifies as meaningful, so an empty
// upstream can select failover before client headers and output are written.
//
// The session merges usage before staging output and releases pending events
// when meaningful content arrives. Stream termination, timeout and read
// failure select different finalization paths, including usage collection
// after client disconnect. Headers are prepared by the first actual writer
// call. The HTTP boundary owns downstream heartbeat generation. The shared
// Gemini reader validates frames, collects trailing usage, closes the body,
// and joins its worker. Tool restoration and conversion state remain local
// to the ordered stream.

// antigravityCompatStreamAdapter converts ordered Claude events and writes
// representation-specific completion or failure output.
type antigravityCompatStreamAdapter interface {
	// Emit converts one ordered Claude event through adapter state and writes
	// selected output. Callers serialize events and writer access; suppressed
	// conversion failures can leave the method without emitted bytes.
	Emit(*protocolanthropic.AnthropicStreamEvent, *ClientWriter)
	// Finalize drains adapter completion state into the writer after input ends.
	// Callers invoke it in stream order, and the writer can suppress final output
	// after a recorded disconnect.
	Finalize(*ClientWriter)
	// WriteError writes representation-specific upstream failure text using the
	// supplied writer. Write failure is retained by ClientWriter, and the owning
	// handler decides commitment and stream termination after this call.
	WriteError(*ClientWriter, string)
}

// antigravityChatStreamAdapter owns ordered Claude-to-Responses-to-Chat
// conversion state for one compatibility stream.
type antigravityChatStreamAdapter struct {
	c *upstream.OutputContext // retained constructor context; emission uses writer
	// reverseTools restores serialized Chat tool names before writing; required
	// for emitted chunks and supplied by the owning response adapter.
	reverseTools func([]byte) []byte
	// anthropicState belongs to one ordered stream and feeds Responses events
	// into chatState; callers serialize emission and finalization of both states.
	anthropicState *bridge.AnthropicEventToResponsesState
	// chatState retains Chat identity and optional usage output across
	// intermediate Responses events; callers serialize access through stream
	// finalization.
	chatState *bridge.ResponsesEventToChatState
}

// antigravityResponsesStreamAdapter owns ordered Responses conversion and
// client-tool restoration state for one stream.
type antigravityResponsesStreamAdapter struct {
	c *upstream.OutputContext // retained constructor context; emission uses writer
	// reverseTools restores provider-mapped tool names before client-tool
	// restoration; required for emitted Responses events and owned by the
	// response caller.
	reverseTools func([]byte) []byte
	// anthropicState accumulates one stream conversion through finalization;
	// callers serialize its event use and do not share it across answers.
	anthropicState *bridge.AnthropicEventToResponsesState
	// clientToolRestorer owns per-stream call/sequence state and borrows client
	// mappings; callers keep mappings stable while restoration processes ordered
	// events.
	clientToolRestorer *bridge.ResponsesClientToolStreamRestorer
}

// antigravityCompatStreamSession stages ordered prelude output and retains
// usage/timing observations for one answer.
type antigravityCompatStreamSession struct {
	// processor owns the ordered upstream-to-Claude conversion state; session
	// finish or disconnected collection finalizes it before retaining final
	// usage.
	processor *StreamingProcessor
	// adapter owns client representation conversion; session emission and
	// finalization use it in order on the same writer.
	adapter antigravityCompatStreamAdapter
	// writer is borrowed for session output and disconnect observation; callers
	// serialize access and own its underlying stream lifecycle.
	writer *ClientWriter
	// usage is mutable session token state shared with returned StreamResult;
	// callers finish ordered processing before reading retained result values.
	usage *upstream.TokenUsage
	// pendingEvents holds prelude events until meaningfulData becomes true; the
	// first qualifying observation releases the entire sequence in original
	// order.
	pendingEvents []protocolanthropic.AnthropicStreamEvent
	// meaningfulData stays true after a qualifying content/completion observation
	// and controls prelude release and empty-stream
	// recovery.
	meaningfulData bool
}

// NewAntigravityChatStreamAdapter constructs per-stream conversion state using
// model and optional usage output. reverse is required for emitted chunks;
// callers serialize emission/finalization on the same writer and keep one
// adapter per stream.
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

// Emit delegates ordered Claude-to-Responses conversion and then Chat
// emission. Callers serialize adapter/writer state across events; shared
// conversion and writer disconnect policy determine whether any bytes are
// emitted.
func (a *antigravityChatStreamAdapter) Emit(event *protocolanthropic.AnthropicStreamEvent, writer *ClientWriter) {
	for _, responseEvent := range bridge.AnthropicEventToResponsesEvents(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, event, a.anthropicState) {
		a.emitResponseEvent(&responseEvent, writer)
	}
}

// Finalize drains Responses conversion before Chat completion and the DONE
// marker. Callers invoke it after input ends and serialize access; a
// disconnected writer suppresses output while state finalization still runs.
func (a *antigravityChatStreamAdapter) Finalize(writer *ClientWriter) {
	for _, responseEvent := range bridge.FinalizeAnthropicResponsesStream(a.anthropicState) {
		a.emitResponseEvent(&responseEvent, writer)
	}
	for _, chunk := range bridge.FinalizeResponsesChatStream(a.chatState) {
		a.writeChunk(chunk, writer)
	}
	writer.Write([]byte("data: [DONE]\n\n"))
}

// WriteError writes a Chat upstream-error event with quoted reason text.
// ClientWriter records a write failure, while the owning handler decides
// commitment and return behavior after this attempt.
func (a *antigravityChatStreamAdapter) WriteError(writer *ClientWriter, reason string) {
	writer.Fprintf("data: {\"error\":{\"message\":%q,\"type\":\"upstream_error\"}}\n\n", reason)
}

// emitResponseEvent delegates one intermediate Responses event to Chat chunks
// and writes them in returned order. Callers serialize shared chatState and
// writer access through the same stream.
func (a *antigravityChatStreamAdapter) emitResponseEvent(event *protocolopenai.ResponsesStreamEvent, writer *ClientWriter) {
	for _, chunk := range bridge.ResponsesEventToChatChunks(event, a.chatState) {
		a.writeChunk(chunk, writer)
	}
}

// writeChunk serializes a Chat chunk and restores provider-mapped tool names
// before writing. Marshal failures are logged and suppress that chunk;
// reverseTools is required, and ClientWriter retains downstream write failure.
func (a *antigravityChatStreamAdapter) writeChunk(chunk protocolopenai.ChatCompletionsChunk, writer *ClientWriter) {
	payload, err := json.Marshal(chunk)
	// Suppress only the chunk whose JSON encoding failed, recording the
	// diagnostic because this adapter emission API returns no error to the
	// handler.
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "Failed to marshal Antigravity chat chunk: %v", err)
		return
	}
	payload = a.reverseTools(payload)
	writer.Fprintf("data: %s\n\n", payload)
}

// NewAntigravityResponsesStreamAdapter constructs per-stream Responses
// conversion and tool restoration using clientToolMapping. reverse is required
// for output; mappings remain shared, so callers keep them stable and
// serialize event/finalization processing.
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

// Emit delegates one ordered Claude event into Responses events and
// restoration. Callers serialize shared conversion/restoration state and
// writer use; decoding/restoration failures can suppress individual output
// events.
func (a *antigravityResponsesStreamAdapter) Emit(event *protocolanthropic.AnthropicStreamEvent, writer *ClientWriter) {
	for _, responseEvent := range bridge.AnthropicEventToResponsesEvents(bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, event, a.anthropicState) {
		a.emitResponseEvent(responseEvent, writer)
	}
}

// Finalize delegates the remaining Responses completion events through normal
// restoration and emission. Invoke it after ordered input processing ends; the
// writer may suppress final bytes after a recorded disconnect.
func (a *antigravityResponsesStreamAdapter) Finalize(writer *ClientWriter) {
	for _, responseEvent := range bridge.FinalizeAnthropicResponsesStream(a.anthropicState) {
		a.emitResponseEvent(responseEvent, writer)
	}
}

// WriteError writes a named Responses error event with quoted reason text.
// ClientWriter retains downstream failure, while its owner decides commitment
// and terminal handling after the attempted error output.
func (a *antigravityResponsesStreamAdapter) WriteError(writer *ClientWriter, reason string) {
	writer.Fprintf("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"upstream_error\",\"message\":%q}}\n\n", reason)
}

// emitResponseEvent serializes an event, restores provider names, then
// restores client tool representation. Callers serialize state and keep the
// borrowed mapping stable; marshal/restoration failures are logged and
// suppress output.
//
// The restorer can produce several payloads from one event. Each restored
// payload supplies its own nonempty type for SSE naming; invalid or
// missing-type output is skipped while later payloads can still emit.
// Restoration state is retained even after a logged error, and this method
// exposes no returned error to the handler. ClientWriter retains write
// failures for the handler to inspect.
func (a *antigravityResponsesStreamAdapter) emitResponseEvent(event protocolopenai.ResponsesStreamEvent, writer *ClientWriter) {
	payload, err := json.Marshal(event)
	// Log and suppress serialization/restoration failures on this event because
	// the emission method has no returned error; later session events can
	// continue.
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "Failed to marshal Antigravity Responses event: %v", err)
		return
	}
	payload = a.reverseTools(payload)
	// Restore client tool representation after provider-name reversal, allowing
	// one upstream event to expand into several client events with their own
	// event names.
	restoredPayloads, _, err := a.clientToolRestorer.RestoreEvent(payload)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "Failed to restore Antigravity Responses client tools: %v", err)
		return
	}
	for _, restored := range restoredPayloads {
		var envelope struct {
			Type string `json:"type"` // restored SSE event name
		}
		// Use each restored payload type for SSE naming, skipping undecodable or
		// blank-type payloads so remaining restored events can still be written in
		// order.
		if err := json.Unmarshal(restored, &envelope); err != nil || envelope.Type == "" {
			logger.LegacyPrintf("service.antigravity_gateway", "Failed to read Antigravity Responses event type: %v", err)
			continue
		}
		writer.Fprintf("event: %s\ndata: %s\n\n", envelope.Type, restored)
	}
}

// NewAntigravityCompatStreamSession constructs upstream conversion, pending
// output, and token state for one ordered stream. Callers supply an adapter
// and writer that remain valid through finalization and serialize all session
// methods.
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
	}
}

// consume delegates one upstream line into ordered Claude events and consumes
// any resulting batch. A payload yielding no events skips downstream session
// consumption, while processor usage can still change; finish/collectResult
// recover that final usage. Callers serialize the processor, adapter and writer.
func (s *antigravityCompatStreamSession) consume(line string) {
	claudeEvents := s.processor.ProcessLine(strings.TrimRight(line, "\r\n"))
	if len(claudeEvents) == 0 {
		return
	}
	s.consumeClaudeEvents(claudeEvents)
}

// hasMeaningfulData reports whether content or completion has selected prelude
// release for this session.
func (s *antigravityCompatStreamSession) hasMeaningfulData() bool {
	return s.meaningfulData
}

// finish finalizes upstream conversion, merges its usage, consumes final
// Claude events, and finalizes the client adapter. Callers invoke it after
// ordered input ends with valid writer/adapter state.
//
// Usage finalization happens before final events and adapter completion can
// write output. Those events still pass through the prelude policy, preserving
// pending ordering when completion first qualifies. The returned result
// references session usage and timing, and reflects writer disconnection after
// attempted final output. Callers stop session mutation before reading that
// retained state.
func (s *antigravityCompatStreamSession) finish() *StreamResult {
	finalEvents, usage := s.processor.Finish()
	MergeAntigravityCompatUsage(s.usage, usage)
	s.consumeClaudeEvents(finalEvents)
	s.adapter.Finalize(s.writer)
	return s.result(s.writer.Disconnected())
}

// collectResult finalizes upstream usage while discarding final events and
// returns retained observations. It is used for terminal/disconnected paths
// that should collect accounting data without another adapter finalization or
// output attempt.
func (s *antigravityCompatStreamSession) collectResult(clientDisconnect bool) *StreamResult {
	_, usage := s.processor.Finish()
	MergeAntigravityCompatUsage(s.usage, usage)
	return s.result(clientDisconnect)
}

// result returns session usage and timing references with the supplied
// disconnect observation. Callers read the result after ordered session
// processing stops, because those pointed values are retained mutable session
// state.
func (s *antigravityCompatStreamSession) result(clientDisconnect bool) *StreamResult {
	return &StreamResult{
		Usage:            s.usage,
		ClientDisconnect: clientDisconnect,
	}
}

// consumeClaudeEvents dispatches data lines from a generated Claude SSE batch
// with the latest event label in that batch. Blank lines do not reset the
// label, and a data payload can supply its own type.
//
// The label starts empty on each call and changes only when another event line
// appears. Each participating data line passes to decoding/usage collection
// before prelude staging, so labels stay associated with the emitted batch
// order. This helper handles generated single-line payloads rather than
// assembling arbitrary multiline SSE data. Callers serialize session mutation
// and output.
func (s *antigravityCompatStreamSession) consumeClaudeEvents(data []byte) {
	var eventType string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			s.consumeClaudeData(eventType, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}

// consumeClaudeData decodes one Claude payload and merges event/message usage
// before staging output. Invalid JSON is skipped, and a missing payload type
// uses the event-line label supplied by the batch reader.
//
// Shared usage merging interprets partial Messages observations before the
// event can trigger prelude release. Event-level usage is processed before
// message-level usage when both exist, then emitOrBuffer decides output timing
// and ordering. Decoder success alone does not mark the session meaningful;
// only the selected content/completion classifier releases pending events.
// Callers serialize all session state.
func (s *antigravityCompatStreamSession) consumeClaudeData(eventType, payload string) {
	// Skip a malformed generated payload before merging usage or releasing the
	// prelude, so partially decoded fields cannot alter this session output state.
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

// emitOrBuffer writes ordered events after meaningful data has been observed,
// or appends them to the pending prelude first. Callers serialize session and
// writer access, and the first qualifying observation records timing even if
// downstream writing then fails.
//
// The qualifying event joins the pending sequence before release, so earlier
// message-start/usage events keep their order ahead of content or completion.
// Every pending event is passed to the adapter before the slice is cleared.
// Later events emit directly; a completion observation can release the
// sequence without visible text, so this state differs from a
// semantic-content-only predicate.
func (s *antigravityCompatStreamSession) emitOrBuffer(event protocolanthropic.AnthropicStreamEvent) {
	if s.meaningfulData {
		s.adapter.Emit(&event, s.writer)
		return
	}

	// Queue the qualifying event before release so earlier prelude messages and
	// their usage remain ahead of content/completion in adapter output.
	s.pendingEvents = append(s.pendingEvents, event)
	if !IsMeaningfulAntigravityCompatEvent(&event) {
		return
	}

	s.meaningfulData = true

	for i := range s.pendingEvents {
		s.adapter.Emit(&s.pendingEvents[i], s.writer)
	}
	s.pendingEvents = nil
}

// IsMeaningfulAntigravityCompatEvent reports whether an event can release
// compatible prelude output. Nil returns false, and message_stop qualifies
// before payload shape inspection, allowing completion to release a stream
// without visible text.
//
// A present ContentBlock selects its tool/text/thinking/signature/source
// checks before Delta is considered. Otherwise a present Delta qualifies on
// text, partial JSON, thinking, signature, or stop reason. An empty present
// block can therefore suppress qualification from a simultaneous delta,
// following shape precedence. Ordinary message-start/usage-only observations
// stay buffered until another event qualifies.
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

// MergeAntigravityCompatUsage copies final Claude token fields into dst,
// including explicit zero values and image output. Nil source or destination
// has no effect; callers serialize access to the mutable destination.
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

// HandleAntigravityCompatStream coordinates one ordered upstream stream with
// compatible output and upstream timeout handling. Callers supply a nonnil
// response/body and flushing output writer, required failure callbacks, and
// may close the idempotent body again after the shared reader has released it.
//
// Prelude remains buffered until content/completion qualifies, and
// headers/status are prepared before the first writer call. Shared frame reading
// validates completion and collects trailing usage before adapter finalization.
// Read failure before meaningful output selects Failover; disconnection retains
// observed usage without further output. Errors after content emit the existing
// committed failure, while successful finalization emits complete cache counts
// before the single protocol terminal event.
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
	// Prepare headers only through the first writer call, leaving empty prelude
	// failures eligible for caller failover before compatible stream status is
	// written.
	writer.beforeFirstWrite = func() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
	}
	session := NewAntigravityCompatStreamSession(originalModel, startTime, adapter, writer)
	observed, err := s.readGeminiStream(resp, s.unwrapConvertedGeminiResponse, func(event upstream.GeminiStreamEvent) error {
		if len(event.Data) > 0 {
			s.observeRaw(event.Data)
			session.consume("data: " + string(event.Data))
		}
		if event.Terminal {
			c.Complete()
		}
		return nil
	})
	// Refresh the processor before either failure collection or successful final
	// output, so all consumers retain the same sparse-tail cache observations.
	session.processor.SetUsageSnapshot(bridge.NativeExtractGeminiUsage(observed.UsageJSON))
	if err != nil {
		return s.HandleAntigravityCompatReadError(c, session, err, s.Options.MaxLineSize, prefix)
	}
	return session.finish(), nil
}

// HandleAntigravityCompatReadError chooses failover, usage collection, or
// committed failure for a validated reader error. Callers provide live
// session/writer state and applicable failure callbacks after shared reader
// cleanup; conversion finalization cannot resume successful output on failure.
//
// When neither meaningful data nor a client disconnect has been observed,
// empty-stream policy wins before cancellation classification. Later
// cancellation or an existing disconnect returns finalized usage without
// another error event. A too-long
// frame emits a committed size error and returns observed usage. Other read
// failures emit the corresponding committed failure and return no adapter
// result; the execution owner retains its independent raw usage observations.
func (s *ResponseAdapter) HandleAntigravityCompatReadError(
	c *upstream.OutputContext,
	session *antigravityCompatStreamSession,
	err error,
	maxLineSize int,
	prefix string,
) (*StreamResult, error) {
	// Choose empty-stream recovery before cancellation classification when no
	// meaningful output exists, so a cancelled early reader can still produce the
	// supplied failover signal.
	if !session.hasMeaningfulData() && !session.writer.Disconnected() {
		return nil, AntigravityCompatEmptyStreamError(s.Options.Failover)
	}
	if disconnect, handled := HandleStreamReadError(err, session.writer.Disconnected(), prefix); handled {
		return session.collectResult(disconnect), nil
	}
	var streamError *upstream.GeminiStreamError
	tooLarge := errors.Is(err, bufio.ErrTooLong) ||
		(errors.As(err, &streamError) && streamError.Kind == "stream_frame_too_large")
	if tooLarge {
		logger.LegacyPrintf("service.antigravity_gateway", "SSE line too long (%s): max_size=%d error=%v", prefix, maxLineSize, err)
		WriteAntigravityCompatStreamError(c, session.adapter, session.writer, "response_too_large", s.Options.MarkCommitted)
		return session.collectResult(false), err
	}
	reason := "stream_read_error"
	if errors.As(err, &streamError) && streamError.Kind == "stream_timeout" {
		reason = "stream_timeout"
	}
	WriteAntigravityCompatStreamError(c, session.adapter, session.writer, reason, s.Options.MarkCommitted)
	return nil, fmt.Errorf("stream read error: %w", err)
}

// WriteAntigravityCompatStreamError attempts representation-specific error
// output, then invokes the required commitment callback. Commitment is
// recorded even if ClientWriter suppresses or fails writing; the caller owns
// subsequent termination.
func WriteAntigravityCompatStreamError(
	c *upstream.OutputContext,
	adapter antigravityCompatStreamAdapter,
	writer *ClientWriter,
	reason string,
	mark func(),
) {
	// Try the protocol error event before recording commitment; the writer can
	// suppress bytes after disconnect, but caller terminal handling still closes
	// this retry window.
	adapter.WriteError(writer, reason)
	// Record commitment after the attempted error write even when writer output
	// failed or was suppressed, leaving terminal policy with the owning handler.
	mark()
}

// AntigravityCompatEmptyStreamError logs empty compatible output and returns
// the supplied failover callback result. The callback is required and
// determines retry/switch classification from the fixed empty-response
// payload.
func AntigravityCompatEmptyStreamError(failover func([]byte) error) error {
	logger.LegacyPrintf("service.antigravity_gateway", "Empty Antigravity compatibility stream, triggering failover")
	return failover([]byte(`{"error":"empty stream response from upstream"}`))
}

// HandleChatCompletionsStreamingFromAntigravity constructs one Chat adapter
// and delegates compatible streaming with optional usage output. Callers
// provide required response callbacks and keep the upstream body open until
// handling returns, then close it.
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

// HandleResponsesStreamingFromAntigravity constructs one Responses adapter and
// delegates compatible streaming using clientToolMapping. Callers keep
// mappings stable, supply required callbacks, and close the upstream body
// after ordered handling completes.
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
