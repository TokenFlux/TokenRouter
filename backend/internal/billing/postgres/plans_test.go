package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	sqlitetest "github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"
)

// TestNormalizePlanGroupIDs_DeduplicatesAndPreservesLegacyGroupID supplies
// legacy ID three alongside duplicate and nonpositive list entries. The
// normalized sequence must keep the legacy ID first and retain accepted list
// order without duplicates.
func TestNormalizePlanGroupIDs_DeduplicatesAndPreservesLegacyGroupID(t *testing.T) {
	got := billing.NormalizePlanGroupIDs(3, []int64{2, 3, -1, 2, 4, 0})

	require.Equal(t, []int64{3, 2, 4}, got)
}

// TestNormalizePlanGroupRateMultipliers_FiltersBySelectedGroupsAndKeepsMissingUnconfigured
// supplies selected groups with two configured rates and one unrelated entry.
// It checks selected rates survive, the unrelated rate disappears and an
// unconfigured selected group stays absent.
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

// TestNormalizePlanGroupRateMultipliers_RejectsNonPositiveRate supplies zero
// as a selected group's rate. It requires an error with
// PLAN_GROUP_RATE_INVALID, protecting rejection of that nonpositive override
// rather than defaulting it silently.
func TestNormalizePlanGroupRateMultipliers_RejectsNonPositiveRate(t *testing.T) {
	_, err := billing.NormalizePlanGroupRateMultipliers([]int64{3}, map[int64]float64{3: 0})

	require.Error(t, err)
	require.Equal(t, "PLAN_GROUP_RATE_INVALID", apperror.Reason(err))
}

// TestCreatePlan_RollsBackWhenGroupMappingSyncFails creates a
// foreign-key-enforced mapping table and requests a missing group ID. Creation
// must fail and leave no plan row with the requested name, checking the owned
// transaction covers mapping synchronization.
func TestCreatePlan_RollsBackWhenGroupMappingSyncFails(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	// Require real mapping foreign keys in the SQLite fixture so the missing
	// group fails synchronization after the plan save has begun.
	_, err := client.ExecContext(ctx, `
		CREATE TABLE subscription_plan_groups (
			plan_id integer NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
			group_id integer NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			rate_multiplier real,
			PRIMARY KEY (plan_id, group_id)
		)
	`)
	require.NoError(t, err)

	svc := billing.NewPlans(NewPlanStore(client), nil, nil)
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
