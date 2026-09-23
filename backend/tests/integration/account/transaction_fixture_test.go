//go:build integration

package account_test

import (
	"context"
	"database/sql"
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/stretchr/testify/require"
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

// 迁移 SQL 契约独立开启事务，并在测试结束恢复原 schema。
func testTx(t *testing.T) *sql.Tx {
	t.Helper()
	tx, err := integrationDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tx.Rollback()) })
	return tx
}
