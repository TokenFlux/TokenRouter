package apikey

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/requestcontext"
)

// requestLimitCacheStub counts renewal and release attempts with configurable
// reservation failures.
type requestLimitCacheStub struct {
	APIKeyCache // unused cache methods require setup
	// released and refreshed are unsynchronized observations written by cleanup
	// and renewal respectively. Tests join renewal or use synctest.Wait before
	// reading the counters, and configure the error fields before acquisition.
	released   int   // counted usable-context cleanup attempts
	reserveErr error // configured reservation failure result
	refreshErr error // configured lease renewal failure
	refreshed  int   // counted lease renewal attempts
}

// ReserveRequest returns one second and the configured reservation error for
// every call. The fixture ignores Key identity and limits, allowing tests to
// isolate service cleanup around a predetermined result.
func (s *requestLimitCacheStub) ReserveRequest(context.Context, int64, string, int, int) (time.Duration, error) {
	return time.Second, s.reserveErr
}

// RefreshRequest counts one renewal attempt and returns its configured failure.
// Context, Key ID and request identity are ignored; tests install that result
// before acquisition and join renewal before reading its unsynchronized counter.
func (s *requestLimitCacheStub) RefreshRequest(context.Context, int64, string) error {
	s.refreshed++
	return s.refreshErr
}

// ReleaseRequest counts cleanup when its context is usable at entry and returns
// the context error. Cancellation prevents recording, allowing callers to
// observe whether the service creates an independent cleanup context.
func (s *requestLimitCacheStub) ReleaseRequest(ctx context.Context, _ int64, _ string) error {
	if ctx.Err() == nil {
		s.released++
	}
	return ctx.Err()
}

// TestRequestLimitsValidation supplies omitted, zero, positive and PostgreSQL
// integer-edge limits. It checks accepted values and rejection of negative or
// oversized limits through the service create and update validators.
func TestRequestLimitsValidation(t *testing.T) {
	for _, n := range []int{0, 1, 2147483647} {
		require.NoError(t, ValidateRequestLimits(&n, &n))
	}
	require.NoError(t, ValidateRequestLimits(nil, nil))
	for _, n := range []int{-1, 2147483648} {
		require.Error(t, KeyValidateCreateAPIKeyRequest(CreateAPIKeyRequest{ConcurrencyLimit: n}))
		require.Error(t, KeyValidateUpdateAPIKeyRequest(UpdateAPIKeyRequest{RPMLimit: &n}))
	}
}

// TestRequestLimitsReleaseAfterCancellation cancels an admitted request and
// invokes release twice. It checks one recorded cleanup and canceled request
// context despite cleanup being called after client cancellation.
func TestRequestLimitsReleaseAfterCancellation(t *testing.T) {
	cache := &requestLimitCacheStub{}
	service := &APIKeyService{cache: cache}
	ctx, cancel := context.WithCancel(context.Background())
	admitted, release, _, err := service.AcquireRequest(ctx, &APIKey{ID: 1, ConcurrencyLimit: 1})
	require.NoError(t, err)
	// Cancel first to reproduce client disconnect before completion; repeated
	// release must still return the lease through an independent cleanup context.
	cancel()
	release()
	release()
	require.ErrorIs(t, admitted.Err(), context.Canceled)
	require.Equal(t, 1, cache.released)
}

// TestRequestLimitsUnavailable supplies a service with no limiter storage and
// compares configured and unlimited Keys. It checks rejection with no release
// for the configured limit and successful no-op release for the unlimited Key.
func TestRequestLimitsUnavailable(t *testing.T) {
	service := &APIKeyService{}
	_, release, _, err := service.AcquireRequest(context.Background(), &APIKey{ConcurrencyLimit: 1})
	require.ErrorIs(t, err, ErrKeyLimiterUnavailable)
	require.Nil(t, release)
	_, release, _, err = service.AcquireRequest(context.Background(), &APIKey{})
	require.NoError(t, err)
	release()
}

// TestRequestLimitSnapshotRoundTrip supplies a preceding snapshot version and a
// current version containing request and Token rate limits. It checks stale rejection and
// reconstruction of the configured limits without dropping a fractional ceiling.
func TestRequestLimitSnapshotRoundTrip(t *testing.T) {
	service := &APIKeyService{}
	_, used, err := service.KeyApplyAuthCacheEntry("old", &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: 47}})
	require.NoError(t, err)
	require.False(t, used)
	snapshot := service.KeySnapshotFromAPIKey(context.Background(), &APIKey{
		ID: 1, UserID: 2, ConcurrencyLimit: 3, RPMLimit: 20, MaxTokenRateMultiplier: .08, User: &User{ID: 2},
	})
	key, used, err := service.KeyApplyAuthCacheEntry("current", &APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	require.True(t, used)
	require.Equal(t, 3, key.ConcurrencyLimit)
	require.Equal(t, 20, key.RPMLimit)
	require.Equal(t, .08, key.MaxTokenRateMultiplier)
}

// TestRequestLeaseRenewalFailure advances simulated time past the first renewal
// with a failing store. It checks cancellation of ordinary and detached
// contexts, one cleanup, and rejection of later limited work after service
// shutdown.
func TestRequestLeaseRenewalFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := &requestLimitCacheStub{refreshErr: ErrKeyLimiterUnavailable}
		service := &APIKeyService{cache: cache}
		ctx, release, _, err := service.AcquireRequest(context.Background(), &APIKey{ID: 1, ConcurrencyLimit: 1})
		require.NoError(t, err)
		detached := requestcontext.Detach(ctx)
		synctest.Wait()
		// Advance beyond one renewal tick; synctest.Wait then joins runnable
		// renewal work before the test inspects its cancellation or count.
		time.Sleep(31 * time.Second)
		synctest.Wait()
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		require.ErrorIs(t, detached.Err(), context.Canceled)
		late := requestcontext.Detach(ctx)
		require.ErrorIs(t, late.Err(), context.Canceled)
		require.Equal(t, 1, cache.refreshed)
		release()
		require.Equal(t, 1, cache.released)
		// Wait for the operation gate to drain after lease cleanup so subsequent
		// admission can observe the service's permanent stopping state.
		service.Stop()
		_, _, _, err = service.AcquireRequest(context.Background(), &APIKey{ID: 1, ConcurrencyLimit: 1})
		require.ErrorIs(t, err, ErrKeyLimiterUnavailable)
	})
}

// TestRequestLeaseSurvivesClientCancellation disconnects an admitted caller
// while retaining its detached processing context. It checks renewal continues
// before release, then stops and cleans up once after the caller finishes.
func TestRequestLeaseSurvivesClientCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := &requestLimitCacheStub{}
		service := &APIKeyService{cache: cache}
		ctx, cancel := context.WithCancel(context.Background())
		admitted, release, _, err := service.AcquireRequest(ctx, &APIKey{ID: 1, ConcurrencyLimit: 1})
		require.NoError(t, err)
		detached := requestcontext.Detach(admitted)
		cancel()
		synctest.Wait()
		require.NoError(t, detached.Err())
		// Advance beyond one renewal tick; synctest.Wait then joins runnable
		// renewal work before the test inspects its cancellation or count.
		time.Sleep(31 * time.Second)
		synctest.Wait()
		require.Equal(t, 1, cache.refreshed)
		require.Zero(t, cache.released)
		release()
		// Advance through additional renewal intervals after release to detect
		// a worker that failed to stop and continued refreshing the completed lease.
		time.Sleep(time.Minute)
		synctest.Wait()
		require.Equal(t, 1, cache.refreshed)
		require.Equal(t, 1, cache.released)
	})
}

// TestRequestReservationUnknownOutcome supplies an unavailable reservation
// result that could follow Redis response loss. It checks independent cleanup
// occurs and no release callback is returned to the rejected caller.
func TestRequestReservationUnknownOutcome(t *testing.T) {
	cache := &requestLimitCacheStub{reserveErr: ErrKeyLimiterUnavailable}
	service := &APIKeyService{cache: cache}
	_, release, _, err := service.AcquireRequest(context.Background(), &APIKey{ID: 1, ConcurrencyLimit: 1})
	require.ErrorIs(t, err, ErrKeyLimiterUnavailable)
	require.Nil(t, release)
	require.Equal(t, 1, cache.released)
	// Wait for service shutdown after rejection; a leaked reservation operation
	// count would keep the drain monitor from completing this fixture's teardown.
	service.Stop()
}
