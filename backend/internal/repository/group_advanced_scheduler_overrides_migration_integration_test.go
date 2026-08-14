//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbmigrations "github.com/BrandonVee/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration239AddsGroupAdvancedSchedulerOverrides(t *testing.T) {
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("239_add_group_advanced_scheduler_overrides.sql")
	require.NoError(t, err)
	tx := testTx(t)

	// 该迁移可能在已有实例的启动阶段被重复探测，必须保持幂等。
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	name := fmt.Sprintf("migration-239-%d", time.Now().UnixNano())
	var groupID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform)
VALUES ($1, 'gemini')
RETURNING id
`, name).Scan(&groupID))

	var stored string
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT advanced_scheduler_overrides::text
FROM groups
WHERE id = $1
`, groupID).Scan(&stored))
	require.JSONEq(t, `{}`, stored)

	_, err = tx.ExecContext(ctx, "SAVEPOINT invalid_advanced_scheduler_overrides")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
UPDATE groups
SET advanced_scheduler_overrides = '[]'::jsonb
WHERE id = $1
`, groupID)
	require.Error(t, err, "数组不能作为分组高级调度器覆盖")
	_, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT invalid_advanced_scheduler_overrides")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "RELEASE SAVEPOINT invalid_advanced_scheduler_overrides")
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `
UPDATE groups
SET advanced_scheduler_overrides = '{"lb_top_k":3,"weight_queue":0}'::jsonb
WHERE id = $1
`, groupID)
	require.NoError(t, err)
}
