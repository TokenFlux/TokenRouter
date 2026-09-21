//go:build unit

package provider

import (
	acct "github.com/TokenFlux/TokenRouter/internal/account"

	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestSetAntigravityModelRateLimits_GeminiWritesFamilyScope(t *testing.T) {
	repo := &antigravityFamilyStoreFixture{}
	svc := &acct.AntigravityHealth{ModelKeys: AntigravityModelLimitKeys, Logf: func(string, ...any) {}}
	account := &acct.Record{ID: 789, Platform: capability.PlatformAntigravity}
	resetAt := time.Now().Add(30 * time.Second)

	success := svc.SetAntigravityModelRateLimits(
		context.Background(),
		repo,
		account,
		"gemini-3-pro",
		"[test]",
		429,
		resetAt,
		false,
	)

	require.True(t, success)
	require.Len(t, repo.modelRateLimitCalls, 2)
	require.Equal(t, "gemini-3-pro", repo.modelRateLimitCalls[0].modelKey)
	require.Equal(t, "antigravity:gemini", repo.modelRateLimitCalls[1].modelKey)
}

func TestSetAntigravityModelRateLimits_ClaudeDoesNotWriteGeminiScope(t *testing.T) {
	repo := &antigravityFamilyStoreFixture{}
	svc := &acct.AntigravityHealth{ModelKeys: AntigravityModelLimitKeys, Logf: func(string, ...any) {}}
	account := &acct.Record{ID: 790, Platform: capability.PlatformAntigravity}
	resetAt := time.Now().Add(30 * time.Second)

	success := svc.SetAntigravityModelRateLimits(
		context.Background(),
		repo,
		account,
		"claude-sonnet-4-5",
		"[test]",
		429,
		resetAt,
		false,
	)

	require.True(t, success)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].modelKey)
}

// 保存每次模型窗口写入，用于核对原模型与家族键。
type antigravityFamilyStoreFixture struct {
	acct.AntigravityHealthStore
	modelRateLimitCalls []struct{ modelKey string }
}

func (s *antigravityFamilyStoreFixture) SetModelRateLimit(_ context.Context, _ int64, key string, _ time.Time, _ ...string) error {
	s.modelRateLimitCalls = append(s.modelRateLimitCalls, struct{ modelKey string }{key})
	return nil
}
