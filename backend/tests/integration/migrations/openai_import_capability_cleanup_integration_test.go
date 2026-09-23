//go:build integration

package migrations_test

import (
	"context"
	"testing"

	dbmigrations "github.com/TokenFlux/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

// 覆盖异常历史数据、模板缺省语义和重复执行，不依赖生产设置内容。
func TestOpenAIImportCapabilityCleanupMigration(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	_, err := tx.ExecContext(ctx, `CREATE SCHEMA import_capability_test; SET LOCAL search_path TO import_capability_test;
CREATE TABLE settings(key TEXT PRIMARY KEY, value TEXT, updated_at TIMESTAMPTZ DEFAULT NOW());`)
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile("271_clean_openai_import_capability_state.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err, "缺失模板不得创建新设置")
	for _, tc := range []struct{ name, input, want string }{
		{"legacy", `{"account":{"priority":0},"extra":{"openai_compact_mode":" AUTO ","openai_native_compaction_v2_mode":"force_off","openai_responses_probe_status":[],"openai_compact_supported":false,"keep":{"a":1}}}`, `{"account":{"priority":0},"extra":{"openai_compact_mode":"force_on","openai_native_compaction_v2_mode":"force_off","keep":{"a":1}}}`},
		{"explicit", `{"extra":{"openai_compact_mode":"force_off","openai_native_compaction_v2_mode":"force_on"}}`, ""},
		{"missing switches", `{"extra":{"keep":false}}`, ""},
		{"missing extra", `{"credentials":{"model_whitelist":[]}}`, ""},
		{"empty extra", `{"extra":{}}`, ""},
		{"invalid JSON", `{broken`, ""},
		{"JSONB unsupported Unicode", `{"extra":{"note":"\u0000","openai_compact_mode":"auto"}}`, ""},
		{"JSONB numeric overflow", `{"extra":{"number":1e1000000,"openai_compact_mode":"auto"}}`, ""},
		{"non-object", `[]`, ""},
		{"null", `null`, ""},
		{"non-object extra", `{"extra":[]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('openai_oauth_import_defaults',$1)
ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, tc.input)
			require.NoError(t, err)
			for repeat := 0; repeat < 2; repeat++ {
				_, err = tx.ExecContext(ctx, string(migration))
				require.NoError(t, err)
			}
			var got string
			require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='openai_oauth_import_defaults'`).Scan(&got))
			if tc.want == "" {
				require.Equal(t, tc.input, got)
			} else {
				require.JSONEq(t, tc.want, got)
			}
		})
	}
}
