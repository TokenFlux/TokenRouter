package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	geminiwire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// response.go reads Gemini responses, collects streamed content, and writes
// native or Messages output.
// Gemini output may arrive as ordinary JSON or one JSON payload per SSE data
// line, with an optional Code Assist response wrapper. Native output retains
// the original REST payload, while Messages conversion delegates its event and
// content rules to the protocol bridge. Observers receive raw payloads before
// converted output is emitted.
//
// Shared stream consumption validates complete frames, candidate termination,
// upstream errors, and read deadlines before output conversion. Buffered clients
// retain each candidate independently and combine text only when its remaining
// metadata matches. Sparse usage observations survive failed reads and final
// usage-only frames; callers still decide whether failed requests qualify for
// settlement. The reader closes its idempotent body and joins its worker before
// the executor releases the attempt.

// ResponseOptions supplies payload observers, response readers, header
// handling, and protocol error mapping.
type ResponseOptions struct {
	// Context controls read cancellation for this attempt. Direct codec callers
	// may leave it nil to use an uncancelled background context.
	Context context.Context
	Stream  upstream.GeminiStreamOptions // shared framing and idle limits

	// ObserveRaw receives upstream payload bytes synchronously before conversion.
	// Its callback leaves borrowed input unchanged and copies bytes retained after returning.
	ObserveRaw func([]byte) // raw payload observation callback
	// RememberToolCall records replay metadata under a generated client ID
	// before the completed call reaches a Messages or OpenAI client.
	RememberToolCall func(string, bridge.NativeGeminiToolCall)

	// GoogleError, ReadBody, WriteHeaders, ObserveImages, ReverseTools,
	// ClaudeError, ChatError, and CompatError are supplied for the output paths
	// that invoke them; these adapters call applicable handlers directly.
	GoogleError func(int, string) error // native response error mapping
	// caller-bounded response body reader
	ReadBody     func(io.Reader) ([]byte, error)
	WriteHeaders func(http.Header, http.Header) // response header forwarding callback
	// DebugHeaders selects rate-header logging; HasHeaderFilter selects streaming
	// header forwarding.
	DebugHeaders, HasHeaderFilter bool
	ObserveImages                 func([]byte)        // inline image observation callback
	ReverseTools                  func([]byte) []byte // upstream tool-name reversal callback
	// ClaudeError maps Messages errors; ChatError maps Chat errors.
	ClaudeError, ChatError func(int, string, string) error
	// selected OpenAI protocol error mapping
	CompatError func(OpenAICompatProtocol, int, string, string) error
}
type (
	// ResponseAdapter reads Gemini payloads and delivers one selected client
	// protocol through its options.
	ResponseAdapter struct{ Options ResponseOptions } // Options supplies conversion and output callbacks.
	// StreamResult holds normalized usage collected during converted stream delivery.
	StreamResult struct {
		Usage *upstream.TokenUsage // observed normalized token usage
	}
)

// NativeStreamResult holds normalized usage collected from Gemini stream payloads.
type NativeStreamResult struct {
	Usage *upstream.TokenUsage // observed normalized token usage
}

// HandleNonStreamingResponse reads at most eight MiB, unwraps Gemini JSON, and
// writes a Messages response with inline images omitted. It observes raw/image
// data before conversion; callers own the source body and inspect
// OutputContext for downstream write errors.
func (s *ResponseAdapter) HandleNonStreamingResponse(c *upstream.OutputContext, resp *http.Response, originalModel string) (*upstream.TokenUsage, error) {
	// Retain a bounded prefix for buffered Messages conversion; an oversized JSON
	// body can fail decoding when the prefix ends within a value.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, s.Options.ClaudeError(http.StatusBadGateway, "upstream_error", "Failed to read upstream response")
	}

	unwrappedBody, err := UnwrapGeminiResponse(body)
	if err != nil {
		return nil, s.Options.ClaudeError(http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}
	s.observeRaw(unwrappedBody)
	s.Options.ObserveImages(unwrappedBody)

	var geminiResp map[string]any
	if err := json.Unmarshal(unwrappedBody, &geminiResp); err != nil {
		return nil, s.Options.ClaudeError(http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}

	claudeResp, usage := s.convertGeminiToClaudeMessage(geminiResp, originalModel, unwrappedBody, false)
	responseBody, err := json.Marshal(claudeResp)
	if err != nil {
		return nil, err
	}
	c.NextEvent(bridge.CompatJSONHasContent(responseBody), true)
	c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)

	return usage, nil
}

// HandleStreamingResponse converts validated Gemini events into Messages SSE.
// Callers supply a flushing output context and configured cancellation and read
// limits; shared consumption owns response-body cleanup and completion validation.
//
// The bridge opens message metadata before reading and emits successful closing
// events only after validated completion. Write failures remain in OutputContext
// while reading continues for accounting, subject to cancellation and idle limits.
// Read failures preserve partial usage without synthesizing message_stop. A sparse
// tail refreshes bridge accounting before the final message usage is emitted.
func (s *ResponseAdapter) HandleStreamingResponse(c *upstream.OutputContext, resp *http.Response, startTime time.Time, originalModel string) (*StreamResult, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	state := bridge.NewNativeGeminiMessagesStream(s.bridgeRuntime())

	writeEvent := func(event bridge.NativeMessageEvent) {
		if event.Name != "" {
			WriteSSE(c.Writer, event.Name, event.Data, func(payload []byte) {
				semantic, terminal := bridge.CompatOutputMeaning(append([]byte("data: "), payload...))
				c.NextEvent(semantic, terminal)
			})
		}
		if event.Flush {
			flusher.Flush()
		}
	}
	for event := range state.Begin(upstream.GenerateAnthropicMsgID(), originalModel) {
		writeEvent(event)
	}
	observed, err := s.readStream(resp, true, func(event upstream.GeminiStreamEvent) error {
		if len(event.Data) == 0 {
			if event.Terminal {
				c.Complete()
			}
			return nil
		}
		s.observeRaw(event.Data)
		s.Options.ObserveImages(event.Data)
		var payload map[string]any
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return err
		}
		for message := range state.Process(payload, event.Data) {
			writeEvent(message)
		}
		if event.Terminal {
			c.Complete()
		}
		return nil
	})
	// Refresh sparse usage before final conversion so absent tail fields cannot
	// replace input or cache counters that were observed in earlier frames.
	if len(observed.UsageJSON) > 0 {
		var usagePayload map[string]any
		_ = json.Unmarshal(observed.UsageJSON, &usagePayload)
		for range state.Process(usagePayload, observed.UsageJSON) {
		}
	}
	result := &StreamResult{Usage: LegacyNativeGeminiUsage(state.Usage())}
	if c.Err() != nil {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for event := range state.Finish() {
		writeEvent(event)
	}
	return result, c.Err()
}

// WriteSSE serializes data into an event/data frame and invokes optional
// payload observers before the data write. JSON and writer errors are
// discarded; OutputContext writers can retain their own error state, and
// callers supply safe event labels.
func WriteSSE(w io.Writer, event string, data any, observers ...func([]byte)) {
	// Continue formatting the frame after write failure. OutputContext retains
	// disconnects for executor accounting; ordinary writers receive no returned
	// error from this helper.
	if event != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	// Observers and the data writer receive the marshaled bytes even after an
	// encoding failure; a nil byte result therefore still produces the
	// surrounding SSE frame.
	b, _ := json.Marshal(data)
	for _, observe := range observers {
		if observe != nil {
			observe(b)
		}
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
}

// UnwrapIfNeeded returns an extracted response only for OAuth payloads and
// otherwise returns raw. Paths returning raw share its byte slice; an
// extracted object or array uses the bytes produced by UnwrapGeminiResponse.
func UnwrapIfNeeded(isOAuth bool, raw []byte) []byte {
	if !isOAuth {
		return raw
	}
	inner, err := UnwrapGeminiResponse(raw)
	if err != nil {
		return raw
	}
	return inner
}

// collectSSE delegates candidate-preserving collection for a buffered client response.
// Shared consumption enforces cancellation, framing, completion, and idle limits.
// Partial content and accumulated usage remain available beside failures for diagnostics.
func (s *ResponseAdapter) collectSSE(resp *http.Response, isOAuth bool) (map[string]any, *upstream.TokenUsage, error) {
	options := s.Options.Stream
	if isOAuth {
		options.Unwrap = UnwrapGeminiResponse
	}
	collected, observed, err := upstream.CollectGeminiStream(s.Options.Context, resp, options, s.observeRaw)
	usage := ExtractGeminiUsage(observed.UsageJSON)
	if usage == nil {
		usage = &upstream.TokenUsage{}
	}
	return collected, usage, err
}

// readStream applies this platform's envelope decoder to shared Gemini consumption.
// OAuth and Code Assist readers unwrap their response field before validation;
// native REST readers retain their JSON object. The shared consumer owns body cleanup.
func (s *ResponseAdapter) readStream(resp *http.Response, wrapped bool, consume func(upstream.GeminiStreamEvent) error) (upstream.GeminiStreamResult, error) {
	options := s.Options.Stream
	if wrapped {
		options.Unwrap = UnwrapGeminiResponse
	}
	return upstream.ReadGeminiStream(s.Options.Context, resp, options, consume)
}

// HandleNativeNonStreamingResponse reads through the caller body limiter,
// optionally unwraps OAuth data, and forwards status, content type, and
// filtered headers. It observes raw/image data before output; absent usage
// returns zero counters, and callers inspect output errors separately.
func (s *ResponseAdapter) HandleNativeNonStreamingResponse(c *upstream.OutputContext, resp *http.Response, isOAuth bool) (*upstream.TokenUsage, error) {
	if s.Options.DebugHeaders {
		logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] ========== Response Headers ==========")
		for key, values := range resp.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-ratelimit") {
				logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] %s: %v", key, values)
			}
		}
		logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] ========================================")
	}

	respBody, err := s.Options.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}

	if isOAuth {
		unwrappedBody, uwErr := UnwrapGeminiResponse(respBody)
		if uwErr == nil {
			respBody = unwrappedBody
		}
	}
	s.observeRaw(respBody)
	s.Options.ObserveImages(respBody)

	s.Options.WriteHeaders(c.Writer.Header(), resp.Header)

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.NextEvent(geminiwire.ObservePayload(respBody).Semantic, true)
	c.Data(resp.StatusCode, contentType, respBody)

	if u := ExtractGeminiUsage(respBody); u != nil {
		return u, nil
	}
	return &upstream.TokenUsage{}, nil
}

// HandleNativeStreamingResponse delivers complete Gemini JSON events and partial usage.
// Upstream comments and DONE never reach the native SDK. The shared reader owns
// response-body cleanup, and all output failures propagate to the execution owner.
//
// Candidate completion is attached to the exact normalized frame that establishes
// it, allowing the HTTP boundary to stop heartbeats atomically with delivery.
// Provider errors and missing completion return failures instead of manufacturing
// a successful EOF; already observed usage remains available on those returns.
func (s *ResponseAdapter) HandleNativeStreamingResponse(c *upstream.OutputContext, resp *http.Response, startTime time.Time, isOAuth bool) (*NativeStreamResult, error) {
	if s.Options.DebugHeaders {
		for key, values := range resp.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-ratelimit") {
				logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] %s: %v", key, values)
			}
		}
	}
	if s.Options.HasHeaderFilter {
		s.Options.WriteHeaders(c.Writer.Header(), resp.Header)
	}
	c.Status(resp.StatusCode)
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	result := &NativeStreamResult{Usage: &upstream.TokenUsage{}}
	observed, err := s.readStream(resp, isOAuth, func(event upstream.GeminiStreamEvent) error {
		if len(event.Data) == 0 {
			if event.Terminal {
				c.Complete()
			}
			return nil
		}
		if usage := ExtractGeminiUsage(event.Data); usage != nil {
			result.Usage = usage
		}
		s.observeRaw(event.Data)
		s.Options.ObserveImages(event.Data)
		if c.Err() != nil {
			return nil
		}
		c.NextEvent(geminiwire.ObservePayload(event.Data).Semantic, event.Terminal)
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", event.Data); err != nil {
			return nil
		}
		c.Writer.Flush()
		return nil
	})
	if usage := ExtractGeminiUsage(observed.UsageJSON); usage != nil {
		result.Usage = usage
	}
	if c.Err() != nil {
		return result, nil
	}
	return result, err
}

// UnwrapGeminiResponse extracts the raw object or array in response as new
// bytes, otherwise returning the supplied slice with nil error. Field
// extraction preserves nested JSON spelling and does not validate the complete
// outer document.
func UnwrapGeminiResponse(raw []byte) ([]byte, error) {
	result := gjson.GetBytes(raw, "response")
	if result.Exists() && result.Type == gjson.JSON {
		return []byte(result.Raw), nil
	}
	return raw, nil
}

// ConvertGeminiToClaudeMessage delegates buffered response conversion with
// generated message and tool identifiers. rawData supplies usage independently
// of the decoded map, and includeInlineData selects the bridge image-text
// representation in the returned Messages body.
func ConvertGeminiToClaudeMessage(geminiResp map[string]any, originalModel string, rawData []byte, includeInlineData bool) (map[string]any, *upstream.TokenUsage) {
	return (&ResponseAdapter{}).convertGeminiToClaudeMessage(geminiResp, originalModel, rawData, includeInlineData)
}

// convertGeminiToClaudeMessage converts a buffered reply and records its tool replay metadata.
func (s *ResponseAdapter) convertGeminiToClaudeMessage(geminiResp map[string]any, originalModel string, rawData []byte, includeInlineData bool) (map[string]any, *upstream.TokenUsage) {
	result, usage := bridge.NativeConvertGeminiToClaudeMessage(s.bridgeRuntime(), geminiResp, originalModel, rawData, includeInlineData)
	return result, LegacyNativeGeminiUsage(usage)
}

// bridgeRuntime supplies the adapter's ID generators and scoped replay callback.
func (s *ResponseAdapter) bridgeRuntime() bridge.NativeGeminiRuntime {
	return bridge.NativeGeminiRuntime{
		MessageID:        upstream.GenerateAnthropicMsgID,
		RandomHex:        upstream.RandomHex,
		RememberToolCall: s.Options.RememberToolCall,
	}
}

// ExtractGeminiUsage copies bridge token buckets into upstream usage,
// returning nil for absent metadata. Explicit null or zero metadata yields
// zero counters; cached input is separated from prompt input and thinking
// joins candidate output.
func ExtractGeminiUsage(data []byte) *upstream.TokenUsage {
	return LegacyNativeGeminiUsage(bridge.NativeExtractGeminiUsage(data))
}

// LegacyNativeGeminiUsage copies the bridge token counters into a fresh
// upstream usage value. Nil input returns nil, and the four integer buckets
// retain their supplied accounting values.
func LegacyNativeGeminiUsage(usage *bridge.NativeGeminiUsage) *upstream.TokenUsage {
	return bridge.NativeUsageProjection(usage)
}

// observeRaw synchronously invokes the optional raw observer with the supplied
// payload bytes.
func (s *ResponseAdapter) observeRaw(payload []byte) {
	if s.Options.ObserveRaw != nil {
		s.Options.ObserveRaw(payload)
	}
}
