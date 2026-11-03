package forward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// conversion_output.go converts Anthropic stream events into buffered or
// streamed OpenAI responses and captures usage. Anthropic stream events
// distribute message content, usage, and stop reasons across separate frames.
// OpenAI conversion accumulates those observations while keeping raw upstream
// model declarations separate from restored client model aliases. Buffered
// output needs a message_start object; streaming output feeds the same
// observations through bridge state and restores tool names before framing the
// resulting client events.
//
// A read failure can follow a usable terminal observation, so partial usage and
// the original error can accompany output that has already been formed. On read
// failure, absent stop/produced-terminal evidence selects a protocol error;
// frame overflow remains an error even after a stop.
// Converter-generated failure frames use the local output operation so HTTP
// adapters can report the error without assigning it an upstream delivery time.

// ResponsesBuffered returns Responses JSON assembled from Anthropic events and
// retains merged usage plus raw response-model evidence for completion. The
// input Lines and Output must be usable; its caller owns response closure.
// Content deltas refer to accumulated block positions and require nonnegative
// upstream indices.
//
// A reader failure before a usable stop reason writes 502 and returns the
// observation result with that error. A stop permits completed JSON despite
// the retained read error, except that frame overflow still rejects conversion.
// A clean stream without message_start returns no result. Tool-name and client
// tool restoration occur after client-model selection and before encoded output.
func ResponsesBuffered(in Response, out Output, originalModel, mappedModel string, reasoningEffort *string, startTime time.Time, clientToolMapping bridge.ResponsesClientToolMapping) (*Result, error) {
	requestID := in.RequestID
	// Observe source model bytes before conversion chooses the client alias.
	// This separates audit identity from the logical model assigned to generated
	// Responses/Chat output, including observations retained after read failure.
	var modelObserver protocol.ResponseModelObserver

	scanner := newConversionSSEScanner(in)

	var finalResp *protocolanthropic.AnthropicResponse
	var usage upstream.TokenUsage

	resultWithUsage := func() *Result {
		return &Result{
			RequestID:             requestID,
			UpstreamHeaders:       in.Headers,
			Usage:                 usage,
			Model:                 originalModel,
			UpstreamModel:         mappedModel,
			UpstreamResponseModel: modelObserver.Model(),
			ReasoningEffort:       reasoningEffort,
			Stream:                false,
			Duration:              time.Since(startTime),
		}
	}

	for scanner.Scan() {
		payload := scanner.frame.Data
		eventType := scanner.frame.EventType

		modelObserver.ObserveAnthropic([]byte(payload))
		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			// An unusable event does not end frame consumption. Record its parse
			// failure and continue so later message/usage events can still contribute
			// to the converted result from the same stream.
			out.Observe("warn", "forward_as_responses buffered: failed to parse event", err, requestID, eventType)
			continue
		}
		// JSON type is authoritative when present. The SSE event field supplies
		// the type only when JSON omits it, supporting equivalent named and
		// unnamed frame shapes through the same conversion path.
		if event.Type == "" {
			event.Type = eventType
		}
		// Resolve compatible cache fields before conversion so the returned usage
		// and client events share the same accumulated prompt partition.
		protocolanthropic.NormalizeStreamUsage([]byte(payload), &event, &usage)

		if event.Type == "message_start" && event.Message != nil {
			finalResp = event.Message
		}

		if event.Type == "message_delta" {
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = bridge.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		}

		if event.Type == "content_block_start" && event.ContentBlock != nil && finalResp != nil {
			finalResp.Content = append(finalResp.Content, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && finalResp != nil && event.Index != nil {
			// Deltas address the accumulated block order rather than appending new
			// blocks. Indices beyond known blocks are ignored; the upstream must
			// supply nonnegative positions because this guard checks only the upper bound.
			idx := *event.Index
			if idx < len(finalResp.Content) {
				switch event.Delta.Type {
				case "text_delta":
					finalResp.Content[idx].Text += event.Delta.Text
				case "thinking_delta":
					finalResp.Content[idx].Thinking += event.Delta.Thinking
				case "input_json_delta":
					finalResp.Content[idx].Input = AppendRawJSON(finalResp.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			out.Observe("warn", "forward_as_responses buffered: read error", err, requestID, "")
		}
		// A stop reason makes the accumulated reply usable after connection
		// failure. Without that evidence, or after frame overflow, return captured
		// accounting observations with the read error and request a client 502.
		if finalResp == nil || finalResp.StopReason == nil || *finalResp.StopReason == "" || errors.Is(err, ErrConversionSSEFrameTooLarge) {
			out.Error(502, "upstream_stream_error", "Upstream stream could not be read completely")
			return resultWithUsage(), err
		}
	}

	if finalResp == nil {
		out.Error(502, "server_error", "Upstream stream ended without a response")
		return nil, fmt.Errorf("upstream stream ended without response")
	}

	// The accumulated snapshot includes cache-only observations from later frames.
	// Copy it before conversion so message_start cannot hide those final counts.
	finalResp.Usage = protocolanthropic.UsageFromTokens(usage)

	responsesResp := bridge.AnthropicToResponsesResponse(in.Runtime, finalResp)
	responsesResp.Model = originalModel // logical client model

	out.CopyHeaders(in.Headers)
	// Override copied stream headers before writing buffered JSON. The HTTP
	// adapter's JSON writer can retain an existing Content-Type, which would make
	// clients interpret this complete body as an event stream.
	out.BeginJSON()
	if respBytes, err := json.Marshal(responsesResp); err == nil {
		respBytes = out.ReverseTools(respBytes)
		respBytes, _, err = bridge.RestoreResponsesClientToolPayload(respBytes, clientToolMapping)
		if err != nil {
			return nil, fmt.Errorf("restore responses client tools: %w", err)
		}
		out.JSONBytes(respBytes)
	} else {
		// Typed output handles serialization failure through the writer's own
		// diagnostics. This path bypasses encoded tool-name/client-tool restoration
		// because no serialized body is available to those transformations here.
		out.ResponsesJSON(responsesResp)
	}

	return resultWithUsage(), scanner.Err()
}

// ResponsesStreaming returns observations while converting Anthropic frames
// into Responses SSE with client-tool restoration. Input Lines and Output must
// be usable, and the caller owns response closure. Bridge state retains output
// identity across events while raw model observations remain separate from the
// logical model assigned to emitted output.
//
// Event write failure stops ordinary frame consumption with current
// observations and nil error, recording a disconnect diagnostic. Finalization
// retains an already recorded reader error if its terminal write fails. An
// unfinished bridge emits response.failed before returning a read error unless
// a terminal event has already been produced. A usable stop can allow
// finalization despite a read failure, while frame overflow remains an error.
func ResponsesStreaming(in Response, out Output, originalModel, mappedModel string, reasoningEffort *string, startTime time.Time, clientToolMapping bridge.ResponsesClientToolMapping) (*Result, error) {
	requestID := in.RequestID
	// Observe source model bytes before conversion chooses the client alias.
	// This separates audit identity from the logical model assigned to generated
	// Responses/Chat output, including observations retained after read failure.
	var modelObserver protocol.ResponseModelObserver

	out.CopyHeaders(in.Headers)
	out.BeginStream()

	state := bridge.NewAnthropicEventToResponsesState(in.Runtime)
	state.Model = originalModel
	clientToolRestorer := bridge.NewResponsesClientToolStreamRestorer(clientToolMapping)
	var usage upstream.TokenUsage

	firstChunk := true

	scanner := newConversionSSEScanner(in)

	resultWithUsage := func() *Result {
		return &Result{
			RequestID:             requestID,
			UpstreamHeaders:       in.Headers,
			Usage:                 usage,
			Model:                 originalModel,
			UpstreamModel:         mappedModel,
			UpstreamResponseModel: modelObserver.Model(),
			ReasoningEffort:       reasoningEffort,
			Stream:                true,
			Duration:              time.Since(startTime),
			Timings:               nil,
		}
	}

	// writeEvent restores both request tool names and client tool shapes before
	// regular/final framing. Its true result means Event failed and the caller
	// should stop; serialization/restoration failures are logged and skipped.
	writeEvent := func(evt protocolopenai.ResponsesStreamEvent) bool {
		payload, err := json.Marshal(evt)
		if err != nil {
			// Skip an unserializable event after the bridge has produced it. Other
			// events can continue through the stream; no replacement envelope is
			// written for this event-level output preparation failure.
			out.Observe("warn", "forward_as_responses stream: failed to marshal event", err, requestID, "")
			return false
		}
		payload = out.ReverseTools(payload)
		payloads, _, err := clientToolRestorer.RestoreEvent(payload)
		if err != nil {
			// Restoration failure drops this prepared event and records diagnostics.
			// The bridge/restorer states belong to this attempt, so continuing does
			// not rebuild the failed event through a fresh conversion state.
			out.Observe("warn", "forward_as_responses stream: failed to restore client tools", err, requestID, "")
			return false
		}
		for _, restored := range payloads {
			eventType := gjson.GetBytes(restored, "type").String()
			if _, err := out.Event(eventType, restored); err != nil {
				out.Observe("info", "forward_as_responses stream: client disconnected", nil, requestID, "")
				return true
			}
		}
		return false
	}

	processEvent := func(event *protocolanthropic.AnthropicStreamEvent) bool {
		// Measure the first parsed upstream event before asking the bridge for
		// client chunks. Message metadata or an event producing no client content
		// can establish this timing; it is not a text-only observation.
		if firstChunk {
			firstChunk = false
		}

		events := bridge.AnthropicEventToResponsesEvents(in.Runtime, event, state)
		if slices.ContainsFunc(events, writeEvent) {
			return true
		}
		if len(events) > 0 {
			out.Flush()
		}
		return false
	}

	// Finalization closes outstanding bridge items when no message_stop was
	// produced. Restore these synthetic events through the same client-tool
	// state so their terminal payload matches regular converted output.
	finalizeStream := func() (*Result, error) {
		if finalEvents := bridge.FinalizeAnthropicResponsesStream(state); len(finalEvents) > 0 {
			if slices.ContainsFunc(finalEvents, writeEvent) {
				return resultWithUsage(), nil
			}
			out.Flush()
		}
		return resultWithUsage(), nil
	}

	for scanner.Scan() {
		payload := scanner.frame.Data
		eventType := scanner.frame.EventType

		modelObserver.ObserveAnthropic([]byte(payload))
		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			// Skip malformed event JSON after recording the parser diagnostic.
			// Later frames can still advance the bridge and usage observations;
			// this individual failure is not returned as a terminal reader error.
			out.Observe("warn", "forward_as_responses stream: failed to parse event", err, requestID, eventType)
			continue
		}
		// JSON type is authoritative when present. The SSE event field supplies
		// the type only when JSON omits it, supporting equivalent named and
		// unnamed frame shapes through the same conversion path.
		if event.Type == "" {
			event.Type = eventType
		}
		// Resolve compatible cache fields before conversion so the returned usage
		// and client events share the same accumulated prompt partition.
		protocolanthropic.NormalizeStreamUsage([]byte(payload), &event, &usage)

		if processEvent(&event) {
			return resultWithUsage(), nil
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			out.Observe("warn", "forward_as_responses stream: read error", err, requestID, "")
		}
		// CompletedSent records a bridge-produced terminal event before writing.
		// A stop can also permit finalization; otherwise append a failure event
		// unless that terminal state already exists. Overflow always returns error.
		if (!state.CompletedSent && state.StopReason == "") || errors.Is(err, ErrConversionSSEFrameTooLarge) {
			if !state.CompletedSent {
				writeConversionStreamError(out, true, state)
			}
			return resultWithUsage(), err
		}
	}

	result, err := finalizeStream()
	return result, errors.Join(err, scanner.Err())
}

// ChatBuffered returns Chat JSON assembled from Anthropic events through a
// Responses intermediate and retains merged usage plus raw model observations.
// Input Lines and Output must be usable, and the caller owns response closure.
// Content deltas require nonnegative indices into the accumulated block order.
//
// A read failure with no usable stop writes 502 and returns captured observations
// with the error. A stop permits final output alongside that error, while frame
// overflow rejects conversion regardless of a prior stop. A clean stream with
// no message_start returns no result, and malformed events are skipped without
// the per-event diagnostics used by ResponsesBuffered.
func ChatBuffered(in Response, out Output, originalModel, mappedModel string, reasoningEffort *string, startTime time.Time) (*Result, error) {
	requestID := in.RequestID
	// Observe source model bytes before conversion chooses the client alias.
	// This separates audit identity from the logical model assigned to generated
	// Responses/Chat output, including observations retained after read failure.
	var modelObserver protocol.ResponseModelObserver

	scanner := newConversionSSEScanner(in)

	var finalResp *protocolanthropic.AnthropicResponse
	var usage upstream.TokenUsage

	resultWithUsage := func() *Result {
		return &Result{
			RequestID:             requestID,
			UpstreamHeaders:       in.Headers,
			Usage:                 usage,
			Model:                 originalModel,
			UpstreamModel:         mappedModel,
			UpstreamResponseModel: modelObserver.Model(),
			ReasoningEffort:       reasoningEffort,
			Stream:                false,
			Duration:              time.Since(startTime),
		}
	}

	for scanner.Scan() {
		payload := scanner.frame.Data
		eventType := scanner.frame.EventType

		modelObserver.ObserveAnthropic([]byte(payload))
		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			// The Chat adapter discards unusable event JSON and continues reading.
			// Later frames retain their chance to supply content/usage, while only
			// the scanner's terminal failure enters the returned read-error path.
			continue
		}
		// JSON type is authoritative when present. The SSE event field supplies
		// the type only when JSON omits it, supporting equivalent named and
		// unnamed frame shapes through the same conversion path.
		if event.Type == "" {
			event.Type = eventType
		}
		// Resolve compatible cache fields before conversion so the returned usage
		// and client events share the same accumulated prompt partition.
		protocolanthropic.NormalizeStreamUsage([]byte(payload), &event, &usage)

		if event.Type == "message_start" && event.Message != nil {
			finalResp = event.Message
		}

		if event.Type == "message_delta" {
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = bridge.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		}
		if event.Type == "content_block_start" && event.ContentBlock != nil && finalResp != nil {
			finalResp.Content = append(finalResp.Content, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && finalResp != nil && event.Index != nil {
			// Deltas address the accumulated block order rather than appending new
			// blocks. Indices beyond known blocks are ignored; the upstream must
			// supply nonnegative positions because this guard checks only the upper bound.
			idx := *event.Index
			if idx < len(finalResp.Content) {
				switch event.Delta.Type {
				case "text_delta":
					finalResp.Content[idx].Text += event.Delta.Text
				case "thinking_delta":
					finalResp.Content[idx].Thinking += event.Delta.Thinking
				case "input_json_delta":
					finalResp.Content[idx].Input = AppendRawJSON(finalResp.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			out.Observe("warn", "forward_as_cc buffered: read error", err, requestID, "")
		}
		// A stop reason makes the accumulated reply usable after connection
		// failure. Without that evidence, or after frame overflow, return captured
		// accounting observations with the read error and request a client 502.
		if finalResp == nil || finalResp.StopReason == nil || *finalResp.StopReason == "" || errors.Is(err, ErrConversionSSEFrameTooLarge) {
			out.Error(502, "upstream_stream_error", "Upstream stream could not be read completely")
			return resultWithUsage(), err
		}
	}

	if finalResp == nil {
		out.Error(502, "server_error", "Upstream stream ended without a response")
		return nil, fmt.Errorf("upstream stream ended without response")
	}

	// The accumulated snapshot includes cache-only observations from later frames.
	// Copy it before conversion so message_start cannot hide those final counts.
	finalResp.Usage = protocolanthropic.UsageFromTokens(usage)

	responsesResp := bridge.AnthropicToResponsesResponse(in.Runtime, finalResp)
	ccResp := bridge.ResponsesToChatCompletions(in.Runtime, responsesResp, originalModel)

	out.CopyHeaders(in.Headers)
	// Override copied stream headers before writing buffered JSON. The HTTP
	// adapter's JSON writer can retain an existing Content-Type, which would make
	// clients interpret this complete body as an event stream.
	out.BeginJSON()
	if respBytes, err := json.Marshal(ccResp); err == nil {
		respBytes = out.ReverseTools(respBytes)
		out.JSONBytes(respBytes)
	} else {
		// Delegate serialization failure to typed Chat output and its diagnostics.
		// Encoded tool-name restoration requires marshaled bytes, so this fallback
		// does not run the byte restoration callback in this adapter.
		out.ChatJSON(ccResp)
	}

	return resultWithUsage(), scanner.Err()
}

// ChatStreaming returns observations while translating Anthropic frames through
// Responses state into Chat SSE. Input Lines and Output must be usable, and
// response closure belongs to the caller. Both bridge states start with the
// supplied logical model while the observer records the raw declaration; includeUsage
// controls the Chat usage chunk produced by the bridge.
//
// Ordinary event write failure stops with observations and nil error. Read
// failure before a stop or produced terminal event emits a Chat error event;
// a stop allows finalization with the retained error, except on frame overflow.
// Finalization writes are attempted without propagating their individual errors,
// and the completed stream ends with the standard DONE marker.
func ChatStreaming(in Response, out Output, originalModel, mappedModel string, reasoningEffort *string, startTime time.Time, includeUsage bool) (*Result, error) {
	requestID := in.RequestID
	// Observe source model bytes before conversion chooses the client alias.
	// This separates audit identity from the logical model assigned to generated
	// Responses/Chat output, including observations retained after read failure.
	var modelObserver protocol.ResponseModelObserver

	out.CopyHeaders(in.Headers)
	out.BeginStream()

	anthState := bridge.NewAnthropicEventToResponsesState(in.Runtime)
	anthState.Model = originalModel
	ccState := bridge.NewResponsesEventToChatState(in.Runtime)
	ccState.Model = originalModel
	ccState.IncludeUsage = includeUsage

	var usage upstream.TokenUsage

	firstChunk := true

	scanner := newConversionSSEScanner(in)

	resultWithUsage := func() *Result {
		return &Result{
			RequestID:             requestID,
			UpstreamHeaders:       in.Headers,
			Usage:                 usage,
			Model:                 originalModel,
			UpstreamModel:         mappedModel,
			UpstreamResponseModel: modelObserver.Model(),
			ReasoningEffort:       reasoningEffort,
			Stream:                true,
			Duration:              time.Since(startTime),
			Timings:               nil,
		}
	}

	writeChunk := func(chunk protocolopenai.ChatCompletionsChunk) bool {
		payload, err := json.Marshal(chunk)
		if err != nil {
			// A Chat chunk that cannot be encoded is skipped while later chunks
			// continue. The caller uses this boolean for Event failure, so encoding
			// failure does not select the disconnect stop path.
			return false
		}
		payload = out.ReverseTools(payload)
		// Restore tool identities before unnamed Chat framing. The concrete
		// HTTP adapter retains this attempt's tool mapping and can also restore
		// recognized static prefixes when no request mapping exists.
		if _, err := out.Event("", payload); err != nil {
			return true // client disconnected
		}
		return false
	}

	processAnthropicEvent := func(event *protocolanthropic.AnthropicStreamEvent) bool {
		// Measure the first parsed upstream event before asking the bridge for
		// client chunks. Message metadata or an event producing no client content
		// can establish this timing; it is not a text-only observation.
		if firstChunk {
			firstChunk = false
		}

		responsesEvents := bridge.AnthropicEventToResponsesEvents(in.Runtime, event, anthState)
		for _, resEvt := range responsesEvents {
			ccChunks := bridge.ResponsesEventToChatChunks(&resEvt, ccState)
			for _, chunk := range ccChunks {
				if disconnected := writeChunk(chunk); disconnected {
					return true
				}
			}
		}
		out.Flush()
		return false
	}

	for scanner.Scan() {
		payload := scanner.frame.Data
		eventType := scanner.frame.EventType

		modelObserver.ObserveAnthropic([]byte(payload))
		var event protocolanthropic.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			// The Chat adapter discards unusable event JSON and continues reading.
			// Later frames retain their chance to supply content/usage, while only
			// the scanner's terminal failure enters the returned read-error path.
			continue
		}
		// JSON type is authoritative when present. The SSE event field supplies
		// the type only when JSON omits it, supporting equivalent named and
		// unnamed frame shapes through the same conversion path.
		if event.Type == "" {
			event.Type = eventType
		}
		// Resolve compatible cache fields before conversion so the returned usage
		// and client events share the same accumulated prompt partition.
		protocolanthropic.NormalizeStreamUsage([]byte(payload), &event, &usage)

		if processAnthropicEvent(&event) {
			return resultWithUsage(), nil
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			out.Observe("warn", "forward_as_cc stream: read error", err, requestID, "")
		}
		// Stop or produced-terminal evidence allows a Chat ending after ordinary
		// connection failure. Without it, append a protocol error before returning;
		// frame overflow still returns an error even after terminal state exists.
		if (!anthState.CompletedSent && anthState.StopReason == "") || errors.Is(err, ErrConversionSSEFrameTooLarge) {
			if !anthState.CompletedSent {
				writeConversionStreamError(out, false, anthState)
			}
			return resultWithUsage(), err
		}
	}

	// Finalize both intermediate and Chat state after normal or usable stopped
	// input. These terminal writes are attempted without propagating each write
	// result, while scanner.Err continues to describe upstream reading.
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

	out.Event("", []byte("[DONE]")) //nolint:errcheck // The stream is complete; failure to write [DONE] has no further recovery.
	out.Flush()

	return resultWithUsage(), scanner.Err()
}

// writeConversionStreamError attempts one client-protocol read-failure event
// and flushes when serialization succeeds. Responses output requires nonnil
// bridge state for identity and sequence fields; Chat output permits nil state.
// Serialization and event-write failures have no return path from this helper.
// The local event operation identifies the generated error to output timing.
func writeConversionStreamError(out Output, responses bool, state *bridge.AnthropicEventToResponsesState) {
	streamError := &protocolopenai.ResponsesError{Code: "upstream_stream_error", Message: "Upstream stream could not be read completely"}
	var value any = map[string]any{"error": streamError}
	eventType := ""
	if responses {
		eventType = "response.failed"
		value = protocolopenai.ResponsesStreamEvent{
			Type:           eventType,
			SequenceNumber: state.SequenceNumber,
			Response: &protocolopenai.ResponsesResponse{
				ID:        state.ResponseID,
				Object:    "response",
				CreatedAt: state.Created,
				Model:     state.Model,
				Status:    "failed",
				Error:     streamError,
				Output:    []protocolopenai.ResponsesOutput{},
			},
		}
	}
	payload, err := json.Marshal(value)
	// The existing stream receives a best-effort error frame whose write result
	// is discarded here. Serialization failure leaves output untouched, and the
	// caller retains its upstream read error regardless of this output attempt.
	if err == nil {
		_, _ = out.LocalEvent(eventType, payload)
		out.Flush()
	}
}

// AppendRawJSON extends a streamed tool-argument value with the next raw
// fragment. An absent value or any valid empty object is replaced by the
// fragment because content_block_start can supply an empty-object placeholder
// before input_json_delta supplies the actual argument bytes.
//
// A nonempty object and all other existing byte shapes are concatenated
// literally. The helper does not validate the completed argument, insert JSON
// separators, or merge object keys; callers supply fragments in stream order.
// This permits partial JSON to remain incomplete until later deltas arrive.
func AppendRawJSON(existing json.RawMessage, fragment string) json.RawMessage {
	var existingObject map[string]json.RawMessage
	isEmptyObject := json.Unmarshal(existing, &existingObject) == nil && existingObject != nil && len(existingObject) == 0
	// Empty-object placeholders carry no streamed argument content, including
	// whitespace-only object formatting. Replacing that placeholder prevents a
	// leading empty object from being concatenated into the actual argument value.
	if len(existing) == 0 || isEmptyObject {
		return json.RawMessage(fragment)
	}
	return json.RawMessage(string(existing) + fragment)
}
