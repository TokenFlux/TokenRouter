//go:build unit

package account

import (
	"context"
	"testing"
	"time"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
)

func TestClearCreditsExhausted(t *testing.T) {
	t.Run("account 为 nil 不操作", func(t *testing.T) {
		repo := &antigravityHealthStoreFixture{}
		svc := &AntigravityHealth{Store: repo, Logf: func(string, ...any) {}}
		svc.ClearCreditsExhausted(context.Background(), nil)
		require.Empty(t, repo.extraUpdateCalls)
	})

	t.Run("Extra 为 nil 不操作", func(t *testing.T) {
		repo := &antigravityHealthStoreFixture{}
		svc := &AntigravityHealth{Store: repo, Logf: func(string, ...any) {}}
		svc.ClearCreditsExhausted(context.Background(), &Record{ID: 1})
		require.Empty(t, repo.extraUpdateCalls)
	})

	t.Run("无 modelRateLimitsKey 不操作", func(t *testing.T) {
		repo := &antigravityHealthStoreFixture{}
		svc := &AntigravityHealth{Store: repo, Logf: func(string, ...any) {}}
		svc.ClearCreditsExhausted(context.Background(), &Record{
			ID:    1,
			Extra: map[string]any{"some_key": "value"},
		})
		require.Empty(t, repo.extraUpdateCalls)
	})

	t.Run("无 AICredits key 不操作", func(t *testing.T) {
		repo := &antigravityHealthStoreFixture{}
		svc := &AntigravityHealth{Store: repo, Logf: func(string, ...any) {}}
		svc.ClearCreditsExhausted(context.Background(), &Record{
			ID: 1,
			Extra: map[string]any{
				"model_rate_limits": map[string]any{
					"claude-sonnet-4-5": map[string]any{
						"rate_limited_at":     "2026-03-15T00:00:00Z",
						"rate_limit_reset_at": "2099-03-15T00:00:00Z",
					},
				},
			},
		})
		require.Empty(t, repo.extraUpdateCalls)
	})

	t.Run("有 AICredits key 时删除并调用 UpdateExtra", func(t *testing.T) {
		repo := &antigravityHealthStoreFixture{}
		svc := &AntigravityHealth{Store: repo, Logf: func(string, ...any) {}}
		account := &Record{
			ID: 1,
			Extra: map[string]any{
				"model_rate_limits": map[string]any{
					"claude-sonnet-4-5": map[string]any{
						"rate_limited_at":     "2026-03-15T00:00:00Z",
						"rate_limit_reset_at": "2099-03-15T00:00:00Z",
					},
					CreditsExhaustedKey: map[string]any{
						"rate_limited_at":     "2026-03-15T00:00:00Z",
						"rate_limit_reset_at": time.Now().Add(5 * time.Hour).UTC().Format(time.RFC3339),
					},
				},
			},
		}
		svc.ClearCreditsExhausted(context.Background(), account)
		require.Len(t, repo.extraUpdateCalls, 1)
		// AICredits key 应被删除
		rawLimits := testassert.MustType[map[string]any](account.Extra["model_rate_limits"])
		_, exists := rawLimits[CreditsExhaustedKey]
		require.False(t, exists, "AICredits key 应被删除")
		// 普通模型限流应保留
		_, exists = rawLimits["claude-sonnet-4-5"]
		require.True(t, exists, "普通模型限流应保留")
	})
}
