package openai

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// images_response.go converts OAuth Responses image events into buffered
// Images output or progressive image streams.
// OAuth image generation arrives as Responses lifecycle, partial-image,
// completed-item, and terminal events. Partial images can be sent before a
// final result exists, while completed items are retained for a later terminal
// merge. Exact encoded result and format keys prevent a completed item and its
// terminal copy from producing duplicate client events.
//
// Usage is observed before error presentation, and downstream failure stops
// further writes while upstream processing continues until a terminal event,
// read failure, EOF, or timeout. Completed-image counts record recognized
// results even when their delivery fails. At EOF, buffered completed items can
// supply the final image events; lifecycle-only streams produce an
// incomplete-generation error. The executor owns response-body closure for
// synchronous and queued readers.

// ImageResponseOptions binds image readers to request-specific output,
// accounting, and error policy callbacks.
type ImageResponseOptions struct {
	ExpectedGIF bool // requested GIF output validation
	// ExpectedImages is the requested final image count; zero uses one when
	// validating a native Images stream before buffered or streaming delivery.
	ExpectedImages int
	// EventPrefix selects generation or editing completions when a native
	// upstream mislabeled its buffered JSON response as an event stream.
	EventPrefix string
	// RequireBase64 makes native Images JSON conversion supply encoded data for
	// every completion; OAuth's existing JSON compatibility path remains independent.
	RequireBase64 bool
	// FetchImage downloads URL-only outputs with the selected provider proxy and
	// public-target validation. Failure ends delivery after retaining upstream observations.
	FetchImage func(string) (string, error)

	// StartKeepalive optionally starts the HTTP SSE controller before a streamed
	// exchange. Its idempotent cleanup waits for writes and restores the writer.
	StartKeepalive func() func()
	// PreserveContentType lets the API-key nonstream reader keep a supplied
	// upstream content type; otherwise that reader uses its JSON default.
	PreserveContentType bool
	// Backfill converts the API-key nonstream body after usage observation; that
	// reader requires this callback even when it returns unchanged bytes.
	Backfill func([]byte) []byte
	// ReadLimit supplies the native image byte budget. It bounds SSE frames,
	// buffered JSON, retained completions, and expanded Base64 delivery payloads.
	ReadLimit func() int64
	// ReadBody consumes buffered JSON under the caller's size and error policy.
	// Nonstream readers and streamed Images JSON conversion require this callback.
	ReadBody func(io.Reader) ([]byte, error)
	// ClassifyReadError translates OAuth read failures after pending event
	// processing; API-key streaming returns its read errors directly.
	ClassifyReadError func(error) error
	// ResponseHeaders copies approved upstream headers into the client header map
	// before each reader selects its response content type.
	ResponseHeaders func(http.Header, http.Header)
	// ObserveError records response failures before return, using status and
	// message plus an optional caller-provided diagnostic summary.
	ObserveError func(int, string, string)
	// ObserveStreamError records a terminal native-stream failure after response
	// commitment. The reader writes its error frame separately on the same goroutine.
	ObserveStreamError func(*OpenAIImagesUpstreamError)
	// WriteHTTPError presents nonretryable OAuth nonstream errors and buffered
	// image-stream failures. JSON validation failures apply to either credential type.
	WriteHTTPError func(*OpenAIImagesUpstreamError) bool
	// EmptyOutput classifies OAuth nonstream responses containing neither an image
	// nor recognizable model text for the executor's retry policy.
	EmptyOutput func([]byte) error
	// Summary builds OAuth no-output diagnostics; its caller owns redaction policy
	// for any upstream text included in that summary.
	Summary func([]byte) string
	// AdjustedWrittenSize returns current output with JSON or SSE heartbeat bytes
	// excluded. Both the baseline and later retry checks use this same measure.
	AdjustedWrittenSize func() int
	// RecordKeepalive optionally records accepted SSE comment bytes so the HTTP
	// caller can exclude body heartbeats from request-wide output and retry decisions.
	RecordKeepalive func(int)
	// StreamInterval enables idle checks when positive; checks use upstream read
	// arrival time even while downstream event processing is delayed.
	StreamInterval func() time.Duration
	// KeepaliveInterval enables comment heartbeats when positive; successful
	// downstream writes postpone the next heartbeat and failed writes disable it.
	KeepaliveInterval func() time.Duration
	Logf              func(string, ...any) // request diagnostic log callback
}

// WriteImagesStreamEvent writes an optional event line, marks image output
// state, then writes and flushes the data frame. Pass a usable context and
// flusher. A later failure can leave an event prefix or an already marked
// semantic/terminal state, so callers treat the returned error as a delivery
// failure.
func WriteImagesStreamEvent(c *upstream.OutputContext, options ImageResponseOptions, flusher http.Flusher, eventName string, payload []byte) error {
	if strings.TrimSpace(eventName) != "" {
		if _, err := fmt.Fprintf(c.Writer, "event: %s\n", eventName); err != nil {
			return err
		}
	}
	// Mark image content after its event prefix but before data delivery, so a
	// data-write failure can still leave output state recorded.
	if strings.HasSuffix(eventName, ".partial_image") || strings.HasSuffix(eventName, ".completed") {
		c.NextEvent(true, strings.HasSuffix(eventName, ".completed"))
	}
	if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// TryWriteImagesStreamEvent writes an event unless the supplied disconnect flag
// is already set. A failure latches that flag and logs through Logf; success
// updates an optional lastWriteAt timestamp. Callers serialize access to the
// writer and pointed-to state, and retain upstream accounting after a false
// result.
func TryWriteImagesStreamEvent(
	c *upstream.OutputContext, options ImageResponseOptions,
	flusher http.Flusher,
	clientDisconnected *bool,
	lastWriteAt *time.Time,
	eventName string,
	payload []byte,
) bool {
	if clientDisconnected != nil && *clientDisconnected {
		return false
	}
	// A partial frame cannot be replayed through this helper; latch failure so
	// later event attempts stop writing to the sink.
	if err := WriteImagesStreamEvent(c, options, flusher, eventName, payload); err != nil {
		if clientDisconnected != nil {
			*clientDisconnected = true
		}
		options.Logf("[OpenAI] Images stream client disconnected, continue draining upstream for billing")
		return false
	}
	if lastWriteAt != nil {
		*lastWriteAt = time.Now()
	}
	return true
}

// ReadImagesOAuthNonStreaming converts OAuth Responses image events to an
// Images JSON response and returns their usage, count, and known sizes. Supply
// callbacks used for body reads, error classification, diagnostics, and output;
// the executor closes the response body.
//
// Completed results and completed-item copies are reconciled by the codec
// before output. If no image remains, structured errors precede model-text
// classification and the caller's empty-output policy. Retryable errors remain
// available to the executor before error presentation; all error returns from
// this reader discard the parsed usage. A blank result model receives
// fallbackModel before response construction.
func ReadImagesOAuthNonStreaming(
	resp *http.Response,
	sink upstream.OutputSink, options ImageResponseOptions,
	responseFormat string,
	fallbackModel string,
) (wire.ForwardUsage, int, []string, error) {
	body, err := options.ReadBody(resp.Body)
	if err != nil {
		err = options.ClassifyReadError(err)
		return wire.ForwardUsage{}, 0, nil, err
	}

	var usage wire.ForwardUsage
	wire.ForEachSSEDataPayload(string(body), func(data []byte) {
		ParseOpenAIImagesSSEUsageBytes(data, &usage)
	})
	results, createdAt, usageRaw, firstMeta, _, err := CollectOpenAIImagesFromResponsesBody(body, time.Now)
	if err != nil {
		return wire.ForwardUsage{}, 0, nil, err
	}
	if len(results) == 0 {
		if upstreamErr := ExtractOpenAIImagesUpstreamError(body); upstreamErr != nil {
			options.ObserveError(upstreamErr.ClientStatusCode(), upstreamErr.ClientMessage(), "")
			if !IsOpenAIImagesRetryableUpstreamError(upstreamErr) {
				options.WriteHTTPError(upstreamErr)
			}
			return wire.ForwardUsage{}, 0, nil, upstreamErr
		}
		if textFallbackErr := OpenAIImagesTextFallbackError(body); textFallbackErr != nil {
			options.ObserveError(textFallbackErr.ClientStatusCode(), textFallbackErr.ClientMessage(), options.Summary(body))
			if !IsOpenAIImagesRetryableUpstreamError(textFallbackErr) {
				options.WriteHTTPError(textFallbackErr)
			}
			return wire.ForwardUsage{}, 0, nil, textFallbackErr
		}
		// Structured failure and model text have already been ruled out; delegate
		// this empty result to the executor-specific retry policy.
		options.ObserveError(http.StatusBadGateway, "upstream did not return image output", options.Summary(body))
		return wire.ForwardUsage{}, 0, nil, options.EmptyOutput(body)
	}
	if strings.TrimSpace(firstMeta.Model) == "" {
		firstMeta.Model = strings.TrimSpace(fallbackModel)
	}

	responseBody, err := BuildOpenAIImagesAPIResponse(results, createdAt, usageRaw, firstMeta, responseFormat, time.Now)
	if err != nil {
		return wire.ForwardUsage{}, 0, nil, err
	}
	responseBody, usage.ImageFrameCount, err = ValidateGIFImagesResponse(responseBody, options.ExpectedGIF, options.FetchImage)
	if err != nil {
		return usage, len(results), OpenAIResponsesImageResultSizes(results), err
	}
	c := upstream.NewOutputContext(sink)
	options.ResponseHeaders(c.Writer.Header(), resp.Header)
	c.Data(resp.StatusCode, "application/json; charset=utf-8", responseBody)
	return usage, len(results), OpenAIResponsesImageResultSizes(results), nil
}

// ReadImagesOAuthStreaming converts Responses events to Images SSE while
// retaining usage, completed-result identity, and image dimensions. Supply a
// flushing writer and callbacks for headers, errors, writer sizes, timing, and
// logging. The executor closes resp.Body when this reader returns.
//
// Partial images are delivered immediately; completed items wait for terminal
// reconciliation or EOF. Result keys are registered before attempted client
// writes, so counts include observed completions after disconnection.
func ReadImagesOAuthStreaming(
	resp *http.Response,
	c *upstream.OutputContext, options ImageResponseOptions,
	startTime time.Time,
	responseFormat string,
	streamPrefix string,
	fallbackModel string,
) (wire.ForwardUsage, int, []string, error) {
	options.ResponseHeaders(c.Writer.Header(), resp.Header)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(resp.StatusCode)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return wire.ForwardUsage{}, 0, nil, fmt.Errorf("streaming is not supported by response writer")
	}

	format := strings.ToLower(strings.TrimSpace(responseFormat))
	if format == "" {
		format = "b64_json"
	}

	usage := wire.ForwardUsage{}
	imageCount := 0
	var imageOutputSizes []string

	emitted := make(map[string]struct{})
	pendingResults := make([]OpenAIResponsesImageResult, 0, 1)
	pendingSeen := make(map[string]struct{})
	streamMeta := OpenAIResponsesImageResult{Model: strings.TrimSpace(fallbackModel)}
	var fallbackText strings.Builder
	appendFallbackText := func(text string) {
		// Limit retained model text to six hundred bytes for no-image classification;
		// this byte cap can split UTF-8 text.
		if text == "" || fallbackText.Len() >= 600 {
			return
		}
		remaining := 600 - fallbackText.Len()
		if len(text) > remaining {
			text = text[:remaining]
		}
		_, _ = fallbackText.WriteString(text)
	}
	var createdAt int64
	clientDisconnected := false
	lastDownstreamWriteAt := time.Now()
	var sseData wire.SSEDataAccumulator
	var processDataErr error
	processDataDone := false
	// Compare the same heartbeat-adjusted measure before and after reading, so
	// comments from either the header wait or body wait cannot block safe recovery.
	writerSizeBeforeResponse := options.AdjustedWrittenSize()

	processData := func(dataBytes []byte) {
		if processDataDone || processDataErr != nil {
			return
		}
		// Record data arrival before validating JSON; lifecycle and malformed
		// payloads therefore contribute to this reader-specific latency measurement.

		ParseOpenAIImagesSSEUsageBytes(dataBytes, &usage)
		if !gjson.ValidBytes(dataBytes) {
			return
		}
		if meta, eventCreatedAt, ok := ExtractOpenAIResponsesImageMetaFromLifecycleEvent(dataBytes); ok {
			MergeOpenAIResponsesImageMeta(&streamMeta, meta)
			if eventCreatedAt > 0 {
				createdAt = eventCreatedAt
			}
		}
		if gjson.GetBytes(dataBytes, "type").String() == "response.output_text.delta" {
			appendFallbackText(gjson.GetBytes(dataBytes, "delta").String())
		}
		switch gjson.GetBytes(dataBytes, "type").String() {
		case "response.image_generation_call.partial_image":
			b64 := strings.TrimSpace(gjson.GetBytes(dataBytes, "partial_image_b64").String())
			if b64 == "" {
				return
			}
			eventName := streamPrefix + ".partial_image"
			partialMeta := streamMeta
			MergeOpenAIResponsesImageMeta(&partialMeta, OpenAIResponsesImageResult{
				OutputFormat: strings.TrimSpace(gjson.GetBytes(dataBytes, "output_format").String()),
				Background:   strings.TrimSpace(gjson.GetBytes(dataBytes, "background").String()),
			})
			payload := BuildOpenAIImagesStreamPartialPayload(
				eventName,
				b64,
				gjson.GetBytes(dataBytes, "partial_image_index").Int(),
				format,
				createdAt,
				partialMeta, time.Now)
			TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, eventName, payload)
		case "response.output_item.done":
			// Retain completed items until final metadata is known, deduplicating their
			// terminal copies by encoded result and output format.
			img, itemID, ok, extractErr := ExtractOpenAIImageFromResponsesOutputItemDone(dataBytes)
			if extractErr != nil {
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(extractErr.Error()))
				processDataErr = extractErr
				processDataDone = true
				return
			}
			if !ok {
				return
			}
			MergeOpenAIResponsesImageMeta(&streamMeta, img)
			MergeOpenAIResponsesImageMeta(&img, streamMeta)
			key := OpenAIResponsesImageResultKey(itemID, img)
			if _, exists := emitted[key]; exists {
				return
			}
			if _, exists := pendingSeen[key]; exists {
				return
			}
			pendingSeen[key] = struct{}{}
			pendingResults = append(pendingResults, img)
		case "response.completed":
			results, _, usageRaw, firstMeta, extractErr := ExtractOpenAIImagesFromResponsesCompleted(dataBytes, time.Now)
			if extractErr != nil {
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(extractErr.Error()))
				processDataErr = extractErr
				processDataDone = true
				return
			}
			MergeOpenAIResponsesImageMeta(&streamMeta, firstMeta)
			// Merge terminal and completed-item observations before dimension
			// reconciliation, so copies of one encoded image produce one completed
			// event.
			finalResults := make([]OpenAIResponsesImageResult, 0, len(results)+len(pendingResults))
			finalSeen := make(map[string]struct{})
			for _, img := range results {
				MergeOpenAIResponsesImageMeta(&img, streamMeta)
				AppendOpenAIResponsesImageResultDedup(&finalResults, finalSeen, "", img)
			}
			for _, img := range pendingResults {
				MergeOpenAIResponsesImageMeta(&img, streamMeta)
				AppendOpenAIResponsesImageResultDedup(&finalResults, finalSeen, "", img)
			}
			ReconcileOpenAIResponsesImageResultSizes(finalResults, nil)
			if len(finalResults) == 0 {
				textFallbackErr := OpenAIImagesTextFallbackErrorForText(fallbackText.String())
				if textFallbackErr == nil {
					textFallbackErr = OpenAIImagesTextFallbackError(dataBytes)
				}
				if textFallbackErr != nil {
					retryable := IsOpenAIImagesRetryableUpstreamError(textFallbackErr)
					options.ObserveError(textFallbackErr.ClientStatusCode(), textFallbackErr.ClientMessage(), SummarizeOpenAIImagesNoOutputBody(dataBytes))
					if !retryable && !clientDisconnected {
						TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBodyFromUpstream(textFallbackErr))
					}
					processDataErr = textFallbackErr
					processDataDone = true
					return
				}
				outputErr := fmt.Errorf("upstream did not return image output")
				// The terminal contains no recognized image or classifiable model text;
				// record the empty-generation outcome before writing its client error.
				options.ObserveError(http.StatusBadGateway, "upstream did not return image output", options.Summary(dataBytes))
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(outputErr.Error()))
				processDataErr = outputErr
				processDataDone = true
				return
			}
			eventName := streamPrefix + ".completed"
			for _, img := range finalResults {
				key := OpenAIResponsesImageResultKey("", img)
				if _, exists := emitted[key]; exists {
					continue
				}
				payload := BuildOpenAIImagesStreamCompletedPayload(eventName, img, format, createdAt, usageRaw, time.Now)
				// Register completion before delivery so a failed sink write retains
				// observed image accounting and prevents duplicate event attempts.
				emitted[key] = struct{}{}
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, eventName, payload)
			}
			imageCount = len(emitted)
			imageOutputSizes = OpenAIResponsesImageResultSizes(finalResults)
			processDataDone = true
		case "error", "response.failed":
			if upstreamErr := OpenAIImagesUpstreamErrorFromSSEPayload(dataBytes); upstreamErr != nil {
				retryable := IsOpenAIImagesRetryableUpstreamError(upstreamErr)
				// A retryable failure before recorded writes remains for executor recovery;
				// after writes, present it through the Images error event.
				if !clientDisconnected && (!retryable || options.AdjustedWrittenSize() != writerSizeBeforeResponse) {
					TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBodyFromUpstream(upstreamErr))
				}
				options.ObserveError(upstreamErr.ClientStatusCode(), upstreamErr.ClientMessage(), "")
				processDataErr = upstreamErr
				processDataDone = true
				return
			}
		}
	}

	processLine := func(line []byte) (bool, error) {
		if len(line) == 0 {
			return false, nil
		}
		sseData.AddLine(string(line), processData)
		if processDataErr != nil {
			return true, processDataErr
		}
		return processDataDone, nil
	}

	flushData := func() (bool, error) {
		sseData.Flush(processData)
		if processDataErr != nil {
			return true, processDataErr
		}
		return processDataDone, nil
	}

	finalizePending := func() error {
		if imageCount > 0 {
			return nil
		}
		// EOF can end after completed items but before response.completed; use those
		// retained results with accumulated metadata as final image events.
		if len(pendingResults) > 0 {
			eventName := streamPrefix + ".completed"
			finalResults := append([]OpenAIResponsesImageResult(nil), pendingResults...)
			for i := range finalResults {
				MergeOpenAIResponsesImageMeta(&finalResults[i], streamMeta)
			}
			ReconcileOpenAIResponsesImageResultSizes(finalResults, nil)
			for _, img := range finalResults {
				key := OpenAIResponsesImageResultKey("", img)
				if _, exists := emitted[key]; exists {
					continue
				}
				payload := BuildOpenAIImagesStreamCompletedPayload(eventName, img, format, createdAt, nil, time.Now)
				// Register completion before delivery so a failed sink write retains
				// observed image accounting and prevents duplicate event attempts.
				emitted[key] = struct{}{}
				TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, eventName, payload)
			}
			imageCount = len(emitted)
			imageOutputSizes = OpenAIResponsesImageResultSizes(finalResults)
			return nil
		}

		streamErr := fmt.Errorf("stream disconnected before image generation completed")
		TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(streamErr.Error()))
		return streamErr
	}

	streamInterval := options.StreamInterval()
	keepaliveInterval := options.KeepaliveInterval()
	if streamInterval <= 0 && keepaliveInterval <= 0 {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			done, processErr := processLine(line)
			if processErr != nil {
				return usage, imageCount, imageOutputSizes, processErr
			}
			if done {
				return usage, imageCount, imageOutputSizes, nil
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				if done, processErr := flushData(); processErr != nil {
					return usage, imageCount, imageOutputSizes, processErr
				} else if done {
					return usage, imageCount, imageOutputSizes, nil
				}
				err = options.ClassifyReadError(err)
				return usage, imageCount, imageOutputSizes, err
			}
		}
		if done, processErr := flushData(); processErr != nil {
			return usage, imageCount, imageOutputSizes, processErr
		} else if done {
			return usage, imageCount, imageOutputSizes, nil
		}
		if err := finalizePending(); err != nil {
			return usage, imageCount, imageOutputSizes, err
		}
		return usage, imageCount, imageOutputSizes, nil
	}

	// readEvent transports one read line or failure into the timed image consumer.
	type readEvent struct {
		line []byte // upstream read line
		err  error  // upstream read failure
	}
	// Bound read-ahead while image conversion writes output; done releases
	// producer sends after terminal handling or another consumer exit.
	events := make(chan readEvent, 16)
	done := make(chan struct{})
	sendEvent := func(ev readEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}
	var lastReadAt int64
	atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
	// Timestamp actual reads before queueing, preventing client conversion time
	// from causing a false upstream-idle decision in the consumer.
	go func() {
		defer close(events)
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
			}
			if len(line) > 0 && !sendEvent(readEvent{line: line}) {
				return
			}
			if err == io.EOF {
				return
			}
			if err != nil {
				_ = sendEvent(readEvent{err: err})
				return
			}
		}
	}()
	// Release sends on every return; a producer blocked inside ReadBytes still
	// depends on the executor closing the response body.
	defer close(done)

	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}

	var keepaliveTicker *time.Ticker
	if keepaliveInterval > 0 {
		keepaliveTicker = time.NewTicker(keepaliveInterval)
		defer keepaliveTicker.Stop()
	}
	var keepaliveCh <-chan time.Time
	if keepaliveTicker != nil {
		keepaliveCh = keepaliveTicker.C
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if done, processErr := flushData(); processErr != nil {
					return usage, imageCount, imageOutputSizes, processErr
				} else if done {
					return usage, imageCount, imageOutputSizes, nil
				}
				if err := finalizePending(); err != nil {
					return usage, imageCount, imageOutputSizes, err
				}
				return usage, imageCount, imageOutputSizes, nil
			}
			if ev.err != nil {
				if done, processErr := flushData(); processErr != nil {
					return usage, imageCount, imageOutputSizes, processErr
				} else if done {
					return usage, imageCount, imageOutputSizes, nil
				}
				ev.err = options.ClassifyReadError(ev.err)
				return usage, imageCount, imageOutputSizes, ev.err
			}
			done, processErr := processLine(ev.line)
			if processErr != nil {
				return usage, imageCount, imageOutputSizes, processErr
			}
			if done {
				return usage, imageCount, imageOutputSizes, nil
			}
		case <-intervalCh:
			// Timer delivery can lag behind processing, so consult the atomic read
			// timestamp before declaring this upstream image generation idle.
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return usage, imageCount, imageOutputSizes, fmt.Errorf("image stream incomplete after timeout")
			}
			options.Logf("[OpenAI] Images responses stream data interval timeout: interval=%s", streamInterval)
			TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, &lastDownstreamWriteAt, "error", BuildOpenAIImagesStreamErrorBody(fmt.Sprintf("upstream image stream idle for %s", streamInterval)))
			return usage, imageCount, imageOutputSizes, fmt.Errorf("image stream data interval timeout")
		case <-keepaliveCh:
			// Heartbeats follow successful downstream delivery, and a write failure
			// suppresses further client writes while observations remain available.
			if clientDisconnected || time.Since(lastDownstreamWriteAt) < keepaliveInterval {
				continue
			}
			n, writeErr := io.WriteString(c.Writer, ":\n\n")
			if options.RecordKeepalive != nil {
				options.RecordKeepalive(n)
			}
			if writeErr != nil {
				clientDisconnected = true
				options.Logf("[OpenAI] Images responses stream client disconnected during keepalive, continue draining upstream for billing")
				continue
			}
			flusher.Flush()
			lastDownstreamWriteAt = time.Now()
		}
	}
}
