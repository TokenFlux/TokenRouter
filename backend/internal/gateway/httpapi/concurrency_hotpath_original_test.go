package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newHelperTestContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, rec
}

func TestWaitForSlotWithPingTimeout_AccountAndUserAcquire(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		AccountSeq: []bool{false, true},
		UserSeq:    []bool{false, true},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)

	t.Run("account_slot_acquired_after_retry", func(t *testing.T) {
		c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
		streamStarted := false
		release, err := helper.WaitForSlotWithPingTimeout(c, "account", 101, 2, time.Second, false, &streamStarted, true)
		require.NoError(t, err)
		require.NotNil(t, release)
		require.False(t, streamStarted)
		release()
		require.GreaterOrEqual(t, cache.AccountAcquireCalls, 2)
		require.GreaterOrEqual(t, cache.AccountReleaseCalls, 1)
	})

	t.Run("user_slot_acquired_after_retry", func(t *testing.T) {
		c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
		streamStarted := false
		release, err := helper.WaitForSlotWithPingTimeout(c, "user", 202, 3, time.Second, false, &streamStarted, true)
		require.NoError(t, err)
		require.NotNil(t, release)
		release()
		require.GreaterOrEqual(t, cache.UserAcquireCalls, 2)
		require.GreaterOrEqual(t, cache.UserReleaseCalls, 1)
	})
}

func TestAcquireUserSlotWithWait_ImmediateAcquireSkipsWaitQueue(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		UserSeq: []bool{true},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, time.Second, false, &streamStarted)
	require.NoError(t, err)
	require.NotNil(t, release)
	release()

	require.Equal(t, 1, cache.UserAcquireCalls)
	require.Equal(t, 0, cache.WaitIncrementCalls)
	require.Equal(t, 0, cache.WaitDecrementCalls)
	require.Equal(t, 1, cache.UserReleaseCalls)
}

func TestAcquireUserSlotWithWait_TracksAPIKeySlot(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		UserSeq: []bool{true},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	c.Set("gateway_effective_key", &apikey.APIKey{ID: 77})
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, time.Second, false, &streamStarted)
	require.NoError(t, err)
	require.NotNil(t, release)
	require.Equal(t, 1, cache.APIKeyTrackCalls)
	require.Equal(t, []int64{77}, cache.APIKeyTrackIDs)

	release()

	require.Equal(t, 1, cache.UserReleaseCalls)
	require.Equal(t, 1, cache.APIKeyReleaseCalls)
}

func TestTryAcquireUserSlotForAPIKey_TracksAPIKeySlot(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		UserSeq: []bool{true},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)

	release, acquired, err := helper.TryAcquireUserSlotForAPIKey(context.Background(), 202, 3, 77)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, release)
	require.Equal(t, 1, cache.APIKeyTrackCalls)
	require.Equal(t, []int64{77}, cache.APIKeyTrackIDs)

	release()

	require.Equal(t, 1, cache.UserReleaseCalls)
	require.Equal(t, 1, cache.APIKeyReleaseCalls)
}

func TestAcquireUserSlotWithWait_WaitSuccessDecrementsBeforeReturn(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		UserSeq:     []bool{false, true},
		WaitAllowed: true,
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, time.Second, false, &streamStarted)
	require.NoError(t, err)
	require.NotNil(t, release)

	require.Equal(t, 2, cache.UserAcquireCalls)
	require.Equal(t, 1, cache.WaitIncrementCalls)
	require.Equal(t, 20, cache.WaitMaxWait)
	require.Equal(t, 1, cache.WaitDecrementCalls)

	release()
	require.Equal(t, 1, cache.UserReleaseCalls)
}

func TestAcquireUserSlotWithWait_WaitQueueFull(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		UserSeq: []bool{false},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, time.Second, false, &streamStarted)
	require.Nil(t, release)
	var waitErr *WaitQueueFullError
	require.ErrorAs(t, err, &waitErr)
	require.Equal(t, "user", waitErr.SlotType)
	require.Equal(t, 1, cache.WaitIncrementCalls)
	require.Equal(t, 0, cache.WaitDecrementCalls)
}

func TestAcquireUserSlotWithWait_TimeoutDecrementsWaitQueue(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		UserSeq:     []bool{false, false, false},
		WaitAllowed: true,
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, 30*time.Millisecond, false, &streamStarted)
	require.Nil(t, release)
	var cErr *ConcurrencyError
	require.ErrorAs(t, err, &cErr)
	require.True(t, cErr.IsTimeout)
	require.Equal(t, 1, cache.WaitIncrementCalls)
	require.Equal(t, 1, cache.WaitDecrementCalls)
	require.Equal(t, 0, cache.UserReleaseCalls)
}

func TestAcquireUserSlotWithWait_RequestCancelDecrementsWaitQueue(t *testing.T) {
	cancelled := make(chan struct{})
	var cancel context.CancelFunc
	cache := &httptestkit.ConcurrencySequence{
		UserSeq:     []bool{false, false},
		WaitAllowed: true,
		WaitIncrementHook: func() {
			cancel()
			close(cancelled)
		},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	reqCtx, cancelFunc := context.WithCancel(c.Request.Context())
	cancel = cancelFunc
	defer cancel()
	c.Request = c.Request.WithContext(reqCtx)
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, time.Second, false, &streamStarted)
	<-cancelled
	require.Nil(t, release)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, cache.WaitIncrementCalls)
	require.Equal(t, 1, cache.WaitDecrementCalls)
	require.Equal(t, 0, cache.UserReleaseCalls)
}

func TestWaitForSlotWithPingTimeout_TimeoutAndStreamPing(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		AccountSeq: []bool{false, false, false},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)

	t.Run("timeout_returns_concurrency_error", func(t *testing.T) {
		helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
		c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
		streamStarted := false
		release, err := helper.WaitForSlotWithPingTimeout(c, "account", 101, 2, 130*time.Millisecond, false, &streamStarted, true)
		require.Nil(t, release)
		var cErr *ConcurrencyError
		require.ErrorAs(t, err, &cErr)
		require.True(t, cErr.IsTimeout)
	})

	t.Run("stream_mode_sends_ping_before_timeout", func(t *testing.T) {
		helper := NewConcurrencyHelper(concurrency, SSEPingFormatComment, 10*time.Millisecond)
		c, rec := newHelperTestContext(http.MethodPost, "/v1/messages")
		streamStarted := false
		release, err := helper.WaitForSlotWithPingTimeout(c, "account", 101, 2, 70*time.Millisecond, true, &streamStarted, true)
		require.Nil(t, release)
		var cErr *ConcurrencyError
		require.ErrorAs(t, err, &cErr)
		require.True(t, cErr.IsTimeout)
		require.True(t, streamStarted)
		require.Contains(t, rec.Body.String(), ":\n\n")
	})
}

func TestWaitForSlotWithPingTimeout_ParentContextCanceled(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		AccountSeq: []bool{false},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	reqCtx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(reqCtx)
	cancel()

	streamStarted := false
	release, err := helper.WaitForSlotWithPingTimeout(c, "account", 101, 2, time.Second, false, &streamStarted, true)
	require.Nil(t, release)
	require.ErrorIs(t, err, context.Canceled)
	var cErr *ConcurrencyError
	require.False(t, errors.As(err, &cErr))
}

func TestWaitForSlotWithPingTimeout_AcquireError(t *testing.T) {
	errCache := &httptestkit.ConcurrencySequenceWithError{
		Err: errors.New("redis unavailable"),
	}
	concurrency := scheduler.NewConcurrencyService(errCache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false
	release, err := helper.WaitForSlotWithPingTimeout(c, "account", 1, 1, 200*time.Millisecond, false, &streamStarted, true)
	require.Nil(t, release)
	require.Error(t, err)
	require.Contains(t, err.Error(), "redis unavailable")
}

func TestAcquireAccountSlotWithWaitTimeout_ImmediateAttemptBeforeBackoff(t *testing.T) {
	cache := &httptestkit.ConcurrencySequence{
		AccountSeq: []bool{false},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := NewConcurrencyHelper(concurrency, SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireAccountSlotWithWaitTimeout(c, 301, 1, 30*time.Millisecond, false, &streamStarted)
	require.Nil(t, release)
	var cErr *ConcurrencyError
	require.ErrorAs(t, err, &cErr)
	require.True(t, cErr.IsTimeout)
	require.GreaterOrEqual(t, cache.AccountAcquireCalls, 1)
}
