package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResponseStreamLifecycleTrackerClearsBareErrorOnCompleted verifies that a
// normalized response.completed frame resolves a withheld Codex bare error,
// drops deferred provider side effects, and allows downstream delivery.
func TestResponseStreamLifecycleTrackerClearsBareErrorOnCompleted(t *testing.T) {
	tracker := newResponseStreamLifecycleTracker(true)

	require.False(t, tracker.ObserveEventHeader("event: error"))
	require.True(t, tracker.suppressCurrentEvent)

	errPayload := []byte(`{"type":"error","error":{"message":"transient warning"}}`)
	_, errType, dup := tracker.NormalizeData(errPayload, tracker.RawEventType(errPayload))
	require.False(t, dup)
	tracker.ResolveNormalizedEvent(errType)
	msg := tracker.BeginFailureEvent(errType, errPayload)
	require.Equal(t, "transient warning", msg)
	sideEffects := 0
	tracker.RecordFailureSideEffects(errType, true, false, func() { sideEffects++ })
	tracker.CommitFailureEvent(errType, errPayload)
	require.Zero(t, sideEffects)
	require.True(t, tracker.HasUnresolvedBareError())
	require.True(t, tracker.BeginEventSeparator(false))

	require.True(t, tracker.ObserveEventHeader("event: response.done"))
	donePayload := []byte(`{"type":"response.done","response":{"id":"resp_ok","status":"completed","output":[]}}`)
	normBytes, normType, dup := tracker.NormalizeData(donePayload, tracker.RawEventType(donePayload))
	require.False(t, dup)
	require.Equal(t, "response.completed", normType)
	tracker.ResolveNormalizedEvent(normType)

	require.False(t, tracker.HasUnresolvedBareError())
	require.False(t, tracker.sawFailedEvent)
	require.False(t, tracker.suppressCurrentEvent)
	require.Empty(t, tracker.failedMessage)
	line := tracker.ApplyLifecycleHeaderAndDuplicate("data: "+string(normBytes), normType, dup)
	require.Contains(t, line, "event: response.completed\ndata: ")

	flushed := false
	tracker.FlushBareErrorSideEffects(func([]byte, string) { flushed = true })
	require.False(t, flushed)
}

// TestResponseStreamLifecycleTrackerSynthesizesUnresolvedBareError verifies
// that an unresolved Codex bare error flushes deferred side effects once and
// promotes its retained payload on EOF synthesis.
func TestResponseStreamLifecycleTrackerSynthesizesUnresolvedBareError(t *testing.T) {
	tracker := newResponseStreamLifecycleTracker(true)

	errPayload := []byte(`{"type":"error","error":{"message":"stream dropped"}}`)
	_, errType, _ := tracker.NormalizeData(errPayload, tracker.RawEventType(errPayload))
	tracker.ResolveNormalizedEvent(errType)
	msg := tracker.BeginFailureEvent(errType, errPayload)
	require.Equal(t, "stream dropped", msg)
	tracker.RecordFailureSideEffects(errType, true, false, func() {
		t.Fatal("bare error must defer immediate side effects")
	})
	tracker.CommitFailureEvent(errType, errPayload)
	require.True(t, tracker.BeginEventSeparator(false))
	require.True(t, tracker.HasUnresolvedBareError())

	var gotPayload []byte
	var gotMessage string
	flushes := 0
	tracker.FlushBareErrorSideEffects(func(payload []byte, message string) {
		flushes++
		gotPayload = append([]byte(nil), payload...)
		gotMessage = message
	})
	tracker.FlushBareErrorSideEffects(func([]byte, string) { flushes++ })
	require.Equal(t, 1, flushes)
	require.Equal(t, errPayload, gotPayload)
	require.Equal(t, "stream dropped", gotMessage)

	tracker.MarkBareErrorSynthesized()
	require.False(t, tracker.HasUnresolvedBareError())
	require.True(t, tracker.sawFailedEvent)
	require.Equal(t, errPayload, tracker.failedPayload)
}
