//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	dbmigrations "github.com/BrandonVee/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration238GeneralizesAdvancedScheduler(t *testing.T) {
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("238_generalize_advanced_scheduler.sql")
	require.NoError(t, err)

	t.Run("旧开关开启时仅迁移 OpenAI 与 Grok 分组", func(t *testing.T) {
		tx := testTx(t)
		clearMigration238Settings(t, ctx, tx)

		openAIGroupID := insertMigration238Group(t, ctx, tx, "openai")
		grokGroupID := insertMigration238Group(t, ctx, tx, "grok")
		geminiGroupID := insertMigration238Group(t, ctx, tx, "gemini")
		setMigration238Setting(t, ctx, tx, "openai_advanced_scheduler_enabled", "true")
		setMigration238Setting(t, ctx, tx, "openai_advanced_scheduler_sticky_weighted_enabled", "true")
		setMigration238Setting(t, ctx, tx, "openai_advanced_scheduler_lb_top_k", "7")
		setMigration238Setting(t, ctx, tx, "openai_advanced_scheduler_weight_priority", "2.5")

		require.NoError(t, executeMigration238(ctx, tx, migrationSQL))
		require.NoError(t, executeMigration238(ctx, tx, migrationSQL), "迁移必须可安全重复执行")

		require.Equal(t, "advanced", migration238GroupSchedulerType(t, ctx, tx, openAIGroupID))
		require.Equal(t, "advanced", migration238GroupSchedulerType(t, ctx, tx, grokGroupID))
		require.Equal(t, "basic", migration238GroupSchedulerType(t, ctx, tx, geminiGroupID))
		require.Equal(t, "true", migration238SettingValue(t, ctx, tx, "advanced_scheduler_sticky_weighted_enabled"))
		require.Equal(t, "7", migration238SettingValue(t, ctx, tx, "advanced_scheduler_lb_top_k"))
		require.Equal(t, "2.5", migration238SettingValue(t, ctx, tx, "advanced_scheduler_weight_priority"))
		require.Zero(t, migration238LegacySettingCount(t, ctx, tx))

		newGroupID := insertMigration238Group(t, ctx, tx, "qoder")
		require.Equal(t, "basic", migration238GroupSchedulerType(t, ctx, tx, newGroupID))
		_, err := tx.ExecContext(ctx, "UPDATE groups SET scheduler_type = 'invalid' WHERE id = $1", newGroupID)
		require.Error(t, err, "scheduler_type 约束必须拒绝非法值")
	})

	t.Run("旧开关关闭时所有存量分组保持基础", func(t *testing.T) {
		tx := testTx(t)
		clearMigration238Settings(t, ctx, tx)

		openAIGroupID := insertMigration238Group(t, ctx, tx, "openai")
		grokGroupID := insertMigration238Group(t, ctx, tx, "grok")
		geminiGroupID := insertMigration238Group(t, ctx, tx, "gemini")
		setMigration238Setting(t, ctx, tx, "openai_advanced_scheduler_enabled", "false")

		require.NoError(t, executeMigration238(ctx, tx, migrationSQL))

		require.Equal(t, "basic", migration238GroupSchedulerType(t, ctx, tx, openAIGroupID))
		require.Equal(t, "basic", migration238GroupSchedulerType(t, ctx, tx, grokGroupID))
		require.Equal(t, "basic", migration238GroupSchedulerType(t, ctx, tx, geminiGroupID))
		require.Zero(t, migration238LegacySettingCount(t, ctx, tx))
	})
}

func executeMigration238(ctx context.Context, tx *sql.Tx, migrationSQL []byte) error {
	_, err := tx.ExecContext(ctx, string(migrationSQL))
	return err
}

func insertMigration238Group(t *testing.T, ctx context.Context, tx *sql.Tx, platform string) int64 {
	t.Helper()
	name := fmt.Sprintf("migration-238-%s-%d", platform, time.Now().UnixNano())
	var groupID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform)
VALUES ($1, $2)
RETURNING id
`, name, platform).Scan(&groupID))
	return groupID
}

func clearMigration238Settings(t *testing.T, ctx context.Context, tx *sql.Tx) {
	t.Helper()
	_, err := tx.ExecContext(ctx, `
DELETE FROM settings
WHERE key LIKE 'openai_advanced_scheduler_%'
   OR key LIKE 'advanced_scheduler_%'
`)
	require.NoError(t, err)
}

func setMigration238Setting(t *testing.T, ctx context.Context, tx *sql.Tx, key string, value string) {
	t.Helper()
	_, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
`, key, value)
	require.NoError(t, err)
}

func migration238GroupSchedulerType(t *testing.T, ctx context.Context, tx *sql.Tx, groupID int64) string {
	t.Helper()
	var schedulerType string
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT scheduler_type FROM groups WHERE id = $1", groupID).Scan(&schedulerType))
	return schedulerType
}

func migration238SettingValue(t *testing.T, ctx context.Context, tx *sql.Tx, key string) string {
	t.Helper()
	var value string
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = $1", key).Scan(&value))
	return value
}

func migration238LegacySettingCount(t *testing.T, ctx context.Context, tx *sql.Tx) int {
	t.Helper()
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM settings
WHERE key LIKE 'openai_advanced_scheduler_%'
`).Scan(&count))
	return count
}
