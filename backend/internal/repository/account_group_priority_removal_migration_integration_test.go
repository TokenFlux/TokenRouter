//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/BrandonVee/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

// TestMigration240RemovesAccountGroupPriority 验证关联优先级迁移可重复执行且清理完整。
func TestMigration240RemovesAccountGroupPriority(t *testing.T) {
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("240_remove_account_group_priority.sql")
	require.NoError(t, err)
	tx := testTx(t)

	// 集成测试框架已应用全部迁移，先在事务内恢复旧 schema，覆盖真实升级路径。
	_, err = tx.ExecContext(ctx, `
ALTER TABLE account_groups
    ADD COLUMN priority INTEGER NOT NULL DEFAULT 50;
CREATE INDEX idx_account_groups_priority
    ON account_groups (priority);
CREATE INDEX idx_account_groups_group_priority_account
    ON account_groups (group_id, priority, account_id);
CREATE INDEX idx_account_groups_account_priority_group
    ON account_groups (account_id, priority, group_id);
`)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	var columnCount int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'account_groups'
  AND column_name = 'priority'
`).Scan(&columnCount))
	require.Zero(t, columnCount)

	var indexCount int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM pg_indexes
WHERE schemaname = current_schema()
  AND indexname IN (
      'idx_account_groups_priority',
      'idx_account_groups_group_priority_account',
      'idx_account_groups_account_priority_group'
  )
`).Scan(&indexCount))
	require.Zero(t, indexCount)

	var groupID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform)
VALUES ('migration-240-capacity-order', 'openai')
RETURNING id
`).Scan(&groupID))

	// 先插入低优先级账号，使 ID 顺序与优先级顺序相反；同优先级账号再按 ID 稳定排序。
	insertAccount := func(name string, priority int) int64 {
		t.Helper()
		var accountID int64
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, priority)
VALUES ($1, 'openai', 'apikey', $2)
RETURNING id
`, name, priority).Scan(&accountID))
		return accountID
	}
	lowPriorityID := insertAccount("migration-240-low-priority", 20)
	highPriorityFirstID := insertAccount("migration-240-high-priority-first", 10)
	highPrioritySecondID := insertAccount("migration-240-high-priority-second", 10)

	// 打乱关联写入顺序，确保容量查询不依赖 account_groups 的物理顺序。
	for _, accountID := range []int64{lowPriorityID, highPrioritySecondID, highPriorityFirstID} {
		_, err = tx.ExecContext(ctx, `
INSERT INTO account_groups (account_id, group_id)
VALUES ($1, $2)
`, accountID, groupID)
		require.NoError(t, err)
	}

	repo := newAccountRepositoryWithSQL(nil, tx, nil)
	capacityRows, err := repo.ListSchedulableCapacityByGroupIDs(ctx, []int64{groupID})
	require.NoError(t, err)
	require.Len(t, capacityRows, 3)
	require.Equal(t, []int64{highPriorityFirstID, highPrioritySecondID, lowPriorityID}, []int64{
		capacityRows[0].AccountID,
		capacityRows[1].AccountID,
		capacityRows[2].AccountID,
	})
}
