package timing

import (
	"context"
	"io"
	"net/http/httptrace"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// stages_test.go exercises admission stage splitting, first-event retention, and
// shared ingress clocks in the timing collector. Controlled reader delays and
// virtual time verify that initial admission freezes before retries and that
// failure snapshots retain request metadata without inventing slot acquisition.

// delayedStageBody represents a network body whose reads consume controlled time.
type delayedStageBody struct {
	io.Reader /* caller-owned input bytes */
}

// Read models a slow network read without changing returned bytes.
func (r delayedStageBody) Read(p []byte) (int, error) {
	time.Sleep(time.Second)
	return r.Reader.Read(p)
}

// Close leaves the test reader available for fixture inspection.
func (r delayedStageBody) Close() error { return nil }

// TestStagesAdmissionSeparatesReadingWaitingAndRetries exercises original reads,
// preparation, two queues, and another attempt. Only original admission contributes
// to the four-way split and later provider waiting remains separately measurable.
func TestStagesAdmissionSeparatesReadingWaitingAndRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, s := WithStages(context.Background(), time.Now(), nil)
		body := s.ReadBody(delayedStageBody{strings.NewReader("body")})
		_, err := io.ReadAll(body)
		require.NoError(t, err)
		time.Sleep(3 * time.Second)
		time.Sleep(2 * time.Second)
		s.Wait("user", 2*time.Second)
		time.Sleep(4 * time.Second)
		s.Wait("provider", 4*time.Second)
		s.Admit()
		initial := s.Snapshot()
		require.Equal(t, int64(2000), initial["request_body_read_ms"])
		require.Equal(t, int64(3000), initial["preprocess_ms"])
		require.Equal(t, int64(11000), initial["provider_slot_acquired_ms"])
		time.Sleep(7 * time.Second)
		s.Wait("provider", 7*time.Second)
		s.Admit()
		s.Finish()
		got := s.Snapshot()
		for _, key := range []string{"request_body_read_ms", "preprocess_ms", "user_slot_wait_ms", "provider_slot_wait_ms", "provider_slot_acquired_ms"} {
			require.Equal(t, initial[key], got[key], key)
		}
		require.Equal(t, int64(7000), got["retry_provider_slot_wait_ms"])
	})
}

// TestStagesFailureAndFirstObservation preserves failure-only admission,
// request content length, and measured zero. Repeated stream events and
// completion cannot rewrite earlier milestones or fabricate an acquired
// provider after the request has ended.
func TestStagesFailureAndFirstObservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, s := WithStages(context.Background(), time.Now(), nil)
		s.SetStream(true)
		s.SetContentLength(4096)
		s.Mark("upstream_first_event_ms")
		time.Sleep(time.Second)
		s.Mark("upstream_first_event_ms")
		s.Finish()
		s.Admit()
		got := s.Snapshot()
		require.Equal(t, int64(4096), got["request_content_length"])
		require.Equal(t, int64(4096), s.FailureSnapshot()["request_content_length"])
		require.Equal(t, int64(0), got["upstream_first_event_ms"])
		require.Equal(t, int64(0), got["timing_admitted"])
		require.Equal(t, int64(1000), got["preprocess_ms"])
		require.NotContains(t, got, "provider_slot_acquired_ms")
		require.Equal(t, int64(0), got["user_slot_wait_ms"])
	})
}

// TestStagesAndHTTPTraceShareIngress installs tracing on either side of stage
// collection. Transport milestones must retain the request ingress reference,
// rather than silently subtracting time spent before trace installation.
func TestStagesAndHTTPTraceShareIngress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		time.Sleep(time.Second)
		ctx, stages := WithStages(t.Context(), start, nil)
		ctx = BeginHTTPTrace(ctx)
		time.Sleep(time.Second)
		httptrace.ContextClientTrace(ctx).GetConn("upstream.test")
		snapshot := stages.Snapshot()
		require.Equal(t, int64(2000), snapshot["upstream_get_conn_ms"])
		require.Equal(t, int64(1000), snapshot["upstream_started_ms"])
	})
}
