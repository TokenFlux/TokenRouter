//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestBatchImageHoldFingerprintIgnoresMutableAllowanceState(t *testing.T) {
	command := &BatchImageBalanceHoldCommand{
		UserID:       1,
		ActorUserID:  2,
		APIKeyID:     3,
		BatchID:      "imgbatch_fingerprint",
		HoldAmount:   1,
		ActualAmount: 0.4,
		ReservedAt:   time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
	}
	command.Normalize()
	initialFingerprint := command.RequestFingerprint

	command.AllowanceReserved = true
	command.RequestFingerprint = ""
	command.Normalize()
	require.Equal(t, initialFingerprint, command.RequestFingerprint)
}

func TestPlanBatchImageBillingCapture_SubscriptionFirstAndReleasesRemainder(t *testing.T) {
	subscriptionID := int64(11)
	planID := int64(22)
	command := &BatchImageBalanceHoldCommand{
		HoldAmount:        1,
		ActualAmount:      0.45,
		BalanceHoldAmount: 0.4,
		SubscriptionHoldAllocations: []domain.BillingAllocation{
			{
				Type:           domain.BillingAllocationTypeSubscription,
				AmountUSD:      0.6,
				SubscriptionID: &subscriptionID,
				PlanID:         &planID,
			},
		},
	}

	plan, err := PlanBatchImageBillingCapture(command)

	require.NoError(t, err)
	require.InDelta(t, 0.45, plan.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0, plan.BalanceAmountUSD, 0.000001)
	require.InDelta(t, 0.4, plan.BalanceHoldAmount, 0.000001)
	require.Len(t, plan.BillingAllocations, 1)
	require.Equal(t, domain.BillingAllocationTypeSubscription, plan.BillingAllocations[0].Type)
	require.Len(t, plan.SubscriptionReleases, 1)
	require.InDelta(t, 0.15, plan.SubscriptionReleases[0].AmountUSD, 0.000001)
}

func TestPlanBatchImageBillingCapture_FallsBackToReservedBalance(t *testing.T) {
	subscriptionID := int64(11)
	command := &BatchImageBalanceHoldCommand{
		HoldAmount:        1,
		ActualAmount:      0.8,
		BalanceHoldAmount: 0.4,
		SubscriptionHoldAllocations: []domain.BillingAllocation{
			{
				Type:           domain.BillingAllocationTypeSubscription,
				AmountUSD:      0.6,
				SubscriptionID: &subscriptionID,
			},
		},
	}

	plan, err := PlanBatchImageBillingCapture(command)

	require.NoError(t, err)
	require.InDelta(t, 0.6, plan.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.2, plan.BalanceAmountUSD, 0.000001)
	require.Len(t, plan.BillingAllocations, 2)
	require.Empty(t, plan.SubscriptionReleases)
}

func TestPlanBatchImageBillingCapture_LegacyJobUsesFullBalanceHold(t *testing.T) {
	plan, err := PlanBatchImageBillingCapture(&BatchImageBalanceHoldCommand{
		HoldAmount:   1,
		ActualAmount: 0.25,
	})

	require.NoError(t, err)
	require.InDelta(t, 1, plan.BalanceHoldAmount, 0.000001)
	require.InDelta(t, 0.25, plan.BalanceAmountUSD, 0.000001)
	require.Zero(t, plan.SubscriptionAmountUSD)
}

func TestPlanBatchImageBillingCapture_RejectsActualCostOverReservedFunds(t *testing.T) {
	_, err := PlanBatchImageBillingCapture(&BatchImageBalanceHoldCommand{
		HoldAmount:        1,
		ActualAmount:      0.8,
		BalanceHoldAmount: 0.4,
	})

	require.ErrorIs(t, err, ErrBatchImageSettlementCostExceedsHold)
}

func TestPlanBatchImageBillingCapture_UsesPerSourceRates(t *testing.T) {
	subscriptionID := int64(11)
	plan, err := PlanBatchImageBillingCapture(&BatchImageBalanceHoldCommand{
		PricingSnapshotVersion: 2,
		BaseAmountUSD:          1,
		ActualBaseAmountUSD:    1,
		BalanceRateMultiplier:  2,
		SettlementRateScale:    0.5,
		BalanceHoldAmount:      1.2,
		SubscriptionHoldAllocations: []domain.BillingAllocation{
			{
				Type:           domain.BillingAllocationTypeSubscription,
				AmountUSD:      0.2,
				BaseAmountUSD:  0.4,
				RateMultiplier: 0.5,
				SubscriptionID: &subscriptionID,
			},
		},
	})

	require.NoError(t, err)
	require.InDelta(t, 0.2, plan.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.2, plan.BalanceAmountUSD, 0.000001)
	require.InDelta(t, 0.4, plan.ActualAmountUSD, 0.000001)
	require.Len(t, plan.BillingAllocations, 2)
	require.InDelta(t, 0.8, plan.BillingAllocations[0].BaseAmountUSD, 0.000001)
	require.InDelta(t, 0.25, plan.BillingAllocations[0].RateMultiplier, 0.000001)
}
