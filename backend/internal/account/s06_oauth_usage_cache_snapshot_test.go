package account

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 缓存命中必须返回请求私有结果，管理员展示不得修改共享倒计时或嵌套配置。
func TestOAuthUsageCachedResultsAreIndependent(t *testing.T) {
	for _, platform := range []string{capability.PlatformAntigravity, capability.PlatformQoder} {
		t.Run(platform, func(t *testing.T) {
			reset := time.Now().Add(time.Hour)
			usage := &UsageInfo{FiveHour: &UsageProgress{RemainingSeconds: 777, ResetsAt: &reset}, ModelForwardingRules: map[string]string{"old": "new"}, QoderQuota: &QoderQuotaInfo{UserQuota: &QoderQuotaProgress{Remaining: 12}}}
			cache := NewOAuthUsageCache()
			svc := NewOAuthUsageService(nil, cache, nil, OAuthUsageOptions{Antigravity: AntigravityUsageOptions{CanFetch: (&AntigravityQuota{}).CanFetch}})
			value := &Record{ID: 981, Platform: platform, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"access_token": "fixture"}}
			read := func() (*UsageInfo, error) {
				if platform == capability.PlatformAntigravity {
					return svc.GetAntigravityUsage(context.Background(), value)
				}
				return svc.GetQoderUsage(context.Background(), value, false)
			}
			if platform == capability.PlatformAntigravity {
				cache.StoreAntigravity(value.ID, &OAuthAntigravityUsageCache{Identity: UsageCacheIdentity(value), UsageInfo: usage, Timestamp: time.Now()})
			} else {
				cache.StoreQoder(value.ID, &OAuthQoderUsageCache{Identity: UsageCacheIdentity(value), UsageInfo: usage, Timestamp: time.Now()})
			}
			first, err := read()
			require.NoError(t, err)
			require.NotNil(t, first)
			first.ModelForwardingRules["old"] = "caller"
			first.QoderQuota.UserQuota.Remaining = 0
			second, err := read()
			require.NoError(t, err)
			require.NotNil(t, second)
			require.Equal(t, "new", second.ModelForwardingRules["old"])
			require.Equal(t, float64(12), second.QoderQuota.UserQuota.Remaining)
			require.Equal(t, 777, usage.FiveHour.RemainingSeconds, "倒计时不能原位写入缓存对象")
		})
	}
}
