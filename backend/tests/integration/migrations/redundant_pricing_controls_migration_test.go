//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

// 迁移只清除退役配置，分组协议、其他功能和价格规则保持原值；重复执行安全。
func TestMigration275RemovesRedundantPricingControls(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	_, err := tx.ExecContext(ctx, `
ALTER TABLE pricing_configs ADD COLUMN apply_pricing_to_account_stats BOOLEAN NOT NULL DEFAULT FALSE;
INSERT INTO pricing_configs(id,name,apply_pricing_to_account_stats) VALUES (97501,'migration275',true);
INSERT INTO groups(id,name,platform,responses_image_policy,routing_policy)
VALUES (97501,'migration275','openai','enabled','{"enabled":true,"model_mapping":{"openai":{"alias":"gpt-test"}},"features_config":{"codex_image_generation_bridge":{"openai":false},"web_search_emulation":{"anthropic":true}}}');
INSERT INTO pricing_config_groups(pricing_config_id,group_id) VALUES (97501,97501);
INSERT INTO pricing_config_model_pricing(id,pricing_config_id,platform,models,input_price) VALUES (97501,97501,'openai','["gpt-test"]',0);
`)
	require.NoError(t, err)
	data, err := migrations.FS.ReadFile("275_remove_redundant_pricing_controls.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = tx.ExecContext(ctx, string(data))
		require.NoError(t, err)
	}
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='pricing_configs' AND column_name='apply_pricing_to_account_stats'`).Scan(&count))
	require.Zero(t, count)
	var protocol, mapping string
	var legacy, other bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT responses_image_policy,routing_policy #>> '{model_mapping,openai,alias}',
 (routing_policy->'features_config') ? 'codex_image_generation_bridge',
 (routing_policy #>> '{features_config,web_search_emulation,anthropic}')::boolean
 FROM groups WHERE id=97501`).Scan(&protocol, &mapping, &legacy, &other))
	require.Equal(t, "enabled", protocol)
	require.Equal(t, "gpt-test", mapping)
	require.False(t, legacy)
	require.True(t, other)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM pricing_config_model_pricing p JOIN pricing_config_groups g ON g.pricing_config_id=p.pricing_config_id WHERE p.id=97501 AND g.group_id=97501 AND p.input_price=0`).Scan(&count))
	require.Equal(t, 1, count)
}
