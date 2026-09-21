package provider

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/stretchr/testify/require"
)

// 授权测试直接配置实际 Adapter，并统一释放原生会话。
func newOpenAIAuthorizationForTest(t *testing.T, proxies egress.ProxyRepository, client OpenAIOAuthClient, dependencies ...*OpenAIAuthorizationDependencies) *account.OpenAIAuthorization {
	t.Helper()
	deps := &OpenAIAuthorizationDependencies{}
	if len(dependencies) > 0 {
		deps = dependencies[0]
	}
	deps.Proxies = proxies
	deps.Client = client
	authorization := account.NewOpenAIAuthorization(account.NewOpenAISessionStore(), OpenAIAuthorizationOptions(deps))
	t.Cleanup(func() { stopOpenAIAuthorizationForTest(t, authorization) })
	return authorization
}

func stopOpenAIAuthorizationForTest(t *testing.T, authorization *account.OpenAIAuthorization) {
	t.Helper()
	require.NoError(t, authorization.StopContext(context.Background()))
}
