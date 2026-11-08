package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// response_passthrough.go forwards passthrough Responses output while
// repairing lifecycle events, terminal output, and client identities.
// Passthrough Responses streams keep provider framing while normalizing
// lifecycle payloads, tool identity, model spelling, and reconstructed terminal
// output. Preamble lines wait until content or a selected failure starts
// delivery. Headers are staged at entry, while the HTTP boundary can commit
// downstream heartbeats before content. Client-output policy excludes recorded
// heartbeat bytes from its recovery decision; failure callbacks receive upstream
// headers directly so retry metadata does not depend on early client exposure.
//
// NativeOpenAI selects this reader's bare-error terminal mode. Error evidence
// is observed before client trimming, and a missing response.failed can be
// synthesized after a retained bare error. Ordinary content delivery releases
// pending lines through the HTTP output boundary; synthetic EOF and deferred
// flush paths retain their own terminal and usage decisions. Returned
// streaming results contain usage/timing/ID/image observations, while the
// shared delivery/commit flags remain their zero values.

// PassthroughOptions binds passthrough readers to model observation, output,
// upstream observation and failure policy.
type PassthroughOptions struct {
	// ObserveModel optionally sees streaming upstream payload/model evidence before
	// lifecycle normalization and client model replacement; buffered readers use
	// their NonStream observation callbacks.
	ObserveModel func([]byte, string)
	// StreamOptions supplies selected observation/restoration/failure hooks;
	// passthrough uses NativeOpenAI for terminal mode and retains its separate
	// preamble staging instead of the standard reader's timeout execution.
	StreamOptions
	// NonStream supplies buffered body reading, terminal failure, restoration, and
	// Compact delivery policy; the passthrough reader keeps the executor's
	// response-body ownership.
	NonStream NonStreamOptions
	// Headers copies approved provider metadata into the codec's private map.
	// The output sink applies it at delivery only if HTTP remains uncommitted.
	Headers func(http.Header, http.Header)
	// MissingUsage observes retained usage with response, terminal label, and
	// disconnect evidence on normal return; early failed paths return before this
	// callback.
	MissingUsage func(*http.Response, *wire.ForwardUsage, string, bool)
	// MissingTerminal records clean EOF without terminal evidence when ctx is
	// active and client delivery is connected; the reader then applies
	// output-specific failure policy.
	MissingTerminal func(string)
	// PassthroughFailoverWithModel proposes pre-output recovery using upstream
	// request ID, failure bytes/message, mapped model, and original response headers.
	// Caller policy decides retry and retains headers for final error presentation.
	PassthroughFailoverWithModel func(string, []byte, string, string, http.Header) error
}

// ReadPassthroughStreaming normalizes and forwards Responses SSE while
// observing usage and completed images. Supply a nonnil ctx, response,
// output context, and callbacks used by this reader; the executor owns body
// closure. NativeOpenAI selects bare-error terminal handling, and configured
// standard-reader timeout/staging fields are not executed here.
//
// Raw model observation precedes lifecycle normalization and client
// restoration. Preamble remains buffered until output policy permits delivery,
// and ordinary content delivery passes through the shared HTTP writer. The
// result populates usage, ResponseID, and image fields, leaving
// other StreamingResult flags zero. Synthetic EOF writes and the final deferred
// flush retain their existing terminal ordering; callers use the request
// writer's own observations for commitment and output state.
func ReadPassthroughStreaming(ctx context.Context, resp *http.Response, c *upstream.OutputContext, options PassthroughOptions, startTime time.Time, originalModel, mappedModel string) (*StreamingResult, error) {
	options.Headers(c.Writer.Header(), resp.Header)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if v := resp.Header.Get("x-request-id"); v != "" {
		c.Header("x-request-id", v)
	}

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	usage := &wire.ForwardUsage{}
	imageCounter := wire.NewOpenAIImageOutputCounter()

	responseID := ""
	clientDisconnected := false
	sawDone := false
	sawTerminalEvent := false
	sawFailedEvent := false
	sawBareError := false
	sawResponseFailed := false
	terminalEventType := ""
	semanticOutputSeen := false
	capacityFailoverSuppressedLogged := false
	failedMessage := ""
	// Failed-return warning classification receives a nil body in this reader;
	// live failure processing observes its actual payload and retains the message separately.
	var failedPayload []byte
	clientOutputStarted := false
	codexFailureTerminal := options.NativeOpenAI
	failureDelivered := false
	suppressCurrentEvent := false
	responseFailedPending := false
	var bareErrorPayload []byte
	bareErrorProviderSideEffectsPending := false
	upstreamRequestID := strings.TrimSpace(resp.Header.Get("x-request-id"))
	// Hold preamble lines before content, so caller output policy can classify
	// empty terminal failure while semantic delivery remains eligible for request recovery.
	pendingLines := make([]string, 0, 8)

	// Retain the pending flush decision across lines until an event separator
	// or return; disconnected sinks skip that delivery while usage observations remain available.
	flushPending := false
	pendingSSEEventType := ""
	flushPendingOutput := func() {
		if clientDisconnected || !flushPending {
			return
		}
		flusher.Flush()
		flushPending = false
	}
	defer flushPendingOutput()
	writePendingLines := func() bool {
		for _, pending := range pendingLines {
			if _, err := fmt.Fprintln(w, pending); err != nil {
				clientDisconnected = true
				options.Logf("[OpenAI passthrough] Client disconnected during streaming, continue draining upstream for usage: provider=%d", options.ProviderID)
				return false
			}
		}
		pendingLines = pendingLines[:0]
		return true
	}
	// A retained bare error can end without response.failed; settle pending
	// provider effects and emit a failed terminal before final stream flushing.
	ensureResponseFailedTerminal := func() {
		if !sawBareError || sawResponseFailed || failureDelivered {
			return
		}
		if bareErrorProviderSideEffectsPending {
			options.TerminalSideEffects(bareErrorPayload, failedMessage, resp.Header, mappedModel)
			bareErrorProviderSideEffectsPending = false
		}
		if clientDisconnected || !writePendingLines() {
			return
		}
		if _, err := fmt.Fprint(w, options.BuildOpenAIResponseFailedSSE(responseID, originalModel, bareErrorPayload, failedMessage)); err != nil {
			clientDisconnected = true
			return
		}
		clientOutputStarted = true
		failureDelivered = true
		flushPending = true
		flushPendingOutput()
	}

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := options.MaxLineSize
	scanBuf := httpclient.GetSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	defer httpclient.PutSSEScannerBuf64K(scanBuf)
	documentScanner := wire.NewSSEJSONDocumentScanner(scanner)

	needModelReplace := strings.TrimSpace(originalModel) != "" && strings.TrimSpace(mappedModel) != "" && strings.TrimSpace(originalModel) != strings.TrimSpace(mappedModel)
	responseAccumulator := bridge.NewBufferedResponseAccumulator()
	streamDoneItems := bridge.NewResponsesStreamOutputItems()
	streamImageOutputs := make([]json.RawMessage, 0, 1)
	streamSeenImages := make(map[string]struct{})
	// These snapshots populate observation fields only; caller writer/policy
	// state supplies commitment because the shared StreamingResult flags stay zero here.
	resultWithUsage := func() *StreamingResult {
		return &StreamingResult{
			Usage:            usage,
			ResponseID:       responseID,
			ImageCount:       imageCounter.Count(),
			ImageFrameCount:  imageCounter.FrameCount(),
			ImageOutputSizes: imageCounter.Sizes(),
		}
	}

	var lifecycleNormalizer wire.ResponseLifecycleNormalizer
	pendingLifecycleHeader := ""
	var heartbeatFilter upstream.SSEHeartbeatLineFilter
	// Preserve incomplete candidates at EOF, while complete heartbeat frames never
	// enter protocol output or establish the reader's client-content commitment.
	for {
		more := documentScanner.Scan()
		var lines [][]byte
		if more {
			lines = heartbeatFilter.Push([]byte(documentScanner.Text() + "\n"))
		} else {
			lines = heartbeatFilter.Flush()
		}
		for _, rawLine := range lines {
			line := strings.TrimSuffix(string(rawLine), "\n")
			if eventType, ok := wire.ExtractSSEEventLine(line); ok {
				pendingSSEEventType = eventType
				eventType = strings.TrimSpace(eventType)
				suppressCurrentEvent = codexFailureTerminal && (eventType == "error" || (sawBareError && !sawResponseFailed && eventType != "response.failed"))
				if wire.IsResponseLifecycleEvent(eventType) {
					// Retain lifecycle headers with their data until normalization decides
					// type and duplication, preventing an orphaned header when its terminal
					// payload is suppressed.
					pendingLifecycleHeader = line
					continue
				}
			}
			lineStartsClientOutput := false
			forceFlushFailedEvent := false
			if data, ok := wire.ExtractSSEDataLine(line); ok {
				dataBytes := []byte(data)
				trimmedData := strings.TrimSpace(data)
				rawEventType := wire.EffectiveOpenAISSEEventType(dataBytes, pendingSSEEventType)
				if options.ObserveModel != nil {
					options.ObserveModel(dataBytes, rawEventType)
				}
				normalizedData, normalizedType, duplicateTerminal := lifecycleNormalizer.Normalize(dataBytes, rawEventType)
				if !bytes.Equal(normalizedData, dataBytes) {
					dataBytes = normalizedData
					data = string(dataBytes)
					trimmedData = strings.TrimSpace(data)
					line = "data: " + data
				}
				rawEventType = normalizedType
				if needModelReplace && strings.Contains(data, mappedModel) {
					line = wire.ReplaceModelInSSELine(line, mappedModel, originalModel)
					if replacedData, replaced := wire.ExtractSSEDataLine(line); replaced {
						dataBytes = []byte(replacedData)
						trimmedData = strings.TrimSpace(replacedData)
					}
				}
				if normalizedData, normalized := wire.NormalizeOpenAIResponsesFunctionCallArguments(dataBytes); normalized {
					dataBytes = normalizedData
					trimmedData = strings.TrimSpace(string(normalizedData))
					line = "data: " + string(normalizedData)
				}
				if normalizedData, normalized := wire.NormalizeCompletedImageGenerationStatus(dataBytes); normalized {
					dataBytes = normalizedData
					trimmedData = strings.TrimSpace(string(normalizedData))
					line = "data: " + string(normalizedData)
				}
				if trimmedData != "[DONE]" {
					restoredData, restoreErr := options.RestoreNamespace(dataBytes)
					if restoreErr != nil {
						return resultWithUsage(), fmt.Errorf("restore OpenAI passthrough namespace response: %w", restoreErr)
					}
					restoredData = options.RestoreToolNames(restoredData, rawEventType)
					if !bytes.Equal(restoredData, dataBytes) {
						dataBytes = restoredData
						trimmedData = strings.TrimSpace(string(restoredData))
						line = "data: " + string(restoredData)
					}
				}
				eventType := wire.EffectiveOpenAISSEEventType(dataBytes, rawEventType)
				// Lifecycle normalization can turn done into failed; recompute suppression
				// using that terminal identity so a stale event-line decision cannot hide the failure.
				suppressCurrentEvent = codexFailureTerminal && (eventType == "error" || (sawBareError && !sawResponseFailed && eventType != "response.failed"))
				if !capacityFailoverSuppressedLogged && options.NativeOpenAI &&
					(eventType == "error" || eventType == "response.failed") &&
					options.ClientOutputStarted(clientOutputStarted) &&
					IsOpenAIUpstreamCapacityShedEvent(dataBytes) {
					options.CapacitySuppressed(upstreamRequestID, eventType)
					capacityFailoverSuppressedLogged = true
				}
				cyberHit := false
				if eventType == "response.failed" || eventType == "error" {
					if codexFailureTerminal && eventType == "error" {
						sawBareError = true
						bareErrorPayload = append(bareErrorPayload[:0], dataBytes...)
						suppressCurrentEvent = true
					} else if codexFailureTerminal && eventType == "response.failed" {
						sawResponseFailed = true
					}
					responseFailedPending = !codexFailureTerminal || eventType == "response.failed"
					failedMessage = ExtractOpenAISSEErrorMessage(dataBytes)
					if failedMessage == "" {
						failedMessage = "Upstream response failed"
					}
					// Observe terminal usage before policy recording and client trimming,
					// retaining consumed-token evidence even when the failure copy later loses its usage field.
					wire.ParseSSEUsageBytesWithType(dataBytes, eventType, usage)
					if hit, code, msg := DetectOpenAICyberPolicy(dataBytes); hit {
						cyberHit = true
						options.MarkCyber(CyberObservation{
							Code:           code,
							Message:        msg,
							Body:           options.TruncateString(string(dataBytes), 4096),
							UpstreamStatus: http.StatusOK,
							UpstreamInTok:  usage.InputTokens,
							UpstreamOutTok: usage.OutputTokens,
						})
					}
					outputStarted := options.ClientOutputStarted(clientOutputStarted)
					if !outputStarted && !cyberHit {
						if compactErr := options.CompactFallback(dataBytes, failedMessage); compactErr != nil {
							return resultWithUsage(), compactErr
						}
					}
					if outputStarted && !cyberHit {
						if codexFailureTerminal && eventType == "error" {
							// Retain provider effects until the authoritative failed terminal
							// arrives, or apply them during bare-error EOF synthesis if no such terminal follows.
							bareErrorProviderSideEffectsPending = true
						} else {
							options.TerminalSideEffects(dataBytes, failedMessage, resp.Header, mappedModel)
							bareErrorProviderSideEffectsPending = false
						}
					}
					if !outputStarted {
						shouldFailover := false
						if !cyberHit {
							if eventType == "error" {
								shouldFailover = OpenAIStreamErrorEventShouldFailover(dataBytes, failedMessage)
							} else {
								shouldFailover = OpenAIStreamFailedEventShouldFailover(dataBytes, failedMessage)
							}
						}
						if shouldFailover {
							return resultWithUsage(),
								options.PassthroughFailoverWithModel(upstreamRequestID, dataBytes, failedMessage, mappedModel, resp.Header)
						}
						if !cyberHit && !sawBareError {
							if status, errType, errMsg, matched := options.ErrorRule(dataBytes, failedMessage); matched {
								// A matched rule commits its JSON client error after recording
								// upstream evidence, keeping observation available despite customized
								// status/type/message presentation.
								options.RecordError(upstreamRequestID, "http_error", dataBytes, failedMessage)
								options.MarkCommitted()
								c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
								c.JSON(status, map[string]any{
									"error": map[string]any{
										"type":    errType,
										"message": errMsg,
									},
								})
								return resultWithUsage(), fmt.Errorf("upstream response failed: passthrough rule matched message=%s", errMsg)
							}
						}
					}
					forceFlushFailedEvent = true
					sawFailedEvent = true
				}
				if trimmedData == "[DONE]" {
					sawDone = true
					terminalEventType = "[DONE]"
				}
				if options.OpenAIStreamEventIsTerminalWithType(trimmedData, eventType) {
					sawTerminalEvent = true
					if trimmedData != "[DONE]" {
						terminalEventType = eventType
					}
				}
				if responseID == "" {
					responseID = wire.ExtractOpenAIResponseIDFromJSONBytes(dataBytes)
				}
				var responseEvent wire.ResponsesStreamEvent
				if err := json.Unmarshal(dataBytes, &responseEvent); err == nil {
					responseAccumulator.ProcessEvent(&responseEvent)
				}
				if imageOutput, ok := wire.ExtractImageGenerationOutputFromSSEData(dataBytes, streamSeenImages); ok {
					streamImageOutputs = append(streamImageOutputs, imageOutput)
				}
				streamDoneItems.Observe(dataBytes)
				if normalizedData, normalized := bridge.NormalizeResponsesStreamingTerminalOutput(dataBytes, responseAccumulator, streamDoneItems, streamImageOutputs); normalized {
					dataBytes = normalizedData
					data = string(normalizedData)
					trimmedData = strings.TrimSpace(data)
					line = "data: " + data
					eventType = strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String())
				}
				if err := wire.ValidateGIFImageOutputs(dataBytes); err != nil {
					wire.ParseSSEUsageBytes(dataBytes, usage)
					return resultWithUsage(), err
				}
				imageCounter.AddSSEData(dataBytes)
				if sanitizedData, sanitized := SanitizeOpenAIResponseFailedEventForClient(
					dataBytes,
					eventType,
					options.ClientOutputStarted(clientOutputStarted),
				); sanitized {
					dataBytes = sanitizedData
					trimmedData = strings.TrimSpace(string(sanitizedData))
					line = "data: " + string(sanitizedData)
				}
				lineStartsClientOutput = forceFlushFailedEvent || OpenAIStreamDataStartsClientOutput(trimmedData, eventType)
				if lineStartsClientOutput && trimmedData != "[DONE]" && !wire.OpenAIStreamEventTypeIsTerminal(eventType) {
					semanticOutputSeen = true
				}
				// This emptiness check applies to passthrough providers generally, before
				// content commitment, so a lifecycle-only empty completion cannot settle as
				// an ordinary success.
				if (eventType == "response.completed" || eventType == "response.done") &&
					!sawFailedEvent && !semanticOutputSeen && !clientOutputStarted &&
					wire.OpenAIResponsesCompletedEventIsEmpty(dataBytes, usage) {
					return resultWithUsage(), options.EmptyCompleted(upstreamRequestID)
				}
				wire.ParseSSEUsageBytesWithType(dataBytes, eventType, usage)
				if pendingLifecycleHeader != "" {
					header := pendingLifecycleHeader
					if eventType != strings.TrimSpace(pendingSSEEventType) {
						header = "event: " + eventType
					}
					line = header + "\n" + line
					pendingLifecycleHeader = ""
				}
				suppressCurrentEvent = suppressCurrentEvent || duplicateTerminal
			}
			if line == "" {
				pendingLifecycleHeader = ""
				pendingSSEEventType = ""
				if suppressCurrentEvent {
					suppressCurrentEvent = false
					responseFailedPending = false
					continue
				}
			}

			if !clientDisconnected && !failureDelivered && !suppressCurrentEvent {
				if !clientOutputStarted && !lineStartsClientOutput {
					pendingLines = append(pendingLines, line)
					continue
				}
				if !clientOutputStarted && len(pendingLines) > 0 {
					if !writePendingLines() {
						continue
					}
				}
				if _, err := fmt.Fprintln(w, line); err != nil {
					clientDisconnected = true
					options.Logf("[OpenAI passthrough] Client disconnected during streaming, continue draining upstream for usage: provider=%d", options.ProviderID)
				} else {
					clientOutputStarted = true
					flushPending = true
					if line == "" {
						flushPendingOutput()
					}
				}
			}
			if line == "" && responseFailedPending {
				responseFailedPending = false
				failureDelivered = true
			}
		}
		if !more {
			break
		}
	}
	ensureResponseFailedTerminal()
	if err := documentScanner.Err(); err != nil {
		// Terminal success takes precedence over a later scan failure, retaining
		// observed usage and clearing disconnect policy instead of reclassifying the
		// completed response.
		if (sawDone || sawTerminalEvent) && !sawFailedEvent {
			options.ClearDisconnect()
			return resultWithUsage(), nil
		}
		if sawFailedEvent {
			err := fmt.Errorf("upstream response failed: %s", failedMessage)
			return resultWithUsage(), options.WrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, failedPayload, failedMessage, err)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", err)
		}
		if errors.Is(err, bufio.ErrTooLong) {
			options.Logf("[OpenAI passthrough] SSE line too long: provider=%d max_size=%d error=%v", options.ProviderID, maxLineSize, err)
			return resultWithUsage(), err
		}
		if !options.ClientOutputStarted(clientOutputStarted) {
			msg := "OpenAI stream disconnected before completion"
			if errText := strings.TrimSpace(err.Error()); errText != "" {
				msg += ": " + errText
			}
			return resultWithUsage(),
				options.Failover(upstreamRequestID, nil, msg)
		}
		if clientDisconnected {
			return resultWithUsage(), fmt.Errorf("stream usage incomplete after disconnect: %w", err)
		}
		options.RecordDisconnect(err, upstreamRequestID)
		options.Logf("[INFO] [OpenAI passthrough] Stream read interrupted by an error: provider=%d request_id=%s err=%v",
			options.ProviderID,
			upstreamRequestID,
			err,
		)
		return resultWithUsage(), fmt.Errorf("stream read error: %w", err)
	}
	if sawFailedEvent {
		err := fmt.Errorf("upstream response failed: %s", failedMessage)
		return resultWithUsage(), options.WrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, failedPayload, failedMessage, err)
	}
	if !clientDisconnected && !sawDone && !sawTerminalEvent && ctx.Err() == nil {
		options.MissingTerminal(upstreamRequestID)
		if !options.ClientOutputStarted(clientOutputStarted) {
			return resultWithUsage(),
				options.Failover(upstreamRequestID, nil, "OpenAI stream ended before a terminal event")
		}
		options.RecordDisconnect(errors.New("stream ended before terminal event"), upstreamRequestID)
		return resultWithUsage(), errors.New("stream usage incomplete: missing terminal event")
	}
	if (sawDone || sawTerminalEvent) && !sawFailedEvent {
		options.ClearDisconnect()
	}
	options.MissingUsage(resp, usage, terminalEventType, clientDisconnected)

	return resultWithUsage(), nil
}

// ReadPassthroughNonStreaming reads a body through NonStream limit policy,
// observes usage, and forwards the restored representation with the upstream
// status. Explicit event-stream content type selects SSEAsJSON; other bodies
// first use JSON usage and then an SSE usage alternative. The executor retains
// body ownership.
//
// Model replacement precedes namespace, reserved-name, and OpenAI client-tool
// restoration in that order. JSON header preparation precedes these required
// callbacks. A Compact bridge can consume delivery; otherwise OutputContext.Data
// writes with upstream content type or the JSON default. The result returns
// usage, response ID, and image observations while shared Served and SearchCount
// fields remain zero. The ctx parameter is retained by the interface but unused
// here.
func ReadPassthroughNonStreaming(ctx context.Context, resp *http.Response, sink upstream.OutputSink, options PassthroughOptions, originalModel, mappedModel string) (*NonStreamingResult, error) {
	body, err := options.NonStream.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}

	if IsEventStreamResponse(resp.Header) {
		options.NonStream.ObserveSSE(string(body))
		return ReadPassthroughSSEAsJSON(resp, sink, options, body, originalModel, mappedModel)
	}
	options.NonStream.ObserveTier(body)

	usage := &wire.ForwardUsage{}
	usageParsed := false
	if len(body) > 0 {
		if parsedUsage, ok := wire.ExtractOpenAIUsageFromJSONBytes(body); ok {
			*usage = parsedUsage
			usageParsed = true
		}
	}
	// A non-JSON usage representation can still accompany the buffered body;
	// retain SSE-observed counters when ordinary JSON paths did not supply a record.
	if !usageParsed {
		usage = wire.ParseSSEUsageFromBody(string(body))
	}
	options.MissingUsage(resp, usage, "json", false)
	if err := wire.ValidateGIFImageOutputs(body); err != nil {
		return &NonStreamingResult{Usage: usage}, err
	}

	c := upstream.NewOutputContext(sink)
	options.Headers(c.Writer.Header(), resp.Header)

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	if originalModel != "" && mappedModel != "" && originalModel != mappedModel {
		body = wire.ReplaceModelInResponseBody(body, mappedModel, originalModel)
	}
	body, err = restoreBufferedPassthroughTools(body, options)
	if err != nil {
		return nil, err
	}
	if !options.NonStream.WriteCompactBridge(resp.StatusCode, body) {
		c.Data(resp.StatusCode, contentType, body)
	}
	return &NonStreamingResult{
		Usage:            usage,
		ResponseID:       wire.ExtractOpenAIResponseIDFromJSONBytes(body),
		ImageCount:       wire.CountOpenAIResponseImageOutputsFromJSONBytes(body),
		ImageFrameCount:  wire.CountOpenAIImageFramesFromJSON(body),
		ImageOutputSizes: wire.CollectOpenAIResponseImageOutputSizesFromJSONBytes(body),
	}, nil
}

// ReadPassthroughSSEAsJSON converts buffered SSE to terminal JSON when a final
// response is available. Failed/bare-error terminals run Compact recovery,
// terminal failover, then protocol-error policy before output. Callers provide
// the already read body and retain executor response ownership.
//
// Empty final output can be reconstructed from deltas; failed optional
// reconstruction keeps the terminal bytes. Compaction supplementation and tool
// restoration precede header preparation and delivery. Final JSON requires the
// namespace, reserved-name, and OpenAI client-tool restoration callbacks.
// Without final JSON, the body stays SSE with compatible model replacement and
// its content type. Image counts/sizes use the original SSE observation, while
// the returned response ID follows delivered body extraction. Served and
// SearchCount remain zero in this result.
func ReadPassthroughSSEAsJSON(resp *http.Response, sink upstream.OutputSink, options PassthroughOptions, body []byte, originalModel, mappedModel string) (*NonStreamingResult, error) {
	if err := wire.ValidateGIFSSEOutputs(string(body)); err != nil {
		return &NonStreamingResult{Usage: wire.ParseSSEUsageFromBody(string(body))}, err
	}
	bodyText := string(body)
	terminalType, terminalPayload, terminalOK := wire.ExtractOpenAISSETerminalEvent(bodyText)
	if terminalOK && (terminalType == "response.failed" || terminalType == "error") {
		msg := ExtractOpenAISSEErrorMessage(terminalPayload)
		if msg == "" {
			msg = "Upstream compact response failed"
		}
		if compactErr := options.CompactFallback(terminalPayload, msg); compactErr != nil {
			return nil, compactErr
		}
		if failoverErr := options.NonStream.TerminalFailover(resp, terminalType, terminalPayload, msg, mappedModel); failoverErr != nil {
			return nil, failoverErr
		}
		return nil, options.NonStream.ProtocolError(resp, msg)
	}
	finalResponse, ok := wire.ExtractCodexFinalResponse(bodyText)

	usage := wire.ParseSSEUsageFromBody(bodyText)
	if ok {
		if parsedUsage, parsed := wire.ExtractOpenAIUsageFromJSONBytes(finalResponse); parsed {
			*usage = parsedUsage
		}

		// An empty terminal output can have earlier deltas; reconstruction is
		// optional, and a failed raw output patch leaves the selected terminal metadata intact.
		if len(gjson.GetBytes(finalResponse, "output").Array()) == 0 {
			if outputJSON, reconstructed := bridge.ReconstructResponseOutputFromSSE(bodyText); reconstructed {
				if patched, err := sjson.SetRawBytes(finalResponse, "output", outputJSON); err == nil {
					finalResponse = patched
				}
			}
		}
		finalResponse = options.NonStream.SupplementCompaction(finalResponse, bodyText)
		body = finalResponse
		if originalModel != "" && mappedModel != "" && originalModel != mappedModel {
			body = wire.ReplaceModelInResponseBody(body, mappedModel, originalModel)
		}

		body = options.NonStream.CorrectToolCalls(body)
		restoredBody, restoreErr := restoreBufferedPassthroughTools(body, options)
		if restoreErr != nil {
			return nil, restoreErr
		}
		body = restoredBody
	} else {
		if originalModel != "" && mappedModel != "" && originalModel != mappedModel {
			bodyText = wire.ReplaceModelInSSEBody(bodyText, mappedModel, originalModel)
		}
		// Missing final JSON retains the framed SSE response, so its original
		// content type remains meaningful for clients even though this is buffered handling.
		body = []byte(bodyText)
	}

	c := upstream.NewOutputContext(sink)
	options.Headers(c.Writer.Header(), resp.Header)
	options.MissingUsage(resp, usage, terminalType, false)

	contentType := "application/json; charset=utf-8"
	if !ok {
		contentType = resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "text/event-stream"
		}
	}
	if !options.NonStream.WriteCompactBridge(resp.StatusCode, body) {
		c.Data(resp.StatusCode, contentType, body)
	}

	return &NonStreamingResult{
		Usage:            usage,
		ResponseID:       wire.ExtractOpenAIResponseIDFromJSONBytes(body),
		ImageCount:       wire.CountOpenAIImageOutputsFromSSEBody(bodyText),
		ImageFrameCount:  wire.CountOpenAIImageFramesFromSSE(bodyText),
		ImageOutputSizes: wire.CollectOpenAIImageOutputSizesFromSSEBody(bodyText),
	}, nil
}

// restoreBufferedPassthroughTools restores tool identities in one passthrough
// JSON body. Callers supply all three callbacks; each receives its predecessor's
// returned slice, including nil or empty results, and the first error stops
// restoration with response-specific context.
func restoreBufferedPassthroughTools(body []byte, options PassthroughOptions) ([]byte, error) {
	// Namespace restoration precedes reserved-name restoration and client-tool
	// schema restoration, so each callback consumes the bytes produced by its predecessor.
	body, err := options.RestoreNamespace(body)
	if err != nil {
		return nil, fmt.Errorf("restore OpenAI passthrough namespace response: %w", err)
	}
	body = options.NonStream.RestoreToolNames(body)
	body, err = options.NonStream.RestoreOpenAITools(body)
	if err != nil {
		return nil, fmt.Errorf("restore OpenAI Responses client tools: %w", err)
	}
	return body, nil
}
