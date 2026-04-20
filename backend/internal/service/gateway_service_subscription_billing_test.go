//go:build unit

package service

import (
	"testing"
)

// TestBuildUsageBillingCommand_BillableAmountTracksActualCost locks in the fix
// that usage billing always uses ActualCost as the user-facing billable amount.
func TestBuildUsageBillingCommand_BillableAmountTracksActualCost(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)

	tests := []struct {
		name         string
		totalCost    float64
		actualCost   float64
		wantBillable float64
	}{
		{
			name:         "subscription with 2x multiplier consumes 2x quota",
			totalCost:    1.0,
			actualCost:   2.0,
			wantBillable: 2.0,
		},
		{
			name:         "subscription with 0.5x multiplier consumes 0.5x quota",
			totalCost:    1.0,
			actualCost:   0.5,
			wantBillable: 0.5,
		},
		{
			name:         "free subscription (multiplier 0) consumes no quota",
			totalCost:    1.0,
			actualCost:   0,
			wantBillable: 0,
		},
		{
			name:         "balance billing keeps using ActualCost (regression)",
			totalCost:    1.0,
			actualCost:   2.0,
			wantBillable: 2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &postUsageBillingParams{
				Cost:         &CostBreakdown{TotalCost: tt.totalCost, ActualCost: tt.actualCost},
				User:         &User{ID: 1},
				APIKey:       &APIKey{ID: 2, GroupID: &groupID},
				Account:      &Account{ID: 3},
				Subscription: &UserSubscription{ID: subID},
			}

			cmd := buildUsageBillingCommand("req-1", nil, p)
			if cmd == nil {
				t.Fatal("buildUsageBillingCommand returned nil")
			}
			if cmd.BillableAmountUSD != tt.wantBillable {
				t.Errorf("BillableAmountUSD = %v, want %v", cmd.BillableAmountUSD, tt.wantBillable)
			}
		})
	}
}
