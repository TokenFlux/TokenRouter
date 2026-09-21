//go:build unit

package rediscache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestTeamInvitationLimiterRecipientCooldown(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewTeamInvitationLimiter(client)

	allowed, retryAfter, err := limiter.CheckAndRecord(context.Background(), 11, " Member@Example.com ")
	require.NoError(t, err)
	require.True(t, allowed)
	require.Zero(t, retryAfter)

	allowed, retryAfter, err = limiter.CheckAndRecord(context.Background(), 11, "member@example.com")
	require.NoError(t, err)
	require.False(t, allowed)
	require.Greater(t, retryAfter, time.Duration(0))
	require.LessOrEqual(t, retryAfter, TeamInvitationRecipientCooldown)
}

func TestTeamInvitationLimiterHourlyLimit(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewTeamInvitationLimiter(client)
	ctx := context.Background()

	for index := 0; index < TeamInvitationHourlyLimit; index++ {
		allowed, retryAfter, err := limiter.CheckAndRecord(ctx, 22, fmt.Sprintf("member-%d@example.com", index))
		require.NoError(t, err)
		require.True(t, allowed)
		require.Zero(t, retryAfter)
	}

	allowed, retryAfter, err := limiter.CheckAndRecord(ctx, 22, "overflow@example.com")
	require.NoError(t, err)
	require.False(t, allowed)
	require.Greater(t, retryAfter, time.Duration(0))
	require.LessOrEqual(t, retryAfter, TeamInvitationHourlyWindow)
}
