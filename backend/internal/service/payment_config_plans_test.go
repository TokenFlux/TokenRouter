//go:build unit

package service

import (
	"testing"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestValidatePlanGroupIDs_RequiresAtLeastOneGroup(t *testing.T) {
	err := validatePlanGroupIDs(nil)

	require.Error(t, err)
	require.Equal(t, "PLAN_GROUPS_REQUIRED", infraerrors.Reason(err))
}

func TestNormalizePlanGroupIDs_DeduplicatesAndPreservesLegacyGroupID(t *testing.T) {
	got := normalizePlanGroupIDs(3, []int64{2, 3, -1, 2, 4, 0})

	require.Equal(t, []int64{3, 2, 4}, got)
}

func TestNormalizePlanGroupRateMultipliers_DefaultsAndFiltersBySelectedGroups(t *testing.T) {
	got, err := normalizePlanGroupRateMultipliers([]int64{3, 2, 4}, map[int64]float64{
		2: 0.5,
		4: 1,
		9: 8,
	})

	require.NoError(t, err)
	require.Equal(t, map[int64]float64{
		3: 1,
		2: 0.5,
		4: 1,
	}, got)
}

func TestNormalizePlanGroupRateMultipliers_RejectsNonPositiveRate(t *testing.T) {
	_, err := normalizePlanGroupRateMultipliers([]int64{3}, map[int64]float64{3: 0})

	require.Error(t, err)
	require.Equal(t, "PLAN_GROUP_RATE_INVALID", infraerrors.Reason(err))
}
