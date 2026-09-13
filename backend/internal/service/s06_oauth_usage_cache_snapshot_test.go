package service

import (
	"context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 缓存命中必须返回请求私有结果，管理员展示不得修改共享倒计时或嵌套配置。
func TestOAuthUsageCachedResultsAreIndependent(t *testing.T) {
	for _, platform := range []string{PlatformAntigravity, PlatformQoder} {
		t.Run(platform, func(t *testing.T) {
			reset := time.Now().Add(time.Hour)
			usage := &UsageInfo{FiveHour: &UsageProgress{RemainingSeconds: 777, ResetsAt: &reset}, ModelForwardingRules: map[string]string{"old": "new"}, QoderQuota: &QoderQuotaInfo{UserQuota: &QoderQuotaProgress{Remaining: 12}}}
			cache := NewUsageCache()
			svc := &AccountUsageService{cache: cache, antigravityQuotaFetcher: &AntigravityQuotaFetcher{}}
			value := &Account{ID: 981, Platform: platform, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "fixture"}}
			read := func() (*UsageInfo, error) {
				if platform == PlatformAntigravity {
					return svc.getAntigravityUsage(context.Background(), value)
				}
				return svc.getQoderUsage(context.Background(), value, false)
			}
			if platform == PlatformAntigravity {
				cache.StoreAntigravity(value.ID, &antigravityUsageCache{Identity: accountcore.UsageCacheIdentity(AccountRecordView(value)), UsageInfo: usage, Timestamp: time.Now()})
			} else {
				cache.StoreQoder(value.ID, &qoderUsageCache{Identity: accountcore.UsageCacheIdentity(AccountRecordView(value)), UsageInfo: usage, Timestamp: time.Now()})
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
