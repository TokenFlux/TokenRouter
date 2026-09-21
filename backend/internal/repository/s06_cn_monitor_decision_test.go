//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/stretchr/testify/require"
)

// 真实 PostgreSQL 校验版本条件、同身份恢复、旧观测拒绝以及原尽力通知边界。
func TestS06CNMonitorDecisionCAS(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	row, err := client.Account.Create().SetName("cn-monitor-cas").SetPlatform(acctcore.PlatformKimi).SetType(acctcore.AccountTypeAPIKey).SetCredentials(map[string]any{"api_key": "old-fixture"}).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
	store := accountpostgres.NewAccountStore(client, integrationDB, accountpostgres.AccountStoreOptions{Events: AccountEventBinding{}})
	count := func() int {
		t.Helper()
		var n int
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&n))
		return n
	}
	before := count()
	until := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	reason := "cn_usage_monitor:fixture: 余额低于监控阈值"
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials='{"api_key":"new-fixture"}'::jsonb,updated_at=updated_at+interval '1 second' WHERE id=$1`, row.ID)
	require.NoError(t, err)
	wrote, err := store.SetCNUsageDecisionCAS(ctx, row.ID, row.UpdatedAt, until, reason, false)
	require.NoError(t, err)
	require.False(t, wrote)
	require.Equal(t, before, count())
	current, err := client.Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Nil(t, current.TempUnschedulableUntil)
	wrote, err = store.SetCNUsageDecisionCAS(ctx, row.ID, current.UpdatedAt, until, reason, false)
	require.NoError(t, err)
	require.True(t, wrote)
	require.Equal(t, before+1, count())
	current, err = client.Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.True(t, until.Equal(*current.TempUnschedulableUntil))
	wrote, err = store.SetCNUsageDecisionCAS(ctx, row.ID, current.UpdatedAt, until.Add(-time.Minute), reason, false)
	require.NoError(t, err)
	require.False(t, wrote)
	wrote, err = store.SetCNUsageDecisionCAS(ctx, row.ID, current.UpdatedAt, time.Time{}, "other-monitor", true)
	require.NoError(t, err)
	require.False(t, wrote)
	// 相同时间版本仍必须检查原因，不能清除其它监控的停调。
	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET temp_unschedulable_reason='manual-cooldown' WHERE id=$1", row.ID)
	require.NoError(t, err)
	wrote, err = store.SetCNUsageDecisionCAS(ctx, row.ID, current.UpdatedAt, time.Time{}, reason, true)
	require.NoError(t, err)
	require.False(t, wrote)
	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET temp_unschedulable_reason=$2 WHERE id=$1", row.ID, reason)
	require.NoError(t, err)
	// outbox 失败继续保留原先已提交的健康写入，不升级成回滚条件。
	store.SetEvents(s06FailConfigurationOutbox{})
	wrote, err = store.SetCNUsageDecisionCAS(ctx, row.ID, current.UpdatedAt, time.Time{}, reason, true)
	require.NoError(t, err)
	require.True(t, wrote)
	current, err = client.Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Nil(t, current.TempUnschedulableUntil)
	require.Equal(t, "new-fixture", current.Credentials["api_key"])
	require.Equal(t, before+1, count())
}
