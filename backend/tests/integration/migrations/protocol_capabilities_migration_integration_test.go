//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

// 独立 schema 验证真实迁移与重放，包含自定义端点、空集合、禁用图片和异步绑定。
func TestUnifiedProtocolMigration(t *testing.T) {
	tx, err := integrationDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.Exec(`CREATE SCHEMA protocol_migration_test; SET LOCAL search_path=protocol_migration_test;
 CREATE TABLE accounts(id bigint,platform text,type text,credentials jsonb,extra jsonb,parent_account_id bigint);
 CREATE TABLE groups(id bigint,platform text,allowed_client_protocols jsonb,allow_image_generation boolean,allow_batch_image_generation boolean,allow_live boolean);
 CREATE TABLE video_jobs(id bigint,account_id bigint);
 INSERT INTO accounts VALUES (1,'deepseek','apikey','{"api_protocol":"adaptive","api_base_urls":{"responses":"https://relay.example"},"api_key":"test"}','{}',NULL),(2,'kimi','apikey','{"api_protocol":"anthropic","base_url":"https://relay.example/anthropic"}','{}',NULL),(3,'openai','apikey','{"openai_workload_capabilities":["embeddings"]}','{}',NULL),(4,'openai','oauth','{"auth_mode":"personalAccessToken"}','{}',NULL);
 INSERT INTO groups VALUES(1,'openai','["openai_responses"]',false,false,false),(2,'grok','[]',true,false,false),(3,'gemini','["gemini_generate_content"]',true,true,false);
 INSERT INTO video_jobs VALUES(8,2);`)
	require.NoError(t, err)
	sql, err := migrations.FS.ReadFile("273_unify_protocol_capabilities.sql")
	require.NoError(t, err)
	_, err = tx.Exec(string(sql))
	require.NoError(t, err)
	var value string
	require.NoError(t, tx.QueryRow(`SELECT credentials->'upstream_protocols' FROM accounts WHERE id=1`).Scan(&value))
	require.JSONEq(t, `["anthropic_messages","openai_responses","openai_chat_completions"]`, value)
	require.NoError(t, tx.QueryRow(`SELECT credentials->'api_base_urls'->>'anthropic' FROM accounts WHERE id=2`).Scan(&value))
	require.Equal(t, "https://relay.example/anthropic", value)
	require.NoError(t, tx.QueryRow(`SELECT credentials->'upstream_protocols' FROM accounts WHERE id=3`).Scan(&value))
	require.JSONEq(t, `["openai_embeddings","openai_images_generations","openai_images_edits"]`, value)
	require.NoError(t, tx.QueryRow(`SELECT responses_image_policy FROM groups WHERE id=1`).Scan(&value))
	require.Equal(t, "block", value)
	_, err = tx.Exec(`UPDATE accounts SET credentials=jsonb_set(credentials,'{upstream_protocols}','[]') WHERE id=1; UPDATE groups SET allowed_protocols='[]',protocol_fallbacks='{}',responses_image_policy='disabled' WHERE id=1`)
	require.NoError(t, err)
	_, err = tx.Exec(string(sql))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(`SELECT credentials->'upstream_protocols' FROM accounts WHERE id=1`).Scan(&value))
	require.JSONEq(t, `[]`, value)
	require.NoError(t, tx.QueryRow(`SELECT allowed_protocols FROM groups WHERE id=1`).Scan(&value))
	require.JSONEq(t, `[]`, value)
	var accountID int
	require.NoError(t, tx.QueryRow(`SELECT account_id FROM video_jobs WHERE id=8`).Scan(&accountID))
	require.Equal(t, 2, accountID)
}
