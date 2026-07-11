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
			p := &usageBillingParams{
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

func TestBuildUsageBillingCommand_IncludesRequestGroupID(t *testing.T) {
	groupID := int64(42)
	p := &usageBillingParams{
		Cost: &CostBreakdown{ActualCost: 1.25},
		User: &User{ID: 10},
		APIKey: &APIKey{
			ID:      20,
			GroupID: &groupID,
		},
		Account: &Account{ID: 30, Type: AccountTypeAPIKey},
	}

	cmd := buildUsageBillingCommand("req-group", nil, p)

	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.GroupID == nil {
		t.Fatal("GroupID is nil")
	}
	if *cmd.GroupID != groupID {
		t.Fatalf("GroupID = %d, want %d", *cmd.GroupID, groupID)
	}
}

func TestBuildUsageBillingCommand_NonTokenModesPreserveEffectiveRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       BillingMode
		totalCost  float64
		actualCost float64
		wantRate   float64
	}{
		{name: "image independent rate", mode: BillingModeImage, totalCost: 0.2, actualCost: 0.2, wantRate: 1},
		{name: "video independent rate", mode: BillingModeVideo, totalCost: 0.08, actualCost: 0.02, wantRate: 0.25},
		{name: "per request rate", mode: BillingModePerRequest, totalCost: 0.4, actualCost: 0.1, wantRate: 0.25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &usageBillingParams{
				Cost: &CostBreakdown{
					TotalCost:   tt.totalCost,
					ActualCost:  tt.actualCost,
					BillingMode: string(tt.mode),
				},
				User:                            &User{ID: 1},
				APIKey:                          &APIKey{ID: 2},
				Account:                         &Account{ID: 3},
				SubscriptionRateMultiplier:      0.15,
				SubscriptionRateMultiplierScale: 2,
				BalanceRateMultiplier:           0.15,
			}

			cmd := buildUsageBillingCommand("req-non-token", nil, p)

			if cmd == nil {
				t.Fatal("buildUsageBillingCommand returned nil")
			}
			if cmd.SubscriptionRateMultiplier != tt.wantRate {
				t.Errorf("SubscriptionRateMultiplier = %v, want %v", cmd.SubscriptionRateMultiplier, tt.wantRate)
			}
			if cmd.SubscriptionRateMultiplierScale != 1 {
				t.Errorf("SubscriptionRateMultiplierScale = %v, want 1", cmd.SubscriptionRateMultiplierScale)
			}
			if cmd.BalanceRateMultiplier != tt.wantRate {
				t.Errorf("BalanceRateMultiplier = %v, want %v", cmd.BalanceRateMultiplier, tt.wantRate)
			}
		})
	}
}

func TestBuildUsageBillingCommand_TokenModeKeepsAllocationRates(t *testing.T) {
	t.Parallel()

	p := &usageBillingParams{
		Cost: &CostBreakdown{
			TotalCost:   1,
			ActualCost:  0.5,
			BillingMode: string(BillingModeToken),
		},
		User:                            &User{ID: 1},
		APIKey:                          &APIKey{ID: 2},
		Account:                         &Account{ID: 3},
		SubscriptionRateMultiplier:      0.8,
		SubscriptionRateMultiplierScale: 1.5,
		BalanceRateMultiplier:           0.3,
	}

	cmd := buildUsageBillingCommand("req-token", nil, p)

	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.SubscriptionRateMultiplier != 0.8 {
		t.Errorf("SubscriptionRateMultiplier = %v, want 0.8", cmd.SubscriptionRateMultiplier)
	}
	if cmd.SubscriptionRateMultiplierScale != 1.5 {
		t.Errorf("SubscriptionRateMultiplierScale = %v, want 1.5", cmd.SubscriptionRateMultiplierScale)
	}
	if cmd.BalanceRateMultiplier != 0.3 {
		t.Errorf("BalanceRateMultiplier = %v, want 0.3", cmd.BalanceRateMultiplier)
	}
}
