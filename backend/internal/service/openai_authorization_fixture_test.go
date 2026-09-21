package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/stretchr/testify/require"
)

// 旧搜索夹具使用该测试端点，生产 PAT 地址由原生 Adapter 提供。
var openAICodexPATWhoamiURL = "https://auth.openai.com/api/accounts/v1/user-auth-credential/whoami"

// 旧消费者测试只保留端点注入，授权与会话直接使用原生实现。
func newOpenAIAuthorizationForTest(t *testing.T, proxies egress.ProxyRepository, client provider.OpenAIOAuthClient, dependencies ...*provider.OpenAIAuthorizationDependencies) *account.OpenAIAuthorization {
	t.Helper()
	deps := &provider.OpenAIAuthorizationDependencies{}
	if len(dependencies) > 0 {
		deps = dependencies[0]
	}
	deps.Proxies = proxies
	deps.Client = client
	deps.WhoamiURL = openAICodexPATWhoamiURL
	authorization := account.NewOpenAIAuthorization(account.NewOpenAISessionStore(), provider.OpenAIAuthorizationOptions(deps))
	t.Cleanup(func() { stopOpenAIAuthorizationForTest(t, authorization) })
	return authorization
}

func stopOpenAIAuthorizationForTest(t *testing.T, authorization *account.OpenAIAuthorization) {
	t.Helper()
	require.NoError(t, authorization.StopContext(context.Background()))
}
