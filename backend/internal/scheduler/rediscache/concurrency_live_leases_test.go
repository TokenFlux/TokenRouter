package rediscache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// Live lease unit tests verify transfer from ordinary provider and user slots,
// shared capacity enforcement against ordinary slot admission, renewal, and
// sixty-second expiration without refresh.

// TestLiveLeaseReplacesRegularSlotsAndCountsTowardLimits reserves ordinary
// provider/user slots before acquiring a replacement Live lease. It checks
// counted Live occupancy, ordinary-provider rejection, successful renewal and
// provider admission after Live release.
func TestLiveLeaseReplacesRegularSlotsAndCountsTowardLimits(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(scheduler.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	providerAcquired, err := regular.AcquireProviderSlot(ctx, 10, 1, "regular-provider")
	require.NoError(t, err)
	require.True(t, providerAcquired)
	userAcquired, err := regular.AcquireUserSlot(ctx, 20, 1, "regular-user")
	require.NoError(t, err)
	require.True(t, userAcquired)

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "live-lease", true)
	require.NoError(t, err)
	require.True(t, acquired)
	// Release the ordinary reservations after successful replacement so
	// subsequent count assertions observe the Live lease alone rather than the
	// transfer overlap.
	require.NoError(t, regular.ReleaseProviderSlot(ctx, 10, "regular-provider"))
	require.NoError(t, regular.ReleaseUserSlot(ctx, 20, "regular-user"))

	providerCount, err := regular.GetProviderConcurrency(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, providerCount)
	userCount, err := regular.GetUserConcurrency(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 1, userCount)
	providerAcquired, err = regular.AcquireProviderSlot(ctx, 10, 1, "ordinary-blocked")
	require.NoError(t, err)
	require.False(t, providerAcquired)

	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "live-lease")
	require.NoError(t, err)
	require.True(t, refreshed)
	require.NoError(t, live.ReleaseLiveLease(ctx, 10, 20, 30, "live-lease"))
	providerAcquired, err = regular.AcquireProviderSlot(ctx, 10, 1, "ordinary-allowed")
	require.NoError(t, err)
	require.True(t, providerAcquired)
}

// TestLiveLeaseExpiresWithoutRefresh acquires a Live lease and expires the
// fixture keys beyond its lifetime. It checks ordinary provider admission and
// failure to renew the expired Live identity.
func TestLiveLeaseExpiresWithoutRefresh(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(scheduler.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "expired-live", false)
	require.NoError(t, err)
	require.True(t, acquired)

	// Expire the unrenewed fixture keys beyond their sixty-second lifetime
	// before checking that ordinary admission can reuse capacity and Live
	// renewal detects absence.
	redisServer.FastForward(61 * time.Second)
	acquired, err = regular.AcquireProviderSlot(ctx, 10, 1, "ordinary-after-expiry")
	require.NoError(t, err)
	require.True(t, acquired)
	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "expired-live")
	require.NoError(t, err)
	require.False(t, refreshed)
}
