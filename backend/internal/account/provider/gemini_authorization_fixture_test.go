//go:build unit

// 本文件把原授权测试的客户端与配置快照接到唯一账号实现。
package provider

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

func newGeminiAuthorizationForTest(proxies *mockGeminiProxyRepo, client account.GeminiOAuthClient, discovery account.GeminiCodeAssistClient, drive account.GeminiDriveClient, cfg *codeassist.OAuthConfig) *account.GeminiAuthorization {
	var resolve func(context.Context, int64) (string, bool)
	if proxies != nil {
		resolve = func(ctx context.Context, id int64) (string, bool) {
			proxy, err := proxies.GetByID(ctx, id)
			if err != nil || proxy == nil {
				return "", false
			}
			return proxy.URL(), true
		}
	}
	return account.NewGeminiAuthorization(client, discovery, drive, GeminiAuthorizationOptions(func() codeassist.OAuthConfig { return *cfg }, resolve))
}

func stopGeminiAuthorization(t *testing.T, value *account.GeminiAuthorization) {
	t.Helper()
	if err := value.StopContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
