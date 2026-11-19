package querycache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTTLMapLifecycleAndExpiration exercises nil-receiver safety, typed value
// lookups, lazy read-time eviction, and explicit DeleteExpired sweeps. Expired
// entries must disappear from both Get and ItemCount without disturbing active keys.
func TestTTLMapLifecycleAndExpiration(t *testing.T) {
	var nilMap *TTLMap[int]
	_, ok := nilMap.Get("missing")
	require.False(t, ok)
	nilMap.SetDefault("ignored", 1)
	nilMap.Delete("ignored")
	nilMap.DeleteExpired()
	require.Zero(t, nilMap.ItemCount())

	cache := NewTTLMap[string](40 * time.Millisecond)
	cache.SetDefault("a", "alpha")
	cache.Set("b", "beta", time.Minute)

	got, ok := cache.Get("a")
	require.True(t, ok)
	require.Equal(t, "alpha", got)
	require.Equal(t, 2, cache.ItemCount())

	time.Sleep(80 * time.Millisecond)
	_, ok = cache.Get("a")
	require.False(t, ok)
	require.Equal(t, 1, cache.ItemCount())

	cache.Set("c", "gamma", 20*time.Millisecond)
	time.Sleep(40 * time.Millisecond)
	cache.DeleteExpired()
	require.Equal(t, 1, cache.ItemCount())

	cache.Delete("b")
	require.Zero(t, cache.ItemCount())
}
