//go:build unit

package rediscache

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	sessionstore "github.com/TokenFlux/TokenRouter/internal/infra/redis/session"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSessionStoreRedisFallbackIsLimitedToFailedWrites(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	store := NewGrokSessionStore(client)
	defer store.Stop()
	session := func(state string) *account.GrokOAuthSession {
		return &account.GrokOAuthSession{State: state, CreatedAt: time.Now()}
	}

	store.Set("remote", session("remote"))
	require.NoError(t, sessionstore.New(client, "oauth:session:xai", account.GrokSessionTTL).Delete(context.Background(), "remote"))
	_, ok := store.Get("remote")
	require.False(t, ok, "a remote miss must not revive the stale local copy")

	mr.Close()
	store.Set("local-only", session("local"))
	got, ok := store.Get("local-only")
	require.True(t, ok)
	require.Equal(t, "local", got.State)
	require.True(t, store.TryConsumeSession("local-only"))
	require.False(t, store.TryConsumeSession("local-only"))
}
