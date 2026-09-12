//go:build unit

package apikey

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newInvalidAuthLimiterForTest(threshold, capacity int) *KeyInvalidAuthAbuseLimiter {
	cfg := &Options{APIKeyAuth: APIKeyAuthCacheConfig{
		InvalidAbuse: InvalidAuthAbuseConfig{
			Enabled: true, Threshold: threshold, WindowSeconds: 60, BlockSeconds: 10, Capacity: capacity,
		},
	}}
	return KeyNewInvalidAuthAbuseLimiter(cfg)
}

func TestInvalidAuthAbuseLimiterBlocksAndExpires(t *testing.T) {
	l := newInvalidAuthLimiterForTest(3, 16)
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	for range 3 {
		l.KeyRecord("203.0.113.1")
	}
	retry, blocked := l.KeyCheck("203.0.113.1")
	require.True(t, blocked)
	require.Equal(t, 10*time.Second, retry)

	now = now.Add(11 * time.Second)
	_, blocked = l.KeyCheck("203.0.113.1")
	require.False(t, blocked)
	now = now.Add(61 * time.Second)
	_, blocked = l.KeyCheck("203.0.113.1")
	require.False(t, blocked)
	require.Zero(t, l.KeyHealth().Tracked)
}

func TestInvalidAuthAbuseLimiterCapacityUsesBoundedOverflowProtection(t *testing.T) {
	l := newInvalidAuthLimiterForTest(2, 2)
	now := time.Now()
	l.now = func() time.Time { return now }
	l.KeyRecord("198.51.100.1")
	l.KeyRecord("198.51.100.2")
	l.KeyRecord("198.51.100.3")
	l.KeyRecord("198.51.100.4")

	_, blocked := l.KeyCheck("198.51.100.5")
	require.True(t, blocked)
	_, trackedBlocked := l.KeyCheck("198.51.100.1")
	require.False(t, trackedBlocked, "global overflow protection should spare existing tracked NATs")
	KeyHealth := l.KeyHealth()
	require.Equal(t, int64(2), KeyHealth.Tracked)
	require.Equal(t, uint64(2), KeyHealth.Overflowed)
	require.Equal(t, uint64(1), KeyHealth.GlobalBlocked)
}

func TestInvalidAuthAbuseLimiterConcurrentCapacityIsBounded(t *testing.T) {
	const capacity = 64
	l := newInvalidAuthLimiterForTest(1000, capacity)
	var wg sync.WaitGroup
	for i := range 1000 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.KeyRecord(fmt.Sprintf("198.51.100.%d", i))
		}(i)
	}
	wg.Wait()
	KeyHealth := l.KeyHealth()
	require.LessOrEqual(t, KeyHealth.Tracked, int64(capacity))
	require.Equal(t, uint64(1000), KeyHealth.Recorded)
	require.Equal(t, uint64(1000-capacity), KeyHealth.Overflowed)
}

func TestInvalidAuthAbuseLimiterReclaimsExpiredCapacity(t *testing.T) {
	const capacity = 16
	l := newInvalidAuthLimiterForTest(100, capacity)
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := range capacity {
		l.KeyRecord(fmt.Sprintf("source-%d", i))
	}
	require.Equal(t, int64(capacity), l.KeyHealth().Tracked)

	now = now.Add(61 * time.Second)
	for i := range KeyInvalidAuthAbuseShardCount {
		l.KeyCheck(fmt.Sprintf("new-source-%d", i))
		now = now.Add(101 * time.Millisecond)
	}
	require.Less(t, l.KeyHealth().Tracked, int64(capacity))
	l.KeyRecord("fresh-source")
	require.LessOrEqual(t, l.KeyHealth().Tracked, int64(capacity))
}
