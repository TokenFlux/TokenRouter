package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// response_stream.go reads Responses streams and coordinates first-output
// staging, terminal handling, usage observation, and downstream delivery.
// HTTP Responses progress, visible content, flush policy, and client
// commitment advance through different checks. Native OpenAI attempts can stage
// initial SSE bytes and provider headers so pre-content failure remains
// recoverable. Event separators complete staged progress and stop its deadline,
// while visible-payload observation can happen before those bytes reach the
// sink. Heartbeat comments can commit HTTP independently of the caller's retry
// commitment.
//
// The timed reader queues scanner lines and acknowledges each guarded token
// before further reading, limiting pre-content read-ahead. First-output expiry
// closes the body and consumes queued acknowledgments through channel closure
// before returning its classification. Other exits stop sends and leave body
// closure to the executor. Bare errors wait for an authoritative failed
// terminal or EOF synthesis; usage and tool/image/search observations occur
// before delivery restoration and error trimming.
// Locally generated read and timeout errors drain pending upstream bytes before
// using the caller's local error writer, keeping their delivery provenance
// separate from the upstream events whose output they terminate.

// CyberObservation carries policy failure evidence and usage observed before
// client error presentation.
type CyberObservation struct {
	Code    string // upstream cyber policy code
	Message string // upstream cyber failure message
	// Body contains caller-truncated policy evidence; the stream readers request a
	// four-KiB prefix before their caller decides diagnostic storage and redaction.
	Body string
	// UpstreamStatus is the transport status assigned to this observation; stream
	// cyber paths use HTTP 200 while code/message carry the policy failure.
	UpstreamStatus int
	// UpstreamInTok and UpstreamOutTok snapshot the usage accumulator after failure
	// parsing, before client trimming can remove the terminal usage record; absent
	// observations can still leave either count zero.
	UpstreamInTok  int // observed input token count
	UpstreamOutTok int // observed output token count
}

// StreamOptions selects attempt timing, staging, restoration, and caller-owned
// output/failure observation policies.
type StreamOptions struct {
	ProviderID int64 // selected provider diagnostic identity
	// NativeOpenAI selects native empty-completion rejection and capacity diagnostics
	// after client output. StageFirstOutput retains initial SSE bytes and selects
	// deferred provider-header application during progress completion before HTTP
	// commitment. CodexFailureTerminal holds bare errors for an authoritative failed
	// terminal or EOF synthesis, keeping its provider effects pending. GrokIdlePolicy
	// enables idle cooldown before recovery selection and later client stream failure.
	// These four values stay fixed per call; the gateway pairs NativeOpenAI with
	// StageFirstOutput, while Codex terminal and Grok idle flags follow provider rules.
	NativeOpenAI, StageFirstOutput, CodexFailureTerminal, GrokIdlePolicy bool
	// FirstOutputTimeout measures an elapsed budget from startTime and needs staged
	// progress completion to stop its deadline. StreamInterval checks inactivity
	// from producer read arrival, independently of time spent in client conversion.
	// Nonpositive values disable the respective timer. The first uses one
	// deadline; the read-idle timer uses independently updated arrival evidence.
	FirstOutputTimeout, StreamInterval time.Duration
	// MaxLineSize sets scanner token growth in bytes; the initial pooled
	// sixty-four-KiB buffer can accept a token larger than a smaller configured
	// budget.
	MaxLineSize int

	// PrepareHeaders receives upstream headers, staging selection, and current
	// client headers; its returned attempt header map stays available until a
	// progress-event commit opportunity.
	PrepareHeaders func(http.Header, bool, http.Header) http.Header
	// Observe sees raw upstream data and effective event type before lifecycle
	// normalization, model rewriting, client-tool restoration, or delivery trimming
	// occurs.
	Observe func([]byte, string)
	Logf    func(string, ...any) // attempt diagnostic log callback
	// MarkTime receives data, visible, or flush observations from the reader; the
	// gateway recorder decides whether repeated observations replace an existing
	// timestamp.

	// WriteLocalStreamError synchronously writes an already-framed local SSE
	// failure. ReadStreamingResponse drains pending upstream bytes first and
	// flushes afterward; the writer keeps local errors outside upstream timing.
	WriteLocalStreamError func([]byte) (int, error)
	// IsCommitted supplies caller retry commitment for result snapshots; physical
	// HTTP writer commitment and visible-payload evidence are measured separately
	// by this reader.
	IsCommitted func() bool
	// MarkCommitted records caller retry commitment before a matched error rule
	// writes JSON, preventing that custom presentation from remaining
	// replay-eligible.
	MarkCommitted func()
	// ClientOutputStarted combines the local delivery flag with caller writer
	// observations; the gateway can exclude tracked heartbeat bytes when deciding
	// safe pre-output recovery.
	ClientOutputStarted func(bool) bool
	// ClearDisconnect clears caller disconnect policy when terminal evidence exists
	// and no failed-event flag is set, including a scan error after that terminal.
	ClearDisconnect func()
	// RecordDisconnect records incomplete post-output transport evidence;
	// cancellation and known client sink failure use their separate returned-error
	// paths.
	RecordDisconnect func(error, string)
	// TerminalSideEffects applies post-output provider policy; Codex bare errors
	// defer it until response.failed or EOF synthesis supplies the selected
	// terminal outcome.
	TerminalSideEffects func([]byte, string, http.Header, string)
	// Failover classifies missing-terminal, pre-output read, or staging failures
	// from request ID, optional body, and message; the outer executor chooses
	// actual retry.
	Failover func(string, []byte, string) error
	// FailoverWithModel classifies a pre-output error event with raw failure bytes,
	// mapped model, and response headers before client error trimming occurs.
	FailoverWithModel func(string, []byte, string, string, http.Header) error
	// MarkSafeFailover annotates guarded staging/scanner failure as safe recovery
	// under caller policy, including when HTTP heartbeat bytes have already been
	// written.
	MarkSafeFailover func(error)
	// RecordError retains upstream failure evidence before a matched error rule
	// writes its client JSON, keeping monitoring separate from customized
	// presentation.
	RecordError func(string, string, []byte, string)
	// CompactFallback can classify an error for configured Compact recovery before
	// client output; a nonnil result ends this reader's attempt immediately.
	CompactFallback func([]byte, string) error
	// ErrorRule supplies selected client status/type/message when matched before
	// output; the reader records evidence and marks retry commitment before writing
	// that JSON.
	ErrorRule func([]byte, string) (int, string, string, bool)
	// CapacitySuppressed records one native OpenAI capacity failure after output
	// has made failover unavailable; this reader suppresses repeated logging for
	// the same attempt.
	CapacitySuppressed func(string, string)
	// MarkCyber receives policy evidence after terminal usage parsing and before
	// client error trimming; caller storage/redaction policy remains outside the
	// reader.
	MarkCyber func(CyberObservation)
	// ToolCorrector runs after native image/search observation; mapped renames
	// require a usable constructor-initialized corrector and its statistics
	// storage.
	ToolCorrector *CodexToolCorrector
	// RestoreClientTools restores provider-adapted client tools. RestoreNamespace
	// restores flattened namespace identity. They run in that order before reserved
	// name restoration; a failure stops delivery with observed usage retained, and
	// callback inputs remain within this attempt's serialized consumer.
	RestoreClientTools, RestoreNamespace func([]byte) ([]byte, error)
	// RestoreToolNames restores reserved client names after RestoreClientTools and
	// RestoreNamespace, using the effective event type for that delivered
	// representation.
	RestoreToolNames func([]byte, string) []byte
	// EmptyCompleted classifies a native OpenAI terminal lacking output/usage/error
	// before prior content, keeping that attempt out of ordinary successful
	// zero-usage settlement.
	EmptyCompleted func(string) error
	// CountSearch receives each payload with one attempt-local seen map before
	// client restoration; the callback supplies deduplication and any
	// provider-specific search policy.
	CountSearch func([]byte, map[string]struct{}) int
	// StreamTimeout records provider policy for an actual idle interval before
	// optional Grok cooldown/recovery and the reader's client stream error outcome.
	StreamTimeout func(string)
	// IdleCooldown applies the selected Grok idle policy before its pre-output
	// recovery check; post-output timeout keeps accounting and presents stream
	// failure.
	IdleCooldown func()
	// IdleFailover supplies Grok pre-output idle classification after Body is
	// closed, using the configured interval while retaining observed usage for the
	// return.
	IdleFailover func(time.Duration) error
	// FirstOutputError classifies elapsed first-output budget after Body closure
	// and queued-event acknowledgment; it receives start, model, reasoning level,
	// budget, label, and headers.
	FirstOutputError func(time.Time, string, string, time.Duration, string, http.Header) error
	// BuildOpenAIResponseFailedSSE builds a failed terminal for a retained Codex
	// bare error when no authoritative response.failed arrived, using its retained
	// error evidence.
	BuildOpenAIResponseFailedSSE func(string, string, []byte, string) string
	// WrapOpenAIUpstreamWarningIfCyber classifies a selected failed return using
	// status, retained failure bytes, message, and error; passthrough supplies its
	// own narrower payload state.
	WrapOpenAIUpstreamWarningIfCyber func(int, []byte, string, error) error
	// TruncateString limits cyber diagnostic payload text to the requested budget;
	// the gateway binds its byte-oriented UTF-8 truncation helper for this
	// callback.
	TruncateString func(string, int) string

	// OpenAIStreamEventIsTerminalWithType recognizes terminal evidence using
	// normalized payload/type; lifecycle headers and duplicate delivery remain
	// coordinated by the reader.
	OpenAIStreamEventIsTerminalWithType func(string, string) bool
}

// StreamingResult records independently observed content, usage, commitment,
// and reader-specific completion metadata.
type StreamingResult struct {
	// Served records visible-payload evidence, which can precede delivery. HasUsage
	// records successful usage parsing. HttpCommitted reads physical writer state.
	// RetryCommitted reads caller retry commitment. ClientDisconnected records output
	// write/flush failure. ObservedOnly marks a later nil-result failure salvaged
	// after observation state is installed. Each flag describes its
	// own observation; first visible data, HTTP heartbeat writes, and retry policy
	// can advance independently, and passthrough readers leave this group zero.
	Served, HasUsage, HttpCommitted, RetryCommitted, ClientDisconnected, ObservedOnly bool

	// Usage shares the attempt's mutable accumulator; HasUsage tracks parsing
	// evidence separately because zero counters alone cannot establish absence of
	// upstream usage.
	Usage *wire.ForwardUsage

	ResponseID      string // observed upstream Responses identity
	ImageCount      int    // observed completed image count
	ImageFrameCount int    // encoded output frame billing units
	// ImageOutputSizes contains known image dimensions from the counter snapshot;
	// missing metadata is omitted, so its positions need not correspond to every
	// counted image.
	ImageOutputSizes []string
	SearchCount      int // observed deduplicated search calls
}

// ReadStreamingResponse observes and delivers Responses SSE using one attempt's
// selected staging and failure policies. Supply usable response/output objects
// and callbacks used by those policies. Pair a positive FirstOutputTimeout with
// StageFirstOutput, as the gateway does, because deadline progress is completed
// through staged events. The ctx argument is unused; response-body and callback
// contexts govern cancellation.
//
// Visible content, completed staging progress, HTTP commitment, and retry
// commitment remain separate observations. Visible payload can set Served
// before delivery succeeds. Timed producers stop sends on return; first-output
// expiry additionally closes Body and waits for event-channel closure,
// requiring Close to unblock reading. Results salvage observed state on later
// failure, but the unsupported-flusher return occurs before that defer is
// installed. The executor retains final body ownership.
func ReadStreamingResponse(ctx context.Context, resp *http.Response, c *upstream.OutputContext, options StreamOptions, startTime time.Time, originalModel, mappedModel, reasoningEffort string) (observed *StreamingResult, failure error) {
	firstOutputTimeout := options.FirstOutputTimeout
	guardFirstOutput := firstOutputTimeout > 0
	stageFirstOutput := options.StageFirstOutput
	attemptResponseHeaders := options.PrepareHeaders(resp.Header, stageFirstOutput, c.Writer.Header())

	// Stable SSE framing headers are applied independently of provider header
	// staging, so retries do not inherit a prior attempt's content-type/cache policy.
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Unstaged attempts prepare request ID immediately; staged headers wait
	// for progress and cannot be applied after the client writer has committed.
	if !stageFirstOutput && resp.Header.Get("x-request-id") != "" {
		v := resp.Header.Get("x-request-id")
		c.Header("x-request-id", v)
	}
	applyAttemptResponseHeaders := func() {
		if !stageFirstOutput || len(attemptResponseHeaders) == 0 || c.Writer.Written() {
			return
		}
		for key, values := range attemptResponseHeaders {
			for _, value := range values {
				c.Writer.Header().Add(key, value)
			}
		}
		// Reapply framing headers after approved provider values, preventing
		// an upstream content type or cache setting from replacing the gateway's SSE format.
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
	}

	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}
	maxLineSize := options.MaxLineSize
	firstOutputFlushed := false

	firstOutputProgressObserved := false
	// Four KiB batches ordinary writes; guarded first output uses its separate
	// bounded stage so early bytes can remain recoverable before sink delivery.
	bufferedWriter := bufio.NewWriterSize(w, 4*1024)
	var firstOutputStage *OpenAIFirstOutputStage
	if stageFirstOutput {
		firstOutputStage = NewDefaultOpenAIFirstOutputStage()
		// Release the attempt spool on all exits; cleanup failure is logged
		// separately and does not replace the selected stream result or failure.
		defer func() {
			if err := firstOutputStage.Close(); err != nil {
				options.Logf("OpenAI first-output staging cleanup failed: provider=%d model=%s error=%v", options.ProviderID, originalModel, err)
			}
		}()
	}
	writePendingString := func(value string) (int, error) {
		if firstOutputStage != nil && !firstOutputStage.Closed() {
			return firstOutputStage.WriteString(value)
		}
		return bufferedWriter.WriteString(value)
	}
	pendingBytes := func() int64 {
		if firstOutputStage != nil && !firstOutputStage.Closed() {
			return firstOutputStage.Buffered()
		}
		return int64(bufferedWriter.Buffered())
	}
	flushBuffered := func() error {
		if firstOutputStage != nil && !firstOutputStage.Closed() {
			if err := firstOutputStage.CommitTo(w); err != nil {
				return err
			}
		} else {
			if err := bufferedWriter.Flush(); err != nil {
				return err
			}
		}
		flusher.Flush()
		return nil
	}

	usage := &wire.ForwardUsage{}
	hasObservedUsage := false
	semanticOutputSeen := false
	imageCounter := wire.NewOpenAIImageOutputCounter()
	responseID := ""
	var firstOutputScanGuard atomic.Bool
	firstOutputScanGuard.Store(stageFirstOutput)
	scanner := bufio.NewScanner(resp.Body)
	scanBuf := httpclient.GetSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	if stageFirstOutput {
		scanner.Split(OpenAIFirstOutputDynamicScanLines(&firstOutputScanGuard))
	}
	documentScanner := wire.NewSSEJSONDocumentScanner(scanner)

	streamInterval := options.StreamInterval
	// Idle checks use producer arrival timestamps, so time spent in downstream
	// conversion/write work cannot alone make recent upstream data look absent.
	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}

	var firstOutputTimer *time.Timer
	var firstOutputCh <-chan time.Time
	if firstOutputTimeout > 0 {
		remaining := time.Until(startTime.Add(firstOutputTimeout))
		// An elapsed budget schedules a near-immediate timer with a one-nanosecond
		// delay; ready-event selection still decides which observation the consumer handles first.
		if remaining <= 0 {
			remaining = time.Nanosecond
		}
		firstOutputTimer = time.NewTimer(remaining)
		firstOutputCh = firstOutputTimer.C
		defer firstOutputTimer.Stop()
	}
	// Stop and drain a pending deadline before disabling its select channel,
	// preventing completed progress from later selecting an already expired first-output timer.
	stopFirstOutputTimer := func() {
		if firstOutputTimer == nil {
			return
		}
		if !firstOutputTimer.Stop() {
			select {
			case <-firstOutputTimer.C:
			default:
			}
		}
		firstOutputTimer = nil
		firstOutputCh = nil
	}
	// Latch error delivery before flushing/writing a Responses-shaped event,
	// so another read or timeout failure cannot attempt a duplicate error after partial delivery.
	errorEventSent := false
	clientDisconnected := false
	sawTerminalEvent := false
	sawFailedEvent := false
	sawBareError := false
	sawResponseFailed := false
	responsesSemanticOutputSeen := false
	capacityFailoverSuppressedLogged := false
	failedMessage := ""
	var failedPayload []byte
	clientOutputStarted := false
	codexFailureTerminal := options.CodexFailureTerminal
	upstreamRequestID := strings.TrimSpace(resp.Header.Get("x-request-id"))
	var streamEarlyErr error
	terminalFailurePending := false
	failureDelivered := false
	suppressCurrentEvent := false
	var bareErrorPayload []byte
	bareErrorProviderSideEffectsPending := false
	pendingSSEEventType := ""
	eventInProgress := false
	eventStartsClientOutput := false
	eventStartsFirstFlush := false
	eventShouldFlush := false
	// A write rejected by the open stage remains recoverable: classify it,
	// annotate safe failover, and close upstream reading before any attempt bytes are committed.
	handlePendingWriteError := func(err error) {
		if firstOutputStage != nil && !firstOutputStage.Closed() {
			message := "OpenAI first-output staging failed"
			if errors.Is(err, ErrOpenAIFirstOutputStageLimit) {
				message = "OpenAI first-output staging limit exceeded"
			}
			options.Logf("%s: provider=%d model=%s error=%v", message, options.ProviderID, originalModel, err)
			failoverErr := options.Failover(upstreamRequestID, nil, message)
			options.MarkSafeFailover(failoverErr)
			streamEarlyErr = failoverErr
			_ = resp.Body.Close()
			return
		}
		clientDisconnected = true
		options.Logf("Client disconnected during streaming, continuing to drain upstream for billing")
	}
	// Complete one framed event before stopping its progress deadline or taking
	// staged headers into the live writer; payload observation and successful
	// delivery remain separate.
	completeGuardedEvent := func(queueDrained bool) {
		completedProgressEvent := eventStartsClientOutput
		completedFirstFlushEvent := eventStartsFirstFlush
		shouldFlush := eventShouldFlush || (queueDrained && clientOutputStarted)
		eventInProgress = false
		if !clientDisconnected {
			if completedProgressEvent {
				applyAttemptResponseHeaders()
			}
			if shouldFlush {
				if err := flushBuffered(); err != nil {
					clientDisconnected = true
					options.Logf("Client disconnected during streaming flush, continuing to drain upstream for billing")
				} else {
					clientOutputStarted = true
				}
			}
		}
		if completedProgressEvent && !firstOutputProgressObserved {
			firstOutputScanGuard.Store(false)
			firstOutputProgressObserved = true
			stopFirstOutputTimer()
		}
		if completedFirstFlushEvent && !firstOutputFlushed {
			// Semantic progress ends the initial flush guard for subsequent stream events.
			firstOutputFlushed = true
		}
		eventStartsClientOutput = false
		eventStartsFirstFlush = false
		eventShouldFlush = false
	}
	sendErrorEvent := func(reason string) {
		if errorEventSent || clientDisconnected {
			return
		}
		errorEventSent = true
		payload := `{"type":"error","sequence_number":0,"error":{"type":"upstream_error","message":` + strconv.Quote(reason) + `,"code":` + strconv.Quote(reason) + `}}`
		if err := flushBuffered(); err != nil {
			clientDisconnected = true
			return
		}
		// Pending upstream bytes have been delivered before this local failure.
		// Keep its write outside upstream buffering so the error cannot establish
		// an upstream delivery timestamp or become mixed with genuine response data.
		if _, err := options.WriteLocalStreamError([]byte("data: " + payload + "\n\n")); err != nil {
			clientDisconnected = true
			return
		}
		if err := flushBuffered(); err != nil {
			clientDisconnected = true
			return
		}
		clientOutputStarted = true
	}

	needModelReplace := originalModel != mappedModel
	responseAccumulator := bridge.NewBufferedResponseAccumulator()
	streamDoneItems := bridge.NewResponsesStreamOutputItems()
	streamImageOutputs := make([]json.RawMessage, 0, 1)
	streamSeenImages := make(map[string]struct{})
	searchCounter := 0
	// Retain one search identity set across completed-item and terminal copies,
	// allowing the callback to avoid duplicate surcharge observations within this attempt.
	streamSearchSeen := make(map[string]struct{})
	resultWithUsage := func() *StreamingResult {
		return &StreamingResult{
			Served:             semanticOutputSeen,
			HasUsage:           hasObservedUsage,
			HttpCommitted:      c.Writer.Written(),
			RetryCommitted:     options.IsCommitted(),
			ClientDisconnected: clientDisconnected,

			Usage: usage,

			ResponseID: responseID,

			ImageCount:      imageCounter.Count(),
			ImageFrameCount: imageCounter.FrameCount(),

			ImageOutputSizes: imageCounter.Sizes(),

			SearchCount: searchCounter,
		}
	}
	// Once observation state exists, salvage a nil-result failure as ObservedOnly;
	// early unsupported-writer rejection above occurs before this defer is registered.
	defer func() {
		if observed == nil && failure != nil {
			observed = resultWithUsage()
			observed.ObservedOnly = true
		}
	}()
	flushPending := func(disconnectMessage string) {
		if clientDisconnected || pendingBytes() == 0 {
			return
		}
		if err := flushBuffered(); err != nil {
			clientDisconnected = true
			options.Logf("%s", disconnectMessage)
			return
		}
		clientOutputStarted = true
	}
	synthesizeBareErrorFailure := func() bool {
		if !codexFailureTerminal || !sawBareError || sawResponseFailed || clientDisconnected {
			return false
		}
		if bareErrorProviderSideEffectsPending {
			options.TerminalSideEffects(bareErrorPayload, failedMessage, resp.Header, mappedModel)
			bareErrorProviderSideEffectsPending = false
		}
		applyAttemptResponseHeaders()
		if _, err := writePendingString(options.BuildOpenAIResponseFailedSSE(responseID, originalModel, bareErrorPayload, failedMessage)); err != nil {
			handlePendingWriteError(err)
			return false
		}
		failedPayload = append(failedPayload[:0], bareErrorPayload...)
		sawResponseFailed = true
		sawFailedEvent = true
		sawTerminalEvent = true
		failureDelivered = true
		return true
	}
	var flushHeartbeatFrames func()
	finalizeStream := func() (*StreamingResult, error) {
		if flushHeartbeatFrames != nil {
			flushHeartbeatFrames()
		}
		if stageFirstOutput && eventInProgress {
			// EOF can finish data without a separator; complete its staged state
			// before terminal selection so available event evidence is not left waiting
			// for another line.
			completeGuardedEvent(true)
		}
		synthesizeBareErrorFailure()
		if sawTerminalEvent && !sawFailedEvent {
			options.ClearDisconnect()
		}
		if !sawTerminalEvent && !options.ClientOutputStarted(clientOutputStarted) && !eventShouldFlush {
			return resultWithUsage(), options.Failover(
				upstreamRequestID,
				nil,
				"OpenAI stream ended before a terminal event",
			)
		}
		flushPending("Client disconnected during final flush, returning collected usage")
		if !sawTerminalEvent {
			if options.ClientOutputStarted(clientOutputStarted) && !clientDisconnected {
				options.RecordDisconnect(errors.New("stream ended before terminal event"), upstreamRequestID)
			}
			return resultWithUsage(), fmt.Errorf("stream usage incomplete: missing terminal event")
		}
		if sawFailedEvent {
			err := fmt.Errorf("upstream response failed: %s", failedMessage)
			return resultWithUsage(), options.WrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, failedPayload, failedMessage, err)
		}
		return resultWithUsage(), nil
	}
	handleScanErr := func(scanErr error) (*StreamingResult, error, bool) {
		if scanErr != nil && flushHeartbeatFrames != nil {
			flushHeartbeatFrames()
		}
		if scanErr == nil {
			return nil, nil, false
		}
		if errors.Is(scanErr, ErrOpenAIFirstOutputScannerLimit) && !firstOutputProgressObserved {
			options.Logf("SSE token exceeded guarded first-output limit: provider=%d limit=%d error=%v", options.ProviderID, OpenAIFirstOutputStageMaxBytes+OpenAIFirstOutputScannerFramingAllowance, scanErr)
			failoverErr := options.Failover(upstreamRequestID, nil,
				"OpenAI SSE line exceeds guarded first-output limit",
			)
			options.MarkSafeFailover(failoverErr)
			return resultWithUsage(), failoverErr, true
		}
		if errors.Is(scanErr, bufio.ErrTooLong) && stageFirstOutput && !firstOutputProgressObserved {
			options.Logf("SSE line too long before first output: provider=%d max_size=%d error=%v", options.ProviderID, maxLineSize, scanErr)
			failoverErr := options.Failover(upstreamRequestID, nil,
				"OpenAI SSE line exceeds guarded first-output limit",
			)
			options.MarkSafeFailover(failoverErr)
			return resultWithUsage(), failoverErr, true
		}
		if sawTerminalEvent {
			if !sawFailedEvent {
				options.ClearDisconnect()
				options.Logf("Upstream scan ended after terminal event: %v", scanErr)
			}
			result, err := finalizeStream()
			return result, err, true
		}
		// Cancellation/deadline reads retain incomplete accounting and optional
		// pending delivery, while selected transport/size failures use their separate
		// error-event paths.
		if errors.Is(scanErr, context.Canceled) || errors.Is(scanErr, context.DeadlineExceeded) {
			if eventShouldFlush {
				flushPending("Client disconnected during canceled stream flush, returning collected usage")
			}
			return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", scanErr), true
		}
		if errors.Is(scanErr, bufio.ErrTooLong) {
			options.Logf("SSE line too long: provider=%d max_size=%d error=%v", options.ProviderID, maxLineSize, scanErr)
			sendErrorEvent("response_too_large")
			return resultWithUsage(), scanErr, true
		}
		if !options.ClientOutputStarted(clientOutputStarted) && !eventShouldFlush {
			msg := "OpenAI stream disconnected before completion"
			if errText := strings.TrimSpace(scanErr.Error()); errText != "" {
				msg += ": " + errText
			}
			return resultWithUsage(), options.Failover(upstreamRequestID, nil, msg), true
		}
		// After disconnect, return collected usage with the upstream read error.
		if clientDisconnected {
			return resultWithUsage(), fmt.Errorf("stream usage incomplete after disconnect: %w", scanErr), true
		}
		options.RecordDisconnect(scanErr, upstreamRequestID)
		sendErrorEvent("stream_read_error")
		return resultWithUsage(), fmt.Errorf("stream read error: %w", scanErr), true
	}
	var lifecycleNormalizer wire.ResponseLifecycleNormalizer
	pendingLifecycleHeader := ""
	processOutputLine := func(line string, queueDrained bool) {
		if streamEarlyErr != nil {
			return
		}
		if eventType, ok := wire.ExtractSSEEventLine(line); ok {
			pendingSSEEventType = eventType
			eventType = strings.TrimSpace(eventType)
			suppressCurrentEvent = codexFailureTerminal && (eventType == "error" || (sawBareError && !sawResponseFailed && eventType != "response.failed"))
			if wire.IsResponseLifecycleEvent(eventType) {
				// Keep lifecycle header and data together through normalization, so
				// renaming or duplicate-terminal suppression cannot leave an orphan event
				// header downstream.
				pendingLifecycleHeader = line
				return
			}
		}
		// Observe upstream data before lifecycle or client-identity repair, keeping
		// model/tier evidence tied to the provider representation rather than the delivered copy.
		if data, ok := wire.ExtractSSEDataLine(line); ok {
			dataBytes := []byte(data)
			eventType := wire.EffectiveOpenAISSEEventType(dataBytes, pendingSSEEventType)
			options.Observe(dataBytes, eventType)
			normalizedData, normalizedType, duplicateTerminal := lifecycleNormalizer.Normalize(dataBytes, eventType)
			if !bytes.Equal(normalizedData, dataBytes) {
				dataBytes = normalizedData
				data = string(dataBytes)
				line = "data: " + data
			}
			eventType = normalizedType
			if codexFailureTerminal && sawBareError && !sawResponseFailed &&
				eventType == "response.completed" {
				// A normalized completed terminal resolves a retained bare error as
				// success; clear its pending delivery and provider effects before ordinary
				// terminal accounting.
				sawBareError = false
				sawFailedEvent = false
				terminalFailurePending = false
				suppressCurrentEvent = false
				bareErrorPayload = nil
				bareErrorProviderSideEffectsPending = false
				failedMessage = ""
			}
			// Recompute suppression from normalized identity because legacy done can
			// become failed; an earlier event-header classification must not hide its
			// authoritative terminal.
			suppressCurrentEvent = codexFailureTerminal && (eventType == "error" || (sawBareError && !sawResponseFailed && eventType != "response.failed"))
			// Terminal detection uses normalized type, allowing a lifecycle event
			// declared in the event line to supply terminal evidence when JSON omitted type.
			if options.OpenAIStreamEventIsTerminalWithType(data, eventType) {
				sawTerminalEvent = true
			}
			if responseID == "" {
				responseID = wire.ExtractOpenAIResponseIDFromJSONBytes(dataBytes)
			}
			forceFlushFailedEvent := false
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
				failedMessage = ExtractOpenAISSEErrorMessage(dataBytes)
				if failedMessage == "" {
					failedMessage = "Upstream response failed"
				}
				// Snapshot failure usage before policy recording and client trimming;
				// the later delivery copy can lose usage while monitoring retains consumed-token evidence.
				hasObservedUsage = wire.ParseSSEUsageBytesWithType(dataBytes, eventType, usage) || hasObservedUsage
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
						sawFailedEvent = true
						streamEarlyErr = compactErr
						return
					}
				}
				if outputStarted && !cyberHit {
					if codexFailureTerminal && eventType == "error" {
						// Defer provider effects for a retained bare error until failed
						// terminal or EOF synthesis, keeping the paired failure frames from
						// applying the same outcome twice.
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
						sawFailedEvent = true
						streamEarlyErr = options.FailoverWithModel(upstreamRequestID, dataBytes, failedMessage, mappedModel, resp.Header)
						return
					}
					if !cyberHit && !sawBareError {
						if status, errType, errMsg, matched := options.ErrorRule(dataBytes, failedMessage); matched {
							sawFailedEvent = true
							// Record upstream errors even when a passthrough rule handles the client response.
							options.RecordError(upstreamRequestID, "http_error", dataBytes, failedMessage)
							options.MarkCommitted()
							c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
							c.JSON(status, map[string]any{
								"error": map[string]any{
									"type":    errType,
									"message": errMsg,
								},
							})
							streamEarlyErr = fmt.Errorf("upstream response failed: passthrough rule matched message=%s", errMsg)
							return
						}
					}
				}
				forceFlushFailedEvent = true
				sawFailedEvent = true
				failedPayload = append(failedPayload[:0], dataBytes...)
				terminalFailurePending = !codexFailureTerminal || eventType == "response.failed"
			}
			if normalizedData, normalized := wire.NormalizeCompletedImageGenerationStatus(dataBytes); normalized {
				dataBytes = normalizedData
				data = string(normalizedData)
				line = "data: " + data
			}
			if err := wire.ValidateGIFImageOutputs(dataBytes); err != nil {
				wire.ParseSSEUsageBytes(dataBytes, usage)
				streamEarlyErr = err
				return
			}
			imageCounter.AddSSEData(dataBytes)
			searchCounter += options.CountSearch(dataBytes, streamSearchSeen)

			// Observe native image/search counters before tool repair, then apply
			// Codex names/arguments before client tool and namespace restoration sees
			// those output nodes.
			if correctedData, corrected := options.ToolCorrector.CorrectToolCallsInSSEBytes(dataBytes); corrected {
				dataBytes = correctedData
				data = string(correctedData)
				line = "data: " + data
				eventType = wire.EffectiveOpenAISSEEventType(dataBytes, eventType)
			}
			if imageOutput, ok := wire.ExtractImageGenerationOutputFromSSEData(dataBytes, streamSeenImages); ok {
				streamImageOutputs = append(streamImageOutputs, imageOutput)
			}
			streamDoneItems.Observe(dataBytes)
			if wire.ResponsesStreamEventMayContributeToOutput(eventType) {
				var streamEvent wire.ResponsesStreamEvent
				if err := json.Unmarshal(dataBytes, &streamEvent); err == nil {
					responseAccumulator.ProcessEvent(&streamEvent)
				}
			}
			if normalizedData, normalized := bridge.NormalizeResponsesStreamingTerminalOutput(dataBytes, responseAccumulator, streamDoneItems, streamImageOutputs); normalized {
				dataBytes = normalizedData
				data = string(normalizedData)
				line = "data: " + data
				eventType = wire.EffectiveOpenAISSEEventType(dataBytes, eventType)
			}
			restoredData, restoreErr := options.RestoreClientTools(dataBytes)
			if restoreErr != nil {
				streamEarlyErr = fmt.Errorf("restore Grok Responses client tool response: %w", restoreErr)
				return
			}
			restoredData, restoreErr = options.RestoreNamespace(restoredData)
			if restoreErr != nil {
				streamEarlyErr = fmt.Errorf("restore OpenAI namespace response: %w", restoreErr)
				return
			}
			restoredData = options.RestoreToolNames(restoredData, eventType)
			if !bytes.Equal(restoredData, dataBytes) {
				dataBytes = restoredData
				data = string(restoredData)
				line = "data: " + data
				eventType = wire.EffectiveOpenAISSEEventType(dataBytes, eventType)
			}
			if sanitizedData, sanitized := SanitizeOpenAIResponseFailedEventForClient(
				dataBytes,
				eventType,
				options.ClientOutputStarted(clientOutputStarted),
			); sanitized {
				dataBytes = sanitizedData
				data = string(sanitizedData)
				line = "data: " + data
			}
			// Model replacement follows client identity restoration and operates on
			// delivery text; provider observations already consumed the pre-replacement
			// upstream payload.
			if needModelReplace && mappedModel != "" && strings.Contains(line, mappedModel) {
				line = wire.ReplaceModelInSSELine(line, mappedModel, originalModel)
			}
			startsClientOutput := forceFlushFailedEvent || OpenAIStreamDataStartsClientOutput(data, eventType)
			startsVisibleOutput := wire.StreamDataStartsVisibleOutput(data, eventType)
			semanticOutputSeen = semanticOutputSeen || startsVisibleOutput
			startsFirstFlush := forceFlushFailedEvent || OpenAIStreamDataStartsFirstFlush(data, eventType)
			if stageFirstOutput {
				eventStartsClientOutput = eventStartsClientOutput || startsClientOutput
				eventStartsFirstFlush = eventStartsFirstFlush || startsFirstFlush
				if startsClientOutput {
					firstOutputScanGuard.Store(false)
				}
			}
			if startsClientOutput && !wire.OpenAIStreamEventTypeIsTerminal(eventType) {
				responsesSemanticOutputSeen = true
			}
			// Native empty completion before content is a recoverable missing-output
			// outcome; output, usage, error, or prior accepted content keeps it out of
			// this classification.
			if options.NativeOpenAI &&
				(eventType == "response.completed" || eventType == "response.done") &&
				!sawFailedEvent && !responsesSemanticOutputSeen && !clientOutputStarted &&
				wire.OpenAIResponsesCompletedEventIsEmpty(dataBytes, usage) {
				sawTerminalEvent = true
				streamEarlyErr = options.EmptyCompleted(upstreamRequestID)
				return
			}

			if pendingLifecycleHeader != "" {
				header := pendingLifecycleHeader
				if eventType != strings.TrimSpace(pendingSSEEventType) {
					header = "event: " + eventType
				}
				line = header + "\n" + line
				pendingLifecycleHeader = ""
			}
			suppressCurrentEvent = suppressCurrentEvent || duplicateTerminal
			// Delivery failure suppresses subsequent writes while usage and terminal
			// observation remain active; suppressed bare errors and duplicate terminals
			// also retain that accounting path.
			if !clientDisconnected && !failureDelivered && !suppressCurrentEvent {
				shouldFlush := queueDrained && (clientOutputStarted || startsClientOutput)
				if !firstOutputFlushed && startsFirstFlush {
					// Flush semantic progress promptly so initial buffering does not delay downstream delivery.
					shouldFlush = true
				}
				eventShouldFlush = eventShouldFlush || shouldFlush
				if _, err := writePendingString(line); err != nil {
					handlePendingWriteError(err)
				} else if _, err := writePendingString("\n"); err != nil {
					handlePendingWriteError(err)
				} else {
					eventInProgress = true
				}
			}

			if !guardFirstOutput && !firstOutputFlushed && startsFirstFlush {
				// Semantic progress ends the initial flush guard for subsequent stream events.
				firstOutputFlushed = true
				stopFirstOutputTimer()
			}
			hasObservedUsage = wire.ParseSSEUsageBytesWithType(dataBytes, eventType, usage) || hasObservedUsage
			return
		}

		// An event separator completes guarded state and its flush decision;
		// suppressing a whole event clears pending progress so headers and content remain paired.
		if stageFirstOutput && line == "" {
			pendingLifecycleHeader = ""
			pendingSSEEventType = ""
			if suppressCurrentEvent {
				suppressCurrentEvent = false
				terminalFailurePending = false
				eventInProgress = false
				eventStartsClientOutput = false
				eventShouldFlush = false
				return
			}
			if failureDelivered {
				terminalFailurePending = false
				eventInProgress = false
				eventStartsClientOutput = false
				eventShouldFlush = false
				return
			}
			if !clientDisconnected {
				if _, err := writePendingString("\n"); err != nil {
					handlePendingWriteError(err)
				}
			}
			if streamEarlyErr == nil {
				completeGuardedEvent(queueDrained)
			}
			if terminalFailurePending && streamEarlyErr == nil {
				terminalFailurePending = false
				failureDelivered = true
			}
			return
		}
		// Non-guarded streams retain upstream's event-boundary flushing: a
		// queue-drain flush must never split an open SSE event.
		shouldFlush := false
		if line == "" {
			pendingLifecycleHeader = ""
			pendingSSEEventType = ""
			if suppressCurrentEvent {
				suppressCurrentEvent = false
				terminalFailurePending = false
				eventInProgress = false
				eventShouldFlush = false
				return
			}
			shouldFlush = eventShouldFlush || (queueDrained && clientOutputStarted)
			eventShouldFlush = false
			if failureDelivered {
				terminalFailurePending = false
			}
		}
		if !clientDisconnected && !failureDelivered && !suppressCurrentEvent {
			if _, err := writePendingString(line); err != nil {
				handlePendingWriteError(err)
			} else if _, err := writePendingString("\n"); err != nil {
				handlePendingWriteError(err)
			} else {
				eventInProgress = line != ""
				if shouldFlush {
					if err := flushBuffered(); err != nil {
						clientDisconnected = true
						options.Logf("Client disconnected during streaming flush, continuing to drain upstream for billing")
					} else {
						clientOutputStarted = true
					}
				}
			}
			if line == "" && terminalFailurePending && streamEarlyErr == nil {
				terminalFailurePending = false
				failureDelivered = true
			}
		}
	}

	var heartbeatFilter upstream.SSEHeartbeatLineFilter
	// Parse only retained lines so upstream heartbeats cannot close the first-output
	// retry window. Scanner arrival timestamps still include all upstream reads.
	processSSELine := func(line string, queueDrained bool) {
		for _, filtered := range heartbeatFilter.Push([]byte(line + "\n")) {
			processOutputLine(strings.TrimSuffix(string(filtered), "\n"), queueDrained)
		}
	}
	flushHeartbeatFrames = func() {
		for _, filtered := range heartbeatFilter.Flush() {
			processOutputLine(strings.TrimSuffix(string(filtered), "\n"), true)
		}
	}

	// With all timers disabled, direct scanning uses the same processing and
	// failure machinery while returning its pooled buffer only after the synchronous reader exits.
	if streamInterval <= 0 && firstOutputTimeout <= 0 {
		defer httpclient.PutSSEScannerBuf64K(scanBuf)
		for documentScanner.Scan() {
			processSSELine(documentScanner.Text(), true)
			if streamEarlyErr != nil {
				return resultWithUsage(), streamEarlyErr
			}
		}
		if result, err, done := handleScanErr(documentScanner.Err()); done {
			return result, err
		}
		return finalizeStream()
	}

	// scanEvent transfers a scanner token or failure to the timed consumer.
	type scanEvent struct {
		line string // upstream scanner token
		err  error  // upstream scanner failure
		// processed is allocated while the guard is active; the consumer closes
		// it once after handling this event, permitting the producer to read onward.
		processed chan struct{}
	}
	// First-output timeout selects a one-entry queue, while other timed modes
	// use sixteen entries. Active staging adds a per-token acknowledgment so producer
	// read-ahead cannot pass a guard decision still being handled by the consumer.
	events := make(chan scanEvent, OpenAIFirstOutputEventQueueSize(guardFirstOutput))
	done := make(chan struct{})
	sendEvent := func(ev scanEvent) bool {
		if firstOutputScanGuard.Load() {
			ev.processed = make(chan struct{})
		}
		select {
		case events <- ev:
		case <-done:
			return false
		}
		if ev.processed == nil {
			return true
		}
		select {
		case <-ev.processed:
			return true
		case <-done:
			return false
		}
	}
	markEventProcessed := func(ev scanEvent) {
		if ev.processed != nil {
			close(ev.processed)
		}
	}
	var lastReadAt int64
	atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
	// The producer owns scanner buffer return and channel closure; publishing
	// closure precedes its deferred buffer return, so closure alone is not a goroutine join.
	go func(scanBuf *httpclient.SSEScannerBuf64K) {
		defer httpclient.PutSSEScannerBuf64K(scanBuf)
		defer close(events)
		for documentScanner.Scan() {
			atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
			if !sendEvent(scanEvent{line: documentScanner.Text()}) {
				return
			}
		}
		if err := documentScanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}(scanBuf)
	// Release producer sends and acknowledgment waits on exit; ordinary body
	// read interruption still depends on executor closure except the explicit
	// timeout branches below.
	defer close(done)

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if stageFirstOutput && eventInProgress {
					// Dispatch the last staged event at EOF without adding a synthetic blank line.
					completeGuardedEvent(true)
				}
				return finalizeStream()
			}
			if result, err, done := handleScanErr(ev.err); done {
				markEventProcessed(ev)
				return result, err
			}
			processSSELine(ev.line, len(events) == 0)
			markEventProcessed(ev)
			if streamEarlyErr != nil {
				return resultWithUsage(), streamEarlyErr
			}

		case <-intervalCh:
			if failureDelivered {
				return resultWithUsage(), fmt.Errorf("upstream response failed: %s", failedMessage)
			}
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if codexFailureTerminal && sawBareError && !sawResponseFailed {
				_ = resp.Body.Close()
				return finalizeStream()
			}
			if clientDisconnected {
				return resultWithUsage(), fmt.Errorf("stream usage incomplete after timeout")
			}
			options.Logf("Stream data interval timeout: provider=%d model=%s interval=%s", options.ProviderID, originalModel, streamInterval)
			// Record timeout policy before optional Grok recovery, keeping its
			// provider observation present whether the attempt fails over or emits a client error.
			options.StreamTimeout(originalModel)
			// Grok cooldown precedes pre-output eligibility. If output already
			// started, retain accounting and finish through the stream-timeout error
			// path rather than replay.
			if options.GrokIdlePolicy {
				options.IdleCooldown()
				if !options.ClientOutputStarted(clientOutputStarted) && !eventShouldFlush {
					_ = resp.Body.Close()
					return resultWithUsage(), options.IdleFailover(streamInterval)
				}
			}
			if synthesizeBareErrorFailure() {
				flushPending("Client disconnected during bare error timeout flush, returning collected usage")
				return resultWithUsage(), fmt.Errorf("upstream response failed: %s", failedMessage)
			}
			sendErrorEvent("stream_timeout")
			return resultWithUsage(), fmt.Errorf("stream data interval timeout")

		case <-firstOutputCh:
			// Closing Body releases blocked scanning; draining and acknowledging
			// queued tokens lets the producer publish channel closure before this deadline return.
			if firstOutputProgressObserved {
				stopFirstOutputTimer()
				continue
			}
			if synthesizeBareErrorFailure() {
				flushPending("Client disconnected during bare error first-output timeout flush, returning collected usage")
				_ = resp.Body.Close()
				for ev := range events {
					markEventProcessed(ev)
				}
				return resultWithUsage(), fmt.Errorf("upstream response failed: %s", failedMessage)
			}
			_ = resp.Body.Close()
			for ev := range events {
				markEventProcessed(ev)
			}
			return resultWithUsage(), options.FirstOutputError(startTime, originalModel, reasoningEffort,
				firstOutputTimeout, "semantic_output", resp.Header,
			)
		}
	}
}
