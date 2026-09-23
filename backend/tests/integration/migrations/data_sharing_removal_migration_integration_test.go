//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	dbmigrations "github.com/TokenFlux/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

// TestMigration265RemovesDataSharing 验证迁移可重复执行，并且只改动目标 schema 与设置。
func TestMigration265RemovesDataSharing(t *testing.T) {
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("265_remove_data_sharing.sql")
	require.NoError(t, err)
	tx := testTx(t)

	// 集成测试库已位于最新 schema，先恢复旧版本中的数据共享对象以模拟升级。
	_, err = tx.ExecContext(ctx, `
CREATE TABLE data_share_sessions (id BIGSERIAL PRIMARY KEY);
CREATE TABLE data_share_export_artifacts (id BIGSERIAL PRIMARY KEY);
ALTER TABLE groups ADD COLUMN data_sharing_enabled BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX idx_groups_data_sharing_enabled ON groups (data_sharing_enabled);
ALTER TABLE api_keys
    ADD COLUMN data_sharing_notice_version INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN data_sharing_confirmed_group_id BIGINT,
    ADD COLUMN data_sharing_confirmed_at TIMESTAMPTZ;
CREATE INDEX idx_api_keys_data_sharing_confirmed_group_id
    ON api_keys (data_sharing_confirmed_group_id);
ALTER TABLE api_key_composite_groups
    ADD COLUMN data_sharing_notice_version INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN data_sharing_confirmed_at TIMESTAMPTZ;
`)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES
    ('data_sharing_enabled', 'true'),
    ('data_sharing_notice_content', 'notice'),
    ('data_sharing_notice_version', '7'),
    ('data_sharing_capture_skip_rules', '{}'),
    ('data_sharing_export_ticket_key', 'secret'),
    ('data_sharing_export_remote_config', '{}'),
    ('data_sharing_export_remote_prefix', 'legacy/'),
    ('data_sharing_storage_limit_bytes', '1024'),
    ('data_sharing_capture_runtime', '{}'),
    ('backup_content_config', '{"include_usage_records":true,"include_data_share_sessions":true,"keep_me":"yes"}'),
    ('migration_265_preserved', 'unchanged')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
`)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	for _, table := range []string{"data_share_export_artifacts", "data_share_sessions"} {
		var regclass sql.NullString
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT to_regclass('public.' || $1)", table).Scan(&regclass))
		require.Falsef(t, regclass.Valid, "表 %s 应已删除", table)
	}

	for table, columns := range map[string][]string{
		"groups":                   {"data_sharing_enabled"},
		"api_keys":                 {"data_sharing_notice_version", "data_sharing_confirmed_group_id", "data_sharing_confirmed_at"},
		"api_key_composite_groups": {"data_sharing_notice_version", "data_sharing_confirmed_at"},
	} {
		for _, column := range columns {
			var count int
			require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = $1
  AND column_name = $2
`, table, column).Scan(&count))
			require.Zero(t, count, "%s.%s 应已删除", table, column)
		}
	}

	var removedSettingCount int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM settings WHERE key LIKE 'data_sharing_%'
`).Scan(&removedSettingCount))
	require.Zero(t, removedSettingCount)

	var backupJSON, preserved string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'backup_content_config'`).Scan(&backupJSON))
	require.JSONEq(t, `{"include_usage_records":true,"keep_me":"yes"}`, backupJSON)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'migration_265_preserved'`).Scan(&preserved))
	require.Equal(t, "unchanged", preserved)

	// 非法历史值保持原样，同时迁移仍需成功并继续清理目标设置。
	_, err = tx.ExecContext(ctx, `
UPDATE settings SET value = 'not-json' WHERE key = 'backup_content_config';
INSERT INTO settings (key, value) VALUES ('data_sharing_enabled', 'true');
`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'backup_content_config'`).Scan(&backupJSON))
	require.Equal(t, "not-json", backupJSON)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'data_sharing_enabled'`).Scan(&removedSettingCount))
	require.Zero(t, removedSettingCount)

	// 包含 NUL Unicode 转义的 JSON 文本语法有效，但 PostgreSQL JSONB 无法表示它。
	invalidJSONB := `{"note":"\u0000"}`
	_, err = tx.ExecContext(ctx, `UPDATE settings SET value = $1 WHERE key = 'backup_content_config'`, invalidJSONB)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('data_sharing_enabled', 'true')`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'backup_content_config'`).Scan(&backupJSON))
	require.Equal(t, invalidJSONB, backupJSON)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'data_sharing_enabled'`).Scan(&removedSettingCount))
	require.Zero(t, removedSettingCount)

	// 非 JSON 转换错误必须继续阻断迁移，不能被异常处理吞掉。
	_, err = tx.ExecContext(ctx, `UPDATE settings SET value = '{"include_data_share_sessions":true}' WHERE key = 'backup_content_config'`)
	require.NoError(t, err)
	functionName := fmt.Sprintf("fail_data_sharing_backup_update_%d", time.Now().UnixNano())
	triggerName := fmt.Sprintf("fail_data_sharing_backup_update_trigger_%d", time.Now().UnixNano())
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'forced backup content update failure';
    RETURN NEW;
END;
$$`, functionName))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, fmt.Sprintf(
		"CREATE TRIGGER %s BEFORE UPDATE ON settings FOR EACH ROW EXECUTE FUNCTION %s()",
		triggerName,
		functionName,
	))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.ErrorContains(t, err, "forced backup content update failure")
}
