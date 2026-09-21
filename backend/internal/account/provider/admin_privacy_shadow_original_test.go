//go:build unit

package provider

import (
	"context"
	"errors"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

// TestEnsureOpenAIPrivacySkipsShadow 验证影子账号跳过隐私设置（不调用 privacyClientFactory）。
// 影子账号透传母账号凭据，但 Extra 通常为空，需给它一个 access_token 才能让
// 现有的 token=="" 提前返回路径失效，从而真实验证 IsCredentialShadow 守卫。
func TestEnsureOpenAIPrivacySkipsShadow(t *testing.T) {
	pid := int64(100)
	shadow := &accountcore.Record{
		ID:              200,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		ParentAccountID: &pid,

		Credentials: map[string]any{"access_token": "shadow-passthrough-token"},
	}
	privacyCalled := false
	svc := accountcore.NewPrivacyService(nil, nil, PrivacyOptions(func(proxyURL string) (*req.Client, error) {
		privacyCalled = true
		return nil, errors.New("should not reach factory for shadow account")
	}, openai.PrivacyEndpoints{}))
	got := svc.EnsureOpenAIPrivacy(context.Background(), shadow)
	require.Equal(t, "", got)
	require.False(t, privacyCalled, "privacyClientFactory 不应被影子账号触发")
}
