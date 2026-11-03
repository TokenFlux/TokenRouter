package antigravity

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// response_stream.go reads Antigravity SSE for streaming output or buffered
// response collection.
// Antigravity generation arrives as Gemini data inside v1internal SSE frames,
// even for buffered client requests. Streaming adapters observe normalized payloads
// before output and keep reading after a client write failure when the
// upstream reader continues, allowing later usage to reach attempt
// observation.
//
// Native, Messages, and OpenAI-compatible clients share validated frame reading
// and the bounded post-completion usage tail. The reader closes the response
// body and joins its worker before returning, including cancellation paths.
// Buffered collection retains candidate metadata while merging content and
// usage independently; streaming conversion defers protocol termination until
// the merged usage snapshot is ready. Neither sparse tail fields nor a client
// disconnect can erase cache counts already observed from the upstream.

// ResponseOptions supplies stream limits, raw observation, tool restoration,
// and representation-specific failure callbacks.
type ResponseOptions struct {
	// Context supplies the selected attempt cancellation. Nil permits
	// standalone codec use with a background context in the shared reader.
	Context    context.Context
	Candidates int // expected native candidate count

	// ObserveRaw receives normalized payloads synchronously before client
	// conversion/output; Executor chains it with attempt model and usage
	// observation. Callbacks leave supplied bytes unchanged and copy retained
	// mutable data before the owner continues synchronous processing.
	ObserveRaw func([]byte)
	// MaxLineSize bounds complete SSE frame bytes, with nonpositive selecting the
	// default. StreamDataIntervalTimeout supplies upstream idle-timeout seconds;
	// nonpositive values disable idle timing while retaining the bounded usage tail.
	MaxLineSize, StreamDataIntervalTimeout int
	// ReverseTools restores provider-mapped tool names in compatible serialized
	// output; required when a selected compatibility adapter emits or collects
	// payloads.
	ReverseTools func([]byte) []byte
	// ClaudeError maps buffered Messages collection failures; CompatError maps
	// compatible decoding, serialization, or tool-restoration failures. Both
	// receive output status, error type, and client-facing message when their
	// representation reaches those failures.
	ClaudeError, CompatError func(int, string, string) error
	// MapCollectionError maps Claude collection failures for OpenAI buffered
	// output; required when compatibility collection returns a failure.
	MapCollectionError func(error) error
	// Failover supplies the selected empty-stream failure or switch signal;
	// adapters require it when their representation-specific empty conditions are
	// reached.
	Failover func([]byte) error
	// IsFailover recognizes supplied empty-stream signals before buffered
	// Messages error mapping; required when that handler receives a collection
	// error.
	IsFailover func(error) bool
	// MarkCommitted records commitment after compatible error emission, even if
	// writing fails; required on that selected post-content error path.
	MarkCommitted func()
}

type (
	// ResponseAdapter uses Options to adapt one upstream stream and leaves final
	// response-body closing to the exchange owner.
	ResponseAdapter struct{ Options ResponseOptions } // stream adaptation and failure callbacks
	// StreamResult carries normalized adapter usage and observed client-write
	// observations for attempt completion.
	StreamResult struct {
		// Usage references collected adapter observations; streaming paths can
		// return this shared state after disconnect, while Executor separately
		// observes raw usage.
		Usage *upstream.TokenUsage

		// ClientDisconnect reports a failed client write or a handled
		// cancelled/read-failure path according to the selected response adapter.
		ClientDisconnect bool
	}
)

// ClientWriter writes and flushes synchronously while retaining a
// client-disconnect observation for stream handlers.
type ClientWriter struct {
	// w is the borrowed stream destination; ClientWriter writes synchronously and
	// leaves its closing or request lifecycle to the output owner.
	w upstream.OutputWriter
	// flusher is required for successful writes and supplies synchronous flush
	// calls; its interface reports no flush error to ClientWriter.
	flusher http.Flusher
	// disconnected stays true after a write error, suppressing later writes while
	// the owning handler can continue upstream usage collection.
	disconnected bool
	prefix       string // response-path diagnostic log prefix
	// beforeFirstWrite optionally prepares headers before output; it is cleared
	// before invocation so serialized reentry does not invoke the hook again.
	beforeFirstWrite func()
}

// NewClientWriter constructs a serial-use stream writer borrowing w and a
// nonnil flusher. Callers own resource closing and decide whether to continue
// upstream collection after Disconnected becomes true.
func NewClientWriter(w upstream.OutputWriter, flusher http.Flusher, prefix string) *ClientWriter {
	return &ClientWriter{w: w, flusher: flusher, prefix: prefix}
}

// Write prepares the first output, writes p synchronously, and flushes after a
// successful write. A prior disconnect or write error returns false; the
// flusher reports no delivery error, and callers serialize access.
func (cw *ClientWriter) Write(p []byte) bool {
	if cw.disconnected {
		return false
	}
	cw.prepareFirstWrite()
	if _, err := cw.w.Write(p); err != nil {
		cw.markDisconnected()
		return false
	}
	cw.flusher.Flush()
	return true
}

// Fprintf prepares the first output, formats into the borrowed destination,
// and flushes on successful writing. Write failure or prior disconnect returns
// false; callers serialize access and own downstream resource cleanup.
func (cw *ClientWriter) Fprintf(format string, args ...any) bool {
	if cw.disconnected {
		return false
	}
	cw.prepareFirstWrite()
	if _, err := fmt.Fprintf(cw.w, format, args...); err != nil {
		cw.markDisconnected()
		return false
	}
	cw.flusher.Flush()
	return true
}

// Disconnected reports whether this writer has already observed a client write
// failure.
func (cw *ClientWriter) Disconnected() bool { return cw.disconnected }

// prepareFirstWrite invokes the optional header-preparation hook before its
// first participating write. It clears the hook before invocation to tolerate
// serialized reentry; callers serialize access to writer state.
//
// Clearing precedes callback execution because a nested write can invoke this
// helper again. That nested write can begin the response before the outer
// write emits bytes, so the hook prepares its headers before starting nested
// output. A nested write failure can mark disconnect; this hook itself does
// not abort the outer Write or Fprintf call.
func (cw *ClientWriter) prepareFirstWrite() {
	if cw.beforeFirstWrite == nil {
		return
	}
	prepare := cw.beforeFirstWrite
	// Clear the hook before invoking it so a callback that performs a serialized
	// nested write does not recursively prepare the same output head.
	cw.beforeFirstWrite = nil
	prepare()
}

// markDisconnected retains the client-write failure observation and logs its
// response path.
func (cw *ClientWriter) markDisconnected() {
	cw.disconnected = true
	logger.LegacyPrintf("service.antigravity_gateway", "Client disconnected during streaming (%s), continuing to drain upstream for billing", cw.prefix)
}

// HandleStreamReadError classifies cancellation or a read error after client
// disconnect as handled collection termination. A handled result tells the
// caller to return collected usage with disconnect=true; other errors retain
// ordinary failure handling.
func HandleStreamReadError(err error, clientDisconnected bool, prefix string) (disconnect bool, handled bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		logger.LegacyPrintf("service.antigravity_gateway", "Context canceled during streaming (%s), returning collected usage", prefix)
		return true, true
	}
	if clientDisconnected {
		logger.LegacyPrintf("service.antigravity_gateway", "Upstream read error after client disconnect (%s): %v, returning collected usage", prefix, err)
		return true, true
	}
	return false, false
}

// HandleGeminiStreamingResponse unwraps and normalizes native Gemini SSE frames.
// Shared consumption validates completion and provider errors before delivery.
// Partial usage accompanies failures, and the reader owns idempotent body cleanup.
//
// A complete frame carries its whole-response terminal flag into the output sink,
// so heartbeat shutdown does not depend on inspecting a bounded JSON prefix.
// Transport-only DONE becomes a notification without a native SDK data event.
// Payload observers retain upstream metadata independently of downstream formatting.
func (s *ResponseAdapter) HandleGeminiStreamingResponse(c *upstream.OutputContext, resp *http.Response, startTime time.Time) (*StreamResult, error) {
	c.Status(resp.StatusCode)
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	result := &StreamResult{Usage: &upstream.TokenUsage{}}
	observed, err := s.readGeminiStream(resp, s.UnwrapV1InternalResponse, func(event upstream.GeminiStreamEvent) error {
		if len(event.Data) == 0 {
			if event.Terminal {
				c.Complete()
			}
			return nil
		}
		s.observeRaw(event.Data)
		if usage := bridge.NativeUsageProjection(bridge.NativeExtractGeminiUsage(event.Data)); usage != nil {
			result.Usage = usage
		}
		if c.Err() != nil {
			return nil
		}
		c.NextEvent(protocolgemini.ObservePayload(event.Data).Semantic, event.Terminal)
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", event.Data); err != nil {
			return nil
		}
		c.Writer.Flush()
		return nil
	})
	if usage := bridge.NativeUsageProjection(bridge.NativeExtractGeminiUsage(observed.UsageJSON)); usage != nil {
		result.Usage = usage
	}
	result.ClientDisconnect = c.Err() != nil || errors.Is(err, context.Canceled) || (s.Options.Context != nil && s.Options.Context.Err() != nil)
	if result.ClientDisconnect {
		return result, nil
	}
	return result, err
}

// readGeminiStream binds the selected envelope extraction to shared validation.
// Native output keeps inner bytes, while converted output retains wrapper identity.
// Both use the same limits, completion expectation, and trailing-usage policy.
func (s *ResponseAdapter) readGeminiStream(resp *http.Response, unwrap func([]byte) ([]byte, error), consume func(upstream.GeminiStreamEvent) error) (upstream.GeminiStreamResult, error) {
	return upstream.ReadGeminiStream(s.Options.Context, resp, upstream.GeminiStreamOptions{
		IdleTimeout:   time.Duration(s.Options.StreamDataIntervalTimeout) * time.Second,
		MaxFrameBytes: s.Options.MaxLineSize, Candidates: s.Options.Candidates,
		Unwrap: unwrap,
	}, consume)
}

// HandleGeminiStreamToNonStreaming collects a validated native Gemini response.
// Candidate indices retain their own parts and metadata through usage-only tails.
// Failed consumption returns partial usage without emitting successful JSON; shared
// consumption closes the response body and joins its worker before returning.
func (s *ResponseAdapter) HandleGeminiStreamToNonStreaming(c *upstream.OutputContext, resp *http.Response, startTime time.Time) (*StreamResult, error) {
	response, observed, err := upstream.CollectGeminiStream(s.Options.Context, resp, upstream.GeminiStreamOptions{
		IdleTimeout:   time.Duration(s.Options.StreamDataIntervalTimeout) * time.Second,
		MaxFrameBytes: s.Options.MaxLineSize, Candidates: s.Options.Candidates, Unwrap: s.UnwrapV1InternalResponse,
	}, s.observeRaw)
	usage := bridge.NativeUsageProjection(bridge.NativeExtractGeminiUsage(observed.UsageJSON))
	if usage == nil {
		usage = &upstream.TokenUsage{}
	}
	result := &StreamResult{Usage: usage}
	if err != nil {
		return result, err
	}
	body, err := json.Marshal(response)
	if err != nil {
		return result, err
	}
	c.Data(http.StatusOK, "application/json", body)
	return result, c.Err()
}

// CollectClaudeStreamResponse collects validated Gemini frames before Messages
// conversion. The shared reader retains sparse usage and candidate content through
// the bounded post-completion tail, then closes and joins its upstream reader.
//
// Missing content selects the existing failover policy. Other read failures
// return their observed usage without constructing successful client JSON. On
// success, conversion consumes the collected content and merged usage together,
// so cached prompt counts in a trailing frame remain visible to buffered clients.
func (s *ResponseAdapter) CollectClaudeStreamResponse(resp *http.Response, startTime time.Time, originalModel string) ([]byte, *StreamResult, error) {
	collected, observed, err := upstream.CollectGeminiStream(s.Options.Context, resp, upstream.GeminiStreamOptions{
		IdleTimeout:   time.Duration(s.Options.StreamDataIntervalTimeout) * time.Second,
		MaxFrameBytes: s.Options.MaxLineSize, Candidates: s.Options.Candidates,
		Unwrap: s.unwrapConvertedGeminiResponse,
	}, s.observeRaw)
	usage := bridge.NativeUsageProjection(bridge.NativeExtractGeminiUsage(observed.UsageJSON))
	if usage == nil {
		usage = &upstream.TokenUsage{}
	}
	result := &StreamResult{Usage: usage}
	if err != nil {
		if streamError, ok := errors.AsType[*upstream.GeminiStreamError](err); ok {
			// Compatibility mappers classify timeout text and scanner-size sentinels;
			// retain those signals while preserving the shared reader's typed cause.
			switch streamError.Kind {
			case "stream_empty":
				return nil, result, s.Options.Failover([]byte(`{"error":"empty stream response from upstream"}`))
			case "stream_timeout":
				return nil, result, fmt.Errorf("stream data interval timeout: %w", err)
			case "stream_frame_too_large":
				return nil, result, fmt.Errorf("%w: %w", bufio.ErrTooLong, err)
			}
		}
		return nil, result, err
	}
	body, err := json.Marshal(collected)
	if err != nil {
		return nil, result, err
	}
	converted, _, err := TransformGeminiToClaude(body, originalModel)
	return converted, result, err
}

// HandleClaudeStreamToNonStreaming delegates collection and writes successful
// Claude JSON. Failover and cancellation errors pass through; other failures
// use ClaudeError mapping, and the exchange owner closes the supplied response
// body after return. Buffered write failures remain in OutputContext.Err;
// the collection result itself keeps its prior ClientDisconnect value.
func (s *ResponseAdapter) HandleClaudeStreamToNonStreaming(c *upstream.OutputContext, resp *http.Response, startTime time.Time, originalModel string) (*StreamResult, error) {
	claudeResp, streamRes, err := s.CollectClaudeStreamResponse(resp, startTime, originalModel)
	if err != nil {
		if s.Options.IsFailover(err) {
			return nil, err
		}

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		errMsg := "Failed to parse upstream response"
		errType := "upstream_error"
		if strings.Contains(err.Error(), "stream data interval timeout") {
			errMsg = "Upstream stream data interval timeout"
			errType = "upstream_timeout"
		} else if errors.Is(err, bufio.ErrTooLong) {
			errMsg = "Upstream response line too long"
			errType = "response_too_large"
		}

		return nil, s.Options.ClaudeError(http.StatusBadGateway, errType, errMsg)
	}
	c.Data(http.StatusOK, "application/json", claudeResp)
	return streamRes, nil
}

// HandleClaudeStreamingResponse converts validated Gemini frames into Messages
// events and waits for trailing usage before emitting its final message_delta.
// The shared reader owns response closure, idle limits, and the five-second
// post-completion tail; caller-visible content can stream before that tail ends.
//
// Client write failure suppresses further delivery while usage collection
// continues. Read failures retain observed counters and emit no successful stop;
// cancellation and disconnection retain the existing collection-only behavior.
// A metadata-only response selects the existing empty-stream failover policy.
func (s *ResponseAdapter) HandleClaudeStreamingResponse(c *upstream.OutputContext, resp *http.Response, startTime time.Time, originalModel string) (*StreamResult, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}
	processor := NewStreamingProcessor(originalModel)
	writer := NewClientWriter(c.Writer, flusher, "antigravity claude")
	observed, err := s.readGeminiStream(resp, s.unwrapConvertedGeminiResponse, func(event upstream.GeminiStreamEvent) error {
		if len(event.Data) > 0 {
			s.observeRaw(event.Data)
			writer.Write(processor.ProcessLine("data: " + string(event.Data)))
		}
		if event.Terminal {
			c.Complete()
		}
		return nil
	})
	processor.SetUsageSnapshot(bridge.NativeExtractGeminiUsage(observed.UsageJSON))
	finalEvents, converted := processor.Finish()
	result := &StreamResult{Usage: &upstream.TokenUsage{
		InputTokens: converted.InputTokens, OutputTokens: converted.OutputTokens,
		CacheReadInputTokens:     converted.CacheReadInputTokens,
		CacheCreationInputTokens: converted.CacheCreationInputTokens,
		ImageOutputTokens:        converted.ImageOutputTokens,
	}, ClientDisconnect: writer.Disconnected()}
	if err != nil {
		if disconnected, handled := HandleStreamReadError(err, writer.Disconnected(), "antigravity claude"); handled {
			result.ClientDisconnect = disconnected
			return result, nil
		}
		var streamError *upstream.GeminiStreamError
		if !processor.MessageStartSent() || (errors.As(err, &streamError) && streamError.Kind == "stream_empty") {
			return result, s.Options.Failover([]byte(`{"error":"empty stream response from upstream"}`))
		}
		// A failed reader cannot authorize a successful message_stop, even when it
		// observed partial content or usage before the upstream failure arrived.
		reason := "stream_read_error"
		if errors.As(err, &streamError) {
			switch streamError.Kind {
			case "stream_frame_too_large":
				reason = "response_too_large"
			case "stream_timeout":
				reason = "stream_timeout"
			}
		}
		writer.Write([]byte(fmt.Sprintf("event: error\ndata: {\"error\":%q}\n\n", reason)))
		return result, err
	}
	writer.Write(finalEvents)
	result.ClientDisconnect = writer.Disconnected()
	return result, nil
}

// ExtractImageInputSize returns trimmed imageSize from decoded Gemini
// generation image settings. Invalid JSON, absent generation/image settings,
// and blank size return empty; the helper follows the shared Gemini wire field
// spelling.
func (s *ResponseAdapter) ExtractImageInputSize(body []byte) string {
	// A failed complete request decode supplies no selected image size. Return
	// empty text so callers retain their ordinary image-size default or policy.
	var req protocolgemini.GeminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}

	if req.GenerationConfig != nil && req.GenerationConfig.ImageConfig != nil {
		return strings.TrimSpace(req.GenerationConfig.ImageConfig.ImageSize)
	}

	return ""
}

// observeRaw forwards data synchronously to the optional callback without
// copying bytes. A missing callback has no effect; retaining callbacks follow
// supplied storage ownership and synchronize their own shared state. Callbacks
// leave the supplied bytes unchanged before subsequent parsing and output.
func (s *ResponseAdapter) observeRaw(data []byte) {
	if s.Options.ObserveRaw != nil {
		s.Options.ObserveRaw(data)
	}
}

// UnwrapV1InternalResponse extracts raw JSON for any existing response field,
// including scalar or null values. Absent fields return supplied bytes by
// reference; this helper performs no full JSON validation and returns no
// error. Callers keep the supplied body stable while reading a borrowed
// return value; extracted response bytes have their own allocation.
func (s *ResponseAdapter) UnwrapV1InternalResponse(body []byte) ([]byte, error) {
	result := gjson.GetBytes(body, "response")
	if result.Exists() {
		return []byte(result.Raw), nil
	}
	return body, nil
}

// unwrapConvertedGeminiResponse retains wrapper response identity while exposing
// native Gemini content to the shared reader. An outer nonempty responseId wins
// over the inner ID, matching the existing Messages conversion contract.
//
// Native passthrough continues using UnwrapV1InternalResponse and is unchanged.
// Converted streams instead need this identity before their first message_start,
// because final usage can arrive much later and cannot replace an already emitted
// ID. Only that known identity field is patched; inner model and content fields
// retain their upstream values, and scalar envelopes remain subject to reader validation.
func (s *ResponseAdapter) unwrapConvertedGeminiResponse(body []byte) ([]byte, error) {
	inner, err := s.UnwrapV1InternalResponse(body)
	if err != nil || !gjson.GetBytes(body, "response").IsObject() {
		return inner, err
	}
	identity := gjson.GetBytes(body, "responseId").String()
	if identity == "" {
		return inner, nil
	}
	return sjson.SetBytes(inner, "responseId", identity)
}
