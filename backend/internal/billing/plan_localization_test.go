package billing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// TestPlanTextRequiresLocalization supplies each legacy plan-text
// pointer without a localization update. It checks LOCALIZATION_REQUIRED
// before repository access, keeping source and translation updates
// on the versioned plan-content path.
func TestPlanTextRequiresLocalization(t *testing.T) {
	text := "Updated"
	for _, request := range []UpdatePlanRequest{
		{Name: &text}, {Description: &text}, {Features: &text}, {ProductName: &text},
	} {
		_, err := NewPlans(nil, nil, nil).UpdatePlan(context.Background(), 1, request)
		require.Equal(t, "LOCALIZATION_REQUIRED", apperror.Reason(err))
	}
}
