//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"

	dbmigrations "github.com/BrandonVee/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration236RemovesUpstreamBillingProbeDataIdempotently(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("236_remove_upstream_billing_probe.sql")
	require.NoError(t, err)

	var accountID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, extra)
VALUES (
    'migration-236-account',
    'openai',
    'apikey',
    '{"upstream_billing_probe":{"status":"ok"},"upstream_billing_probe_enabled":true,"custom":{"nested":1}}'::jsonb
)
RETURNING id
`).Scan(&accountID))

	targetSettingKeys := []string{
		"upstream_billing_probe_settings",
		"openai_low_upstream_rate_priority_enabled",
		"openai_oauth_scheduling_rate_multiplier",
		"openai_advanced_scheduler_weight_upstream_cost",
	}
	for _, key := range targetSettingKeys {
		_, err = tx.ExecContext(ctx, `
INSERT INTO settings (key, value)
VALUES ($1, 'legacy')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
`, key)
		require.NoError(t, err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO settings (key, value)
VALUES ('migration_236_unrelated', 'preserved')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
`)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	var rawExtra []byte
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT extra FROM accounts WHERE id = $1", accountID).Scan(&rawExtra))
	var extra map[string]any
	require.NoError(t, json.Unmarshal(rawExtra, &extra))
	require.NotContains(t, extra, "upstream_billing_probe")
	require.NotContains(t, extra, "upstream_billing_probe_enabled")
	require.Equal(t, map[string]any{"nested": float64(1)}, extra["custom"])

	var removedCount int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM settings
WHERE key IN (
    'upstream_billing_probe_settings',
    'openai_low_upstream_rate_priority_enabled',
    'openai_oauth_scheduling_rate_multiplier',
    'openai_advanced_scheduler_weight_upstream_cost'
)
`).Scan(&removedCount))
	require.Zero(t, removedCount)
	var unrelatedValue string
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'migration_236_unrelated'").Scan(&unrelatedValue))
	require.Equal(t, "preserved", unrelatedValue)
}
