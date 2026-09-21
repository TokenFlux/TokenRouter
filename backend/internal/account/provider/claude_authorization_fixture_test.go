//go:build unit

// 本文件仅绑定授权测试的代理替身和平台参数。
package provider

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

func newClaudeAuthorizationForTest(proxies *mockProxyRepoForOAuth, client account.ClaudeOAuthClient) *account.ClaudeAuthorization {
	options := ClaudeAuthorizationOptions(func(ctx context.Context, id int64) (string, bool) {
		proxy, err := proxies.GetByID(ctx, id)
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	})
	return account.NewClaudeAuthorization(client, options)
}

func stopClaudeAuthorization(t *testing.T, value *account.ClaudeAuthorization) {
	t.Helper()
	if err := value.StopContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
