//go:build unit

package provider_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/stretchr/testify/require"
)

// 凭据契约直接组装原生授权和本地供应商替身。
func newGrokAuthorizationForTest(proxies egress.ProxyRepository, client account.GrokAuthorizationClient) *account.GrokAuthorization {
	return account.NewGrokAuthorization(client, provider.GrokAuthorizationOptions(proxies, nil))
}

func stopGrokAuthorizationForTest(t *testing.T, authorization *account.GrokAuthorization) {
	t.Helper()
	require.NoError(t, authorization.StopContext(context.Background()))
}
