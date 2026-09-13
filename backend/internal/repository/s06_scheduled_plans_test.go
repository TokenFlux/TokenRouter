//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/stretchr/testify/require"
)

// 使用隔离 PostgreSQL 验证计划边界、结果保留数及级联删除，沿用原表和 SQL。
func TestS06ScheduledPlanStorageContract(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	row, err := client.Account.Create().SetName("s06-scheduled-fixture").SetPlatform(account.PlatformOpenAI).SetType(account.AccountTypeAPIKey).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(row.ID).Exec(context.Background())) })
	plans := accountpostgres.NewScheduledTestPlanRepository(integrationDB)
	results := accountpostgres.NewScheduledTestResultRepository(integrationDB)
	now := time.Date(2026, 9, 13, 10, 2, 0, 0, time.UTC)
	svc := account.NewScheduledTestService(plans, results, account.ScheduledTestOptions{Now: func() time.Time { return now }, NextRun: accountprovider.NextScheduledTestRun})
	plan, err := svc.CreatePlan(ctx, &account.ScheduledTestPlan{AccountID: row.ID, ModelID: "fixture-model", CronExpression: "*/5 * * * *", Enabled: true})
	require.NoError(t, err)
	require.Equal(t, 50, plan.MaxResults)
	require.NotNil(t, plan.NextRunAt)
	// PostgreSQL timestamp 以微秒表示边界，不能用会被舍入的纳秒差值构造夹具。
	before, err := plans.ListDue(ctx, plan.NextRunAt.Add(-time.Microsecond))
	require.NoError(t, err)
	for _, p := range before {
		require.NotEqual(t, plan.ID, p.ID)
	}
	due, err := plans.ListDue(ctx, *plan.NextRunAt)
	require.NoError(t, err)
	found := false
	for _, p := range due {
		found = found || p.ID == plan.ID
	}
	require.True(t, found)
	for i := 0; i < 3; i++ {
		err = svc.SaveResult(ctx, plan.ID, 2, &account.ScheduledTestResult{Status: "success", ResponseText: "fixture", StartedAt: now, FinishedAt: now.Add(time.Second), LatencyMs: 1000})
		require.NoError(t, err)
	}
	retained, err := svc.ListResults(ctx, plan.ID, 0)
	require.NoError(t, err)
	require.Len(t, retained, 2)
	require.Equal(t, "fixture", retained[0].ResponseText)
	plan.CronExpression = "0 * * * *"
	plan.Enabled = false
	plan, err = svc.UpdatePlan(ctx, plan)
	require.NoError(t, err)
	require.False(t, plan.Enabled)
	require.Equal(t, 0, plan.NextRunAt.Minute())
	require.NoError(t, svc.DeletePlan(ctx, plan.ID))
	retained, err = svc.ListResults(ctx, plan.ID, 50)
	require.NoError(t, err)
	require.Empty(t, retained)
}
