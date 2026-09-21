//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/stretchr/testify/require"
)

// 编译期接口断言
var _ AccountRepository = (*stubAntigravityAccountRepo)(nil)
var _ SchedulerCache = (*stubSchedulerCache)(nil)

type rateLimitCall struct {
	accountID int64
	resetAt   time.Time
}

type modelRateLimitCall struct {
	accountID int64
	modelKey  string // 存储的 key（应该是官方模型 ID，如 "claude-sonnet-4-5"）
	resetAt   time.Time
}

type extraUpdateCall struct {
	accountID int64
	updates   map[string]any
}

type stubAntigravityAccountRepo struct {
	AccountRepository
	rateCalls           []rateLimitCall
	modelRateLimitCalls []modelRateLimitCall
	extraUpdateCalls    []extraUpdateCall
}

func (s *stubAntigravityAccountRepo) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	s.rateCalls = append(s.rateCalls, rateLimitCall{accountID: id, resetAt: resetAt})
	return nil
}

func (s *stubAntigravityAccountRepo) SetModelRateLimit(ctx context.Context, id int64, modelKey string, resetAt time.Time, reason ...string) error {
	s.modelRateLimitCalls = append(s.modelRateLimitCalls, modelRateLimitCall{accountID: id, modelKey: modelKey, resetAt: resetAt})
	return nil
}

func (s *stubAntigravityAccountRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	s.extraUpdateCalls = append(s.extraUpdateCalls, extraUpdateCall{accountID: id, updates: updates})
	return nil
}

func TestAccountIsSchedulableForModel_AntigravityRateLimits(t *testing.T) {
	now := time.Now()
	future := now.Add(10 * time.Minute)

	account := &Account{
		ID:          1,
		Name:        "acc",
		Platform:    capability.PlatformAntigravity,
		Status:      billing.StatusActive,
		Schedulable: true,
	}

	account.RateLimitResetAt = &future
	require.False(t, account.IsSchedulableForModel("claude-sonnet-4-5"))
	require.False(t, account.IsSchedulableForModel("gemini-3-flash"))

	account.RateLimitResetAt = nil
	require.True(t, account.IsSchedulableForModel("claude-sonnet-4-5"))
	require.True(t, account.IsSchedulableForModel("gemini-3-flash"))
}

func buildGeminiRateLimitBody(delay string) []byte {
	return []byte(fmt.Sprintf(`{"error":{"message":"too many requests","details":[{"metadata":{"quotaResetDelay":%q}}]}}`, delay))
}

func TestParseGeminiRateLimitResetTime_QuotaResetDelay_RoundsUp(t *testing.T) {
	// Avoid flakiness around Unix second boundaries.
	for {
		now := time.Now()
		if now.Nanosecond() < 800*1e6 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	baseUnix := time.Now().Unix()
	ts := ParseGeminiRateLimitResetTime(buildGeminiRateLimitBody("0.1s"))
	require.NotNil(t, ts)
	require.Equal(t, baseUnix+1, *ts, "fractional seconds should be rounded up to the next second")
}

func TestResolveAntigravityForwardBaseURL(t *testing.T) {
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
	}()

	prodURL := "https://prod.test"
	dailyURL := "https://daily.test"
	antigravity.BaseURLs = []string{prodURL, dailyURL}

	tests := []struct {
		name    string
		env     string
		account *Account
		want    string
	}{
		{
			name: "pro defaults to daily", account: &Account{Credentials: map[string]any{"plan_type": " Pro "}},
			want: dailyURL,
		},
		{
			name: "ultra defaults to daily", account: &Account{Credentials: map[string]any{"plan_type": "ULTRA"}},
			want: dailyURL,
		},
		{name: "free defaults to prod", account: &Account{Credentials: map[string]any{"plan_type": "free"}}, want: prodURL},
		{name: "abnormal defaults to prod", account: &Account{Credentials: map[string]any{"plan_type": "Abnormal"}}, want: prodURL},
		{name: "unknown defaults to prod", account: &Account{Credentials: map[string]any{"plan_type": "enterprise"}}, want: prodURL},
		{name: "malformed defaults to prod", account: &Account{Credentials: map[string]any{"plan_type": map[string]any{"name": "pro"}}}, want: prodURL},
		{name: "missing defaults to prod", account: &Account{Credentials: map[string]any{}}, want: prodURL},
		{name: "nil account defaults to prod", account: nil, want: prodURL},
		{
			name: "daily override wins for free tier", env: " daily ",
			account: &Account{Credentials: map[string]any{"plan_type": "free"}},
			want:    dailyURL,
		},
		{name: "prod override keeps production for paid tier", env: " prod ", account: &Account{Credentials: map[string]any{"plan_type": "pro"}}, want: prodURL},
		{name: "unknown override keeps production", env: "unknown", account: &Account{Credentials: map[string]any{"plan_type": "pro"}}, want: prodURL},
		{
			name: "prod override wins for paid tier", env: " PROD ",
			account: &Account{Credentials: map[string]any{"plan_type": "pro"}},
			want:    prodURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(antigravityForwardBaseURLEnv, tt.env)
			require.Equal(t, tt.want, resolveAntigravityForwardBaseURL(tt.account))
		})
	}
}

// stubSchedulerCache 用于测试的 SchedulerCache 实现
type stubSchedulerCache struct {
	SchedulerCache
	setAccountCalls []*Account
	setAccountErr   error
}

func (s *stubSchedulerCache) SetAccount(ctx context.Context, account *Account) error {
	s.setAccountCalls = append(s.setAccountCalls, account)
	return s.setAccountErr
}

// TestSchedulerSnapshotService_UpdateAccountInCache 测试 UpdateAccountInCache 方法
func TestSchedulerSnapshotService_UpdateAccountInCache(t *testing.T) {
	t.Run("calls cache.SetAccount", func(t *testing.T) {
		cache := &stubSchedulerCache{}
		svc := NewSchedulerSnapshotService(cache, nil, nil, nil, nil)

		account := &Account{ID: 123, Name: "test"}
		err := svc.UpdateAccountInCache(context.Background(), account)

		require.NoError(t, err)
		require.Len(t, cache.setAccountCalls, 1)
		require.Equal(t, int64(123), cache.setAccountCalls[0].ID)
	})

	t.Run("returns nil when cache is nil", func(t *testing.T) {
		svc := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)

		err := svc.UpdateAccountInCache(context.Background(), &Account{ID: 1})

		require.NoError(t, err)
	})

	t.Run("returns nil when account is nil", func(t *testing.T) {
		cache := &stubSchedulerCache{}
		svc := NewSchedulerSnapshotService(cache, nil, nil, nil, nil)

		err := svc.UpdateAccountInCache(context.Background(), nil)

		require.NoError(t, err)
		require.Empty(t, cache.setAccountCalls)
	})

	t.Run("propagates cache error", func(t *testing.T) {
		expectedErr := fmt.Errorf("cache error")
		cache := &stubSchedulerCache{setAccountErr: expectedErr}
		svc := NewSchedulerSnapshotService(cache, nil, nil, nil, nil)

		err := svc.UpdateAccountInCache(context.Background(), &Account{ID: 1})

		require.ErrorIs(t, err, expectedErr)
	})
}
