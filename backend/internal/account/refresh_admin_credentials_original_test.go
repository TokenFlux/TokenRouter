//go:build unit

package account

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 模拟交换使用旧凭据，管理员在返回持久化之前完成重新授权。
func TestS06RefreshPreservesAdministratorCredentials(t *testing.T) {
	for _, platform := range []string{capability.PlatformOpenAI, capability.PlatformAnthropic, capability.PlatformGemini, capability.PlatformAntigravity} {
		t.Run(platform, func(t *testing.T) {
			original := &Record{ID: 1, Platform: platform, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Credentials: map[string]any{"refresh_token": "before", "access_token": "before"}}
			repo := &refreshAPIAccountRepo{account: original}
			executor := &refreshAPIExecutorStub{needsRefresh: true, credentials: map[string]any{"refresh_token": "rotated-old", "access_token": "rotated-old"}, onRefresh: func() {
				current := *original
				current.Credentials = map[string]any{"refresh_token": "administrator-new", "access_token": "administrator-new"}
				repo.account = &current
			}}
			api := newOriginalRefreshAPI(repo, nil)
			result, err := api.RefreshIfNeeded(context.Background(), original, executor, time.Minute)
			require.NoError(t, err)
			require.False(t, result.Refreshed)
			require.Nil(t, result.NewCredentials)
			require.Equal(t, "administrator-new", result.Account.Credentials["refresh_token"])
			require.Equal(t, "administrator-new", repo.account.Credentials["refresh_token"])
			require.Equal(t, 1, executor.refreshCalls)
		})
	}
}
