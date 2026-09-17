//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/settings"
	settingspg "github.com/TokenFlux/TokenRouter/internal/settings/postgres"
	"github.com/stretchr/testify/require"
)

// TestS15SettingsAtomicFailure 使用隔离 PostgreSQL 的真实约束故障验证整批回滚。
func TestS15SettingsAtomicFailure(t *testing.T) {
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `CREATE FUNCTION s15_reject_setting() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 's15 rejected settings write'; END $$;
 CREATE TRIGGER s15_reject_setting BEFORE INSERT OR UPDATE ON settings FOR EACH ROW WHEN (NEW.key='s15_atomic_failure') EXECUTE FUNCTION s15_reject_setting();`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(context.Background(), `DROP TRIGGER s15_reject_setting ON settings; DROP FUNCTION s15_reject_setting(); DELETE FROM settings WHERE key IN ('s15_atomic_site','s15_atomic_failure');`)
		require.NoError(t, cleanupErr)
	})
	repo := settingspg.NewSettingRepository(integrationEntClient)
	require.NoError(t, repo.Set(ctx, "s15_atomic_site", "before"))
	update, err := settings.New(repo).Updates().Begin(ctx)
	require.NoError(t, err)
	defer update.Close()
	applied := false
	err = update.Commit(settings.PreparedChange{Module: "site", Values: map[string]string{"s15_atomic_site": "after"}, Apply: func(context.Context) error { applied = true; return nil }}, settings.PreparedChange{Module: "payment", Values: map[string]string{"s15_atomic_failure": "true"}})
	require.Error(t, err)
	require.False(t, applied)
	actual, err := repo.GetValue(ctx, "s15_atomic_site")
	require.NoError(t, err)
	require.Equal(t, "before", actual)
	_, err = repo.GetValue(ctx, "s15_atomic_failure")
	require.ErrorIs(t, err, settings.ErrSettingNotFound)
}
