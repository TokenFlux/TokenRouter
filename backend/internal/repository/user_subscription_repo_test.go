package repository

import (
	"testing"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionPlanEntityToServiceMapsGroupIDs(t *testing.T) {
	plan := &dbent.SubscriptionPlan{
		ID:       1,
		Name:     "standard",
		GroupIds: []int64{2, 3},
	}

	got := subscriptionPlanEntityToService(plan)

	require.NotNil(t, got)
	require.Equal(t, []int64{int64(2), int64(3)}, got.GroupIDs)
	plan.GroupIds[0] = 99
	require.Equal(t, []int64{int64(2), int64(3)}, got.GroupIDs)
}
