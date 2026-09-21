//go:build integration

package account_test

import (
	"context"
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/stretchr/testify/require"
)

// 历史废弃键是清理断言的输入，不能由当前账号配置重新生成。
const (
	deprecatedUpstreamBillingProbeExtraKey        = "upstream_billing_probe"
	deprecatedUpstreamBillingProbeEnabledExtraKey = "upstream_billing_probe_enabled"
)

// 每个事务测试结束回滚；提交型竞争场景由原断言显式删除其测试行。
func testEntTx(t *testing.T) *dbent.Tx {
	t.Helper()
	tx, err := integrationEntClient.Tx(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func testEntClient(t *testing.T) *dbent.Client {
	t.Helper()
	return integrationEntClient
}
