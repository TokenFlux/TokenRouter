//go:build unit

package account_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// --- 1. CanRefresh 守卫 ---

// TestOpenAITokenRefresherSkipsShadow 验证影子账号不被后台 token 刷新器处理。
func TestOpenAITokenRefresherSkipsShadow(t *testing.T) {
	pid := int64(100)
	r := &accountcore.OpenAITokenRefresher{}
	// 影子账号：ParentAccountID 非 nil → CanRefresh 应返回 false
	require.False(t, r.CanRefresh(&accountcore.Record{ID: 200, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, ParentAccountID: &pid}))
	// 普通账号：有 refresh_token → CanRefresh 应返回 true
	require.True(t, r.CanRefresh(&accountcore.Record{ID: 100, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"refresh_token": "RT"}}))
}

// --- 2. TestAccountConnection 影子凭据解析 ---

// --- 3. EnsureOpenAIPrivacy 守卫 ---
