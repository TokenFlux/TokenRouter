package rediscache

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestNewDashboardCacheKeyPrefix(t *testing.T) {
	var cache usage.DashboardStatsCache = NewDashboardCache(nil, "prod")
	impl, ok := cache.(*DashboardCache)
	require.True(t, ok)
	require.Equal(t, "prod:", impl.keyPrefix)

	cache = NewDashboardCache(nil, "staging:")
	impl, ok = cache.(*DashboardCache)
	require.True(t, ok)
	require.Equal(t, "staging:", impl.keyPrefix)
}
