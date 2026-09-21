//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/stretchr/testify/require"
)

// 未迁消费者测试只组装原生授权，不保留旧服务状态。
func newGrokAuthorizationForTest(proxies egress.ProxyRepository, client account.GrokAuthorizationClient) *account.GrokAuthorization {
	return account.NewGrokAuthorization(client, provider.GrokAuthorizationOptions(proxies, nil))
}

func stopGrokAuthorizationForTest(t *testing.T, authorization *account.GrokAuthorization) {
	t.Helper()
	require.NoError(t, authorization.StopContext(context.Background()))
}
