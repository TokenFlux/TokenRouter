package apikey

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// dailyUsageCalendarCache 只接收本次日期迁移涉及的计数与 TTL，不实现其他缓存行为。
type dailyUsageCalendarCache struct {
	APIKeyCache
	key string
	ttl time.Duration
}

func (c *dailyUsageCalendarCache) IncrementDailyUsage(_ context.Context, key string) error {
	c.key = key
	return nil
}
func (c *dailyUsageCalendarCache) SetDailyUsageExpiry(_ context.Context, key string, ttl time.Duration) error {
	if c.key != key {
		return fmt.Errorf("expiry key differs from counter key")
	}
	c.ttl = ttl
	return nil
}

func TestDailyUsageCacheUsesInjectedCalendar(t *testing.T) {
	for _, location := range []*time.Location{time.FixedZone("east", 14*3600), time.FixedZone("west", -12*3600)} {
		t.Run(location.String(), func(t *testing.T) {
			calendar := timezone.NewCalendar(location)
			cache := &dailyUsageCalendarCache{}
			service := NewAPIKeyService(nil, nil, nil, nil, nil, cache, &Options{Calendar: calendar})
			before := calendar.Now().Format("2006-01-02")
			require.NoError(t, service.IncrementUsage(context.Background(), 17))
			after := calendar.Now().Format("2006-01-02")
			require.Contains(t, []string{"apikey:usage:17:" + before, "apikey:usage:17:" + after}, cache.key)
			require.Equal(t, 24*time.Hour, cache.ttl)
		})
	}
}
