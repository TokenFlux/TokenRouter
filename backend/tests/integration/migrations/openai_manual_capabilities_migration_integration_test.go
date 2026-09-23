//go:build integration

package migrations_test

import (
	"context"
	"testing"

	dbmigrations "github.com/TokenFlux/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

// 隔离旧表验证两轮迁移的回填、幂等以及无关配置保留。
func TestOpenAIManualCapabilityMigrations(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	_, err := tx.ExecContext(ctx, `CREATE SCHEMA manual_capability_test; SET LOCAL search_path TO manual_capability_test;
 CREATE TABLE groups(id BIGINT,platform TEXT,force_openai_fast BOOLEAN);
 INSERT INTO groups VALUES(1,'openai',true),(2,'anthropic',true);
 CREATE TABLE accounts(id BIGINT,platform TEXT,type TEXT,credentials JSONB,extra JSONB);
 INSERT INTO accounts VALUES
 (1,'openai','apikey','{}','{"openai_compact_supported":false,"openai_native_compaction_v2_supported":true,"openai_text_route_mode":"preserve_client_protocol","keep":42}'),
 (2,'openai','oauth','{}','{"openai_compact_mode":"force_on","openai_compact_supported":false,"openai_native_compaction_v2_mode":"force_off"}'),
 (3,'openai','apikey','{}',NULL),
 (4,'kimi','apikey','{"api_protocol":"responses"}','{"openai_responses_probe_status":"unsupported"}'),
 (5,'zhipu','apikey','{"api_protocol":"adaptive"}','{}');`)
	require.NoError(t, err)
	for repeat := 0; repeat < 2; repeat++ {
		for _, file := range []string{"269_group_openai_fast_policy.sql", "270_openai_manual_protocol_capabilities.sql"} {
			data, err := dbmigrations.FS.ReadFile(file)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, string(data))
			require.NoError(t, err)
		}
	}
	for id, want := range map[int]string{
		1: `{"openai_compact_mode":"force_off","openai_native_compaction_v2_mode":"force_on","openai_text_route_mode":"preserve_client_protocol","keep":42}`,
		2: `{"openai_compact_mode":"force_on","openai_native_compaction_v2_mode":"force_off"}`,
		3: `{"openai_compact_mode":"force_on","openai_native_compaction_v2_mode":"force_on"}`,
		4: `{"openai_text_route_mode":"force_responses"}`,
		5: `{"openai_text_route_mode":"force_chat_completions"}`,
	} {
		var got string
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT extra::text FROM accounts WHERE id=$1", id).Scan(&got))
		require.JSONEq(t, want, got)
	}
	var policy string
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT openai_fast_policy FROM groups WHERE id=1").Scan(&policy))
	require.Equal(t, "force_priority", policy)
	_, err = tx.ExecContext(ctx, "UPDATE groups SET openai_fast_policy='force_ultrafast' WHERE id=1")
	require.NoError(t, err)
	migration, _ := dbmigrations.FS.ReadFile("269_group_openai_fast_policy.sql")
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT openai_fast_policy FROM groups WHERE id=1").Scan(&policy))
	require.Equal(t, "force_ultrafast", policy)
}
