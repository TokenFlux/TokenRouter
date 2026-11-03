package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// response_chat.go converts Responses output to Chat Completions while
// controlling deferred delivery and terminal failure handling.
// Responses-to-Chat conversion can observe protocol progress before producing a
// client chunk. Large-request silent-refusal detection retains converted chunks
// until content evidence permits release, keeping a refusal eligible for
// recovery before semantic client output. Terminal response usage overrides
// top-level event usage, and tool names are restored before typed decoding and
// observation.
//
// Header creation is deferred until actual conversion output, an error, or an
// allowed heartbeat needs it. HTTP header commitment and the local
// clientOutputStarted flag can therefore differ. A failed sink write stops
// delivery while upstream events continue through accounting until a terminal
// or another exit. Timed reading uses a bounded queue and an upstream arrival
// clock; returning releases sends, while the response owner closes the body to
// unblock a pending scan.

// CompatResponseResult carries reader-specific usage and upstream/request
// metadata for inbound result adaptation.
type CompatResponseResult struct {
	// ReasoningEffort is the optional request reasoning level. ResolvedTier is the
	// optional request tier. Both can share caller-owned string storage; readers
	// lacking these annotations leave them nil, and consumers keep pointed-to values stable.
	ReasoningEffort, ResolvedTier *string
	// ResponseID is a Responses turn ID when the selected reader records it;
	// readers omitting turn identity leave this field empty.
	ResponseID string
	// ClientDisconnect is populated by Messages stream conversions; the Responses
	// Messages reader also sets it for policy-forced output halt, while Chat
	// readers leave it zero.
	ClientDisconnect bool
	// RequestID is the upstream HTTP request identity. Model is the client model.
	// UpstreamModel is the selected upstream request model. ServiceTier is the
	// observed upstream response tier. These describe the same attempt independently
	// of ResponseID and the inbound handler's separately selected billing model.
	RequestID, Model, UpstreamModel, ServiceTier string
	// UpstreamHeaders shares the response header map with the executor; callers
	// clone it before edits that would alter another consumer's observation.
	UpstreamHeaders http.Header
	Usage           wire.ForwardUsage // observed upstream billing usage
	Stream          bool              // client streaming response mode
	Duration        time.Duration     // total response handling duration

	SearchCount int // observed native search calls
}

// ChatFailure selects pre-output recovery or client error presentation for
// compatibility response readers.
type ChatFailure struct {
	// Failover takes precedence over client error fields when nonnil; readers
	// return it through their output-state and observed-usage handling.
	Failover error
	// Status supplies the HTTP status passed to WriteError when Failover is nil and
	// the client error needs presentation before output.
	Status int
	// Type is the client error category. Message is the client error description.
	// They accompany Status only when Failover is nil; the policy callback supplies
	// their protocol spelling and redaction before a reader invokes client output.
	Type, Message string
}

// ChatResponseOptions binds protocol conversion to one attempt's reading,
// observations, and output policy.
type ChatResponseOptions struct {
	Runtime bridge.Runtime // conversion clock and random source
	// RequestContext supplies the Chat scanner-error logger context; network
	// cancellation depends on the response body and its caller-owned request
	// lifecycle.
	RequestContext context.Context
	// ReadBuffered returns this attempt's terminal, usage, and accumulator; gateway
	// bindings capture the same response whose headers the reader reports.
	ReadBuffered func() (*wire.ResponsesResponse, wire.ForwardUsage, *bridge.BufferedResponseAccumulator, error)
	// BufferedReadFailure translates Chat buffered read errors; the Messages
	// buffered reader instead unwraps CompatBufferedReadError directly before
	// returning its cause.
	BufferedReadFailure func(error) error
	// BufferedFailure handles failed buffered terminals before JSON conversion,
	// using the terminal and usage observed by the caller's buffered reader.
	BufferedFailure func(*wire.ResponsesResponse, wire.ForwardUsage) error
	// ObserveFinal runs before failed-terminal and usage policy; gateway binding
	// records response model/tier observations even when conversion subsequently
	// returns an error.
	ObserveFinal func(*wire.ResponsesResponse)
	// MissingUsage can reject a Chat buffered terminal before output;
	// MessagesResponseOptions shadows this field with a separate void observation
	// callback.
	MissingUsage func(*wire.ResponsesResponse, wire.ForwardUsage) error
	// Headers copies approved upstream headers for buffered output or the first
	// stream-header invocation; the readers then select their client content type.
	Headers func(http.Header, http.Header)
	// WriteError presents selected status/type/message before client content
	// starts; readers treat its invocation as output and do not inspect a write
	// result.
	WriteError func(int, string, string)
	// ServiceTier supplies observed upstream tier when building a result; the
	// gateway getter reads request-local response observations rather than the
	// selected model name.
	ServiceTier func() string
	// CountSearch enables JSONSearch and StreamSearch observation; when false,
	// those optional callbacks are skipped and SearchCount remains its default
	// value.
	CountSearch bool
	// JSONSearch observes a successfully serialized buffered terminal when
	// CountSearch is true; only a positive returned count populates the result.
	JSONSearch func([]byte) int
	// StreamSearch receives each restored data payload before typed decoding plus
	// an attempt-local seen map, allowing search observations to deduplicate
	// repeated events.
	StreamSearch func([]byte, map[string]struct{}) int
	// Scanner builds the upstream line scanner under caller limits; the executor
	// retains response-body ownership after synchronous or timed reading exits.
	Scanner func(io.Reader) *bufio.Scanner
	// StreamInterval supplies the upstream read-idle budget at stream entry.
	// Positive values enable its timer; the producer arrival clock remains
	// independent of downstream conversion and HTTP heartbeat activity.
	StreamInterval func() time.Duration
	// RestoreToolNames transforms each stream payload before search counting,
	// typed decoding, usage observation, and protocol conversion. The transformed
	// bytes therefore supply both accounting inspection and client output.
	RestoreToolNames func([]byte) []byte
	// Observe receives restored payloads after successful typed decoding, before
	// terminal usage and error policy; gateway binding updates service-tier
	// observations.
	Observe func([]byte, string)
	// CyberForwarded is the required nonnil policy sentinel after a cyber
	// rejection; finalization uses it to stop ordinary successful stream closure.
	CyberForwarded error
	// MarkCyber records policy code/message, bounded payload text, HTTP stream
	// status, and usage seen before client error presentation is attempted.
	MarkCyber func(CyberObservation)
	// Truncate limits cyber diagnostic text at the requested length; the gateway
	// supplies TruncateUTF8 for the reader's 4096-byte diagnostic budget.
	Truncate func(string, int) string
	// BuildStreamError produces the client's protocol error frame; Chat and
	// Messages gateway bindings select their own SSE envelope for the same policy
	// call.
	BuildStreamError func(string, string) string
	// StreamFailure classifies Chat error/failed payloads with the local
	// client-output flag; physical heartbeat commitment is handled separately when
	// returning its Failover error.
	StreamFailure func([]byte, string, bool) ChatFailure
	// SilentRefusal supplies pre-output recovery after the Chat detector classifies
	// a held terminal; it is required on paths where that detection is enabled.
	SilentRefusal func() error
	// ReadFailure translates Chat scanner failures after excluding cancellation,
	// deadline expiration, and known client disconnection; Messages scanner errors
	// use their own incomplete wrapper.
	ReadFailure func(error) error
}

// NewCompatStreamHeaderWriter returns a closure that commits filtered HTTP 200
// SSE headers on its first invocation. Construction retains headers and the
// required filter without writing. Serialize closure calls: its ordinary
// boolean is set before filtering/writing, and repeated calls perform no
// further header work.
func NewCompatStreamHeaderWriter(c *upstream.OutputContext, headers http.Header, filter func(http.Header, http.Header)) func() {
	written := false
	return func() {
		if written {
			return
		}
		// Claim this serialized closure before filtering and committing headers,
		// so later conversion chunks cannot repeat the first-output header side effects.
		written = true
		filter(c.Writer.Header(), headers)
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(http.StatusOK)
	}
}

// ReadChatBuffered converts the terminal Responses object to Chat JSON after
// caller-provided buffered reading and usage policy. Supply callbacks used for
// reads, failure handling, observation, headers, and service tier. The executor
// owns closing resp.Body.
//
// Missing terminal data produces a client 502; failed terminals and
// missing-usage policy can prevent conversion. The accumulator supplements an
// empty output before the bridge creates client JSON, and forwarded SSE content
// type is replaced for that representation. Optional search accounting uses
// serialized terminal data when available. ResponseID and ClientDisconnect
// remain unpopulated in this reader's returned result.
func ReadChatBuffered(resp *http.Response, c *upstream.OutputContext, options ChatResponseOptions, originalModel, upstreamModel string, startTime time.Time) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")

	finalResponse, usage, acc, err := options.ReadBuffered()
	if err != nil {
		return nil, options.BufferedReadFailure(err)
	}

	if finalResponse == nil {
		options.WriteError(http.StatusBadGateway, "api_error", "Upstream stream ended without a terminal response event")
		return nil, fmt.Errorf("upstream stream ended without terminal event")
	}
	options.ObserveFinal(finalResponse)
	if strings.TrimSpace(finalResponse.Status) == "failed" {
		return nil, options.BufferedFailure(finalResponse, usage)
	}

	if err := options.MissingUsage(finalResponse, usage); err != nil {
		return nil, err
	}

	// Supplement empty terminal output from accumulated deltas before protocol
	// conversion, retaining terminal metadata while making earlier content
	// available to the Chat bridge.
	acc.SupplementResponseOutput(finalResponse)

	chatResp := bridge.ResponsesToChatCompletions(options.Runtime, finalResponse, originalModel)

	options.Headers(c.Writer.Header(), resp.Header)
	// Replace the forwarded SSE Content-Type before writing aggregated JSON.
	// Downstream proxies that select a parser from this header would otherwise treat
	// the buffered JSON body as an SSE stream.
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusOK, chatResp)

	result := &CompatResponseResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   upstreamModel,
		ServiceTier:     options.ServiceTier(),
		Stream:          false,
		Duration:        time.Since(startTime),
	}
	// Optional search billing uses the terminal JSON when marshaling succeeds;
	// failure leaves SearchCount zero while client conversion has already completed.
	if options.CountSearch && finalResponse != nil {
		if body, err := json.Marshal(finalResponse); err == nil {
			if n := options.JSONSearch(body); n > 0 {
				result.SearchCount = n
			}
		}
	}
	return result, nil
}

// ReadChatStreaming converts Responses SSE to Chat chunks while retaining usage
// and optional search observations. Supply stream callbacks and keep the
// response body owned by the executor. Result ClientDisconnect and ResponseID
// remain unpopulated by this conversion reader.
//
// Tool-name restoration precedes decoding; nested terminal response usage wins
// over event usage. Large-request refusal detection can hold converted output
// until release or terminal classification. Recovery before physical writer
// commitment can return a nil result; later errors return observed usage. A
// timed producer is signaled to stop sends on return, but blocked scanning
// still needs body closure. Missing terminal, read failure, and idle timeout
// retain their separate outcomes.
func ReadChatStreaming(resp *http.Response, c *upstream.OutputContext, options ChatResponseOptions, originalModel, upstreamModel string, startTime time.Time, requestBodyLen int) (*CompatResponseResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := NewCompatStreamHeaderWriter(c, resp.Header, options.Headers)

	state := bridge.NewResponsesEventToChatState(options.Runtime)
	state.Model = originalModel
	// Request usage chunks from the bridge even when the client omitted them,
	// allowing downstream billing proxies to observe token totals at stream completion.
	state.IncludeUsage = true

	var usage wire.ForwardUsage
	var usageAccumulator wire.UsageAccumulator

	clientDisconnected := false
	clientOutputStarted := false
	pendingSSE := make([]string, 0, 4)
	refusalDetector := NewChatSilentRefusalDetector(requestBodyLen)
	var streamFailoverErr error
	var streamNonFailoverErr error
	var cyberPolicyErr error
	// Keep one attempt-local search identity set across events, so repeated
	// lifecycle copies can be counted once by the supplied search observer.
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

	resultWithUsage := func() *CompatResponseResult {
		out := &CompatResponseResult{
			RequestID:       requestID,
			UpstreamHeaders: resp.Header,
			Usage:           usage,
			Model:           originalModel,
			UpstreamModel:   upstreamModel,
			ServiceTier:     options.ServiceTier(),
			Stream:          true,
			Duration:        time.Since(startTime),
		}
		if searchCount > 0 {
			out.SearchCount = searchCount
		}
		return out
	}

	processDataLine := func(payload string) bool {
		payload = string(options.RestoreToolNames([]byte(payload)))
		if countSearch {
			searchCount += options.StreamSearch([]byte(payload), streamSearchSeen)
		}

		var event wire.ResponsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("openai chat_completions stream: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
			return false
		}
		options.Observe([]byte(payload), event.Type)
		refusalDetector.ObservePayload([]byte(payload))
		// Client conversion and accounting receive the same presence-aware usage,
		// retaining earlier cache fields when a later payload supplies only output.
		if snapshot := usageAccumulator.ObserveResponsesEvent([]byte(payload), &event); snapshot != nil {
			usage = usageAccumulator.ForwardUsage()
		}

		isTerminalEvent := IsCompatResponsesTerminalEvent(event.Type)
		if strings.TrimSpace(event.Type) == "response.failed" || strings.TrimSpace(event.Type) == "error" {
			payloadBytes := []byte(payload)
			message := ExtractOpenAISSEErrorMessage(payloadBytes)
			if hit, code, msg := DetectOpenAICyberPolicy(payloadBytes); hit {
				// Capture up to four KiB of policy evidence with terminal usage before
				// client error delivery, so failed delivery does not discard those observations.
				options.MarkCyber(CyberObservation{
					Code:           code,
					Message:        msg,
					Body:           options.Truncate(string(payloadBytes), 4096),
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
					if _, err := fmt.Fprint(c.Writer, options.BuildStreamError(code, clientMsg)); err == nil {
						_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
						if flusher, ok := c.Writer.(http.Flusher); ok {
							flusher.Flush()
						}
					}
				}
				cyberPolicyErr = options.CyberForwarded
				return true
			}
			// Keep failure policy ahead of chunk conversion, so a retryable error
			// can return while held content still leaves the attempt eligible for recovery.
			failure := options.StreamFailure(payloadBytes, message, clientOutputStarted)
			if failure.Failover != nil {
				streamFailoverErr = failure.Failover
				return true
			}
			defaultStatus, defaultErrType, defaultMsg := failure.Status, failure.Type, failure.Message

			// This envelope contains selected string scalars, which JSON supports;
			// error formatting advances independently of subsequent client write failure.
			errorPayload, _ := json.Marshal(map[string]any{
				"error": map[string]any{
					"type":    defaultErrType,
					"message": defaultMsg,
				},
			})
			if !clientDisconnected {
				if !clientOutputStarted {
					options.WriteError(defaultStatus, defaultErrType, defaultMsg)
					clientOutputStarted = true
				} else if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", errorPayload); err != nil {
					clientDisconnected = true
					logger.L().Info("openai chat_completions stream: client disconnected while writing upstream error",
						zap.String("request_id", requestID),
					)
				}
			}
			if !clientDisconnected {
				c.Writer.Flush()
			}
			streamNonFailoverErr = fmt.Errorf("upstream response failed: %s", defaultMsg)
			return true
		}

		// Advance conversion and refusal observation before sink writes, retaining
		// upstream accounting even when this or an earlier chunk cannot reach the client.
		chunks := bridge.ResponsesEventToChatChunks(&event, state)
		for _, chunk := range chunks {
			refusalDetector.ObserveChatChunk(chunk)
		}
		if !clientDisconnected {
			for _, chunk := range chunks {
				// A failed chunk encoding is skipped after bridge state advances;
				// later observations and terminal accounting continue through this attempt.
				sse, err := bridge.ChatChunkToSSE(chunk)
				if err != nil {
					logger.L().Warn("openai chat_completions stream: failed to marshal chunk",
						zap.Error(err),
						zap.String("request_id", requestID),
					)
					continue
				}
				// Hold converted chunks while the large-request detector lacks release
				// evidence, keeping possible silent refusal ahead of semantic client commitment.
				if !clientOutputStarted && !refusalDetector.ShouldReleaseClientOutput() {
					pendingSSE = append(pendingSSE, sse)
					continue
				}
				if !clientOutputStarted {
					writeStreamHeaders()
					for _, pending := range pendingSSE {
						if _, err := fmt.Fprint(c.Writer, pending); err != nil {
							clientDisconnected = true
							logger.L().Info("openai chat_completions stream: client disconnected while flushing pending chunks",
								zap.String("request_id", requestID),
							)
							break
						}
					}
					pendingSSE = pendingSSE[:0]
					clientOutputStarted = !clientDisconnected
					if clientDisconnected {
						break
					}
				}
				if _, err := fmt.Fprint(c.Writer, sse); err != nil {
					clientDisconnected = true
					logger.L().Info("openai chat_completions stream: client disconnected, continuing to drain upstream for billing",
						zap.String("request_id", requestID),
					)
					break
				}
			}
		}
		if len(chunks) > 0 && !clientDisconnected && clientOutputStarted {
			c.Writer.Flush()
		}
		return isTerminalEvent
	}

	finalizeStream := func() (*CompatResponseResult, error) {
		if cyberPolicyErr != nil {
			return resultWithUsage(), cyberPolicyErr
		}
		if streamFailoverErr != nil {
			if c == nil || c.Writer == nil || !c.Writer.ContentWritten() {
				return nil, streamFailoverErr
			}
			return resultWithUsage(), streamFailoverErr
		}
		if streamNonFailoverErr != nil {
			return resultWithUsage(), streamNonFailoverErr
		}
		finalChunks := bridge.FinalizeResponsesChatStream(state)
		for _, chunk := range finalChunks {
			refusalDetector.ObserveChatChunk(chunk)
		}
		if len(finalChunks) > 0 && !clientDisconnected {
			for _, chunk := range finalChunks {
				// A failed chunk encoding is skipped after bridge state advances;
				// later observations and terminal accounting continue through this attempt.
				sse, err := bridge.ChatChunkToSSE(chunk)
				if err != nil {
					continue
				}
				// Hold converted chunks while the large-request detector lacks release
				// evidence, keeping possible silent refusal ahead of semantic client commitment.
				if !clientOutputStarted && !refusalDetector.ShouldReleaseClientOutput() {
					pendingSSE = append(pendingSSE, sse)
					continue
				}
				if !clientOutputStarted {
					writeStreamHeaders()
					for _, pending := range pendingSSE {
						if _, err := fmt.Fprint(c.Writer, pending); err != nil {
							clientDisconnected = true
							logger.L().Info("openai chat_completions stream: client disconnected during pending final flush",
								zap.String("request_id", requestID),
							)
							break
						}
					}
					pendingSSE = pendingSSE[:0]
					clientOutputStarted = !clientDisconnected
					if clientDisconnected {
						break
					}
				}
				if _, err := fmt.Fprint(c.Writer, sse); err != nil {
					clientDisconnected = true
					logger.L().Info("openai chat_completions stream: client disconnected during final flush",
						zap.String("request_id", requestID),
					)
					break
				}
			}
		}
		// Resolve held output after bridge finalization, allowing refusal policy
		// to reject an empty attempt before headers or a DONE frame are written.
		if !clientDisconnected && !clientOutputStarted {
			if refusalDetector.IsSilentRefusal() {
				return nil, options.SilentRefusal()
			}
			if len(pendingSSE) > 0 {
				writeStreamHeaders()
				for _, pending := range pendingSSE {
					if _, err := fmt.Fprint(c.Writer, pending); err != nil {
						clientDisconnected = true
						logger.L().Info("openai chat_completions stream: client disconnected during final pending flush",
							zap.String("request_id", requestID),
						)
						break
					}
				}
				pendingSSE = pendingSSE[:0]
				clientOutputStarted = !clientDisconnected
			}
		}
		// A recognized terminal closes the Chat stream with DONE after held
		// content has been settled; disconnect suppresses delivery while usage remains available.
		if !clientDisconnected {
			writeStreamHeaders()
			if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err != nil {
				clientDisconnected = true
				logger.L().Info("openai chat_completions stream: client disconnected during done flush",
					zap.String("request_id", requestID),
				)
			}
			clientOutputStarted = !clientDisconnected
		}
		if !clientDisconnected {
			c.Writer.Flush()
		}
		return resultWithUsage(), nil
	}

	handleScanErr := func(err error) {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.FromContext(options.RequestContext).Warn("openai chat_completions stream: read error",
				zap.Error(err),
				zap.String("upstream_request_id", requestID),
			)
		}
	}
	missingTerminalErr := func() (*CompatResponseResult, error) {
		return resultWithUsage(), fmt.Errorf("stream usage incomplete: missing terminal event")
	}
	processFrame := func(frame wire.OpenAICompatSSEFrame) bool {
		payload := wire.OpenAICompatPayloadWithEventType(frame.Data, frame.EventType)
		if strings.TrimSpace(payload) == "[DONE]" {
			return false
		}
		return processDataLine(payload)
	}

	// With upstream timeout disabled, direct scanning uses the same frame parser
	// and terminal requirement while avoiding read-ahead and a producer goroutine.
	if streamInterval <= 0 {
		var parser wire.OpenAICompatSSEFrameParser
		for scanner.Scan() {
			line := scanner.Text()
			frame, ok := parser.AddLine(line)
			if !ok {
				continue
			}
			if strings.TrimSpace(frame.Data) == "[DONE]" {
				return missingTerminalErr()
			}
			if processFrame(frame) {
				return finalizeStream()
			}
		}
		if err := scanner.Err(); err != nil {
			handleScanErr(err)
			if clientDisconnected || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", err)
			}
			return resultWithUsage(), options.ReadFailure(err)
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

	// scanEvent carries a scanner line or failure to the timed consumer.
	// The producer supplies one of line or err per event and owns channel closure.
	type scanEvent struct {
		line string // upstream scanner line
		err  error  // upstream scanner failure
	}
	// Bound queued read-ahead while conversion or client writes run; done
	// releases pending sends after terminal processing or another consumer exit.
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
	// Publish actual scan arrival before queueing, so a slow client consumer
	// does not make recently read upstream lines appear idle to timeout policy.
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
	// Stop producer sends on return; a scanner blocked in Body.Read still
	// depends on the executor closing that response body to finish its goroutine.
	defer close(done)

	var parser wire.OpenAICompatSSEFrameParser

	for {
		select {
		case ev, ok := <-events:
			if !ok {
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
				if clientDisconnected || errors.Is(ev.err, context.Canceled) || errors.Is(ev.err, context.DeadlineExceeded) {
					return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", ev.err)
				}
				return resultWithUsage(), options.ReadFailure(ev.err)
			}

			line := ev.line
			frame, ok := parser.AddLine(line)
			if !ok {
				continue
			}
			if strings.TrimSpace(frame.Data) == "[DONE]" {
				return missingTerminalErr()
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
			logger.L().Warn("openai chat_completions stream: data interval timeout",
				zap.String("request_id", requestID),
				zap.String("model", originalModel),
				zap.Duration("interval", streamInterval),
			)
			return resultWithUsage(), fmt.Errorf("stream data interval timeout")
		}
	}
}
