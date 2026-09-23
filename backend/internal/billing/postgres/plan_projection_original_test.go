package postgres_test

import (
	"testing"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionPlanEntityToServiceMapsGroupIDs(t *testing.T) {
	plan := &dbent.SubscriptionPlan{
		ID:       1,
		Name:     "standard",
		GroupIds: []int64{2, 3},
	}

	got := billingpostgres.PlanFromEntity(plan)

	require.NotNil(t, got)
	require.Equal(t, []int64{int64(2), int64(3)}, got.GroupIDs)
	plan.GroupIds[0] = 99
	require.Equal(t, []int64{int64(2), int64(3)}, got.GroupIDs)
}
