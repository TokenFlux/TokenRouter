package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// response_messages.go converts Responses output into Messages responses and
// streams while retaining terminal usage and response identity.
// Responses event conversion to Messages emits content-block and message
// terminal events while tracking upstream usage separately. A terminal's
// nested usage overrides supplied top-level categories in this reader, and its
// Response ID is retained for result adaptation. Buffered terminal output can
// be supplemented from earlier deltas before Messages JSON is constructed.
//
// Messages output begins immediately when the bridge produces an event; this
// path has no Chat silent-refusal holding queue. A failed client write
// suppresses delivery while later events can still update accounting until a
// terminal or another exit. An Anthropic ping marks local client output as
// started, and cyber rejection also latches the disconnect flag to stop
// successful finalization. Missing-terminal policy differs before and after
// output, while the response owner closes the body to release any pending
// asynchronous scan.

// MessagesResponseOptions binds Messages-specific failure and usage policy
// around shared compatibility reader callbacks.
type MessagesResponseOptions struct {
	// ChatResponseOptions supplies shared reading and observation callbacks;
	// Messages bindings replace error envelope functions and shadow the base
	// MissingUsage callback below.
	ChatResponseOptions
	// MessagesFailure classifies error/failed data using client-output and
	// bare-error flags; its Failover result takes priority over selected client
	// status/type/message.
	MessagesFailure func([]byte, string, bool, bool) ChatFailure
	// MissingUsage observes completed buffered terminals and finalized streams; it
	// receives mutable usage plus terminal/disconnect evidence and returns no
	// rejection error.
	MissingUsage func(*wire.ForwardUsage, string, bool)
	// MissingTerminal supplies failure classification when the upstream ends
	// without a terminal before local output; the returned observed-usage result
	// remains available.
	MissingTerminal func(string) error
	// RecordMissingTerminal records an incomplete stream after client output has
	// started; the reader then returns its own missing-terminal error with observed
	// usage.
	RecordMissingTerminal func(string)
}

// ReadMessagesBuffered converts a buffered Responses terminal to Messages JSON
// after model/tier observation and failed-terminal policy. Supply the shared
// read/output callbacks and Messages usage observer; the executor owns
// response-body closure. CompatBufferedReadError is unwrapped directly instead
// of using BufferedReadFailure.
//
// Completed terminals notify MissingUsage before delta supplementation and
// conversion. The reconstructed output retains terminal metadata, and the
// result includes its ResponseID alongside the HTTP request ID. Optional search
// observation uses terminal JSON when serialization succeeds. JSON delivery
// occurs with HTTP 200 and Messages content type after approved upstream
// headers have been copied.
func ReadMessagesBuffered(resp *http.Response, c *upstream.OutputContext, options MessagesResponseOptions, originalModel, upstreamModel string, startTime time.Time) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")

	finalResponse, usage, acc, err := options.ReadBuffered()
	if err != nil {
		var readErr *CompatBufferedReadError
		if errors.As(err, &readErr) && readErr != nil {
			return nil, readErr.Unwrap()
		}
		return nil, err
	}

	if finalResponse == nil {
		options.WriteError(http.StatusBadGateway, "api_error", "Upstream stream ended without a terminal response event")
		return nil, fmt.Errorf("upstream stream ended without terminal event")
	}
	options.ObserveFinal(finalResponse)
	if strings.TrimSpace(finalResponse.Status) == "failed" {
		return nil, options.BufferedFailure(finalResponse, usage)
	}

	if strings.TrimSpace(finalResponse.Status) == "completed" {
		options.MissingUsage(&usage, "response.completed", false)
	}

	// Supply earlier delta content when terminal output is empty before
	// conversion, keeping terminal identity and usage separate from reconstructed
	// Messages content.
	acc.SupplementResponseOutput(finalResponse)

	anthropicResp := bridge.ResponsesToAnthropic(finalResponse, originalModel)

	options.Headers(c.Writer.Header(), resp.Header)
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusOK, anthropicResp)

	result := &CompatResponseResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		ResponseID:      finalResponse.ID,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   upstreamModel,
		ServiceTier:     options.ServiceTier(),
		Stream:          false,
		Duration:        time.Since(startTime),
	}
	// Optional search accounting uses terminal JSON after client conversion;
	// a serialization failure leaves the count zero while the response remains delivered.
	if options.CountSearch && finalResponse != nil {
		if body, err := json.Marshal(finalResponse); err == nil {
			if n := options.JSONSearch(body); n > 0 {
				result.SearchCount = n
			}
		}
	}
	return result, nil
}

// ReadMessagesStreaming converts Responses SSE to Messages events and returns
// observed usage on terminal failures, read errors, or timeout. Supply the
// shared stream callbacks and Messages-specific error/usage policy. The
// executor retains body ownership; timed producer sends stop on return while a
// blocked scan depends on body closure.
//
// Nested terminal usage overrides supplied top-level categories, and terminal IDs
// populate ResponseID. Output events and pings mark local output,
// while cyber policy latches ClientDisconnect even after successful error
// delivery. Missing terminals are classified through separate pre-output and
// post-output callbacks. Terminals without a selected error outcome, including
// incomplete/cancelled types, run the bridge's closing events.
func ReadMessagesStreaming(resp *http.Response, c *upstream.OutputContext, options MessagesResponseOptions, originalModel, upstreamModel string, startTime time.Time) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := NewCompatStreamHeaderWriter(c, resp.Header, options.Headers)

	state := bridge.NewResponsesEventToAnthropicState(options.Runtime)
	state.Model = originalModel
	var usage wire.ForwardUsage
	var usageAccumulator wire.UsageAccumulator
	responseID := ""

	clientDisconnected := false
	clientOutputStarted := false
	var cyberPolicyErr error
	var streamFailoverErr error
	var streamNonFailoverErr error
	terminalEventType := ""
	searchCount := 0
	streamSearchSeen := make(map[string]struct{})
	countSearch := options.CountSearch

	scanner := options.Scanner(resp.Body)

	streamInterval := options.StreamInterval()
	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}

	// Retain accounting and turn identity on failure returns as well as
	// success; ClientDisconnect also carries the policy-forced halt set by cyber rejection.
	resultWithUsage := func() *CompatResponseResult {
		out := &CompatResponseResult{
			RequestID:        requestID,
			UpstreamHeaders:  resp.Header,
			ResponseID:       responseID,
			Usage:            usage,
			Model:            originalModel,
			UpstreamModel:    upstreamModel,
			ServiceTier:      options.ServiceTier(),
			Stream:           true,
			Duration:         time.Since(startTime),
			ClientDisconnect: clientDisconnected,
		}
		if searchCount > 0 {
			out.SearchCount = searchCount
		}
		return out
	}

	// Restore tool names before search counting and typed observation so those
	// consumers inspect the same payload that supplies downstream protocol conversion.
	processDataLine := func(payload string) bool {
		payload = string(options.RestoreToolNames([]byte(payload)))
		if countSearch {
			searchCount += options.StreamSearch([]byte(payload), streamSearchSeen)
		}

		var event wire.ResponsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("openai messages stream: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
			return false
		}
		options.Observe([]byte(payload), event.Type)
		wire.ParseSSEUsageBytesWithType([]byte(payload), event.Type, &usage)
		// Share accumulated cache observations with the event converter before it
		// creates terminal Messages usage, including sparse or cache-only updates.
		if snapshot := usageAccumulator.ObserveResponsesEvent([]byte(payload), &event); snapshot != nil {
			usage = usageAccumulator.ForwardUsage()
		}

		eventType := strings.TrimSpace(event.Type)
		isBareErrorEvent := eventType == "error"
		isTerminalEvent := IsCompatResponsesTerminalEvent(eventType) || isBareErrorEvent
		// Both event locations already contain the same accumulated snapshot.
		// Retain terminal identity and explicit zero totals without selecting another usage source.
		if isTerminalEvent {
			terminalEventType = eventType
			if event.Response != nil {
				if id := strings.TrimSpace(event.Response.ID); id != "" {
					responseID = id
				}
			}
		}
		if eventType == "response.failed" || isBareErrorEvent {
			payloadBytes := []byte(payload)
			// Record cyber policy before client presentation, then latch output halt
			// even after a successful error write so ordinary message closure cannot follow rejection.
			if hit, code, msg := DetectOpenAICyberPolicy(payloadBytes); hit {
				// Capture up to four KiB of policy evidence with observed usage before
				// error delivery, so its outcome cannot remove the recorded upstream rejection.
				options.MarkCyber(CyberObservation{
					Code:           code,
					Message:        msg,
					Body:           options.Truncate(payload, 4096),
					UpstreamStatus: http.StatusOK,
					UpstreamInTok:  usage.InputTokens,
					UpstreamOutTok: usage.OutputTokens,
				})
				if !clientDisconnected {
					writeStreamHeaders()
					clientMsg := msg
					if clientMsg == "" {
						clientMsg = "Request blocked by upstream cyber-security policy"
					}
					if _, err := fmt.Fprint(c.Writer, options.BuildStreamError("invalid_request_error", clientMsg)); err == nil {
						c.Writer.Flush()
					}
					clientDisconnected = true
				}
				cyberPolicyErr = options.CyberForwarded
				return true
			}
			message := ExtractOpenAISSEErrorMessage(payloadBytes)
			failure := options.MessagesFailure(payloadBytes, message, clientOutputStarted, isBareErrorEvent)
			if failure.Failover != nil {
				streamFailoverErr = failure.Failover
				return true
			}
			errStatus, errType, errMsg := failure.Status, failure.Type, failure.Message

			if !clientDisconnected {
				if !clientOutputStarted {
					options.WriteError(errStatus, errType, errMsg)
					clientOutputStarted = true
				} else {
					writeStreamHeaders()
					if _, err := fmt.Fprint(c.Writer, options.BuildStreamError(errType, errMsg)); err == nil {
						c.Writer.Flush()
					}
				}
			}
			streamNonFailoverErr = fmt.Errorf("upstream response failed: %s", errMsg)
			return true
		}

		// Conversion state advances independently of client delivery; suppress
		// writes after disconnection while still observing subsequent terminal usage and failures.
		events := bridge.ResponsesEventToAnthropicEvents(&event, state)
		if !clientDisconnected {
			for _, evt := range events {
				// Skip an event whose encoding fails after conversion state advances;
				// later events and accounting remain active for this same upstream attempt.
				sse, err := bridge.ResponsesAnthropicEventToSSE(evt)
				if err != nil {
					logger.L().Warn("openai messages stream: failed to marshal event",
						zap.Error(err),
						zap.String("request_id", requestID),
					)
					continue
				}
				writeStreamHeaders()
				if _, err := fmt.Fprint(c.Writer, sse); err != nil {
					clientDisconnected = true
					logger.L().Info("openai messages stream: client disconnected, continuing to drain upstream for billing",
						zap.String("request_id", requestID),
					)
					break
				}
				clientOutputStarted = true
			}
		}
		if len(events) > 0 && !clientDisconnected {
			c.Writer.Flush()
		}
		return isTerminalEvent
	}

	// Selected error outcomes return accounting first; other recognized terminals
	// emit remaining block/message closure events and invoke the usage observer,
	// including incomplete and cancelled terminal types.
	finalizeStream := func() (*CompatResponseResult, error) {
		if cyberPolicyErr != nil {
			return resultWithUsage(), cyberPolicyErr
		}
		if streamFailoverErr != nil {
			return resultWithUsage(), streamFailoverErr
		}
		if streamNonFailoverErr != nil {
			return resultWithUsage(), streamNonFailoverErr
		}
		finalEvents := bridge.FinalizeResponsesAnthropicStream(state)
		if len(finalEvents) > 0 && !clientDisconnected {
			for _, evt := range finalEvents {
				// Skip an event whose encoding fails after conversion state advances;
				// later events and accounting remain active for this same upstream attempt.
				sse, err := bridge.ResponsesAnthropicEventToSSE(evt)
				if err != nil {
					continue
				}
				writeStreamHeaders()
				if _, err := fmt.Fprint(c.Writer, sse); err != nil {
					clientDisconnected = true
					logger.L().Info("openai messages stream: client disconnected during final flush",
						zap.String("request_id", requestID),
					)
					break
				}
				clientOutputStarted = true
			}
			if !clientDisconnected {
				c.Writer.Flush()
			}
		}
		options.MissingUsage(&usage, terminalEventType, clientDisconnected)
		return resultWithUsage(), nil
	}

	// Cancellation and deadline errors still return incomplete usage, while
	// diagnostic logging is reserved for other scanner failures to avoid
	// expected-cancellation noise.
	handleScanErr := func(err error) {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai messages stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	missingTerminalErr := func() (*CompatResponseResult, error) {
		result := resultWithUsage()
		if clientDisconnected {
			return result, fmt.Errorf("stream usage incomplete: missing terminal event")
		}
		message := "OpenAI messages stream ended before a terminal event"
		if !clientOutputStarted {
			return result, options.MissingTerminal(message)
		}
		options.RecordMissingTerminal(message)
		return result, fmt.Errorf("stream usage incomplete: missing terminal event")
	}
	processFrame := func(frame wire.OpenAICompatSSEFrame) bool {
		payload := wire.OpenAICompatPayloadWithEventType(frame.Data, frame.EventType)
		return processDataLine(payload)
	}

	// Snapshot heartbeat spacing for this attempt; client pings use consumed
	// upstream line time while idle failure checks use the producer's arrival timestamp.

	// With both timer policies disabled, synchronous scanning retains the
	// same parser and terminal requirement while avoiding queued read-ahead and its producer.
	if streamInterval <= 0 {
		var parser wire.OpenAICompatSSEFrameParser
		for scanner.Scan() {
			line := scanner.Text()
			if IsCompatDoneSentinelLine(line) {
				return missingTerminalErr()
			}
			frame, ok := parser.AddLine(line)
			if !ok {
				continue
			}
			if processFrame(frame) {
				return finalizeStream()
			}
		}
		if err := scanner.Err(); err != nil {
			handleScanErr(err)
			return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", err)
		}
		if frame, ok := parser.Finish(); ok {
			if strings.TrimSpace(frame.Data) == "[DONE]" {
				return missingTerminalErr()
			}
			if processFrame(frame) {
				return finalizeStream()
			}
		}
		return missingTerminalErr()
	}

	// scanEvent carries one scanner line or failure to the timed consumer.
	// The producer supplies one of line or err per event and closes the channel.
	type scanEvent struct {
		line string // upstream scanner line
		err  error  // upstream scanner failure
	}
	// Bound pending scan lines while conversion writes client events; done
	// releases producer sends once a terminal, failure, or timeout ends the consumer.
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	var lastReadAt int64
	atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
	sendEvent := func(ev scanEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}
	// Record actual line arrival before enqueueing, keeping read-idle policy
	// separate from time spent converting events or writing to the client sink.
	go func() {
		defer close(events)
		for scanner.Scan() {
			atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}()
	// Release producer sends on every exit; the executor still closes Body
	// to interrupt a scanner read blocked after this consumer has returned.
	defer close(done)

	var parser wire.OpenAICompatSSEFrameParser

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// Flush a final unterminated SSE frame at EOF before declaring a
				// missing terminal, so buffered event data can still establish its actual outcome.
				if frame, ok := parser.Finish(); ok {
					if strings.TrimSpace(frame.Data) == "[DONE]" {
						return missingTerminalErr()
					}
					if processFrame(frame) {
						return finalizeStream()
					}
				}
				return missingTerminalErr()
			}
			if ev.err != nil {
				handleScanErr(ev.err)
				return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", ev.err)
			}

			line := ev.line
			if IsCompatDoneSentinelLine(line) {
				return missingTerminalErr()
			}
			frame, ok := parser.AddLine(line)
			if !ok {
				continue
			}
			if processFrame(frame) {
				return finalizeStream()
			}

		case <-intervalCh:
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return resultWithUsage(), fmt.Errorf("stream usage incomplete after timeout")
			}
			logger.L().Warn("openai messages stream: data interval timeout",
				zap.String("request_id", requestID),
				zap.String("model", originalModel),
				zap.Duration("interval", streamInterval),
			)
			return resultWithUsage(), fmt.Errorf("stream data interval timeout")
		}
	}
}
