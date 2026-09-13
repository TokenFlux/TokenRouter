//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// 使用真实事务验证消费写入和配置修改同连接；参与方法不发布 outbox，也不自行提交。
func TestS06AccountUsageParticipantKeepsOuterTransaction(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(time.Hour)
	row, err := client.Account.Create().SetName(fmt.Sprintf("s06-usage-%d", time.Now().UnixNano())).
		SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeAPIKey).
		SetStatus(service.StatusError).SetErrorMessage("preserve-health").SetSchedulable(false).
		SetRateLimitedAt(now).SetRateLimitResetAt(until).SetOverloadUntil(until).
		SetExtra(map[string]any{"quota_used": 5, "quota_limit": 6, "quota_daily_used": 2, "quota_weekly_used": 3, "quota_daily_start": now.Format(time.RFC3339), "quota_weekly_start": now.Format(time.RFC3339), "custom": "preserve"}).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
	var before int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1", row.ID).Scan(&before))
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	participant := billingpostgres.AccountUsageInTx(tx.Client())
	crossed, err := participant.Increment(ctx, row.ID, 2)
	require.NoError(t, err)
	require.True(t, crossed)
	inside, err := tx.Client().Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, float64(7), inside.Extra["quota_used"])
	require.NoError(t, participant.ResetAndClearRateLimitCooldown(ctx, row.ID))
	_, err = tx.Client().Account.UpdateOneID(row.ID).SetName("pending-name").Save(ctx)
	require.NoError(t, err)
	inside, err = tx.Client().Account.Get(ctx, row.ID)
	require.NoError(t, err)
	for _, key := range []string{"quota_used", "quota_daily_used", "quota_weekly_used"} {
		require.Equal(t, float64(0), inside.Extra[key], key)
	}
	require.NotContains(t, inside.Extra, "quota_daily_start")
	require.NotContains(t, inside.Extra, "quota_weekly_start")
	require.Equal(t, "preserve", inside.Extra["custom"])
	require.Nil(t, inside.RateLimitedAt)
	require.Nil(t, inside.RateLimitResetAt)
	require.Equal(t, service.StatusError, inside.Status)
	require.NotNil(t, inside.ErrorMessage)
	require.Equal(t, "preserve-health", *inside.ErrorMessage)
	require.False(t, inside.Schedulable)
	require.NotNil(t, inside.OverloadUntil)
	outside, err := client.Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, row.Name, outside.Name)
	require.Equal(t, float64(5), outside.Extra["quota_used"])
	rows, err := tx.Client().QueryContext(ctx, "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1", row.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	var pending int
	require.NoError(t, rows.Scan(&pending))
	require.NoError(t, rows.Close())
	require.Equal(t, before, pending)
	require.NoError(t, tx.Rollback())
	restored, err := client.Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, row.Name, restored.Name)
	require.Equal(t, float64(5), restored.Extra["quota_used"])
	require.NotNil(t, restored.RateLimitResetAt)
}
