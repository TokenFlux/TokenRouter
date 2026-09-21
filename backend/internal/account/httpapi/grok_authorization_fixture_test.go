//go:build unit

package httpapi

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/stretchr/testify/require"
)

// HTTP 组合测试保留原生授权与响应断言，共享生产参数投影。
func newGrokAuthorizationForTest(proxies egress.ProxyRepository, client account.GrokAuthorizationClient, enabled ...bool) *account.GrokAuthorization {
	return account.NewGrokAuthorization(client, provider.GrokAuthorizationOptions(proxies, func() bool {
		return len(enabled) > 0 && enabled[0]
	}))
}

func stopGrokAuthorizationForTest(t *testing.T, authorization *account.GrokAuthorization) {
	t.Helper()
	require.NoError(t, authorization.StopContext(context.Background()))
}
