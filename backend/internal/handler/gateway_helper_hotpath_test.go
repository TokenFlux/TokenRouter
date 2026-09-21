package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type helperConcurrencyCacheStub struct {
	mu sync.Mutex

	accountSeq []bool
	userSeq    []bool

	accountAcquireCalls int
	userAcquireCalls    int
	accountReleaseCalls int
	userReleaseCalls    int
	waitAllowed         bool
	waitIncrementCalls  int
	waitDecrementCalls  int
	waitMaxWait         int
	waitIncrementHook   func()
	apiKeyTrackCalls    int
	apiKeyReleaseCalls  int
	apiKeyTrackIDs      []int64
}

func (s *helperConcurrencyCacheStub) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountAcquireCalls++
	if len(s.accountSeq) == 0 {
		return false, nil
	}
	v := s.accountSeq[0]
	s.accountSeq = s.accountSeq[1:]
	return v, nil
}

func (s *helperConcurrencyCacheStub) ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountReleaseCalls++
	return nil
}

func (s *helperConcurrencyCacheStub) GetAccountConcurrency(ctx context.Context, accountID int64) (int, error) {
	return 0, nil
}

func (s *helperConcurrencyCacheStub) GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(accountIDs))
	for _, accountID := range accountIDs {
		out[accountID] = 0
	}
	return out, nil
}

func (s *helperConcurrencyCacheStub) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	return true, nil
}

func (s *helperConcurrencyCacheStub) DecrementAccountWaitCount(ctx context.Context, accountID int64) error {
	return nil
}

func (s *helperConcurrencyCacheStub) GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error) {
	return 0, nil
}

func (s *helperConcurrencyCacheStub) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userAcquireCalls++
	if len(s.userSeq) == 0 {
		return false, nil
	}
	v := s.userSeq[0]
	s.userSeq = s.userSeq[1:]
	return v, nil
}

func (s *helperConcurrencyCacheStub) ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userReleaseCalls++
	return nil
}

func (s *helperConcurrencyCacheStub) GetUserConcurrency(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

func (s *helperConcurrencyCacheStub) TrackAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiKeyTrackCalls++
	s.apiKeyTrackIDs = append(s.apiKeyTrackIDs, apiKeyID)
	return nil
}

func (s *helperConcurrencyCacheStub) ReleaseAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiKeyReleaseCalls++
	return nil
}

func (s *helperConcurrencyCacheStub) GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(apiKeyIDs))
	for _, apiKeyID := range apiKeyIDs {
		out[apiKeyID] = 0
	}
	return out, nil
}

func (s *helperConcurrencyCacheStub) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	s.mu.Lock()
	s.waitIncrementCalls++
	s.waitMaxWait = maxWait
	waitAllowed := s.waitAllowed
	hook := s.waitIncrementHook
	s.mu.Unlock()

	if hook != nil {
		hook()
	}
	if !waitAllowed {
		return false, nil
	}
	return true, nil
}

func (s *helperConcurrencyCacheStub) DecrementWaitCount(ctx context.Context, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitDecrementCalls++
	return nil
}

func (s *helperConcurrencyCacheStub) GetAccountsLoadBatch(ctx context.Context, accounts []scheduler.AccountWithConcurrency) (map[int64]*scheduler.AccountLoadInfo, error) {
	out := make(map[int64]*scheduler.AccountLoadInfo, len(accounts))
	for _, acc := range accounts {
		out[acc.ID] = &scheduler.AccountLoadInfo{AccountID: acc.ID}
	}
	return out, nil
}

func (s *helperConcurrencyCacheStub) GetUsersLoadBatch(ctx context.Context, users []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	out := make(map[int64]*scheduler.UserLoadInfo, len(users))
	for _, user := range users {
		out[user.ID] = &scheduler.UserLoadInfo{UserID: user.ID}
	}
	return out, nil
}

func (s *helperConcurrencyCacheStub) CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error {
	return nil
}

func (s *helperConcurrencyCacheStub) CleanupExpiredAccountSlotKeys(ctx context.Context) error {
	return nil
}

func (s *helperConcurrencyCacheStub) CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error {
	return nil
}

func newHelperTestContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, rec
}

func TestWaitForSlotWithPingTimeout_AccountAndUserAcquire(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		accountSeq: []bool{false, true},
		userSeq:    []bool{false, true},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)

	t.Run("account_slot_acquired_after_retry", func(t *testing.T) {
		c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
		streamStarted := false
		release, err := helper.WaitForSlotWithPingTimeout(c, "account", 101, 2, time.Second, false, &streamStarted, true)
		require.NoError(t, err)
		require.NotNil(t, release)
		require.False(t, streamStarted)
		release()
		require.GreaterOrEqual(t, cache.accountAcquireCalls, 2)
		require.GreaterOrEqual(t, cache.accountReleaseCalls, 1)
	})

	t.Run("user_slot_acquired_after_retry", func(t *testing.T) {
		c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
		streamStarted := false
		release, err := helper.WaitForSlotWithPingTimeout(c, "user", 202, 3, time.Second, false, &streamStarted, true)
		require.NoError(t, err)
		require.NotNil(t, release)
		release()
		require.GreaterOrEqual(t, cache.userAcquireCalls, 2)
		require.GreaterOrEqual(t, cache.userReleaseCalls, 1)
	})
}

func TestAcquireUserSlotWithWait_ImmediateAcquireSkipsWaitQueue(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		userSeq: []bool{true},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, time.Second, false, &streamStarted)
	require.NoError(t, err)
	require.NotNil(t, release)
	release()

	require.Equal(t, 1, cache.userAcquireCalls)
	require.Equal(t, 0, cache.waitIncrementCalls)
	require.Equal(t, 0, cache.waitDecrementCalls)
	require.Equal(t, 1, cache.userReleaseCalls)
}

func TestAcquireUserSlotWithWait_TracksAPIKeySlot(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		userSeq: []bool{true},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	c.Set("gateway_effective_key", &apikey.APIKey{ID: 77})
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, time.Second, false, &streamStarted)
	require.NoError(t, err)
	require.NotNil(t, release)
	require.Equal(t, 1, cache.apiKeyTrackCalls)
	require.Equal(t, []int64{77}, cache.apiKeyTrackIDs)

	release()

	require.Equal(t, 1, cache.userReleaseCalls)
	require.Equal(t, 1, cache.apiKeyReleaseCalls)
}

func TestTryAcquireUserSlotForAPIKey_TracksAPIKeySlot(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		userSeq: []bool{true},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)

	release, acquired, err := helper.TryAcquireUserSlotForAPIKey(context.Background(), 202, 3, 77)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, release)
	require.Equal(t, 1, cache.apiKeyTrackCalls)
	require.Equal(t, []int64{77}, cache.apiKeyTrackIDs)

	release()

	require.Equal(t, 1, cache.userReleaseCalls)
	require.Equal(t, 1, cache.apiKeyReleaseCalls)
}

func TestAcquireUserSlotWithWait_WaitSuccessDecrementsBeforeReturn(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		userSeq:     []bool{false, true},
		waitAllowed: true,
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, time.Second, false, &streamStarted)
	require.NoError(t, err)
	require.NotNil(t, release)

	require.Equal(t, 2, cache.userAcquireCalls)
	require.Equal(t, 1, cache.waitIncrementCalls)
	require.Equal(t, 20, cache.waitMaxWait)
	require.Equal(t, 1, cache.waitDecrementCalls)

	release()
	require.Equal(t, 1, cache.userReleaseCalls)
}

func TestAcquireUserSlotWithWait_WaitQueueFull(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		userSeq: []bool{false},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, time.Second, false, &streamStarted)
	require.Nil(t, release)
	var waitErr *gatewayhttp.WaitQueueFullError
	require.ErrorAs(t, err, &waitErr)
	require.Equal(t, "user", waitErr.SlotType)
	require.Equal(t, 1, cache.waitIncrementCalls)
	require.Equal(t, 0, cache.waitDecrementCalls)
}

func TestAcquireUserSlotWithWait_TimeoutDecrementsWaitQueue(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		userSeq:     []bool{false, false, false},
		waitAllowed: true,
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireUserSlotWithWaitTimeout(c, 202, 3, 30*time.Millisecond, false, &streamStarted)
	require.Nil(t, release)
	var cErr *gatewayhttp.ConcurrencyError
	require.ErrorAs(t, err, &cErr)
	require.True(t, cErr.IsTimeout)
	require.Equal(t, 1, cache.waitIncrementCalls)
	require.Equal(t, 1, cache.waitDecrementCalls)
	require.Equal(t, 0, cache.userReleaseCalls)
}

func TestAcquireUserSlotWithWait_RequestCancelDecrementsWaitQueue(t *testing.T) {
	cancelled := make(chan struct{})
	var cancel context.CancelFunc
	cache := &helperConcurrencyCacheStub{
		userSeq:     []bool{false, false},
		waitAllowed: true,
		waitIncrementHook: func() {
			cancel()
			close(cancelled)
		},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
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
	require.Equal(t, 1, cache.waitIncrementCalls)
	require.Equal(t, 1, cache.waitDecrementCalls)
	require.Equal(t, 0, cache.userReleaseCalls)
}

func TestWaitForSlotWithPingTimeout_TimeoutAndStreamPing(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		accountSeq: []bool{false, false, false},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)

	t.Run("timeout_returns_concurrency_error", func(t *testing.T) {
		helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
		c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
		streamStarted := false
		release, err := helper.WaitForSlotWithPingTimeout(c, "account", 101, 2, 130*time.Millisecond, false, &streamStarted, true)
		require.Nil(t, release)
		var cErr *gatewayhttp.ConcurrencyError
		require.ErrorAs(t, err, &cErr)
		require.True(t, cErr.IsTimeout)
	})

	t.Run("stream_mode_sends_ping_before_timeout", func(t *testing.T) {
		helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatComment, 10*time.Millisecond)
		c, rec := newHelperTestContext(http.MethodPost, "/v1/messages")
		streamStarted := false
		release, err := helper.WaitForSlotWithPingTimeout(c, "account", 101, 2, 70*time.Millisecond, true, &streamStarted, true)
		require.Nil(t, release)
		var cErr *gatewayhttp.ConcurrencyError
		require.ErrorAs(t, err, &cErr)
		require.True(t, cErr.IsTimeout)
		require.True(t, streamStarted)
		require.Contains(t, rec.Body.String(), ":\n\n")
	})
}

func TestWaitForSlotWithPingTimeout_ParentContextCanceled(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		accountSeq: []bool{false},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	reqCtx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(reqCtx)
	cancel()

	streamStarted := false
	release, err := helper.WaitForSlotWithPingTimeout(c, "account", 101, 2, time.Second, false, &streamStarted, true)
	require.Nil(t, release)
	require.ErrorIs(t, err, context.Canceled)
	var cErr *gatewayhttp.ConcurrencyError
	require.False(t, errors.As(err, &cErr))
}

func TestWaitForSlotWithPingTimeout_AcquireError(t *testing.T) {
	errCache := &helperConcurrencyCacheStubWithError{
		err: errors.New("redis unavailable"),
	}
	concurrency := scheduler.NewConcurrencyService(errCache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false
	release, err := helper.WaitForSlotWithPingTimeout(c, "account", 1, 1, 200*time.Millisecond, false, &streamStarted, true)
	require.Nil(t, release)
	require.Error(t, err)
	require.Contains(t, err.Error(), "redis unavailable")
}

func TestAcquireAccountSlotWithWaitTimeout_ImmediateAttemptBeforeBackoff(t *testing.T) {
	cache := &helperConcurrencyCacheStub{
		accountSeq: []bool{false},
	}
	concurrency := scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)
	helper := gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatNone, 5*time.Millisecond)
	c, _ := newHelperTestContext(http.MethodPost, "/v1/messages")
	streamStarted := false

	release, err := helper.AcquireAccountSlotWithWaitTimeout(c, 301, 1, 30*time.Millisecond, false, &streamStarted)
	require.Nil(t, release)
	var cErr *gatewayhttp.ConcurrencyError
	require.ErrorAs(t, err, &cErr)
	require.True(t, cErr.IsTimeout)
	require.GreaterOrEqual(t, cache.accountAcquireCalls, 1)
}

type helperConcurrencyCacheStubWithError struct {
	helperConcurrencyCacheStub
	err error
}

func (s *helperConcurrencyCacheStubWithError) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	return false, s.err
}
