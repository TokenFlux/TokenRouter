//go:build unit

package provider

import (
	"context"
	"net/http"
	"testing"
	"time"

	"sync/atomic"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 测试只控制账号运行时的时间输入，不访问私有缓存。
type response429Clock struct{ nanos atomic.Int64 }

func (c *response429Clock) Now() time.Time {
	if n := c.nanos.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return time.Now()
}
func (c *response429Clock) Set(now time.Time) { c.nanos.Store(now.UnixNano()) }
func expireResponseRetryForTest(h *OpenAIResponseHealth, id int64) *response429Clock {
	clock := &response429Clock{}
	h.Runtime = accountcore.NewRuntimeBlockState(clock.Now)
	clock.Set(time.Now().Add(-accountcore.RuntimeRetryWindow - time.Second))
	h.Runtime.RetryWindowActive(id)
	clock.nanos.Store(0)
	return clock
}
func TestOpenAI429FastPath_BlocksOAuthOnlyAfterRetryWindow(t *testing.T) {
	svc := &OpenAIResponseHealth{Runtime: accountcore.NewRuntimeBlockState(time.Now)}
	account := &accountcore.Record{LoadLocation: time.LoadLocation, ID: 420, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}
	expireResponseRetryForTest(svc, account.ID)

	svc.markOAuth429(context.Background(), account, http.Header{}, nil)

	require.True(t, svc.Runtime.Blocked(account.ID, func() string { return accountcore.RefreshCredentialIdentity(account) }))
	require.False(t, svc.RetryOAuth429(account, http.StatusTooManyRequests, false, nil, nil))
}

func TestOpenAIHTTP429StillUsesQuotaResetHeaders(t *testing.T) {
	svc := &OpenAIResponseHealth{Runtime: accountcore.NewRuntimeBlockState(time.Now), Health: &UpstreamHealth{Core: accountcore.NewHealthService(nil, nil, accountcore.HealthOptions{Now: time.Now})}}
	account := &accountcore.Record{LoadLocation: time.LoadLocation, ID: 422, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}
	clock := expireResponseRetryForTest(svc, account.ID)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "37")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")

	svc.markOAuth429(context.Background(), account, headers, nil)

	clock.Set(time.Now().Add(6 * 24 * time.Hour))
	require.True(t, svc.Runtime.Blocked(account.ID, func() string { return accountcore.RefreshCredentialIdentity(account) }), "HTTP 429 保留上游配额重置边界")
}

// TestOpenAI429FastPath_SkipsSparkShadow 外审第8轮 P1:spark 影子被选中后若 /responses 返回 429,
// 不得按 global x-codex-* 信号写内存运行时熔断(否则 spark 被冷却到 global reset、单影子场景无可用账号)。
func TestOpenAI429FastPath_SkipsSparkShadow(t *testing.T) {
	svc := &OpenAIResponseHealth{Runtime: accountcore.NewRuntimeBlockState(time.Now)}
	parentID := int64(800)
	shadow := &accountcore.Record{LoadLocation: time.LoadLocation, ID: 801,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  accountcore.QuotaDimensionSpark}
	normal := &accountcore.Record{LoadLocation: time.LoadLocation, ID: 802, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}

	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "18000")
	headers.Set("x-codex-primary-window-minutes", "300")

	svc.markOAuth429(context.Background(), shadow, headers, nil)
	svc.markOAuth429(context.Background(), normal, headers, nil)

	require.False(t, svc.Runtime.Blocked(shadow.ID, func() string { return accountcore.RefreshCredentialIdentity(shadow) }), "spark shadow must not be runtime-blocked by /responses global 429")
	require.True(t, svc.Runtime.Blocked(normal.ID, func() string { return accountcore.RefreshCredentialIdentity(normal) }), "normal OpenAI OAuth account with an exhausted 5h window must be paused")
}
