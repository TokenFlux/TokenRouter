package openai

import (
	"strings"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// response_stream_lifecycle.go coordinates SSE lifecycle normalization and
// bare-error pairing across standard and passthrough Responses stream readers.
// Both HTTP SSE readers normalize bare Response objects, legacy response.done
// events, and duplicate terminal frames while buffering lifecycle event headers
// so renamed or suppressed terminal payloads cannot leave orphan event lines in
// downstream streams.
//
// In Codex terminal mode, a standalone error frame is held out of downstream
// output until a later terminal resolves the stream. A subsequent
// response.completed frame clears the buffered bare error and deferred provider
// side effects as a recovered turn, whereas response.failed or end-of-stream
// synthesis commits the failure and flushes deferred provider observations once.

// responseStreamLifecycleTracker tracks per-stream SSE header pairing,
// lifecycle normalization, and Codex bare-error resolution for one HTTP
// Responses stream attempt.
type responseStreamLifecycleTracker struct {
	codexFailureTerminal bool                             // whether standalone error frames wait for a terminal resolution
	normalizer           wire.ResponseLifecycleNormalizer // stateful normalizer for bare objects, legacy done, and duplicate terminals

	// pendingSSEEventType and pendingLifecycleHeader pair an incoming event:
	// header with its following data: line until normalization selects the final
	// event name or an empty separator line resets the frame.
	pendingSSEEventType    string // raw event name from the current SSE frame header
	pendingLifecycleHeader string // buffered lifecycle header line awaiting normalized data

	// suppressCurrentEvent, sawBareError, sawResponseFailed, sawFailedEvent, and
	// terminalFailurePending coordinate downstream suppression and terminal
	// failure latching across multi-line SSE frames and blank-line separators.
	suppressCurrentEvent   bool // whether lines in the current SSE frame are withheld downstream
	sawBareError           bool // whether a standalone error event is awaiting terminal resolution
	sawResponseFailed      bool // whether an authoritative or synthesized response.failed occurred
	sawFailedEvent         bool // whether the stream holds an unresolved failure verdict
	terminalFailurePending bool // whether the current failure frame awaits its blank-line separator

	// bareErrorPayload, bareErrorSideEffectsPending, failedPayload, and
	// failedMessage retain failure evidence for deferred provider side effects,
	// synthetic response.failed emission, and cyber-policy warning wrapping.
	bareErrorPayload            []byte // copied JSON bytes of a withheld standalone error frame
	bareErrorSideEffectsPending bool   // whether provider terminal side effects were deferred on bare error
	failedPayload               []byte // copied JSON bytes of the active terminal failure frame
	failedMessage               string // normalized error message for the active failure verdict
}

// newResponseStreamLifecycleTracker constructs per-stream lifecycle and
// bare-error state for one HTTP Responses SSE reader.
func newResponseStreamLifecycleTracker(codexFailureTerminal bool) *responseStreamLifecycleTracker {
	return &responseStreamLifecycleTracker{codexFailureTerminal: codexFailureTerminal}
}

// ObserveEventHeader inspects one SSE line for an event header and reports
// whether the caller should hold that line until normalized data arrives.
// Non-header lines return false without mutating frame state, while lifecycle
// headers are buffered so renamed or duplicate terminals stay paired with data.
func (t *responseStreamLifecycleTracker) ObserveEventHeader(line string) bool {
	eventType, ok := wire.ExtractSSEEventLine(line)
	if !ok {
		return false
	}
	t.pendingSSEEventType = eventType
	trimmed := strings.TrimSpace(eventType)
	t.suppressCurrentEvent = t.shouldSuppressEvent(trimmed)
	if wire.IsResponseLifecycleEvent(trimmed) {
		t.pendingLifecycleHeader = line
		return true
	}
	return false
}

// RawEventType resolves the effective SSE event type for dataBytes using any
// pending event header captured on the current frame.
func (t *responseStreamLifecycleTracker) RawEventType(dataBytes []byte) string {
	return wire.EffectiveOpenAISSEEventType(dataBytes, t.pendingSSEEventType)
}

// NormalizeData normalizes lifecycle payloads against rawEventType and reports
// the resulting JSON bytes, canonical event type, and duplicate-terminal flag.
func (t *responseStreamLifecycleTracker) NormalizeData(dataBytes []byte, rawEventType string) ([]byte, string, bool) {
	return t.normalizer.Normalize(dataBytes, rawEventType)
}

// ResolveNormalizedEvent reconciles retained bare-error state and downstream
// suppression after lifecycle and tool normalizations settle eventType.
//
// In Codex terminal mode, a standalone error event can precede a valid
// response.completed terminal when upstream recovers mid-turn. Clearing the
// buffered bare error, failure verdict, and deferred provider side effects here
// lets the completed terminal pass downstream and prevents end-of-stream logic
// from synthesizing a contradictory response.failed event.
func (t *responseStreamLifecycleTracker) ResolveNormalizedEvent(eventType string) {
	if t.codexFailureTerminal && t.sawBareError && !t.sawResponseFailed && eventType == "response.completed" {
		t.sawBareError = false
		t.sawFailedEvent = false
		t.terminalFailurePending = false
		t.suppressCurrentEvent = false
		t.bareErrorPayload = nil
		t.bareErrorSideEffectsPending = false
		t.failedPayload = nil
		t.failedMessage = ""
	}
	// Recompute suppression from the normalized event name so legacy response.done
	// frames normalized to response.failed are not hidden by earlier header checks.
	t.suppressCurrentEvent = t.shouldSuppressEvent(eventType)
}

// BeginFailureEvent records a failure frame's bare-error or response.failed
// identity and extracts a non-empty failure message before pre-output recovery
// checks run. Callers invoke CommitFailureEvent once pre-output failover and
// passthrough error rules decline to terminate the stream early.
func (t *responseStreamLifecycleTracker) BeginFailureEvent(eventType string, dataBytes []byte) string {
	if t.codexFailureTerminal && eventType == "error" {
		t.sawBareError = true
		t.bareErrorPayload = append(t.bareErrorPayload[:0], dataBytes...)
		t.suppressCurrentEvent = true
	} else if t.codexFailureTerminal && eventType == "response.failed" {
		t.sawResponseFailed = true
	}
	t.failedMessage = ExtractOpenAISSEErrorMessage(dataBytes)
	if t.failedMessage == "" {
		t.failedMessage = "Upstream response failed"
	}
	return t.failedMessage
}

// RecordFailureSideEffects either defers provider terminal side effects on a
// Codex bare error or runs them immediately for an authoritative failure frame.
// Pre-output and cyber-flagged frames skip provider side effects here because
// their dedicated failover and moderation paths own provider state updates.
func (t *responseStreamLifecycleTracker) RecordFailureSideEffects(eventType string, outputStarted, cyberHit bool, run func()) {
	if !outputStarted || cyberHit {
		return
	}
	if t.codexFailureTerminal && eventType == "error" {
		t.bareErrorSideEffectsPending = true
		return
	}
	run()
	t.bareErrorSideEffectsPending = false
}

// MarkEarlyFailure latches sawFailedEvent when pre-output recovery or an error
// passthrough rule aborts the stream before CommitFailureEvent runs.
func (t *responseStreamLifecycleTracker) MarkEarlyFailure() {
	t.sawFailedEvent = true
}

// CommitFailureEvent latches the active failure payload and separator-pending
// flag once a failure frame proceeds to downstream delivery or bare-error hold.
func (t *responseStreamLifecycleTracker) CommitFailureEvent(eventType string, dataBytes []byte) {
	t.sawFailedEvent = true
	t.failedPayload = append(t.failedPayload[:0], dataBytes...)
	t.terminalFailurePending = !t.codexFailureTerminal || eventType == "response.failed"
}

// ApplyLifecycleHeaderAndDuplicate prepends any buffered lifecycle event header,
// rewritten when normalization changed eventType, and folds duplicateTerminal
// into current-frame suppression.
func (t *responseStreamLifecycleTracker) ApplyLifecycleHeaderAndDuplicate(line, eventType string, duplicateTerminal bool) string {
	if t.pendingLifecycleHeader != "" {
		header := t.pendingLifecycleHeader
		if eventType != strings.TrimSpace(t.pendingSSEEventType) {
			header = "event: " + eventType
		}
		line = header + "\n" + line
		t.pendingLifecycleHeader = ""
	}
	t.suppressCurrentEvent = t.suppressCurrentEvent || duplicateTerminal
	return line
}

// BeginEventSeparator resets per-frame header state at a blank SSE line and
// reports whether the just-ended frame was suppressed from downstream output.
// Already delivered failures also clear any stale terminalFailurePending latch.
func (t *responseStreamLifecycleTracker) BeginEventSeparator(failureDelivered bool) bool {
	t.pendingLifecycleHeader = ""
	t.pendingSSEEventType = ""
	if t.suppressCurrentEvent {
		t.suppressCurrentEvent = false
		t.terminalFailurePending = false
		return true
	}
	if failureDelivered {
		t.terminalFailurePending = false
	}
	return false
}

// ConsumeTerminalFailurePending clears and reports whether the completed SSE
// frame delivered a terminal failure event downstream.
func (t *responseStreamLifecycleTracker) ConsumeTerminalFailurePending() bool {
	if !t.terminalFailurePending {
		return false
	}
	t.terminalFailurePending = false
	return true
}

// HasUnresolvedBareError reports whether a withheld Codex bare error still
// awaits terminal resolution or synthetic response.failed emission.
func (t *responseStreamLifecycleTracker) HasUnresolvedBareError() bool {
	return t.codexFailureTerminal && t.sawBareError && !t.sawResponseFailed
}

// FlushBareErrorSideEffects invokes run once if provider terminal side effects
// were deferred on an unresolved bare error.
func (t *responseStreamLifecycleTracker) FlushBareErrorSideEffects(run func(payload []byte, message string)) {
	if !t.bareErrorSideEffectsPending {
		return
	}
	run(t.bareErrorPayload, t.failedMessage)
	t.bareErrorSideEffectsPending = false
}

// MarkBareErrorSynthesized records that a synthetic response.failed frame was
// emitted from the retained bare-error payload.
func (t *responseStreamLifecycleTracker) MarkBareErrorSynthesized() {
	t.failedPayload = append(t.failedPayload[:0], t.bareErrorPayload...)
	t.sawResponseFailed = true
	t.sawFailedEvent = true
}

// shouldSuppressEvent reports whether an event of eventType must be withheld
// while Codex bare-error pairing waits for an authoritative failure terminal.
func (t *responseStreamLifecycleTracker) shouldSuppressEvent(eventType string) bool {
	return t.codexFailureTerminal &&
		(eventType == "error" || (t.sawBareError && !t.sawResponseFailed && eventType != "response.failed"))
}
