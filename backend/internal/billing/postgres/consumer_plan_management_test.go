//go:build unit

package postgres_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	sqlitetest "github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"

	"github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/stretchr/testify/require"
)

func TestNormalizePlanGroupIDs_DeduplicatesAndPreservesLegacyGroupID(t *testing.T) {
	got := billing.NormalizePlanGroupIDs(3, []int64{2, 3, -1, 2, 4, 0})

	require.Equal(t, []int64{3, 2, 4}, got)
}

func TestNormalizePlanGroupRateMultipliers_FiltersBySelectedGroupsAndKeepsMissingUnconfigured(t *testing.T) {
	got, err := billing.NormalizePlanGroupRateMultipliers([]int64{3, 2, 4}, map[int64]float64{
		2: 0.5,
		4: 1,
		9: 8,
	})

	require.NoError(t, err)
	require.Equal(t, map[int64]float64{
		2: 0.5,
		4: 1,
	}, got)
}

func TestNormalizePlanGroupRateMultipliers_RejectsNonPositiveRate(t *testing.T) {
	_, err := billing.NormalizePlanGroupRateMultipliers([]int64{3}, map[int64]float64{3: 0})

	require.Error(t, err)
	require.Equal(t, "PLAN_GROUP_RATE_INVALID", apperror.Reason(err))
}

func TestCreatePlan_RollsBackWhenGroupMappingSyncFails(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	_, err := client.ExecContext(ctx, `
		CREATE TABLE subscription_plan_groups (
			plan_id integer NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
			group_id integer NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			rate_multiplier real,
			PRIMARY KEY (plan_id, group_id)
		)
	`)
	require.NoError(t, err)

	svc := billing.NewPlans(billingpostgres.NewPlanStore(client), nil)
	_, err = svc.CreatePlan(ctx, billing.CreatePlanRequest{
		Name:         "rollback-plan",
		Description:  "mapping sync should fail",
		Price:        1,
		ValidityDays: 30,
		ValidityUnit: "day",
		GroupIDs:     []int64{404},
		ForSale:      true,
	})

	require.Error(t, err)
	count, countErr := client.SubscriptionPlan.Query().
		Where(subscriptionplan.NameEQ("rollback-plan")).
		Count(ctx)
	require.NoError(t, countErr)
	require.Equal(t, 0, count)
}
