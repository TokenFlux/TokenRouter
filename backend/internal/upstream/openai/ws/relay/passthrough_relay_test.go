package openai_ws_v2

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// Responses terminal observation can precede client delivery or arrive during a
// disconnect drain. Local frame fixtures and controlled read gates exercise
// those timing differences alongside turn usage selection, response identity,
// model/tier evidence, and token-latency predicates; assertions distinguish
// settled observations from captured transport writes.
//
// A cancelled context can be observed before a blocked reader has returned. The
// probe holds a separate release gate while a result channel observes Relay
// completion, letting the fixture keep those milestones distinct. Delayed reads
// and EOF substitution separate transport closure from identified turn
// completion, while synthetic clocks place token observation before terminal
// duration in supplied local event sequences.

// MetricsSnapshot holds process-wide usage rejection evidence sampled for relay
// test assertions.
type MetricsSnapshot struct {
	UsageParseFailureTotal int64 // process-wide rejected usage count
}

// delayedReadFrameConn delays one delegated read to model terminal arrival
// after client closure.
type delayedReadFrameConn struct {
	// base, firstDelay, and once combine a shared transport with one synthetic
	// first-read pause. once owns that pause while later reads and all writes
	// continue through the same base transport.
	//
	// base supplies delegated I/O and closure; nil base or receiver makes
	// reads/writes return EOF and cleanup return nil.
	base FrameConn // shared fixture frame transport
	// firstDelay pauses the first read when positive; context cancellation ends
	// that pause before the same context reaches base.
	firstDelay time.Duration // first-read synthetic delay duration
	once       sync.Once     // initial read delay guard
}

// readStartSpyFrameConn signals reader entry before delegating to a shared test
// transport.
type readStartSpyFrameConn struct {
	// base is the shared delegated transport; callers initialize it before invoking
	// the readiness wrapper's read, write, or close methods.
	base FrameConn
	// started and startOnce publish reader entry once before delegation. The signal
	// establishes that reading has begun, while the base transport can still wait
	// for a frame or cancellation.
	//
	// started is initialized as an open channel by the fixture and closed by
	// ReadFrame before entering base.
	started   chan struct{} // reader entry signal channel
	startOnce sync.Once     // reader entry signal guard
}

// closeSpyFrameConn counts cleanup calls while its reads wait solely for
// context cancellation.
type closeSpyFrameConn struct {
	closeCalls atomic.Int32 // synthetic cleanup invocation count
}

// eofReplacementFrameConn substitutes a chosen close error for delegated EOF
// reads.
type eofReplacementFrameConn struct {
	// FrameConn supplies delegated frame I/O and closure; ReadFrame substitutes
	// matching EOF while preserving this transport's frame type and bytes.
	FrameConn
	// err replaces any base error matching io.EOF, including wrapped EOF; a nil
	// replacement is returned as a nil error.
	err error
}

// cancelJoinProbeFrameConn separates reader entry, cancellation observation,
// and fixture-controlled completion for join assertions.
type cancelJoinProbeFrameConn struct {
	// readStarted, readCanceled, allowReturn, and readReturned separate read entry,
	// cancellation notice, release, and return milestones. The probe can observe
	// cancellation while still blocking on fixture release, which lets Relay's join
	// be tested independently.
	readStarted  chan struct{} // reader entry milestone channel
	readCanceled chan struct{} // cancellation observation milestone channel
	// allowReturn belongs to the fixture, which closes it after cancellation to
	// release the held read; Close leaves it untouched.
	allowReturn chan struct{} // fixture-controlled reader release channel
	// readReturned closes immediately before the read method returns after release;
	// authoritative worker completion still comes from Relay's upstreamDone join.
	readReturned chan struct{} // reader return milestone channel
	// startOnce, cancelOnce, and returnOnce guard their corresponding milestone
	// channel closures across read invocations. The shared gates remain valid even
	// if a transport method is called more than once.
	startOnce  sync.Once // reader entry closure guard
	cancelOnce sync.Once // cancellation notice closure guard
	returnOnce sync.Once // return milestone closure guard
}

// errorOnWriteFrameConn rejects every write to isolate initial upstream write
// failure.
type errorOnWriteFrameConn struct{}

// SnapshotMetrics samples the process-wide rejected-usage counter for relay
// test assertions.
func SnapshotMetrics() MetricsSnapshot {
	return MetricsSnapshot{
		UsageParseFailureTotal: passthroughUsageParseFailureTotal.Load(),
	}
}

// TestRunClientToUpstream_ErrorPaths exercises client EOF and failed upstream
// writes with direct worker calls. It checks exit stage/gracefulness,
// successful forwarding count, and trace callback activity.
func TestRunClientToUpstream_ErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("read client eof", func(t *testing.T) {
		t.Parallel()

		// Give this directly invoked worker a buffered final-signal slot, because the
		// fixture begins receiving only after the forwarding call has returned.
		exitCh := make(chan relayExitSignal, 1)
		runClientToUpstream(
			context.Background(),
			newPassthroughTestFrameConn(nil, true),
			nil,
			func(_ coderws.MessageType, _ []byte) error { return nil },
			func() {},
			nil,
			nil,
			exitCh,
		)
		sig := <-exitCh
		require.Equal(t, "read_client", sig.stage)
		require.True(t, sig.graceful)
	})

	t.Run("write upstream failed", func(t *testing.T) {
		t.Parallel()

		// Give this directly invoked worker a buffered final-signal slot, because the
		// fixture begins receiving only after the forwarding call has returned.
		exitCh := make(chan relayExitSignal, 1)
		runClientToUpstream(
			context.Background(),
			newPassthroughTestFrameConn([]passthroughTestFrame{
				{msgType: coderws.MessageText, payload: []byte(`{"x":1}`)},
			}, true),
			nil,
			func(_ coderws.MessageType, _ []byte) error { return errors.New("boom") },
			func() {},
			nil,
			nil,
			exitCh,
		)
		sig := <-exitCh
		require.Equal(t, "write_upstream", sig.stage)
		require.False(t, sig.graceful)
	})

	t.Run("forwarded counter and trace callback", func(t *testing.T) {
		t.Parallel()

		// Give this directly invoked worker a buffered final-signal slot, because the
		// fixture begins receiving only after the forwarding call has returned.
		exitCh := make(chan relayExitSignal, 1)
		forwarded := &atomic.Int64{}
		traces := make([]RelayTraceEvent, 0, 2)
		runClientToUpstream(
			context.Background(),
			newPassthroughTestFrameConn([]passthroughTestFrame{
				{msgType: coderws.MessageText, payload: []byte(`{"x":1}`)},
			}, true),
			nil,
			func(_ coderws.MessageType, _ []byte) error { return nil },
			func() {},
			forwarded,
			func(event RelayTraceEvent) {
				traces = append(traces, event)
			},
			exitCh,
		)
		sig := <-exitCh
		require.Equal(t, "read_client", sig.stage)
		require.Equal(t, int64(1), forwarded.Load())
		require.NotEmpty(t, traces)
	})
}

// TestRunUpstreamToClient_ErrorAndDropPaths exercises upstream EOF, client
// write failure, and terminal draining with downstream drops enabled. It checks
// exit stages, graceful drain termination, and the dropped-frame count.
func TestRunUpstreamToClient_ErrorAndDropPaths(t *testing.T) {
	t.Parallel()

	t.Run("read upstream eof", func(t *testing.T) {
		t.Parallel()

		// Buffer the direct worker's exit so EOF, rejection, or drain completion can
		// return before the fixture starts inspecting its signal.
		exitCh := make(chan relayExitSignal, 1)
		drop := &atomic.Bool{}
		drop.Store(false)
		runUpstreamToClient(
			context.Background(),
			newPassthroughTestFrameConn(nil, true),
			func(_ coderws.MessageType, _ []byte) error { return nil },
			time.Now(),
			time.Now,
			&relayState{},
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			drop,
			nil,
			nil,
			func() {},
			nil,
			exitCh,
		)
		sig := <-exitCh
		require.Equal(t, "read_upstream", sig.stage)
		require.True(t, sig.graceful)
	})

	t.Run("write client failed", func(t *testing.T) {
		t.Parallel()

		// Buffer the direct worker's exit so EOF, rejection, or drain completion can
		// return before the fixture starts inspecting its signal.
		exitCh := make(chan relayExitSignal, 1)
		drop := &atomic.Bool{}
		drop.Store(false)
		runUpstreamToClient(
			context.Background(),
			newPassthroughTestFrameConn([]passthroughTestFrame{
				{msgType: coderws.MessageText, payload: []byte(`{"type":"response.output_text.delta","delta":"x"}`)},
			}, true),
			func(_ coderws.MessageType, _ []byte) error { return errors.New("write failed") },
			time.Now(),
			time.Now,
			&relayState{},
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			drop,
			nil,
			nil,
			func() {},
			nil,
			exitCh,
		)
		sig := <-exitCh
		require.Equal(t, "write_client", sig.stage)
	})

	t.Run("drop downstream and stop on terminal", func(t *testing.T) {
		t.Parallel()

		// Buffer the direct worker's exit so EOF, rejection, or drain completion can
		// return before the fixture starts inspecting its signal.
		exitCh := make(chan relayExitSignal, 1)
		drop := &atomic.Bool{}
		drop.Store(true)
		dropped := &atomic.Int64{}
		runUpstreamToClient(
			context.Background(),
			newPassthroughTestFrameConn([]passthroughTestFrame{
				{
					msgType: coderws.MessageText,
					payload: []byte(`{"type":"response.completed","response":{"id":"resp_drop","usage":{"input_tokens":1,"output_tokens":1}}}`),
				},
			}, true),
			func(_ coderws.MessageType, _ []byte) error { return nil },
			time.Now(),
			time.Now,
			&relayState{},
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			drop,
			nil,
			dropped,
			func() {},
			nil,
			exitCh,
		)
		sig := <-exitCh
		require.Equal(t, "drain_terminal", sig.stage)
		require.True(t, sig.graceful)
		require.Equal(t, int64(1), dropped.Load())
	})
}

// TestRunIdleWatchdog_NoTimeoutWhenDisabled starts a watchdog with a zero idle
// budget. No exit signal may arrive during the observation window, confirming
// disabled idle detection.
func TestRunIdleWatchdog_NoTimeoutWhenDisabled(t *testing.T) {
	t.Parallel()

	exitCh := make(chan relayExitSignal, 1)
	startAt := time.Now()
	lastActivity := &atomic.Int64{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run disabled detection with an available exit slot while observing for
	// unexpected publication; the configured zero budget should end this worker
	// without a signal.
	go runIdleWatchdog(ctx, startAt, time.Now, 0, lastActivity, nil, exitCh)
	select {
	case <-exitCh:
		t.Fatal("unexpected idle timeout signal")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestRunIdleWatchdog_UsesElapsedOffsetFromRelayStart advances virtual time
// with refreshed activity offsets before exceeding the idle budget. Only the
// post-budget tick emits an idle_timeout exit signal.
func TestRunIdleWatchdog_UsesElapsedOffsetFromRelayStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exitCh := make(chan relayExitSignal, 1)
		startAt := time.Now()
		lastActivity := &atomic.Int64{}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go runIdleWatchdog(ctx, startAt, time.Now, 3*time.Second, lastActivity, nil, exitCh)

		time.Sleep(2 * time.Second)
		synctest.Wait()
		select {
		case sig := <-exitCh:
			t.Fatalf("unexpected early timeout: %+v", sig)
		default:
		}

		// Refresh activity at +2s so +4s remains only 2s idle (< 3s budget).
		lastActivity.Store(int64(time.Since(startAt)))
		time.Sleep(2 * time.Second)
		synctest.Wait()
		select {
		case sig := <-exitCh:
			t.Fatalf("unexpected timeout after refreshed activity: %+v", sig)
		default:
		}

		// Advancing another 2s reaches 4s since the +2s activity offset (>= 3s budget).
		time.Sleep(2 * time.Second)
		synctest.Wait()
		sig := <-exitCh
		require.Equal(t, "idle_timeout", sig.stage)
		require.ErrorIs(t, sig.err, context.DeadlineExceeded)
	})
}

// TestHelperFunctionsCoverage supplies representative trace labels, disconnect
// errors, token names, durations, exit-channel waits, and numeric usage fields.
// It checks their concrete mapped results and rejection/default cases.
func TestHelperFunctionsCoverage(t *testing.T) {
	t.Parallel()

	require.Equal(t, "text", relayMessageTypeString(coderws.MessageText))
	require.Equal(t, "binary", relayMessageTypeString(coderws.MessageBinary))
	require.Contains(t, relayMessageTypeString(coderws.MessageType(99)), "unknown(")

	require.Equal(t, "", relayErrorString(nil))
	require.Equal(t, "x", relayErrorString(errors.New("x")))

	require.True(t, isDisconnectError(io.EOF))
	require.True(t, isDisconnectError(net.ErrClosed))
	require.True(t, isDisconnectError(context.Canceled))
	require.True(t, isDisconnectError(coderws.CloseError{Code: coderws.StatusGoingAway}))
	require.True(t, isDisconnectError(errors.New("broken pipe")))
	require.False(t, isDisconnectError(errors.New("unrelated")))

	require.True(t, isTokenEvent("response.output_text.delta"))
	require.True(t, isTokenEvent("response.output_audio.delta"))
	require.False(t, isTokenEvent("response.completed"))
	require.False(t, isTokenEvent(""))
	require.False(t, isTokenEvent("response.created"))

	require.Equal(t, 2*time.Second, minDuration(2*time.Second, 5*time.Second))
	require.Equal(t, 2*time.Second, minDuration(5*time.Second, 2*time.Second))
	require.Equal(t, 5*time.Second, minDuration(0, 5*time.Second))
	require.Equal(t, 2*time.Second, minDuration(2*time.Second, 0))

	// Queue signals before each helper call so supplied and default wait budgets
	// can read immediately, then leave the channel empty to test timeout.
	ch := make(chan relayExitSignal, 1)
	ch <- relayExitSignal{stage: "ok"}
	sig, ok := waitRelayExit(ch, 10*time.Millisecond)
	require.True(t, ok)
	require.Equal(t, "ok", sig.stage)
	ch <- relayExitSignal{stage: "ok2"}
	sig, ok = waitRelayExit(ch, 0)
	require.True(t, ok)
	require.Equal(t, "ok2", sig.stage)
	_, ok = waitRelayExit(ch, 10*time.Millisecond)
	require.False(t, ok)

	n, ok := parseUsageIntField(gjson.Get(`{"n":3}`, "n"), true)
	require.True(t, ok)
	require.Equal(t, 3, n)
	_, ok = parseUsageIntField(gjson.Get(`{"n":"x"}`, "n"), true)
	require.False(t, ok)
	n, ok = parseUsageIntField(gjson.Result{}, false)
	require.True(t, ok)
	require.Equal(t, 0, n)
	_, ok = parseUsageIntField(gjson.Result{}, true)
	require.False(t, ok)
}

// TestParseUsageAndEnrichCoverage supplies malformed, missing, and complete
// usage snapshots before finalization. Assertions check zero session totals
// before finalization, five valid finalized buckets, duration copying, and
// later progress remaining outside input totals.
func TestParseUsageAndEnrichCoverage(t *testing.T) {
	t.Parallel()

	state := &relayState{}
	parseUsageAndAccumulate(state, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":"bad"}}}`), "response.completed", nil)
	require.Equal(t, 0, state.usage.InputTokens)

	parseUsageAndAccumulate(
		state,
		[]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":9,"output_tokens":"bad","input_tokens_details":{"cached_tokens":2}}}}`),
		"response.completed",
		nil,
	)
	require.Equal(t, 0, state.usage.InputTokens, "部分字段解析失败时不应累加 usage")
	require.Equal(t, 0, state.usage.OutputTokens)
	require.Equal(t, 0, state.usage.CacheReadInputTokens)

	parseUsageAndAccumulate(
		state,
		[]byte(`{"type":"response.completed","response":{"usage":{"input_tokens_details":{"cached_tokens":2}}}}`),
		"response.completed",
		nil,
	)
	require.Equal(t, 0, state.usage.InputTokens, "必填 usage 字段缺失时不应累加 usage")
	require.Equal(t, 0, state.usage.OutputTokens)
	require.Equal(t, 0, state.usage.CacheReadInputTokens)

	parseUsageAndAccumulate(state, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":1,"input_tokens_details":{"cached_tokens":1,"cache_write_tokens":4},"output_tokens_details":{"image_tokens":3}}}}`), "response.completed", nil)
	finalizeRelayTurnUsage(state)
	require.Equal(t, 2, state.usage.InputTokens)
	require.Equal(t, 1, state.usage.OutputTokens)
	require.Equal(t, 1, state.usage.CacheReadInputTokens)
	require.Equal(t, 4, state.usage.CacheCreationInputTokens)
	require.Equal(t, 3, state.usage.ImageOutputTokens)

	result := &RelayResult{}
	enrichResult(result, state, 5*time.Millisecond)
	require.Equal(t, state.usage.InputTokens, result.Usage.InputTokens)
	require.Equal(t, state.usage.CacheCreationInputTokens, result.Usage.CacheCreationInputTokens)
	require.Equal(t, state.usage.ImageOutputTokens, result.Usage.ImageOutputTokens)
	require.Equal(t, 5*time.Millisecond, result.Duration)
	parseUsageAndAccumulate(state, []byte(`{"type":"response.in_progress","response":{"usage":{"input_tokens":9}}}`), "response.in_progress", nil)
	require.Equal(t, 2, state.usage.InputTokens)
	enrichResult(nil, state, 0)
}

// TestParseUsageAndAccumulateIncludesIndependentReasoningTokens compares output
// excluding reasoning with output already including it under the same total.
// The parsed output adds the independent count and leaves inclusive output
// unchanged.
func TestParseUsageAndAccumulateIncludesIndependentReasoningTokens(t *testing.T) {
	t.Parallel()

	state := &relayState{}
	got := parseUsageAndAccumulate(
		state,
		[]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":32,"output_tokens":9,"total_tokens":151,"output_tokens_details":{"reasoning_tokens":110}}}}`),
		"response.completed",
		nil,
	)
	require.Equal(t, 32, got.InputTokens)
	require.Equal(t, 119, got.OutputTokens)

	state = &relayState{}
	got = parseUsageAndAccumulate(
		state,
		[]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":32,"output_tokens":119,"total_tokens":151,"output_tokens_details":{"reasoning_tokens":110}}}}`),
		"response.completed",
		nil,
	)
	require.Equal(t, 119, got.OutputTokens, "inclusive Responses output must not double-count reasoning")
}

// TestParseUsageAndAccumulateAcceptsChatUsageAliases supplies prompt/completion
// counters and detail aliases in a done event. Parsed input, output,
// cache-read, and image counters must equal the finalized session snapshot.
func TestParseUsageAndAccumulateAcceptsChatUsageAliases(t *testing.T) {
	t.Parallel()

	state := &relayState{}
	got := parseUsageAndAccumulate(
		state,
		[]byte(`{"type":"response.done","response":{"usage":{"prompt_tokens":12,"completion_tokens":6,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"image_tokens":2}}}}`),
		"response.done",
		nil,
	)
	finalizeRelayTurnUsage(state)
	require.Equal(t, 12, got.InputTokens)
	require.Equal(t, 6, got.OutputTokens)
	require.Equal(t, 4, got.CacheReadInputTokens)
	require.Equal(t, 2, got.ImageOutputTokens)
	require.Equal(t, got, state.usage)
}

// TestRelayUsageTerminalWithoutUsageKeepsFallback supplies positive progress
// usage followed by a completed event with no usage object. Finalized turn and
// session counts must retain that earlier snapshot.
func TestRelayUsageTerminalWithoutUsageKeepsFallback(t *testing.T) {
	t.Parallel()

	state := &relayState{}
	parseUsageAndAccumulate(state, []byte(`{"type":"response.in_progress","usage":{"input_tokens":9,"output_tokens":4}}`), "response.in_progress", nil)
	parseUsageAndAccumulate(state, []byte(`{"type":"response.completed","response":{"id":"resp_1"}}`), "response.completed", nil)

	turnUsage := finalizeRelayTurnUsage(state)
	require.Equal(t, Usage{InputTokens: 9, OutputTokens: 4}, turnUsage)
	require.Equal(t, turnUsage, state.usage)
}

// TestRelayUsageTerminalZeroKeepsFallback follows positive progress usage with
// an all-zero completed snapshot. Both returned turn usage and accumulated
// session usage must keep the earlier positive counts.
func TestRelayUsageTerminalZeroKeepsFallback(t *testing.T) {
	t.Parallel()

	state := &relayState{}
	parseUsageAndAccumulate(state, []byte(`{"type":"response.in_progress","usage":{"input_tokens":9,"output_tokens":4}}`), "response.in_progress", nil)
	parseUsageAndAccumulate(state, []byte(`{"type":"response.completed","usage":{"input_tokens":0,"output_tokens":0}}`), "response.completed", nil)

	require.Equal(t, Usage{InputTokens: 9, OutputTokens: 4}, finalizeRelayTurnUsage(state))
	require.Equal(t, Usage{InputTokens: 9, OutputTokens: 4}, state.usage)
}

// TestRelayUsageTerminalNonZeroReplacesFallbackAsWhole follows positive
// input/output/cache progress with a terminal containing input alone and zeros
// elsewhere. Whole-snapshot replacement must clear earlier output and cache
// counts.
func TestRelayUsageTerminalNonZeroReplacesFallbackAsWhole(t *testing.T) {
	t.Parallel()

	state := &relayState{}
	parseUsageAndAccumulate(state, []byte(`{"type":"response.in_progress","usage":{"input_tokens":9,"output_tokens":4,"input_tokens_details":{"cached_tokens":2}}}`), "response.in_progress", nil)
	parseUsageAndAccumulate(state, []byte(`{"type":"response.completed","usage":{"input_tokens":3,"output_tokens":0,"input_tokens_details":{"cached_tokens":0}}}`), "response.completed", nil)

	require.Equal(t, Usage{InputTokens: 3}, finalizeRelayTurnUsage(state))
	require.Equal(t, Usage{InputTokens: 3}, state.usage)
}

// TestObserveUpstreamMessageBareErrorClearsTurnStateAndFinalizesUsageOnce binds
// a bare error to an active response while another start remains pending.
// Explicit settlement clears active state, retains the next start, and avoids
// adding prior usage after another empty error.
func TestObserveUpstreamMessageBareErrorClearsTurnStateAndFinalizesUsageOnce(t *testing.T) {
	t.Parallel()

	now := time.Unix(200, 0)
	state := &relayState{}
	state.setPendingTurnStartedAt(now.Add(-2 * time.Second))
	timing := openAIWSRelayGetOrInitTurnTiming(state, "resp_active", now.Add(-time.Second))
	require.NotNil(t, timing)
	state.setPendingTurnStartedAt(now.Add(-500 * time.Millisecond))
	parseUsageAndAccumulate(state, []byte(`{"type":"response.in_progress","usage":{"input_tokens":9,"output_tokens":4}}`), "response.in_progress", nil)

	observed := observeUpstreamMessage(
		state,
		[]byte(`{"type":"error","usage":{"input_tokens":3,"output_tokens":1},"error":{"message":"failed"}}`),
		now.Add(-3*time.Second),
		func() time.Time { return now },
		nil,
		nil,
	)

	require.False(t, observed.terminal, "bare error settlement is deferred in case response.failed follows")
	require.Equal(t, "error", observed.eventType)
	observed = finalizePendingBareError(state, now)
	require.True(t, observed.terminal)
	require.Equal(t, Usage{InputTokens: 3, OutputTokens: 1}, observed.usage)
	require.Equal(t, observed.usage, state.usage)
	require.Equal(t, Usage{}, state.turnUsage)
	require.Nil(t, state.activeTurn)
	require.Empty(t, state.turnTimingByID)
	// The error is bound to the active turn id, so settlement must not consume a
	// later pending start that belongs to the next turn.
	require.Equal(t, now.Add(-500*time.Millisecond), state.consumePendingTurnStartedAt())

	// Re-observing and settling a later terminal without usage must not re-add the prior turn.
	observeUpstreamMessage(state, []byte(`{"type":"error","error":{"message":"again"}}`), now, func() time.Time { return now }, nil, nil)
	finalizePendingBareError(state, now)
	require.Equal(t, Usage{InputTokens: 3, OutputTokens: 1}, state.usage)
}

// TestObserveUpstreamMessageErrorThenFailedSettlesUsageOnce holds an error
// after progress usage and then observes an identified failed snapshot. The
// failed terminal alone settles its counters and clears the pending error.
func TestObserveUpstreamMessageErrorThenFailedSettlesUsageOnce(t *testing.T) {
	t.Parallel()

	now := time.Unix(300, 0)
	state := &relayState{}
	state.setPendingTurnStartedAt(now.Add(-time.Second))
	openAIWSRelayGetOrInitTurnTiming(state, "resp_1", now)
	parseUsageAndAccumulate(state, []byte(`{"type":"response.in_progress","usage":{"input_tokens":9,"output_tokens":4}}`), "response.in_progress", nil)

	errorObserved := observeUpstreamMessage(
		state,
		[]byte(`{"type":"error","error":{"code":"server_is_overloaded","message":"overloaded"}}`),
		now,
		func() time.Time { return now },
		nil,
		nil,
	)
	require.False(t, errorObserved.terminal)
	require.NotNil(t, state.pendingBareError)
	require.Equal(t, Usage{}, state.usage)

	failedObserved := observeUpstreamMessage(
		state,
		[]byte(`{"type":"response.failed","response":{"id":"resp_1","usage":{"input_tokens":7,"output_tokens":2}}}`),
		now,
		func() time.Time { return now },
		nil,
		nil,
	)
	require.True(t, failedObserved.terminal)
	require.Equal(t, Usage{InputTokens: 7, OutputTokens: 2}, failedObserved.usage)
	require.Equal(t, failedObserved.usage, state.usage)
	require.Nil(t, state.pendingBareError)
}

// TestObserveUpstreamMessageBareErrorBeforeNextCompletedKeepsBothTurns
// explicitly settles an ID-less error before a later identified completion.
// Each turn retains its own usage and the session sums both snapshots.
func TestObserveUpstreamMessageBareErrorBeforeNextCompletedKeepsBothTurns(t *testing.T) {
	t.Parallel()

	now := time.Unix(400, 0)
	state := &relayState{}
	parseUsageAndAccumulate(state, []byte(`{"type":"response.in_progress","usage":{"input_tokens":5,"output_tokens":1}}`), "response.in_progress", nil)
	bare := observeUpstreamMessage(
		state,
		[]byte(`{"type":"error","error":{"message":"first turn failed"}}`),
		now,
		func() time.Time { return now },
		nil,
		nil,
	)
	require.False(t, bare.terminal)
	bare = finalizePendingBareError(state, now)
	require.True(t, bare.terminal)
	require.Equal(t, Usage{InputTokens: 5, OutputTokens: 1}, bare.usage)

	completed := observeUpstreamMessage(
		state,
		[]byte(`{"type":"response.completed","response":{"id":"resp_next","usage":{"input_tokens":3,"output_tokens":2}}}`),
		now,
		func() time.Time { return now },
		nil,
		nil,
	)
	require.True(t, completed.terminal)
	require.Equal(t, Usage{InputTokens: 3, OutputTokens: 2}, completed.usage)
	require.Equal(t, Usage{InputTokens: 8, OutputTokens: 3}, state.usage)
}

// TestOpenAICacheCreationTokensFromUsageNestedZeroWins supplies a present
// nested zero alongside a positive top-level cache-creation counter. Selection
// must return zero because nested presence has precedence.
func TestOpenAICacheCreationTokensFromUsageNestedZeroWins(t *testing.T) {
	t.Parallel()

	usage := gjson.Parse(`{"input_tokens_details":{"cache_write_tokens":0},"cache_creation_input_tokens":19}`)
	require.Zero(t, openai.OpenAICacheCreationTokensFromUsage(usage))
}

// TestEmitTurnCompleteCoverage exercises callback eligibility for progress,
// ID-less completion, bare error, and identified completion. It checks call
// counts, retained terminal usage/identity, and empty request model with nil
// state.
func TestEmitTurnCompleteCoverage(t *testing.T) {
	t.Parallel()

	// Nonterminal events do not complete a turn.
	called := 0
	emitTurnComplete(func(turn RelayTurnResult) {
		called++
	}, &relayState{requestModel: "gpt-5"}, observedUpstreamEvent{
		terminal:   false,
		eventType:  "response.output_text.delta",
		responseID: "resp_ignored",
		usage:      Usage{InputTokens: 1},
	})
	require.Equal(t, 0, called)

	// A non-error terminal event requires a response ID to complete a turn.
	emitTurnComplete(func(turn RelayTurnResult) {
		called++
	}, &relayState{requestModel: "gpt-5"}, observedUpstreamEvent{
		terminal:  true,
		eventType: "response.completed",
	})
	require.Equal(t, 0, called)

	// An ID-less error still reaches the completion callback with its own usage.
	// Keep it eligible before the later identified completion so this fixture can
	// inspect both notifications.
	var bareError RelayTurnResult
	emitTurnComplete(func(turn RelayTurnResult) {
		called++
		bareError = turn
	}, &relayState{requestModel: "gpt-5"}, observedUpstreamEvent{
		terminal:  true,
		eventType: "error",
		usage:     Usage{InputTokens: 4, OutputTokens: 1},
	})
	require.Equal(t, 1, called)
	require.Empty(t, bareError.RequestID)
	require.Equal(t, "error", bareError.TerminalEventType)
	require.Equal(t, Usage{InputTokens: 4, OutputTokens: 1}, bareError.Usage)

	// A terminal event with a response ID completes a turn; nil state leaves its request model empty.
	var got RelayTurnResult
	emitTurnComplete(func(turn RelayTurnResult) {
		called++
		got = turn
	}, nil, observedUpstreamEvent{
		terminal:   true,
		eventType:  "response.completed",
		responseID: "resp_emit",
		usage:      Usage{InputTokens: 2, OutputTokens: 3},
	})
	require.Equal(t, 2, called)
	require.Equal(t, "resp_emit", got.RequestID)
	require.Equal(t, "response.completed", got.TerminalEventType)
	require.Equal(t, 2, got.Usage.InputTokens)
	require.Equal(t, 3, got.Usage.OutputTokens)
	require.Equal(t, "", got.RequestModel)
}

// TestIsDisconnectErrorCoverage_CloseStatusesAndMessageBranches supplies
// normal, absent-status, and abnormal WebSocket closes plus reset and blank
// errors. Recognized close/reset inputs qualify, while a blank error does not.
func TestIsDisconnectErrorCoverage_CloseStatusesAndMessageBranches(t *testing.T) {
	t.Parallel()

	require.True(t, isDisconnectError(coderws.CloseError{Code: coderws.StatusNormalClosure}))
	require.True(t, isDisconnectError(coderws.CloseError{Code: coderws.StatusNoStatusRcvd}))
	require.True(t, isDisconnectError(coderws.CloseError{Code: coderws.StatusAbnormalClosure}))
	require.True(t, isDisconnectError(errors.New("connection reset by peer")))
	require.False(t, isDisconnectError(errors.New("   ")))
}

// TestIsTokenEventCoverageBranches supplies content, audio, reasoning, and tool
// event names to the timing predicate. Delta suffixes and selected done names
// qualify; structural progress and other done names do not.
func TestIsTokenEventCoverageBranches(t *testing.T) {
	t.Parallel()

	require.False(t, isTokenEvent("response.in_progress"))
	require.False(t, isTokenEvent("response.output_item.added"))
	require.True(t, isTokenEvent("response.output_audio.delta"))
	require.True(t, isTokenEvent("response.function_call_arguments.delta"))
	require.True(t, isTokenEvent("response.reasoning_summary_text.delta"))
	require.True(t, isTokenEvent("response.output_text.done"))
	require.True(t, isTokenEvent("response.function_call_arguments.done"))
	require.False(t, isTokenEvent("response.output"))
	require.False(t, isTokenEvent("response.output_audio.done"))
	require.False(t, isTokenEvent("response.content_part.done"))
	require.False(t, isTokenEvent("response.output_item.done"))
	require.False(t, isTokenEvent("response.output_text.annotation.added"))
	require.False(t, isTokenEvent("response.done"))
}

// TestTerminalAndTokenEventSetsAreDisjoint iterates completion, failure,
// incomplete, and both cancellation spellings. Each name must qualify as
// terminal while remaining outside the token-timing predicate.
func TestTerminalAndTokenEventSetsAreDisjoint(t *testing.T) {
	t.Parallel()

	for _, eventType := range []string{
		"response.completed",
		"response.done",
		"response.failed",
		"response.incomplete",
		"response.cancelled",
		"response.canceled",
	} {
		require.True(t, isTerminalEvent(eventType), eventType)
		require.False(t, isTokenEvent(eventType), eventType)
	}
}

// TestShouldParseUsageTerminalEvents iterates the completion, failure,
// incomplete, and cancellation names accepted for terminal accounting. Each
// qualifies for usage parsing, while a text delta and empty name do not.
func TestShouldParseUsageTerminalEvents(t *testing.T) {
	t.Parallel()

	for _, eventType := range []string{
		"response.completed",
		"response.done",
		"response.failed",
		"response.incomplete",
		"response.cancelled",
		"response.canceled",
	} {
		require.True(t, shouldParseUsage(eventType), eventType)
	}
	require.False(t, shouldParseUsage("response.output_text.delta"))
	require.False(t, shouldParseUsage(""))
}

// TestRelayTurnTimingHelpersCoverage creates, reuses, and deletes one
// identified timing record alongside nil-state cases. Repeated lookup retains
// its initial start, deletion returns that start, and a second deletion fails.
func TestRelayTurnTimingHelpersCoverage(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	require.Nil(t, openAIWSRelayGetOrInitTurnTiming(nil, "resp_nil", now))
	_, ok := openAIWSRelayDeleteTurnTiming(nil, "resp_nil")
	require.False(t, ok)

	state := &relayState{}
	timing := openAIWSRelayGetOrInitTurnTiming(state, "resp_a", now)
	require.NotNil(t, timing)
	require.Equal(t, now, timing.startAt)

	// Repeated lookup keeps the initial start time.
	timing2 := openAIWSRelayGetOrInitTurnTiming(state, "resp_a", now.Add(5*time.Second))
	require.NotNil(t, timing2)
	require.Equal(t, now, timing2.startAt)

	deleted, ok := openAIWSRelayDeleteTurnTiming(state, "resp_a")
	require.True(t, ok)
	require.Equal(t, now, deleted.startAt)

	_, ok = openAIWSRelayDeleteTurnTiming(state, "resp_a")
	require.False(t, ok)
}

// TestObserveUpstreamMessage_ResponseIDFallbackPolicy compares an event-level
// ID on a delta with a top-level terminal response ID. The delta creates no
// response identity, while the terminal fallback is retained.
func TestObserveUpstreamMessage_ResponseIDFallbackPolicy(t *testing.T) {
	t.Parallel()

	state := &relayState{requestModel: "gpt-5"}
	startAt := time.Unix(0, 0)
	now := startAt
	// Each callback advances five milliseconds for distinct event observation
	// timestamps.
	nowFn := func() time.Time {
		now = now.Add(5 * time.Millisecond)
		return now
	}

	// A nonterminal top-level ID identifies the event, not the response.
	observed := observeUpstreamMessage(
		state,
		[]byte(`{"type":"response.output_text.delta","id":"evt_123","delta":"hi"}`),
		startAt,
		nowFn,
		nil,
		nil,
	)
	require.False(t, observed.terminal)
	require.Equal(t, "", observed.responseID)

	// Terminal events may use a top-level ID when the response ID is absent.
	observed = observeUpstreamMessage(
		state,
		[]byte(`{"type":"response.completed","id":"resp_fallback","response":{"usage":{"input_tokens":1,"output_tokens":1}}}`),
		startAt,
		nowFn,
		nil,
		nil,
	)
	require.True(t, observed.terminal)
	require.Equal(t, "resp_fallback", observed.responseID)
}

// TestObserveUpstreamMessage_ResponseServiceTierOnlyFromTerminalEvents sends an
// early priority echo followed by a default terminal tier. Completion and
// aggregate tier use default, and a later untiered turn reports empty.
func TestObserveUpstreamMessage_ResponseServiceTierOnlyFromTerminalEvents(t *testing.T) {
	t.Parallel()

	state := &relayState{requestModel: "gpt-5.6-sol"}
	startAt := time.Unix(0, 0)
	now := startAt
	// Each callback advances five milliseconds for distinct tier observation
	// timestamps.
	nowFn := func() time.Time {
		now = now.Add(5 * time.Millisecond)
		return now
	}

	created := observeUpstreamMessage(
		state,
		[]byte(`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5.6-sol","service_tier":"priority"}}`),
		startAt,
		nowFn,
		nil,
		nil,
	)
	require.False(t, created.terminal)
	require.Equal(t, "", created.responseServiceTier)

	completed := observeUpstreamMessage(
		state,
		[]byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.6-sol","service_tier":"default","usage":{"input_tokens":1,"output_tokens":2}}}`),
		startAt,
		nowFn,
		nil,
		nil,
	)
	require.True(t, completed.terminal)
	require.Equal(t, "default", completed.responseServiceTier, "终止事件档位应覆盖早期回显的 priority")

	var turn RelayTurnResult
	emitTurnComplete(func(result RelayTurnResult) { turn = result }, state, completed)
	require.Equal(t, "default", turn.ResponseServiceTier)

	var result RelayResult
	enrichResult(&result, state, now.Sub(startAt))
	require.Equal(t, "default", result.ResponseServiceTier)

	// A later turn without a tier must not inherit the previous turn's value.
	observeUpstreamMessage(state, []byte(`{"type":"response.created","response":{"id":"resp_2","model":"gpt-5.6-sol"}}`), startAt, nowFn, nil, nil)
	second := observeUpstreamMessage(
		state,
		[]byte(`{"type":"response.completed","response":{"id":"resp_2","model":"gpt-5.6-sol","usage":{"input_tokens":3,"output_tokens":4}}}`),
		startAt,
		nowFn,
		nil,
		nil,
	)
	require.Equal(t, "", second.responseServiceTier)
}

// TestRelayResponseModelsAreIsolated interleaves two response IDs and corrects
// one model at completion. Each result retains its matching model, callback
// output carries the correction, and a later undeclared model stays empty.
func TestRelayResponseModelsAreIsolated(t *testing.T) {
	state := &relayState{requestModel: "sent"}
	start := time.Now()
	observe := func(body string) observedUpstreamEvent {
		return observeUpstreamMessage(state, []byte(body), start, time.Now, nil, nil)
	}
	observe(`{"type":"response.created","response":{"id":"a","model":"early-a"}}`)
	observe(`{"type":"response.created","response":{"id":"b","model":"model-b"}}`)
	a := observe(`{"type":"response.completed","response":{"id":"a","model":"final-a"}}`)
	b := observe(`{"type":"response.completed","response":{"id":"b"}}`)
	require.Equal(t, "final-a", a.responseModel)
	require.Equal(t, "model-b", b.responseModel)
	var turn RelayTurnResult
	emitTurnComplete(func(value RelayTurnResult) { turn = value }, state, a)
	require.Equal(t, "final-a", turn.UpstreamResponseModel)
	observe(`{"type":"response.created","response":{"id":"c"}}`)
	c := observe(`{"type":"response.completed","response":{"id":"c"}}`)
	require.Empty(t, c.responseModel)
}

// ReadFrame delegates frame reading and substitutes err when the base error
// matches io.EOF. Frame type and payload are retained, other errors pass
// through, and a nil replacement can produce a nil read error.
func (c *eofReplacementFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	msgType, payload, err := c.FrameConn.ReadFrame(ctx)
	// Preserve the delegated frame while changing EOF classification, letting the
	// fixture model a normal WebSocket handshake close after an unfinished
	// response.
	if errors.Is(err, io.EOF) {
		return msgType, payload, c.err
	}
	return msgType, payload, err
}

// ReadFrame delays the first delegated read through sync.Once and then reads
// from base. Nil receiver or base returns EOF. The first positive-delay call
// requires nonnil ctx; cancellation ends the pause but still reaches the base
// read with that context.
//
// The first caller owns the delay, and another reader entering sync.Once waits
// for that phase to finish. The drain fixture uses this pause to give client
// closure time to select draining while subsequent reads are immediate. Closing
// delegates to base and does not interrupt the delay timer by itself.
func (c *delayedReadFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if c == nil || c.base == nil {
		return coderws.MessageText, nil, io.EOF
	}
	// Delay one read to model terminal arrival after prompt client closure; the
	// context can end the pause, and subsequent reads avoid this synthetic wait.
	c.once.Do(func() {
		if c.firstDelay > 0 {
			timer := time.NewTimer(c.firstDelay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		}
	})
	return c.base.ReadFrame(ctx)
}

// WriteFrame delegates writes immediately, independently of the first-read
// delay. Nil receiver or base returns EOF; the base transport receives the
// supplied context, frame type, and payload.
func (c *delayedReadFrameConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	if c == nil || c.base == nil {
		return io.EOF
	}
	return c.base.WriteFrame(ctx, msgType, payload)
}

// Close delegates cleanup to the shared base transport and returns its error.
// Nil receiver or base returns nil, and the wrapper's first-delay timer remains
// controlled by its read context.
func (c *delayedReadFrameConn) Close() error {
	if c == nil || c.base == nil {
		return nil
	}
	return c.base.Close()
}

// ReadFrame closes the readiness channel once before delegating its read.
// Callers provide a nonnil base and open started channel; readiness means
// reader entry even when the base still blocks.
func (c *readStartSpyFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	// Expose reader entry before base can block, so the preamble assertion observes
	// startup even when the client has supplied no additional frame.
	c.startOnce.Do(func() { close(c.started) })
	return c.base.ReadFrame(ctx)
}

// WriteFrame delegates the supplied frame and context to the shared base
// transport.
func (c *readStartSpyFrameConn) WriteFrame(ctx context.Context, msgType coderws.MessageType, payload []byte) error {
	return c.base.WriteFrame(ctx, msgType, payload)
}

// Close delegates transport cleanup to base and returns the same cleanup error.
func (c *readStartSpyFrameConn) Close() error {
	return c.base.Close()
}

// ReadFrame waits for context cancellation and returns that error with an empty
// text frame. Nil context selects Background; Close counts calls but does not
// release this context-controlled read.
func (c *closeSpyFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Keep read termination tied to context cancellation, allowing Close-call
	// evidence to stay independent of whether this synthetic reader has finished.
	<-ctx.Done()
	return coderws.MessageText, nil, ctx.Err()
}

// WriteFrame reports cancellation when already observed, otherwise succeeds
// without capturing bytes. Nil context selects Background; this stub lets relay
// tests count closures independently of actual frame delivery.
func (c *closeSpyFrameConn) WriteFrame(ctx context.Context, _ coderws.MessageType, _ []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// Close increments the synthetic close-call counter and returns nil. A nil
// receiver also returns nil; pending reads remain controlled by their context
// so tests can observe relay cancellation separately.
func (c *closeSpyFrameConn) Close() error {
	if c != nil {
		// Use invocation count as evidence for repeated relay cleanup; the fake leaves
		// its reader blocked until context cancellation regardless of this increment.
		c.closeCalls.Add(1)
	}
	return nil
}

// CloseCalls samples how many synthetic cleanup calls reached this stub. Nil
// receivers report zero, and the atomic count describes method invocation
// rather than a transport read-unblocking guarantee.
func (c *closeSpyFrameConn) CloseCalls() int32 {
	if c == nil {
		return 0
	}
	return c.closeCalls.Load()
}

// newCancelJoinProbeFrameConn creates separate entry, cancellation, release,
// and completion gates for one probe. The fixture cancels the read context and
// closes allowReturn to let Relay's upstream join finish.
func newCancelJoinProbeFrameConn() *cancelJoinProbeFrameConn {
	return &cancelJoinProbeFrameConn{
		readStarted:  make(chan struct{}),
		readCanceled: make(chan struct{}),
		allowReturn:  make(chan struct{}),
		readReturned: make(chan struct{}),
	}
}

// ReadFrame announces entry, observes cancellation, and waits for the fixture's
// release before returning the context error. Callers supply a nonnil context
// and initialize every signal channel. The once guards let each milestone be
// observed through one channel closure.
//
// Close leaves this read blocked, so cancellation notice and reader completion
// remain separate observations. Closing allowReturn permits the return
// milestone immediately before the method returns. This exposes a held read to
// Relay's upstreamDone join while the fixture checks notifications and result
// arrival separately.
func (c *cancelJoinProbeFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	// Publish entry before waiting for cancellation, then hold the reader behind
	// allowReturn after cancellation notice. The separate return milestone lets the
	// fixture compare notification with Relay's joined completion.
	c.startOnce.Do(func() { close(c.readStarted) })
	<-ctx.Done()
	c.cancelOnce.Do(func() { close(c.readCanceled) })
	<-c.allowReturn
	c.returnOnce.Do(func() { close(c.readReturned) })
	return coderws.MessageText, nil, ctx.Err()
}

// WriteFrame succeeds until the supplied context has already been cancelled,
// using Background for nil. It captures no bytes, keeping the fixture focused
// on read cancellation and join ordering.
func (c *cancelJoinProbeFrameConn) WriteFrame(ctx context.Context, _ coderws.MessageType, _ []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// Close returns nil while leaving the cancellation probe's gates unchanged. The
// test releases allowReturn after cancelling the context; transport cleanup
// alone cannot finish the held reader in this fixture.
func (c *cancelJoinProbeFrameConn) Close() error { return nil }

// TestRelay_BasicRelayAndUsage sends one prepared request and one completed
// upstream frame through local transports. It checks raw frame type/JSON,
// request identity, terminal usage, absent token latency, and direction
// counters.
func TestRelay_BasicRelayAndUsage(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_123","usage":{"input_tokens":7,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}}`),
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[{"type":"input_text","text":"hello"}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	require.Nil(t, relayExit)
	require.Equal(t, "gpt-5.3-codex", result.RequestModel)
	require.Equal(t, "resp_123", result.RequestID)
	require.Equal(t, "response.completed", result.TerminalEventType)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Nil(t, result.Timings.TTFTMs())
	require.Equal(t, int64(1), result.ClientToUpstreamFrames)
	require.Equal(t, int64(1), result.UpstreamToClientFrames)
	require.Equal(t, int64(0), result.DroppedDownstreamFrames)

	upstreamWrites := upstreamConn.Writes()
	require.Len(t, upstreamWrites, 1)
	require.Equal(t, coderws.MessageText, upstreamWrites[0].msgType)
	require.JSONEq(t, string(firstPayload), string(upstreamWrites[0].payload))

	clientWrites := clientConn.Writes()
	require.Len(t, clientWrites, 1)
	require.Equal(t, coderws.MessageText, clientWrites[0].msgType)
	require.JSONEq(t, `{"type":"response.completed","response":{"id":"resp_123","usage":{"input_tokens":7,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}}`, string(clientWrites[0].payload))
}

// TestRelay_FunctionCallOutputBytesPreserved sends a function result containing
// escaped JSON through the initial request path. The captured upstream frame
// must retain its text type and exact payload bytes.
func TestRelay_FunctionCallOutputBytesPreserved(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_func","usage":{"input_tokens":1,"output_tokens":1}}}`),
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[{"type":"function_call_output","call_id":"call_abc123","output":"{\"ok\":true}"}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	require.Nil(t, relayExit)

	upstreamWrites := upstreamConn.Writes()
	require.Len(t, upstreamWrites, 1)
	require.Equal(t, coderws.MessageText, upstreamWrites[0].msgType)
	require.Equal(t, firstPayload, upstreamWrites[0].payload)
}

// TestRelay_UpstreamDisconnect closes upstream before any response ID
// establishes an active turn. Relay must return a nil exit and retain the
// initial model in its aggregate result.
func TestRelay_UpstreamDisconnect(t *testing.T) {
	t.Parallel()

	// The upstream closes before declaring a response; the client sends no additional frames.
	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn(nil, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	// EOF is graceful here because no upstream response ID established an active turn.
	require.Nil(t, relayExit, "上游 EOF 应被视为 graceful disconnect")
	require.Equal(t, "gpt-4o", result.RequestModel)
}

// TestRelay_UpstreamNormalCloseBeforeTerminalIsFailure replaces EOF with a
// normal WebSocket close after identified progress frames. Relay reports
// read_upstream failure, prior downstream writes, and no terminal observation.
func TestRelay_UpstreamNormalCloseBeforeTerminalIsFailure(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := &eofReplacementFrameConn{
		FrameConn: newPassthroughTestFrameConn([]passthroughTestFrame{
			{
				msgType: coderws.MessageText,
				payload: []byte(`{"type":"response.created","response":{"id":"resp_incomplete","status":"in_progress"}}`),
			},
			{
				msgType: coderws.MessageText,
				payload: []byte(`{"type":"response.in_progress","response":{"id":"resp_incomplete","status":"in_progress"}}`),
			},
		}, true),
		err: coderws.CloseError{Code: coderws.StatusNormalClosure},
	}

	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	require.NotNil(t, relayExit)
	require.Equal(t, "read_upstream", relayExit.Stage)
	require.False(t, relayExit.Graceful)
	require.True(t, relayExit.WroteDownstream)
	require.ErrorContains(t, relayExit.Err, "upstream websocket closed before terminal event")
	require.Empty(t, result.TerminalEventType)
	require.Len(t, clientConn.Writes(), 2)
}

// TestRelay_ClientDisconnect gives the client immediate EOF while upstream
// remains blocked. Relay must return an observable client_disconnected exit and
// retain the initial request model.
func TestRelay_ClientDisconnect(t *testing.T) {
	t.Parallel()

	// The client closes immediately while the upstream read waits for cancellation.
	clientConn := newPassthroughTestFrameConn(nil, true)
	upstreamConn := newPassthroughTestFrameConn(nil, false)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	require.NotNil(t, relayExit, "客户端 EOF 应返回可观测的中断状态")
	require.Equal(t, "client_disconnected", relayExit.Stage)
	require.Equal(t, "gpt-4o", result.RequestModel)
}

// TestRelay_ClientDisconnect_DrainCapturesLateUsage delays a completed frame
// until after immediate client EOF. Draining retains terminal identity and
// usage while counting one dropped frame and zero downstream deliveries.
func TestRelay_ClientDisconnect_DrainCapturesLateUsage(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, true)
	upstreamBase := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_drain","usage":{"input_tokens":6,"output_tokens":4,"input_tokens_details":{"cached_tokens":1}}}}`),
		},
	}, true)
	// The eighty-millisecond first-read pause gives client EOF time to select
	// draining; the four-hundred-millisecond drain budget admits the supplied
	// terminal afterward.
	upstreamConn := &delayedReadFrameConn{
		base:       upstreamBase,
		firstDelay: 80 * time.Millisecond,
	}

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		UpstreamDrainTimeout: 400 * time.Millisecond,
	})
	require.NotNil(t, relayExit)
	require.Equal(t, "client_disconnected", relayExit.Stage)
	require.Equal(t, "resp_drain", result.RequestID)
	require.Equal(t, "response.completed", result.TerminalEventType)
	require.Equal(t, 6, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.Equal(t, int64(1), result.ClientToUpstreamFrames)
	require.Equal(t, int64(0), result.UpstreamToClientFrames)
	require.Equal(t, int64(1), result.DroppedDownstreamFrames)
}

// TestRelay_IdleTimeout leaves both peers without more frames and advances its
// supplied clock past the idle budget. The returned exit must identify
// idle_timeout and retain the initial model.
func TestRelay_IdleTimeout(t *testing.T) {
	t.Parallel()

	// Neither peer sends more frames, so the relay reaches its idle timeout.
	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn(nil, false)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Keep the first five clock samples at startup time; later samples advance an
	// hour, so watchdog polling can observe timeout without waiting that wall-clock
	// duration.
	now := time.Now()
	callCount := 0
	nowFn := func() time.Time {
		callCount++
		if callCount <= 5 {
			return now
		}
		return now.Add(time.Hour)
	}

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		IdleTimeout: 2 * time.Second,
		Now:         nowFn,
	})
	require.NotNil(t, relayExit, "应因 idle timeout 退出")
	require.Equal(t, "idle_timeout", relayExit.Stage)
	require.Equal(t, "gpt-4o", result.RequestModel)
}

// TestRelay_IdleTimeoutDoesNotCloseClientOnError triggers idle shutdown using
// context-controlled close spies. It checks an idle_timeout exit, at least one
// upstream close call, and zero client close calls.
func TestRelay_IdleTimeoutDoesNotCloseClientOnError(t *testing.T) {
	t.Parallel()

	clientConn := &closeSpyFrameConn{}
	upstreamConn := &closeSpyFrameConn{}

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Keep initial clock samples at startup time and advance later samples an hour,
	// allowing the watchdog to trigger while both fake reads remain blocked.
	now := time.Now()
	callCount := 0
	nowFn := func() time.Time {
		callCount++
		if callCount <= 5 {
			return now
		}
		return now.Add(time.Hour)
	}

	_, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		IdleTimeout: 2 * time.Second,
		Now:         nowFn,
	})
	require.NotNil(t, relayExit, "应因 idle timeout 退出")
	require.Equal(t, "idle_timeout", relayExit.Stage)
	require.Zero(t, clientConn.CloseCalls(), "错误路径不应提前关闭客户端连接，交给上层决定 close code")
	require.GreaterOrEqual(t, upstreamConn.CloseCalls(), int32(1))
}

// TestRelay_JoinsUpstreamReaderBeforeReturning holds the upstream reader after
// cancellation notice. It checks no early result during its observation window,
// then cancellation after reader release with client closure untouched.
func TestRelay_JoinsUpstreamReaderBeforeReturning(t *testing.T) {
	t.Parallel()

	clientConn := &closeSpyFrameConn{}
	upstreamConn := newCancelJoinProbeFrameConn()
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan *RelayExit, 1)

	// Run Relay beside the fixture so read entry, cancellation notice, and release
	// can be controlled separately while its result waits in a buffered channel.
	go func() {
		_, relayExit := Relay(
			ctx,
			clientConn,
			upstreamConn,
			[]byte(`{"type":"response.create","model":"gpt-4o","input":[]}`),
			RelayOptions{},
		)
		resultCh <- relayExit
	}()

	select {
	case <-upstreamConn.readStarted:
	case <-time.After(time.Second):
		t.Fatal("upstream reader did not start")
	}
	// Cancel after upstream read entry, then wait for the probe's cancellation
	// notice before checking whether the relay call has returned early.
	cancel()
	select {
	case <-upstreamConn.readCanceled:
	case <-time.After(time.Second):
		t.Fatal("upstream reader did not observe relay cancellation")
	}
	// Observe fifty milliseconds of result-channel silence while allowReturn
	// remains unreleased; the next phase separately checks return after reader
	// release.
	select {
	case <-resultCh:
		t.Fatal("Relay returned before the upstream reader exited")
	case <-time.After(50 * time.Millisecond):
	}

	// Release the held read after the silence check, allowing its return milestone
	// and Relay's upstream join to precede result-channel completion.
	close(upstreamConn.allowReturn)
	select {
	case relayExit := <-resultCh:
		require.NotNil(t, relayExit)
		require.ErrorIs(t, relayExit.Err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Relay did not return after the upstream reader exited")
	}
	select {
	case <-upstreamConn.readReturned:
	default:
		t.Fatal("Relay returned before the upstream reader completion signal")
	}
	require.Zero(t, clientConn.CloseCalls(), "错误路径不应提前关闭客户端连接")
}

// TestRelay_NilConnections passes a nil client or upstream transport with a
// prepared request. Both cases must return relay_init and an error message
// identifying the missing connection.
func TestRelay_NilConnections(t *testing.T) {
	t.Parallel()

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx := context.Background()

	t.Run("nil client conn", func(t *testing.T) {
		upstreamConn := newPassthroughTestFrameConn(nil, true)
		_, relayExit := Relay(ctx, nil, upstreamConn, firstPayload, RelayOptions{})
		require.NotNil(t, relayExit)
		require.Equal(t, "relay_init", relayExit.Stage)
		require.Contains(t, relayExit.Err.Error(), "nil")
	})

	t.Run("nil upstream conn", func(t *testing.T) {
		clientConn := newPassthroughTestFrameConn(nil, true)
		_, relayExit := Relay(ctx, clientConn, nil, firstPayload, RelayOptions{})
		require.NotNil(t, relayExit)
		require.Equal(t, "relay_init", relayExit.Stage)
		require.Contains(t, relayExit.Err.Error(), "nil")
	})
}

// TestRelay_MultipleUpstreamMessages relays two text deltas followed by a
// completed usage frame. It checks all three client writes, terminal
// identity/counters, and observed first-token latency.
func TestRelay_MultipleUpstreamMessages(t *testing.T) {
	t.Parallel()

	// Relay deltas and a completion frame, including the terminal usage.
	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.output_text.delta","delta":"Hello"}`),
		},
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.output_text.delta","delta":" world"}`),
		},
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_multi","usage":{"input_tokens":10,"output_tokens":5,"input_tokens_details":{"cached_tokens":3}}}}`),
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[{"type":"input_text","text":"hi"}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	require.Nil(t, relayExit)
	require.Equal(t, "resp_multi", result.RequestID)
	require.Equal(t, "response.completed", result.TerminalEventType)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	require.NotNil(t, result.Timings.TTFTMs())

	clientWrites := clientConn.Writes()
	require.Len(t, clientWrites, 3)
}

// TestRelay_OnTurnComplete_PerTerminalEvent supplies one completed and one
// failed response with separate IDs. It checks ordered callback identity/usage
// and aggregate input/output equal to their sums.
func TestRelay_OnTurnComplete_PerTerminalEvent(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_turn_1","usage":{"input_tokens":2,"output_tokens":1}}}`),
		},
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.failed","response":{"id":"resp_turn_2","usage":{"input_tokens":3,"output_tokens":4}}}`),
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	turns := make([]RelayTurnResult, 0, 2)
	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		OnTurnComplete: func(turn RelayTurnResult) {
			turns = append(turns, turn)
		},
	})
	require.Nil(t, relayExit)
	require.Len(t, turns, 2)
	require.Equal(t, "resp_turn_1", turns[0].RequestID)
	require.Equal(t, "response.completed", turns[0].TerminalEventType)
	require.Equal(t, 2, turns[0].Usage.InputTokens)
	require.Equal(t, 1, turns[0].Usage.OutputTokens)
	require.Equal(t, "resp_turn_2", turns[1].RequestID)
	require.Equal(t, "response.failed", turns[1].TerminalEventType)
	require.Equal(t, 3, turns[1].Usage.InputTokens)
	require.Equal(t, 4, turns[1].Usage.OutputTokens)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}

// TestRelay_OnTurnComplete_BareErrorWithoutIDBeforeLaterCompleted supplies an
// unassociated bare error followed by a distinct completion. Both callbacks
// retain their terminal usage and the session totals include both turns.
func TestRelay_OnTurnComplete_BareErrorWithoutIDBeforeLaterCompleted(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"error","usage":{"input_tokens":5,"output_tokens":1},"error":{"message":"first turn failed"}}`),
		},
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_next","usage":{"input_tokens":3,"output_tokens":2}}}`),
		},
	}, true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	turns := make([]RelayTurnResult, 0, 2)
	result, relayExit := Relay(
		ctx,
		clientConn,
		upstreamConn,
		[]byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[]}`),
		RelayOptions{OnTurnComplete: func(turn RelayTurnResult) { turns = append(turns, turn) }},
	)

	require.Nil(t, relayExit)
	require.Len(t, turns, 2)
	require.Equal(t, "error", turns[0].TerminalEventType)
	require.Empty(t, turns[0].RequestID)
	require.Equal(t, Usage{InputTokens: 5, OutputTokens: 1}, turns[0].Usage)
	require.Equal(t, "response.completed", turns[1].TerminalEventType)
	require.Equal(t, "resp_next", turns[1].RequestID)
	require.Equal(t, Usage{InputTokens: 3, OutputTokens: 2}, turns[1].Usage)
	require.Equal(t, Usage{InputTokens: 8, OutputTokens: 3}, result.Usage)
}

// TestRelay_OnTurnComplete_AuxiliaryFrameDoesNotSettleBareErrorBeforeFailed
// inserts a rate-limit update between error and response.failed. One callback
// reports the identified failed snapshot, which also supplies the aggregate
// usage.
func TestRelay_OnTurnComplete_AuxiliaryFrameDoesNotSettleBareErrorBeforeFailed(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{msgType: coderws.MessageText, payload: []byte(`{"type":"error","error":{"code":"server_error","message":"failed"}}`)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"rate_limits.updated","rate_limits":[{"name":"requests","remaining":1}]}`)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.failed","response":{"id":"resp_authoritative","usage":{"input_tokens":7,"output_tokens":2},"error":{"code":"server_error","message":"failed"}}}`)},
	}, true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	turns := make([]RelayTurnResult, 0, 1)
	result, relayExit := Relay(ctx, clientConn, upstreamConn, []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[]}`), RelayOptions{
		OnTurnComplete: func(turn RelayTurnResult) { turns = append(turns, turn) },
	})

	require.Nil(t, relayExit)
	require.Len(t, turns, 1)
	require.Equal(t, "response.failed", turns[0].TerminalEventType)
	require.Equal(t, "resp_authoritative", turns[0].RequestID)
	require.Equal(t, Usage{InputTokens: 7, OutputTokens: 2}, turns[0].Usage)
	require.Equal(t, turns[0].Usage, result.Usage)
}

// TestRelay_OnTurnComplete_UsesCurrentResponseCreateModel supplies a
// warning-shaped bare error to the event observer. Its assertions check the
// error event type and JSON delivered to OnUpstreamEvent.
func TestRelay_OnTurnComplete_UsesCurrentResponseCreateModel(t *testing.T) {
	t.Parallel()

	errorEvent := []byte(`{"type":"error","error":{"message":"This request may pose a cybersecurity risk."}}`)
	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{msgType: coderws.MessageText, payload: errorEvent},
	}, true)
	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var gotEventType string
	var gotPayload []byte
	_, _ = Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		OnUpstreamEvent: func(eventType string, payload []byte) {
			gotEventType = eventType
			gotPayload = append([]byte(nil), payload...)
		},
	})

	require.Equal(t, "error", gotEventType)
	require.JSONEq(t, string(errorEvent), string(gotPayload))
}

// TestRelay_OnUpstreamError_ReportsFailedEvent supplies a failed response
// containing a warning message to the event observer. It checks the
// response.failed type and callback JSON payload.
func TestRelay_OnUpstreamError_ReportsFailedEvent(t *testing.T) {
	t.Parallel()

	failedEvent := []byte(`{"type":"response.failed","response":{"id":"resp_failed","error":{"message":"This request may pose a cybersecurity risk."}}}`)
	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{msgType: coderws.MessageText, payload: failedEvent},
	}, true)
	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var gotEventType string
	var gotPayload []byte
	_, _ = Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		OnUpstreamEvent: func(eventType string, payload []byte) {
			gotEventType = eventType
			gotPayload = append([]byte(nil), payload...)
		},
	})

	require.Equal(t, "response.failed", gotEventType)
	require.JSONEq(t, string(failedEvent), string(gotPayload))
}

// TestRelay_OnTurnComplete_ProvidesTurnMetrics supplies an identified delta and
// completion under an advancing clock. Callback and session metrics retain
// token observation and positive duration alongside terminal identity.
func TestRelay_OnTurnComplete_ProvidesTurnMetrics(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.output_text.delta","response_id":"resp_metric","delta":"hi"}`),
		},
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_metric","usage":{"input_tokens":2,"output_tokens":1}}}`),
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	base := time.Unix(0, 0)
	// Assign clock steps atomically across relay workers; five milliseconds per
	// sample gives the metric assertions distinct token and terminal observations.
	var nowTick atomic.Int64
	nowFn := func() time.Time {
		step := nowTick.Add(1)
		return base.Add(time.Duration(step) * 5 * time.Millisecond)
	}

	var turn RelayTurnResult
	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		Now: nowFn,
		OnTurnComplete: func(current RelayTurnResult) {
			turn = current
		},
	})
	require.Nil(t, relayExit)
	require.Equal(t, "resp_metric", turn.RequestID)
	require.Equal(t, "response.completed", turn.TerminalEventType)
	require.NotNil(t, turn.Timings.TTFTMs())
	require.GreaterOrEqual(t, *turn.Timings.TTFTMs(), 0)
	require.Greater(t, turn.Duration.Milliseconds(), int64(0))
	require.NotNil(t, result.Timings.TTFTMs())
	require.Greater(t, result.Duration.Milliseconds(), int64(0))
}

// TestRelay_OnTurnCompleteUsesExplicitFirstTurnStart supplies a fixed accepted
// start for the initial response.create while the clock is later. Completion
// must report that supplied start.
func TestRelay_OnTurnCompleteUsesExplicitFirstTurnStart(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_initial_boundary","usage":{"input_tokens":1,"output_tokens":1}}}`),
		},
	}, true)
	startedAt := time.Date(2026, time.August, 17, 9, 59, 59, 0, time.UTC)
	var turn RelayTurnResult
	_, relayExit := Relay(context.Background(), clientConn, upstreamConn,
		[]byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`), RelayOptions{
			FirstTurnStartedAt: startedAt,
			Now:                func() time.Time { return startedAt.Add(time.Second) },
			OnTurnComplete:     func(current RelayTurnResult) { turn = current },
		})
	require.Nil(t, relayExit)
	require.Equal(t, startedAt, turn.StartedAt)
}

// TestRelay_OnTurnCompleteUsesSubsequentResponseCreateStart completes one
// response before injecting a later request under a controlled clock. The
// second callback uses that request's start and the call finishes after
// cancellation.
func TestRelay_OnTurnCompleteUsesSubsequentResponseCreateStart(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn(nil, false)
	firstStartedAt := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	secondStartedAt := time.Date(2026, time.August, 17, 9, 59, 59, 0, time.UTC)
	secondResponseAt := secondStartedAt.Add(time.Second)
	var now atomic.Int64
	now.Store(firstStartedAt.UnixNano())
	turns := make(chan RelayTurnResult, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	// Run the relay alongside sequenced fixture injection; buffered turn callbacks
	// let the test wait for completion before advancing the accepted-request clock.
	go func() {
		defer close(done)
		_, _ = Relay(ctx, clientConn, upstreamConn,
			[]byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`), RelayOptions{
				Now: func() time.Time { return time.Unix(0, now.Load()).UTC() },
				OnTurnComplete: func(current RelayTurnResult) {
					turns <- current
				},
			})
	}()
	require.Eventually(t, func() bool { return len(upstreamConn.Writes()) == 1 }, time.Second, time.Millisecond)
	// Complete the initial request before changing the clock, so the later pending
	// start belongs to the subsequent client frame rather than initial response
	// observation.
	upstreamConn.readCh <- passthroughTestFrame{
		msgType: coderws.MessageText,
		payload: []byte(`{"type":"response.completed","response":{"id":"resp_first","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}
	select {
	case <-turns:
	case <-time.After(time.Second):
		t.Fatal("first turn did not complete")
	}

	// Publish the second request's acceptance time before injecting
	// response.create; its pending timing must be captured before the later
	// response clock is selected.
	now.Store(secondStartedAt.UnixNano())
	clientConn.readCh <- passthroughTestFrame{
		msgType: coderws.MessageText,
		payload: []byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`),
	}
	require.Eventually(t, func() bool { return len(upstreamConn.Writes()) == 2 }, time.Second, time.Millisecond)
	// Wait for the second upstream write before advancing to response time,
	// separating its accepted start from the terminal event that consumes timing.
	now.Store(secondResponseAt.UnixNano())
	upstreamConn.readCh <- passthroughTestFrame{
		msgType: coderws.MessageText,
		payload: []byte(`{"type":"response.completed","response":{"id":"resp_second","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}
	var secondTurn RelayTurnResult
	select {
	case secondTurn = <-turns:
	case <-time.After(time.Second):
		t.Fatal("second turn did not complete")
	}
	require.Equal(t, secondStartedAt, secondTurn.StartedAt)

	// Cancel the ongoing relay after collecting the second turn, then wait for its
	// call to return before this fixture scope ends.
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not stop after cancellation")
	}
}

// TestRelay_BinaryFramePassthrough forwards a short binary payload through
// local transports. The client receives one binary frame with identical bytes
// and aggregate input usage remains zero.
func TestRelay_BinaryFramePassthrough(t *testing.T) {
	t.Parallel()

	// Binary frames pass through unchanged and contribute no usage.
	binaryPayload := []byte{0x00, 0x01, 0x02, 0x03}
	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageBinary,
			payload: binaryPayload,
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	require.Nil(t, relayExit)
	require.Equal(t, 0, result.Usage.InputTokens)

	clientWrites := clientConn.Writes()
	require.Len(t, clientWrites, 1)
	require.Equal(t, coderws.MessageBinary, clientWrites[0].msgType)
	require.Equal(t, binaryPayload, clientWrites[0].payload)
}

// TestRelay_BinaryJSONFrameSkipsObservation supplies completed-response JSON
// carried as a binary frame. Forwarding retains binary type while usage,
// request identity, and terminal observation stay empty.
func TestRelay_BinaryJSONFrameSkipsObservation(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageBinary,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_binary","usage":{"input_tokens":7,"output_tokens":3}}}`),
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	require.Nil(t, relayExit)
	require.Equal(t, 0, result.Usage.InputTokens)
	require.Equal(t, "", result.RequestID)
	require.Equal(t, "", result.TerminalEventType)

	clientWrites := clientConn.Writes()
	require.Len(t, clientWrites, 1)
	require.Equal(t, coderws.MessageBinary, clientWrites[0].msgType)
}

// TestRelay_UpstreamErrorEventPassthroughRaw supplies a bare invalid-request
// error frame to the local upstream reader. The client receives one text frame
// with exact bytes and Relay returns nil exit.
func TestRelay_UpstreamErrorEventPassthroughRaw(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	errorEvent := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"No tool call found"}}`)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: errorEvent,
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	require.Nil(t, relayExit)

	clientWrites := clientConn.Writes()
	require.Len(t, clientWrites, 1)
	require.Equal(t, coderws.MessageText, clientWrites[0].msgType)
	require.Equal(t, errorEvent, clientWrites[0].payload)
}

// TestRelay_PreservesFirstMessageType configures the prepared request as a
// binary frame despite its JSON payload. The captured upstream write must
// retain binary type and exact bytes.
func TestRelay_PreservesFirstMessageType(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn(nil, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		FirstMessageType: coderws.MessageBinary,
	})
	require.Nil(t, relayExit)

	upstreamWrites := upstreamConn.Writes()
	require.Len(t, upstreamWrites, 1)
	require.Equal(t, coderws.MessageBinary, upstreamWrites[0].msgType)
	require.Equal(t, firstPayload, upstreamWrites[0].payload)
}

// TestRelay_UsageParseFailureDoesNotBlockRelay supplies valid JSON whose
// terminal usage value is a string. The frame is delivered, terminal
// observation survives with zero input usage, and shared failure count
// increases.
func TestRelay_UsageParseFailureDoesNotBlockRelay(t *testing.T) {
	baseline := SnapshotMetrics().UsageParseFailureTotal

	// The JSON is valid but usage has the wrong type; forwarding must continue.
	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_bad","usage":"not_an_object"}}`),
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	require.Nil(t, relayExit)
	// Invalid usage contributes zero tokens while the terminal event remains observable.
	require.Equal(t, 0, result.Usage.InputTokens)
	require.Equal(t, "response.completed", result.TerminalEventType)

	clientWrites := clientConn.Writes()
	require.Len(t, clientWrites, 1)
	require.GreaterOrEqual(t, SnapshotMetrics().UsageParseFailureTotal, baseline+1)
}

// TestRelay_WriteUpstreamFirstMessageFails supplies a writer that rejects the
// prepared initial request. Relay must report write_upstream before any ongoing
// forwarding work is needed.
func TestRelay_WriteUpstreamFirstMessageFails(t *testing.T) {
	t.Parallel()

	upstreamConn := newPassthroughTestFrameConn(nil, true)
	// The helper queue was auto-closed; Close tolerates that fixture state, and the
	// separate rejecting writer remains the upstream transport used by Relay.
	_ = upstreamConn.Close()

	// Use the failing writer for the relay's first upstream frame.
	errConn := &errorOnWriteFrameConn{}
	clientConn := newPassthroughTestFrameConn(nil, false)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, relayExit := Relay(ctx, clientConn, errConn, firstPayload, RelayOptions{})
	require.NotNil(t, relayExit)
	require.Equal(t, "write_upstream", relayExit.Stage)
}

// TestRelay_ContextCanceled cancels the request context before invoking Relay
// with the prepared initial frame and local transports. Its assertion requires
// a nonnil exit.
func TestRelay_ContextCanceled(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn(nil, false)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{})
	// Cancellation prevents the first upstream write.
	require.NotNil(t, relayExit)
}

// TestRelay_DownstreamPreambleStartsClientReader enables delayed client reading
// and injects a structural response.created preamble. Successful preamble
// delivery starts the reader, and terminal plus upstream closure finishes
// normally.
func TestRelay_DownstreamPreambleStartsClientReader(t *testing.T) {
	clientBase := newPassthroughTestFrameConn(nil, false)
	clientConn := &readStartSpyFrameConn{base: clientBase, started: make(chan struct{})}
	upstreamConn := newPassthroughTestFrameConn(nil, false)
	resultCh := make(chan *RelayExit, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Run the relay while the fixture injects a structural preamble; the spy
	// channel observes reader entry after successful delivery without requiring
	// another client message.
	go func() {
		_, relayExit := Relay(
			ctx,
			clientConn,
			upstreamConn,
			[]byte(`{"type":"response.create","model":"gpt-5.1"}`),
			RelayOptions{
				StartClientAfterFirstDownstream: true,
			},
		)
		resultCh <- relayExit
	}()

	// Deliver response.created before terminal completion so the readiness
	// assertion tests a structural preamble as the physical write that enables
	// client reading.
	upstreamConn.readCh <- passthroughTestFrame{
		msgType: coderws.MessageText,
		payload: []byte(`{"type":"response.created","response":{"id":"resp_semantic_gate"}}`),
	}
	require.Eventually(t, func() bool { return len(clientBase.Writes()) == 1 }, time.Second, 10*time.Millisecond)
	select {
	case <-clientConn.started:
	case <-time.After(time.Second):
		t.Fatal("response.created did not start the client reader")
	}

	upstreamConn.readCh <- passthroughTestFrame{
		msgType: coderws.MessageText,
		payload: []byte(`{"type":"response.completed","response":{"id":"resp_semantic_gate","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}
	// Close the fixture queue after enqueueing its terminal frame; queued bytes
	// remain readable before EOF lets the relay select normal completion.
	_ = upstreamConn.Close()
	select {
	case relayExit := <-resultCh:
		require.Nil(t, relayExit)
	case <-time.After(time.Second):
		t.Fatal("relay did not finish after terminal event and upstream close")
	}
}

// TestRelay_TraceEvents_ContainsLifecycleStages captures diagnostics while one
// completed frame is relayed. The synchronized snapshot must contain
// relay_start, initial-write success, first_exit, and relay_complete stages.
func TestRelay_TraceEvents_ContainsLifecycleStages(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_trace","usage":{"input_tokens":1,"output_tokens":1}}}`),
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Collect worker diagnostics under stagesMu and copy under the same lock after
	// Relay; late callback activity cannot alter the captured slice used for
	// assertions.
	stages := make([]string, 0, 8)
	var stagesMu sync.Mutex
	_, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		OnTrace: func(event RelayTraceEvent) {
			stagesMu.Lock()
			stages = append(stages, event.Stage)
			stagesMu.Unlock()
		},
	})
	require.Nil(t, relayExit)
	stagesMu.Lock()
	capturedStages := append([]string(nil), stages...)
	stagesMu.Unlock()
	require.Contains(t, capturedStages, "relay_start")
	require.Contains(t, capturedStages, "write_first_message_ok")
	require.Contains(t, capturedStages, "first_exit")
	require.Contains(t, capturedStages, "relay_complete")
}

// TestRelay_TraceEvents_IdleTimeout advances the supplied clock while both
// frame readers remain idle. Its synchronized diagnostic snapshot contains
// timeout detection and relay_exit alongside the returned idle stage.
func TestRelay_TraceEvents_IdleTimeout(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn(nil, false)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-4o","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Keep startup samples at the base time, then move later samples an hour
	// forward so the idle trace is produced at a watchdog polling tick.
	now := time.Now()
	callCount := 0
	nowFn := func() time.Time {
		callCount++
		if callCount <= 5 {
			return now
		}
		return now.Add(time.Hour)
	}

	// Protect diagnostic append and snapshot copying with stagesMu because relay
	// workers can emit traces independently of the controlling goroutine's return.
	stages := make([]string, 0, 8)
	var stagesMu sync.Mutex
	_, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		IdleTimeout: 2 * time.Second,
		Now:         nowFn,
		OnTrace: func(event RelayTraceEvent) {
			stagesMu.Lock()
			stages = append(stages, event.Stage)
			stagesMu.Unlock()
		},
	})
	require.NotNil(t, relayExit)
	require.Equal(t, "idle_timeout", relayExit.Stage)
	stagesMu.Lock()
	capturedStages := append([]string(nil), stages...)
	stagesMu.Unlock()
	require.Contains(t, capturedStages, "idle_timeout_triggered")
	require.Contains(t, capturedStages, "relay_exit")
}

// ReadFrame waits for a nonnil context's cancellation and returns its error
// with an empty text frame. Close does not release this stub, and the
// first-write fixture normally returns before reading.
func (c *errorOnWriteFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	// This stub has no input queue; cancellation supplies its observable read
	// termination path even though Close itself leaves the fake unchanged.
	<-ctx.Done()
	return coderws.MessageText, nil, ctx.Err()
}

// WriteFrame returns the fixed synthetic connection-refused error used by
// initial-write tests. Context and frame arguments are ignored, allowing Relay
// to classify the failure directly at its write stage.
func (c *errorOnWriteFrameConn) WriteFrame(_ context.Context, _ coderws.MessageType, _ []byte) error {
	return errors.New("write failed: connection refused")
}

// Close returns nil and supplies no additional cleanup effects for this
// context-controlled stub.
func (c *errorOnWriteFrameConn) Close() error {
	return nil
}

// TestRelay_NoSemanticOutputTerminalSequence_TTFTNil sends structural
// progress and done frames followed by either completed or done. All five
// frames are delivered while callback and session first-token values remain
// nil.
func TestRelay_NoSemanticOutputTerminalSequence_TTFTNil(t *testing.T) {
	t.Parallel()

	for _, terminalEvent := range []string{"response.completed", "response.done"} {
		t.Run(terminalEvent, func(t *testing.T) {
			t.Parallel()

			clientConn := newPassthroughTestFrameConn(nil, false)
			upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
				{
					msgType: coderws.MessageText,
					payload: []byte(`{"type":"response.created","response":{"id":"resp_no_output"}}`),
				},
				{
					msgType: coderws.MessageText,
					payload: []byte(`{"type":"response.in_progress","response":{"id":"resp_no_output"}}`),
				},
				{
					msgType: coderws.MessageText,
					payload: []byte(`{"type":"response.content_part.done","response_id":"resp_no_output"}`),
				},
				{
					msgType: coderws.MessageText,
					payload: []byte(`{"type":"response.output_item.done","response_id":"resp_no_output"}`),
				},
				{
					msgType: coderws.MessageText,
					payload: []byte(`{"type":"` + terminalEvent + `","response":{"id":"resp_no_output","usage":{"input_tokens":2,"output_tokens":0}}}`),
				},
			}, true)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			var turn RelayTurnResult
			result, relayExit := Relay(
				ctx,
				clientConn,
				upstreamConn,
				[]byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`),
				RelayOptions{OnTurnComplete: func(current RelayTurnResult) { turn = current }},
			)

			require.Nil(t, relayExit)
			require.Equal(t, terminalEvent, turn.TerminalEventType)
			require.Nil(t, turn.Timings.TTFTMs())
			require.Equal(t, terminalEvent, result.TerminalEventType)
			require.Nil(t, result.Timings.TTFTMs())
			require.Equal(t, int64(5), result.UpstreamToClientFrames)
		})
	}
}

// TestRelay_NoDeltaOutputDoneEvent_RecordsFirstTokenBeforeTerminal supplies
// full text or tool arguments through selected done events under an advancing
// clock. Callback and session token latency must precede terminal duration.
func TestRelay_NoDeltaOutputDoneEvent_RecordsFirstTokenBeforeTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// name and donePayload select the done event placed between created and
		// completed frames. Each case supplies content while the same advancing clock
		// compares token observation against terminal duration.
		name        string // named done-event fixture case
		donePayload string // complete done-event JSON bytes
	}{
		{
			name:        "output text done",
			donePayload: `{"type":"response.output_text.done","response_id":"resp_done","text":"hello"}`,
		},
		{
			name:        "function call arguments done",
			donePayload: `{"type":"response.function_call_arguments.done","response_id":"resp_done","arguments":"{\"city\":\"Paris\"}"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clientConn := newPassthroughTestFrameConn(nil, false)
			upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
				{msgType: coderws.MessageText, payload: []byte(`{"type":"response.created","response":{"id":"resp_done"}}`)},
				{msgType: coderws.MessageText, payload: []byte(tt.donePayload)},
				{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"resp_done","usage":{"input_tokens":2,"output_tokens":1}}}`)},
			}, true)

			base := time.Unix(0, 0)
			// Use atomic ten-millisecond clock steps because several relay workers can
			// request time; the supplied done frame precedes terminal observation in the
			// upstream sequence.
			var nowTick atomic.Int64
			nowFn := func() time.Time {
				return base.Add(time.Duration(nowTick.Add(1)) * 10 * time.Millisecond)
			}
			var turn RelayTurnResult
			result, relayExit := Relay(
				context.Background(),
				clientConn,
				upstreamConn,
				[]byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`),
				RelayOptions{
					Now:            nowFn,
					OnTurnComplete: func(current RelayTurnResult) { turn = current },
				},
			)

			require.Nil(t, relayExit)
			require.NotNil(t, turn.Timings.TTFTMs())
			require.Less(t, int64(*turn.Timings.TTFTMs()), turn.Duration.Milliseconds())
			require.NotNil(t, result.Timings.TTFTMs())
			require.Less(t, int64(*result.Timings.TTFTMs()), result.Duration.Milliseconds())
		})
	}
}

// TestRelay_OnTurnComplete_RealOpenAIStream_TTFT uses local
// created/delta/completed fixtures and an advancing clock. Callback and session
// latency are observed, and callback first-token time remains below terminal
// duration.
func TestRelay_OnTurnComplete_RealOpenAIStream_TTFT(t *testing.T) {
	t.Parallel()

	clientConn := newPassthroughTestFrameConn(nil, false)
	upstreamConn := newPassthroughTestFrameConn([]passthroughTestFrame{
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.created","response":{"id":"resp_real"}}`),
		},
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.output_text.delta","delta":"He"}`),
		},
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.output_text.delta","delta":"llo"}`),
		},
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.output_text.delta","delta":" world"}`),
		},
		{
			msgType: coderws.MessageText,
			payload: []byte(`{"type":"response.completed","response":{"id":"resp_real","usage":{"input_tokens":2,"output_tokens":3}}}`),
		},
	}, true)

	firstPayload := []byte(`{"type":"response.create","model":"gpt-5.3-codex","input":[]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	base := time.Unix(0, 0)
	// Use atomic ten-millisecond clock steps for concurrent relay time reads; the
	// local delta sequence records token timing before its later completed frame.
	var nowTick atomic.Int64
	nowFn := func() time.Time {
		step := nowTick.Add(1)
		return base.Add(time.Duration(step) * 10 * time.Millisecond)
	}

	var turn RelayTurnResult
	result, relayExit := Relay(ctx, clientConn, upstreamConn, firstPayload, RelayOptions{
		Now: nowFn,
		OnTurnComplete: func(current RelayTurnResult) {
			turn = current
		},
	})
	require.Nil(t, relayExit)
	require.Equal(t, "resp_real", turn.RequestID)
	require.Equal(t, "response.completed", turn.TerminalEventType)

	require.NotNil(t, turn.Timings.TTFTMs(), "per-turn TTFT must be captured for real OpenAI streams")
	require.Greater(t, turn.Duration.Milliseconds(), int64(0))

	require.Less(t,
		int64(*turn.Timings.TTFTMs()),
		turn.Duration.Milliseconds(),
		"per-turn TTFT (%dms) should be strictly less than Duration (%dms); "+
			"equality indicates the bug where first_token is mistakenly stamped on the terminal event",
		*turn.Timings.TTFTMs(), turn.Duration.Milliseconds(),
	)

	require.NotNil(t, result.Timings.TTFTMs())
	require.Greater(t, *result.Timings.TTFTMs(), 0)
}

// TestRelayToolArgumentsDoneExtendsContentInterval supplies a tool name followed by
// its first full arguments and repeated terminal snapshots. Callback and aggregate
// performance must retain two useful arrivals and the same nonempty TPS estimate.
func TestRelayToolArgumentsDoneExtendsContentInterval(t *testing.T) {
	client := newPassthroughTestFrameConn(nil, false)
	upstream := newPassthroughTestFrameConn([]passthroughTestFrame{
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.created","response":{"id":"r"}}`)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.output_item.added","response_id":"r","item":{"id":"call1","type":"function_call","name":"lookup","arguments":""}}`)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.function_call_arguments.done","response_id":"r","item_id":"call1","arguments":"{}"}`)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.output_item.done","response_id":"r","item":{"id":"call1","type":"function_call","name":"lookup","arguments":"{}"}}`)},
		{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"r","output":[{"id":"call1","type":"function_call","name":"lookup","arguments":"{}"}],"usage":{"input_tokens":10,"output_tokens":20}}}`)},
	}, true)
	base := time.Unix(0, 0)
	var tick atomic.Int64
	var turn RelayTurnResult
	result, exit := Relay(t.Context(), client, upstream, []byte(`{"type":"response.create","model":"model","input":[]}`), RelayOptions{
		// Workers share an advancing atomic clock, keeping distinct frame arrivals
		// ordered while avoiding real sleeps or assumptions about goroutine scheduling.
		Now:            func() time.Time { return base.Add(time.Duration(tick.Add(1)) * 10 * time.Millisecond) },
		OnTurnComplete: func(value RelayTurnResult) { turn = value },
	})
	require.Nil(t, exit)
	require.NotNil(t, turn.Timings)
	require.Equal(t, int64(2), turn.Timings.Values["upstream_output_event_count"])
	require.NotNil(t, turn.Timings.TPS(20))
	require.Equal(t, turn.Timings.TPS(20), result.Timings.TPS(20))
	require.Less(t, turn.Timings.Values["upstream_last_output_ms"], turn.Timings.Values["upstream_completed_ms"])
}

// TestRelayZeroTerminalKeepsEntireCacheWriteObservation checks the retained
// counters at turn settlement. A complete zero terminal must leave the prior
// split and its inclusive input and output totals together.
func TestRelayZeroTerminalKeepsEntireCacheWriteObservation(t *testing.T) {
	t.Parallel()

	state := &relayState{}
	progress := []byte(`{"usage":{"input_tokens":12000,"output_tokens":10,"input_tokens_details":{"cached_tokens":4000,"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}}}`)
	terminal := []byte(`{"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	parseUsageAndAccumulate(state, progress, "response.in_progress", nil)
	parseUsageAndAccumulate(state, terminal, "response.completed", nil)

	require.Equal(t, Usage{
		InputTokens: 12000, OutputTokens: 10, CacheReadInputTokens: 4000, CacheCreationInputTokens: 7000,
		CacheWriteDetails: &openai.CacheWriteTokensDetails{Tokens30m: 3000, Tokens12h: 4000},
	}, finalizeRelayTurnUsage(state))
}
