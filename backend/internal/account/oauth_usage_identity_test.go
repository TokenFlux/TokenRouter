package account

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// 正负缓存与共享 flight 都维持账号命名空间，仅允许当前身份消费其结果。
func TestAnthropicUsageNegativeCacheIdentityAndTTL(t *testing.T) {
	now := time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC)
	cache := NewOAuthUsageCache()
	var calls int
	marker := errors.New("provider failure")
	core := NewOAuthUsageService(nil, cache, nil, OAuthUsageOptions{Now: func() time.Time { return now }, Anthropic: func(context.Context, *Record) (*ClaudeUsageResponse, error) { calls++; return nil, marker }})
	old := &Record{ID: 8, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"access_token": "old"}}
	_, err := core.GetUsageForAccount(context.Background(), old, false)
	require.ErrorIs(t, err, marker)
	_, err = core.GetUsageForAccount(context.Background(), old, false)
	require.ErrorIs(t, err, marker)
	require.Equal(t, 1, calls)
	fresh := CloneRecord(old)
	fresh.Credentials = map[string]any{"access_token": "new"}
	_, err = core.GetUsageForAccount(context.Background(), fresh, false)
	require.ErrorIs(t, err, marker)
	require.Equal(t, 2, calls)
	now = now.Add(OAuthUsageAPIErrorCacheTTL)
	_, err = core.GetUsageForAccount(context.Background(), fresh, false)
	require.ErrorIs(t, err, marker)
	require.Equal(t, 3, calls)
}
func TestAntigravitySharedUsageRejectsDifferentIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		entered := make(chan struct{})
		release := make(chan struct{})
		cache := NewOAuthUsageCache()
		core := NewOAuthUsageService(nil, cache, nil, OAuthUsageOptions{Antigravity: AntigravityUsageOptions{CanFetch: func(*Record) bool { return true }, Enrich: func(*UsageInfo, *Record) {}, Fetch: func(context.Context, *Record) (*UsageInfo, error) {
			calls.Add(1)
			close(entered)
			<-release
			return &UsageInfo{Source: "old"}, nil
		}}})
		old := &Record{ID: 9, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"access_token": "old"}}
		first := make(chan error, 1)
		go func() { _, err := core.GetAntigravityUsage(context.Background(), old); first <- err }()
		<-entered
		fresh := CloneRecord(old)
		fresh.Credentials = map[string]any{"access_token": "new"}
		second := make(chan error, 1)
		go func() { _, err := core.GetAntigravityUsage(context.Background(), fresh); second <- err }()
		// 等待两个 goroutine 均阻塞，明确验证第二身份正在等待原 flight。
		synctest.Wait()
		require.Equal(t, int32(1), calls.Load())
		close(release)
		require.NoError(t, <-first)
		require.ErrorIs(t, <-second, ErrUsageObservationChanged)
		require.Equal(t, int32(1), calls.Load())
	})
}
