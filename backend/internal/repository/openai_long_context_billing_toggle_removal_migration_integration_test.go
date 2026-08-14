//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"

	dbmigrations "github.com/BrandonVee/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration241RemovesOpenAILongContextBillingToggleIdempotently(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	legacySQL, err := dbmigrations.FS.ReadFile("203_default_openai_long_context_billing.sql")
	require.NoError(t, err)
	removalSQL, err := dbmigrations.FS.ReadFile("241_remove_openai_long_context_billing_toggle.sql")
	require.NoError(t, err)

	// 测试数据库可能已经应用最新迁移，先恢复历史触发器以验证完整升级路径。
	_, err = tx.ExecContext(ctx, string(legacySQL))
	require.NoError(t, err)

	insertAccount := func(name, platform, extra string) int64 {
		t.Helper()
		var id int64
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, extra)
VALUES ($1, $2, 'oauth', $3::jsonb)
RETURNING id
`, name, platform, extra).Scan(&id))
		return id
	}

	openAIID := insertAccount(
		"migration-241-openai",
		"openai",
		`{"openai_long_context_billing_enabled":true,"custom":{"nested":1}}`,
	)
	otherPlatformID := insertAccount(
		"migration-241-anthropic",
		"anthropic",
		`{"openai_long_context_billing_enabled":"legacy","custom":{"nested":2}}`,
	)

	_, err = tx.ExecContext(ctx, string(removalSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(removalSQL))
	require.NoError(t, err)

	assertExtra := func(accountID int64, nested float64) {
		t.Helper()
		var rawExtra []byte
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT extra FROM accounts WHERE id = $1", accountID).Scan(&rawExtra))
		var extra map[string]any
		require.NoError(t, json.Unmarshal(rawExtra, &extra))
		require.NotContains(t, extra, "openai_long_context_billing_enabled")
		require.Equal(t, map[string]any{"nested": nested}, extra["custom"])
	}
	assertExtra(openAIID, 1)
	assertExtra(otherPlatformID, 2)

	var triggerCount int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM pg_trigger
WHERE tgname IN (
    'accounts_enforce_openai_long_context_billing_extra',
    'accounts_propagate_openai_long_context_billing_extra'
)
`).Scan(&triggerCount))
	require.Zero(t, triggerCount)

	var functionCount int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM pg_proc AS procedure
JOIN pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
WHERE namespace.nspname = 'public'
  AND procedure.proname IN (
      'enforce_openai_long_context_billing_extra',
      'propagate_openai_long_context_billing_extra_to_shadows'
  )
`).Scan(&functionCount))
	require.Zero(t, functionCount)
}
