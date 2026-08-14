//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGrokTokenRefreshWindowWithJitter_StableAndBounded(t *testing.T) {
	base := grokTokenRefreshSkew
	w1 := grokTokenRefreshWindowWithJitter(42, base)
	w2 := grokTokenRefreshWindowWithJitter(42, base)
	require.Equal(t, w1, w2, "same account id must yield stable window")
	require.GreaterOrEqual(t, w1, grokTokenRefreshSkewMin)
	require.LessOrEqual(t, w1, base)

	// 不同账号通常应得到不同结果。该性质并不保证任意账号对都不同，
	// 但连续 ID 的哈希分布足以在小样本中断言存在差异。
	seen := map[time.Duration]bool{}
	for id := int64(1); id <= 50; id++ {
		seen[grokTokenRefreshWindowWithJitter(id, base)] = true
	}
	require.Greater(t, len(seen), 1, "jitter should spread windows across accounts")
}

func TestGrokTokenRefresher_NeedsRefresh_UsesSkewFloor(t *testing.T) {
	refresher := NewGrokTokenRefresher(nil)
	// 令牌 50 分钟后过期，位于一小时预热窗口内，应当刷新。
	expires := time.Now().Add(50 * time.Minute).UTC().Format(time.RFC3339)
	account := &Account{
		ID:       7,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "at",
			"refresh_token": "rt",
			"expires_at":    expires,
		},
	}
	// 传入很小的窗口，NeedsRefresh 会先提升到 grokTokenRefreshSkew 再应用错峰偏移。
	require.True(t, refresher.NeedsRefresh(account, time.Minute))

	// 过期时间仍很远时无需刷新。
	account.Credentials["expires_at"] = time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	require.False(t, refresher.NeedsRefresh(account, time.Minute))
}
