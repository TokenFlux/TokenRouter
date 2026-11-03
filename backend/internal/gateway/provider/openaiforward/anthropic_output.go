package openaiforward

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// anthropic_output.go converts Anthropic event streams into buffered or
// streaming OpenAI responses and records their usage.
// Anthropic stream events carry content fragments and separate uncached, cache-
// creation, and cache-read usage buckets. Converted Responses and Chat output
// reconstruct those fragments while retaining the vendor model declaration
// before restoring the caller model, and their OpenAI completion input count
// includes every input bucket. Completion separately receives normalized native
// usage, including cache lifetimes and speed, so inclusive client counts cannot
// be mistaken for billable uncached input. All four readers pass each data
// payload to anthropicUsageCollector. It decodes the event once for conversion
// and the typed usage merge, reconciles raw usage fields from the same payload,
// and builds the returned Result with the vendor model evidence.
//
// Buffered conversion waits for a message_start before producing JSON, while
// streaming conversion commits SSE headers before reading vendor events. An
// idle timeout closes the upstream body; buffered output returns any observed
// usage beside the error, and streaming output returns its current usage
// snapshot. Responses streaming continues conversion state after client
// disconnection, while Chat streaming continues usage collection and stops its
// ordinary event conversion. The execution caller retains final body cleanup.

// AnthropicOutputOptions supplies request-bound conversion, header, and diagnostic operations for Anthropic events.
type AnthropicOutputOptions struct {
	Runtime        bridge.Runtime                 // conversion clock and entropy
	MaxLineSize    func() int                     // configured scanner growth limit
	StreamInterval func() time.Duration           // line-consumer idle interval reader
	CopyHeaders    func(http.Header, http.Header) // filtered response header copier
	ReverseTools   func([]byte) []byte            // request-bound tool name restorer
	Error          func(int, string, string)      // client conversion error presenter
	Warn           func(string, ...zap.Field)     // structured read diagnostic writer
}

// anthropicUsageCollector accumulates the completion evidence shared by the
// converted Messages readers: normalized native usage and the vendor model
// declaration. It also builds each reader's Result from that evidence.
type anthropicUsageCollector struct {
	usage  upstream.TokenUsage            // normalized native buckets
	models protocol.ResponseModelObserver // vendor model declaration evidence
	// identity holds the request, model, endpoint, and stream fields copied into
	// every Result; Usage, NativeUsage, Duration, and disconnect state are
	// filled when a Result is built.
	identity  Result
	startTime time.Time // request start for result durations
}

// AnthropicUsageToOpenAI builds inclusive OpenAI input usage from normalized
// Anthropic token buckets. Callers supply uncached InputTokens plus separate
// cache-creation and cache-read quantities, such as those produced by
// MergeAnthropicUsage; nil input returns empty usage.
//
// The input total includes all three buckets, while the two cache quantities
// also remain available separately for pricing. For example, 250 uncached
// tokens and 173056 cache-read tokens produce 173306 input tokens with 173056
// still reported in CacheReadInputTokens. Output tokens are copied from the
// same observation.
func AnthropicUsageToOpenAI(u *upstream.TokenUsage) protocolopenai.ForwardUsage {
	if u == nil {
		return protocolopenai.ForwardUsage{}
	}
	return protocolopenai.ForwardUsage{InputTokens: u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens, OutputTokens: u.OutputTokens, CacheCreationInputTokens: u.CacheCreationInputTokens, CacheReadInputTokens: u.CacheReadInputTokens}
}

// ResponsesFromAnthropicBuffered reconstructs Responses JSON and completion
// usage from paired Anthropic event/data lines. The caller closes the response
// body after reading; a missing message_start or idle expiry writes the client
// failure and returns any accumulated usage beside the error. Non-idle scanner
// failures end accumulation after diagnostic handling.
//
// Content deltas append to the matching reconstructed block, including split
// tool arguments, and vendor model evidence is captured before caller-model
// restoration. Client-tool mapping is applied after ordinary reverse names on
// successful marshaling. When a message exists, the accumulated usage becomes
// the completion result even if scanning ended early, so callers use this
// reader's actual return rules when determining completion.
func ResponsesFromAnthropicBuffered(resp *http.Response, c *upstream.OutputContext, o AnthropicOutputOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time, clientToolMapping bridge.ResponsesClientToolMapping) (out *Result, failure error) {
	requestID := resp.Header.Get("x-request-id")

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := o.MaxLineSize()
	// The 64 KiB starting capacity remains usable when the configured maximum
	// is smaller; that maximum bounds subsequent scanner growth while this
	// converter reads Anthropic lines.
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var finalResp *protocolanthropic.AnthropicResponse
	collector := newAnthropicUsageCollector(resp, originalModel, billingModel, upstreamModel, reasoningEffort, startTime, false)
	// Preserve measured work when buffered conversion fails after observing usage.
	// Completion eligibility stays with the caller, while the returned evidence
	// remains available for diagnostics or an eligible partial-completion record.
	defer func() {
		if failure != nil && out == nil {
			out = collector.PartialResult()
		}
	}()

	// The line-consumer interval bounds buffered waiting before JSON output;
	// expiry closes the body so the scanner can finish and the client receives a
	// conversion failure.
	streamInterval := o.StreamInterval()
	pump := NewAnthropicLinePump(scanner, streamInterval)
	defer pump.Stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			o.Warn("openai responses via native anthropic buffered: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*Result, error) {
		// Close the idle upstream body to unblock scanner input before returning; the
		// pump stop deferred by this reader then releases pending publication.
		_ = resp.Body.Close()
		o.Warn("openai responses via native anthropic buffered: data interval timeout",
			zap.String("request_id", requestID),
			zap.Duration("interval", streamInterval),
		)
		o.Error(http.StatusBadGateway, "server_error", "Upstream stream data interval timeout")
		return nil, fmt.Errorf("stream data interval timeout")
	}

	for {
		line, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		// Accept compact event:xxx framing so the immediately following data
		// line still enters paired message reconstruction for compatible
		// upstream streams.
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		dataLine, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		// Consume the data line immediately following an accepted event line; other
		// line shapes are skipped so only that paired payload enters message
		// reconstruction.
		payload, ok := protocolopenai.ExtractSSEDataLine(dataLine)
		if !ok {
			continue
		}

		event, decoded := collector.Observe(payload)
		// Skip payloads that cannot decode as an Anthropic event, allowing later
		// valid events to continue supplying message state and usage observations.
		if !decoded {
			continue
		}

		if event.Type == "message_start" && event.Message != nil {
			finalResp = event.Message
		}
		if event.Type == "message_delta" && event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
			finalResp.StopReason = bridge.AnthropicStopReasonPtr(event.Delta.StopReason)
		}
		if event.Type == "content_block_start" && event.ContentBlock != nil && finalResp != nil {
			finalResp.Content = append(finalResp.Content, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && finalResp != nil && event.Index != nil {
			idx := *event.Index
			// Deltas address blocks in append order. The upper-bound check skips
			// later blocks, but a negative upstream index passes it and panics
			// when the selected delta accesses Content.
			if idx < len(finalResp.Content) {
				switch event.Delta.Type {
				case "text_delta":
					finalResp.Content[idx].Text += event.Delta.Text
				case "thinking_delta":
					finalResp.Content[idx].Thinking += event.Delta.Thinking
				// Tool arguments arrive as partial JSON fragments; concatenate them into
				// the retained content block before whole-response conversion serializes
				// its completed tool input.
				case "input_json_delta":
					finalResp.Content[idx].Input = forwardcore.AppendRawJSON(finalResp.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
	}

	if finalResp == nil {
		o.Error(http.StatusBadGateway, "server_error", "Upstream stream ended without a response")
		return nil, fmt.Errorf("upstream stream ended without response")
	}

	// Cache-only observations remain meaningful when ordinary input and output
	// are zero; copy the complete observed snapshot into the client conversion.
	finalResp.Usage = protocolanthropic.UsageFromTokens(collector.usage)

	responsesResp := bridge.AnthropicToResponsesResponse(o.Runtime, finalResp)
	responsesResp.Model = originalModel

	o.CopyHeaders(c.Writer.Header(), resp.Header)
	// Override the upstream SSE content type after reconstruction so buffered
	// callers parse the converted Responses or Chat body as JSON.
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if respBytes, err := json.Marshal(responsesResp); err == nil {
		respBytes = o.ReverseTools(respBytes)
		respBytes, _, err = bridge.RestoreResponsesClientToolPayload(respBytes, clientToolMapping)
		if err != nil {
			return nil, fmt.Errorf("restore responses client tools: %w", err)
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		// If preparatory marshaling fails, delegate encoding of the reconstructed
		// object to the output context; that path bypasses the byte-level tool
		// restoration applied above.
		c.JSON(http.StatusOK, responsesResp)
	}

	return collector.Result(false), nil
}

// ResponsesFromAnthropicStreaming converts paired Anthropic event/data lines
// into Responses SSE with request-local tool restoration. It commits streaming
// headers before reading. The caller retains final response-body cleanup
// and captures performance from the shared observer after conversion returns.
//
// A failed client write stops further output while conversion state and
// trailing usage continue advancing. Idle expiry closes the body and returns
// collected observations beside an error; other scanner endings finalize
// conversion and return its accumulated result. Encoding or restoration
// failures skip the affected event, and terminal conversion output is written
// only while the client remains connected.
func ResponsesFromAnthropicStreaming(resp *http.Response, c *upstream.OutputContext, o AnthropicOutputOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time, clientToolMapping bridge.ResponsesClientToolMapping) (out *Result, failure error) {
	requestID := resp.Header.Get("x-request-id")

	o.CopyHeaders(c.Writer.Header(), resp.Header)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	state := bridge.NewAnthropicEventToResponsesState(o.Runtime)
	state.Model = originalModel
	clientToolRestorer := bridge.NewResponsesClientToolStreamRestorer(clientToolMapping)

	collector := newAnthropicUsageCollector(resp, originalModel, billingModel, upstreamModel, reasoningEffort, startTime, true)
	clientDisconnected := false

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := o.MaxLineSize()
	// The 64 KiB starting capacity remains usable when the configured maximum
	// is smaller; that maximum bounds subsequent scanner growth while this
	// converter reads Anthropic lines.
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	// Bound idle reads so draining a detached upstream request cannot wait indefinitely
	// when the upstream keeps its connection open without sending more lines.
	streamInterval := o.StreamInterval()
	pump := NewAnthropicLinePump(scanner, streamInterval)
	defer pump.Stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			o.Warn("openai responses via native anthropic stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*Result, error) {
		// Close the idle upstream body to unblock scanner input before returning; the
		// pump stop deferred by this reader then releases pending publication.
		_ = resp.Body.Close()
		o.Warn("openai responses via native anthropic stream: data interval timeout",
			zap.String("request_id", requestID),
			zap.Duration("interval", streamInterval),
		)
		return collector.Result(clientDisconnected), fmt.Errorf("stream data interval timeout")
	}

	// After client disconnection, keep advancing conversion state while the read
	// loop drains usage; the final output token count arrives in a trailing
	// message_delta event.
	processAnthropicEvent := func(event *protocolanthropic.AnthropicStreamEvent) {
		events := bridge.AnthropicEventToResponsesEvents(o.Runtime, event, state)
		if clientDisconnected {
			return
		}
		// Prepare each converted event before writing: failed JSON encoding or
		// client-tool restoration drops that event while later events can still
		// update output and usage.
		for _, evt := range events {
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			payload = o.ReverseTools(payload)
			payloads, _, err := clientToolRestorer.RestoreEvent(payload)
			if err != nil {
				continue
			}
			for _, restored := range payloads {
				eventType := gjson.GetBytes(restored, "type").String()
				if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, restored); err != nil {
					clientDisconnected = true
					return
				}
			}
		}
		if len(events) > 0 {
			c.Writer.Flush()
		}
	}

	for {
		line, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		dataLine, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		// Consume the data line immediately following an accepted event line; other
		// line shapes are skipped so only that paired payload enters message
		// reconstruction.
		payload, ok := protocolopenai.ExtractSSEDataLine(dataLine)
		if !ok {
			continue
		}

		// Observe usage before conversion, so trailing message_delta tokens still
		// count after output to this client has stopped. Skip payloads that cannot
		// decode as an Anthropic event, allowing later valid events to continue
		// supplying conversion state and usage observations.
		event, decoded := collector.Observe(payload)
		if !decoded {
			continue
		}

		processAnthropicEvent(event)
	}

	// Finalize Responses conversion after scanner completion even if client output
	// failed; emit restored terminal events only while writes remain available for
	// that client.
	if finalEvents := bridge.FinalizeAnthropicResponsesStream(state); len(finalEvents) > 0 && !clientDisconnected {
		wrote := false
		// Terminal events use the same encoding and restoration path; skip an
		// unusable event and continue until client writing fails or available final
		// events finish.
		for _, evt := range finalEvents {
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			payload = o.ReverseTools(payload)
			payloads, _, err := clientToolRestorer.RestoreEvent(payload)
			if err != nil {
				continue
			}
			for _, restored := range payloads {
				eventType := gjson.GetBytes(restored, "type").String()
				if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, restored); err != nil {
					clientDisconnected = true
					break
				}
				wrote = true
			}
			if clientDisconnected {
				break
			}
		}
		if wrote {
			c.Writer.Flush()
		}
	}

	return collector.Result(clientDisconnected), nil
}

// ChatFromAnthropicBuffered reconstructs Chat JSON and completion usage from
// paired Anthropic event/data lines. The caller retains response-body cleanup;
// idle expiry and an absent message_start produce client failure with any
// observed usage, while other scanner endings allow reconstruction from available
// content.
//
// Vendor model evidence is captured before converting through Responses into
// the caller's Chat model. Content and tool-argument fragments accumulate in
// the Anthropic message before conversion. Successful preparatory marshaling
// applies reverse-tool names, and the returned result carries normalized
// inclusive input usage and complete native buckets independently of that client
// output representation.
func ChatFromAnthropicBuffered(resp *http.Response, c *upstream.OutputContext, o AnthropicOutputOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time) (out *Result, failure error) {
	requestID := resp.Header.Get("x-request-id")

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := o.MaxLineSize()
	// The 64 KiB starting capacity remains usable when the configured maximum
	// is smaller; that maximum bounds subsequent scanner growth while this
	// converter reads Anthropic lines.
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var finalResp *protocolanthropic.AnthropicResponse
	collector := newAnthropicUsageCollector(resp, originalModel, billingModel, upstreamModel, reasoningEffort, startTime, false)
	// Preserve measured work when buffered conversion fails after observing usage.
	// Completion eligibility stays with the caller, while the returned evidence
	// remains available for diagnostics or an eligible partial-completion record.
	defer func() {
		if failure != nil && out == nil {
			out = collector.PartialResult()
		}
	}()

	// The line-consumer interval bounds buffered waiting before JSON output;
	// expiry closes the body so the scanner can finish and the client receives a
	// conversion failure.
	streamInterval := o.StreamInterval()
	pump := NewAnthropicLinePump(scanner, streamInterval)
	defer pump.Stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			o.Warn("openai cc via native anthropic buffered: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*Result, error) {
		// Close the idle upstream body to unblock scanner input before returning; the
		// pump stop deferred by this reader then releases pending publication.
		_ = resp.Body.Close()
		o.Warn("openai cc via native anthropic buffered: data interval timeout",
			zap.String("request_id", requestID),
			zap.Duration("interval", streamInterval),
		)
		o.Error(http.StatusBadGateway, "server_error", "Upstream stream data interval timeout")
		return nil, fmt.Errorf("stream data interval timeout")
	}

	for {
		line, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		// Accept compact event:xxx framing so the immediately following data
		// line still enters paired message reconstruction for compatible
		// upstream streams.
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		dataLine, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		// Consume the data line immediately following an accepted event line; other
		// line shapes are skipped so only that paired payload enters message
		// reconstruction.
		payload, ok := protocolopenai.ExtractSSEDataLine(dataLine)
		if !ok {
			continue
		}

		event, decoded := collector.Observe(payload)
		// Skip payloads that cannot decode as an Anthropic event, allowing later
		// valid events to continue supplying message state and usage observations.
		if !decoded {
			continue
		}

		if event.Type == "message_start" && event.Message != nil {
			finalResp = event.Message
		}
		if event.Type == "message_delta" && event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
			finalResp.StopReason = bridge.AnthropicStopReasonPtr(event.Delta.StopReason)
		}
		if event.Type == "content_block_start" && event.ContentBlock != nil && finalResp != nil {
			finalResp.Content = append(finalResp.Content, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && finalResp != nil && event.Index != nil {
			idx := *event.Index
			// Deltas address blocks in append order. The upper-bound check skips
			// later blocks, but a negative upstream index passes it and panics
			// when the selected delta accesses Content.
			if idx < len(finalResp.Content) {
				switch event.Delta.Type {
				case "text_delta":
					finalResp.Content[idx].Text += event.Delta.Text
				case "thinking_delta":
					finalResp.Content[idx].Thinking += event.Delta.Thinking
				// Tool arguments arrive as partial JSON fragments; concatenate them into
				// the retained content block before whole-response conversion serializes
				// its completed tool input.
				case "input_json_delta":
					finalResp.Content[idx].Input = forwardcore.AppendRawJSON(finalResp.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
	}

	if finalResp == nil {
		o.Error(http.StatusBadGateway, "server_error", "Upstream stream ended without a response")
		return nil, fmt.Errorf("upstream stream ended without response")
	}

	// Cache-only observations remain meaningful when ordinary input and output
	// are zero; copy the complete observed snapshot into the client conversion.
	finalResp.Usage = protocolanthropic.UsageFromTokens(collector.usage)

	responsesResp := bridge.AnthropicToResponsesResponse(o.Runtime, finalResp)
	ccResp := bridge.ResponsesToChatCompletions(o.Runtime, responsesResp, originalModel)

	o.CopyHeaders(c.Writer.Header(), resp.Header)
	// Override the upstream SSE content type after reconstruction so buffered
	// callers parse the converted Responses or Chat body as JSON.
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if respBytes, err := json.Marshal(ccResp); err == nil {
		respBytes = o.ReverseTools(respBytes)
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		// If preparatory marshaling fails, delegate encoding of the reconstructed
		// object to the output context; that path bypasses the byte-level tool
		// restoration applied above.
		c.JSON(http.StatusOK, ccResp)
	}

	return collector.Result(false), nil
}

// ChatFromAnthropicStreaming converts Anthropic events through Responses state
// into Chat SSE and optional usage output. It commits streaming headers before
// reading. The caller closes the borrowed response body after processing
// and obtains performance from the shared exchange observer.
//
// Client write failure stops ordinary event conversion and output while
// message_start and message_delta continue contributing usage. Idle expiry
// closes the body and returns the collected result beside an error; other
// scanner endings finalize both converters. Serialization failures skip
// affected chunks, and client-disconnect evidence comes from chunk writes; the
// final [DONE] write is attempted without recording its error.
func ChatFromAnthropicStreaming(resp *http.Response, c *upstream.OutputContext, o AnthropicOutputOptions, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time, includeUsage bool) (out *Result, failure error) {
	requestID := resp.Header.Get("x-request-id")

	o.CopyHeaders(c.Writer.Header(), resp.Header)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	anthState := bridge.NewAnthropicEventToResponsesState(o.Runtime)
	anthState.Model = originalModel
	ccState := bridge.NewResponsesEventToChatState(o.Runtime)
	ccState.Model = originalModel
	ccState.IncludeUsage = includeUsage

	collector := newAnthropicUsageCollector(resp, originalModel, billingModel, upstreamModel, reasoningEffort, startTime, true)
	clientDisconnected := false

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := o.MaxLineSize()
	// The 64 KiB starting capacity remains usable when the configured maximum
	// is smaller; that maximum bounds subsequent scanner growth while this
	// converter reads Anthropic lines.
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	// Bound idle reads while draining a detached upstream request after client disconnection.
	// A connection left open without more data would otherwise keep the scanner blocked.
	streamInterval := o.StreamInterval()
	pump := NewAnthropicLinePump(scanner, streamInterval)
	defer pump.Stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			o.Warn("openai cc via native anthropic stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	// Close the body on idle expiry so its blocked scanner can finish; keep
	// accumulated usage and disconnect state in the observation returned beside
	// the timeout.
	onIdle := func() (*Result, error) {
		// Close the idle upstream body to unblock scanner input before returning; the
		// pump stop deferred by this reader then releases pending publication.
		_ = resp.Body.Close()
		if !clientDisconnected {
			o.Warn("openai cc via native anthropic stream: data interval timeout",
				zap.String("request_id", requestID),
				zap.Duration("interval", streamInterval),
			)
		}
		return collector.Result(clientDisconnected), fmt.Errorf("stream data interval timeout")
	}

	writeChunk := func(chunk protocolopenai.ChatCompletionsChunk) bool {
		if clientDisconnected {
			return false // Continue draining usage after writes stop.
		}
		// A chunk that cannot encode is skipped before writing, leaving earlier
		// output and accumulated usage available while later convertible chunks can
		// still be sent.
		sse, err := bridge.ChatChunkToSSE(chunk)
		if err != nil {
			return false
		}
		out := string(o.ReverseTools([]byte(sse)))
		if _, err := fmt.Fprint(c.Writer, out); err != nil {
			clientDisconnected = true
			return false
		}
		return false
	}

	processAnthropicEvent := func(event *protocolanthropic.AnthropicStreamEvent) bool {
		// After output failure, skip ordinary event conversion while the outer reader
		// continues consuming lines; accumulated usage still reaches the returned
		// partial observation.
		if clientDisconnected {
			return false
		}

		responsesEvents := bridge.AnthropicEventToResponsesEvents(o.Runtime, event, anthState)
		for _, resEvt := range responsesEvents {
			ccChunks := bridge.ResponsesEventToChatChunks(&resEvt, ccState)
			for _, chunk := range ccChunks {
				writeChunk(chunk)
			}
		}
		if len(responsesEvents) > 0 {
			c.Writer.Flush()
		}
		return false
	}

	for {
		line, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			logReadErr(rerr)
			break
		}
		if _, ok := protocolopenai.ExtractSSEEventLine(line); !ok {
			continue
		}

		dataLine, rerr := pump.Next()
		if rerr != nil {
			if errors.Is(rerr, ErrAnthropicStreamIdle) {
				return onIdle()
			}
			// Finalize when the stream ends or fails between the event and data lines.
			logReadErr(rerr)
			break
		}
		// Consume the data line immediately following an accepted event line; other
		// line shapes are skipped so only that paired payload enters message
		// reconstruction.
		payload, ok := protocolopenai.ExtractSSEDataLine(dataLine)
		if !ok {
			continue
		}

		// Observe usage before conversion, so trailing message_delta tokens still
		// count after output to this client has stopped. Skip payloads that cannot
		// decode as an Anthropic event, allowing later valid events to continue
		// supplying conversion state and usage observations.
		event, decoded := collector.Observe(payload)
		if !decoded {
			continue
		}

		if processAnthropicEvent(event) {
			return collector.Result(clientDisconnected), nil
		}
	}

	// Finalize Anthropic-to-Responses state before Responses-to-Chat state so
	// final message events can produce their Chat finish chunks; writeChunk
	// continues suppressing disconnected-client output.
	finalResEvents := bridge.FinalizeAnthropicResponsesStream(anthState)
	for _, resEvt := range finalResEvents {
		ccChunks := bridge.ResponsesEventToChatChunks(&resEvt, ccState)
		for _, chunk := range ccChunks {
			writeChunk(chunk)
		}
	}
	finalCCChunks := bridge.FinalizeResponsesChatStream(ccState)
	for _, chunk := range finalCCChunks {
		writeChunk(chunk)
	}

	// Attempt the final marker after converter finalization; its write error is
	// discarded, so the returned disconnect flag reflects earlier chunk writes.
	if !clientDisconnected {
		fmt.Fprint(c.Writer, "data: [DONE]\n\n")
		c.Writer.Flush()
	}

	return collector.Result(clientDisconnected), nil
}

// newAnthropicUsageCollector prepares usage collection for one converted
// Messages response. The response headers and request ID are captured for
// every Result, and stream selects whether results describe streamed output.
func newAnthropicUsageCollector(resp *http.Response, originalModel, billingModel, upstreamModel string, reasoningEffort *string, startTime time.Time, stream bool) *anthropicUsageCollector {
	return &anthropicUsageCollector{
		identity: Result{
			RequestID:        resp.Header.Get("x-request-id"),
			Headers:          resp.Header,
			Model:            originalModel,
			BillingModel:     billingModel,
			UpstreamModel:    upstreamModel,
			UpstreamEndpoint: "/v1/messages",
			ReasoningEffort:  reasoningEffort,
			Stream:           stream,
		},
		startTime: startTime,
	}
}

// Observe records model evidence from one Messages SSE data payload, decodes
// it once, and merges its usage. It returns the decoded event for conversion,
// or false when the payload is not an Anthropic event; such payloads still
// contribute model evidence but no usage.
//
// The typed merge runs first so compatible providers keep output counts they
// report in message_start. Raw parsing of the same payload then reconciles
// what the typed fields cannot represent: cache TTL presence, explicit zero
// TTL buckets, and prompt totals that include TTL-only cache writes. Readers
// call Observe before deciding whether to convert an event, so usage keeps
// accumulating after client output stops.
func (c *anthropicUsageCollector) Observe(payload string) (*protocolanthropic.AnthropicStreamEvent, bool) {
	c.models.ObserveAnthropic([]byte(payload))
	var event protocolanthropic.AnthropicStreamEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return nil, false
	}
	protocolanthropic.NormalizeStreamUsage([]byte(payload), &event, &c.usage)
	return &event, true
}

// Result builds the reader result from the evidence collected so far, with
// clientDisconnect reporting failed client output. NativeUsage points at the
// collector's live buckets, so callers build the result after the last
// observation; Usage holds the inclusive OpenAI projection of the same buckets.
func (c *anthropicUsageCollector) Result(clientDisconnect bool) *Result {
	out := c.identity
	out.Usage = AnthropicUsageToOpenAI(&c.usage)
	out.NativeUsage = &c.usage
	out.UpstreamResponseModel = c.models.Model()
	out.Duration = time.Since(c.startTime)
	out.ClientDisconnect = clientDisconnect
	return &out
}

// PartialResult returns the buffered reader result for a failed conversion, or
// nil when no usage has been observed. Buffered output never reaches a client
// before conversion succeeds, so the result reports no client disconnect.
func (c *anthropicUsageCollector) PartialResult() *Result {
	if c.usage == (upstream.TokenUsage{}) {
		return nil
	}
	return c.Result(false)
}
