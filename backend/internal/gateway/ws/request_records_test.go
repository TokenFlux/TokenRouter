package ws

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// TestEntryRequestRecordsTracksRetryAndRejectedTurns retries the first logical
// turn on another provider and opens a later unfinished turn. It checks
// retained identity, two attempts, parent and user attribution, completed
// state, and cleanup's incomplete error code.
func TestEntryRequestRecordsTracksRetryAndRejectedTurns(t *testing.T) {
	var snapshots []telemetry.RequestRecord
	root := telemetry.WithRequestCapture(context.WithValue(context.Background(), telemetry.RequestID, "connection"), telemetry.RequestRecord{RequestID: "connection", UserID: 7, APIKeyID: 8, StartedAt: time.Now(), State: "running"}, func(record telemetry.RequestRecord) { snapshots = append(snapshots, record) })
	session := newEntryRequestRecords(root)
	first := session.attempt()
	firstID := telemetry.RequestIDValue(first.context(root, 1))
	first.finish(root, TurnCapture{Turn: 1, Err: errors.New("retry")}, 9, "openai", true)
	retry := session.attempt()
	require.Equal(t, firstID, telemetry.RequestIDValue(retry.context(root, 1)))
	retry.finish(root, TurnCapture{Turn: 1, Result: &ForwardResult{RequestID: "response", OpenAIWSMode: true, UpstreamTerminalEvent: "response.completed"}}, 10, "openai", false)
	secondID := telemetry.RequestIDValue(retry.context(root, 2))
	require.NotEqual(t, firstID, secondID)
	session.close()
	latest := make(map[string]telemetry.RequestRecord)
	for _, record := range snapshots {
		latest[record.RequestID] = record
	}
	require.Equal(t, "completed", latest[firstID].State)
	require.Len(t, latest[firstID].Attempts, 2)
	require.Equal(t, "connection", latest[firstID].ParentRequestID)
	require.Equal(t, int64(7), latest[firstID].UserID)
	require.Equal(t, "failed", latest[secondID].State)
	require.Equal(t, "websocket_turn_incomplete", latest[secondID].ErrorCode)
}

// TestEntryPerformanceKeepsUsageAttemptOwnership switches providers within one logical
// turn. The successful performance belongs to the second dispatch, while failure
// diagnostics and the original mode survive the retry without a duplicate attempt.
func TestEntryPerformanceKeepsUsageAttemptOwnership(t *testing.T) {
	mode := timing.FirstEvent
	saved := make(map[string]telemetry.RequestRecord)
	root := telemetry.WithRequestCapture(t.Context(), telemetry.RequestRecord{RequestID: "connection", StartedAt: time.Now()}, func(record telemetry.RequestRecord) { saved[record.RequestID] = record })
	root = timing.WithModeReader(root, func(context.Context) string { return mode })
	session := newEntryRequestRecords(root)
	first := session.attempt()
	firstContext := first.context(root, 1)
	requestID := telemetry.RequestIDValue(firstContext)
	failed := timing.BeginPerformance(firstContext, 10, time.Now())
	require.Equal(t, int64(1), failed.Values["attempt_number"])
	first.finish(root, TurnCapture{Turn: 1, Err: errors.New("connection failed")}, 10, "openai", true)
	mode = timing.FirstOutput
	second := session.attempt()
	secondContext := second.context(root, 1)
	require.Equal(t, requestID, telemetry.RequestIDValue(secondContext))
	performance := timing.BeginPerformance(secondContext, 20, time.Now())
	require.Equal(t, int64(2), performance.Values["attempt_number"])
	require.Equal(t, timing.FirstEvent, performance.Mode)
	second.finish(root, TurnCapture{Turn: 1, Result: &ForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.completed", Timings: performance}}, 20, "openai", false)
	require.Len(t, saved[requestID].Attempts, 2)
	require.Equal(t, "failed", saved[requestID].Attempts[0].Outcome)
	require.Contains(t, saved[requestID].Attempts[0].Reason, "connection failed")
	require.NotNil(t, saved[requestID].Attempts[0].DurationMs)
	require.Nil(t, saved[requestID].Attempts[1].DurationMs)
	require.Nil(t, saved[requestID].FailureTimings)
	nextContext := second.context(root, 2)
	next := timing.BeginPerformance(nextContext, 20, time.Now())
	require.Equal(t, timing.FirstOutput, next.Mode)
}

// TestEntryPerformanceAverageSpeedAcrossRetryAndTurns delays a failed provider before
// completing two socket turns. Each TPS includes only its own dispatch interval,
// and the second turn cannot alter the first turn's captured estimate.
func TestEntryPerformanceAverageSpeedAcrossRetryAndTurns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := telemetry.WithRequestCapture(t.Context(), telemetry.RequestRecord{RequestID: "connection", StartedAt: time.Now()}, func(telemetry.RequestRecord) {})
		session := newEntryRequestRecords(root)
		first := session.attempt()
		failed := timing.BeginPerformance(first.context(root, 1), 10, time.Now())
		time.Sleep(5 * time.Second)
		first.finish(root, TurnCapture{Turn: 1, Err: errors.New("connection failed")}, 10, "openai", true)
		second := session.attempt()
		ctx := second.context(root, 1)
		performance := timing.BeginPerformance(ctx, 20, time.Now())
		time.Sleep(time.Second)
		performance.ObserveAt(`{"type":"response.created"}`, "", time.Now())
		time.Sleep(time.Second)
		terminal := `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}],"usage":{"output_tokens":20}}}`
		performance.ObserveAt(terminal, "", time.Now())
		performance.FinishAt(time.Now())
		require.Equal(t, int64(5000), performance.Values["upstream_started_ms"])
		require.Equal(t, 10.0, *performance.TPS(20))
		require.Nil(t, failed.TPS(20))
		second.finish(root, TurnCapture{Turn: 1, Result: &ForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.completed", Timings: performance}}, 20, "openai", false)
		nextContext := second.context(root, 2)
		next := timing.BeginPerformance(nextContext, 20, time.Now())
		time.Sleep(500 * time.Millisecond)
		next.ObserveAt(terminal, "", time.Now())
		next.FinishAt(time.Now())
		require.NotEqual(t, telemetry.RequestIDValue(ctx), telemetry.RequestIDValue(nextContext))
		require.Equal(t, int64(0), next.Values["upstream_started_ms"])
		require.Equal(t, 40.0, *next.TPS(20))
		require.Equal(t, 10.0, *performance.TPS(20))
		second.finish(root, TurnCapture{Turn: 2, Result: &ForwardResult{OpenAIWSMode: true, UpstreamTerminalEvent: "response.completed", Timings: next}}, 20, "openai", false)
		session.close()
	})
}

// TestEntryCloseKeepsDisconnectBeforeIncompleteDrain closes a turn that never received
// a terminal response. The request ends at its observed downstream disconnect even
// though session cleanup happens later, while its incomplete upstream state remains diagnosable.
func TestEntryCloseKeepsDisconnectBeforeIncompleteDrain(t *testing.T) {
	saved := make(map[string]telemetry.RequestRecord)
	root := telemetry.WithRequestCapture(t.Context(), telemetry.RequestRecord{RequestID: "connection"}, func(record telemetry.RequestRecord) { saved[record.RequestID] = record })
	session := newEntryRequestRecords(root)
	ctx := session.attempt().context(root, 1)
	started, ok := ctx.Value(telemetry.RequestStartedAt).(time.Time)
	require.True(t, ok)
	timing.ObserveClientDisconnect(ctx, started)
	session.close()
	record := saved[telemetry.RequestIDValue(ctx)]
	require.Equal(t, "canceled", record.State)
	require.Equal(t, started, *record.FinishedAt)
	require.Zero(t, record.DurationMs)
	require.Equal(t, "websocket_turn_incomplete", record.ErrorCode)
}

// TestEntryHTTPAttemptsKeepOneOrdinal completes HTTP bridge attempts with and
// without a result. Existing transport failures remain singular, and a later
// same-provider preparation failure cannot overwrite the preceding exchange.
func TestEntryHTTPAttemptsKeepOneOrdinal(t *testing.T) {
	for _, failed := range []bool{false, true} {
		saved := make(map[string]telemetry.RequestRecord)
		root := telemetry.WithRequestCapture(t.Context(), telemetry.RequestRecord{RequestID: "connection", StartedAt: time.Now()}, func(r telemetry.RequestRecord) { saved[r.RequestID] = r })
		session := newEntryRequestRecords(root)
		attempt := session.attempt()
		base := telemetry.WithRequestProvider(root, 7, "X-Custom")
		ctx := attempt.context(base, 1)
		id := telemetry.RequestIDValue(ctx)
		performance := timing.NewRequestPerformance(ctx, time.Now())
		exchange := timing.BeginHTTPTrace(ctx)
		ref := telemetry.RequestAttemptFromContext(exchange)
		status := 200
		if failed {
			status = 503
		}
		timing.ObserveResponse(exchange, &http.Response{StatusCode: status, Header: http.Header{"X-Custom": {"upstream"}}})
		capture := TurnCapture{Turn: 1, Result: &ForwardResult{RequestAttempt: ref, RequestID: "resp_bridge", ResponseID: "resp_bridge", OpenAIWSMode: true, UpstreamTerminalEvent: "response.completed", Timings: performance}}
		if failed {
			capture.Result = nil
			capture.Err = errors.New("first transport failed")
		}
		attempt.finish(base, capture, 7, "openai", failed)
		require.Len(t, saved[id].Attempts, 1)
		require.Equal(t, "upstream", saved[id].Attempts[0].RequestID)
		if !failed {
			require.Equal(t, "completed", saved[id].Attempts[0].Outcome)
			require.Equal(t, int64(1), capture.Result.Timings.Values["attempt_number"])
			require.Contains(t, saved[id].Aliases, telemetry.RequestAlias{Kind: "response", Value: "resp_bridge", Source: "response.id", AttemptNumber: 1, ProviderID: 7})
			continue
		}
		retry := session.attempt()
		retry.finish(base, TurnCapture{Turn: 1, Err: errors.New("next credentials failed")}, 7, "openai", false)
		require.Len(t, saved[id].Attempts, 2)
		require.Equal(t, "first transport failed", saved[id].Attempts[0].Reason)
		require.Equal(t, "next credentials failed", saved[id].Attempts[1].Reason)
		require.Empty(t, saved[id].Attempts[1].RequestID)
	}
}
