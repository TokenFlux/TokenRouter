package billing_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// Task capture plans distinguish legacy balance holds from recorded
// subscription and balance splits. These public-API scenarios compare source
// amounts, unused subscription releases, held-funds rejection and versioned
// rate conversion without applying a storage transaction.

// TestBatchImageHoldFingerprintIgnoresMutableAllowanceState fingerprints a
// legacy task before and after setting its allowance flag. Clearing and
// rebuilding the digest must retain the same identity for these unchanged
// funding facts.
func TestBatchImageHoldFingerprintIgnoresMutableAllowanceState(t *testing.T) {
	command := &billing.TaskFundsCommand{
		UserID:      1,
		ActorUserID: 2,
		APIKeyID:    3,

		HoldAmount:   1,
		ActualAmount: 0.4,
		ReservedAt:   time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC), Task: batchimage.FundingReference("imgbatch_fingerprint"),
	}
	command.Normalize()
	initialFingerprint := command.RequestFingerprint

	// Change the mutable allowance marker and clear its prior digest together, so
	// the next Normalize actually recomputes identity from the otherwise
	// unchanged funding facts.
	command.AllowanceReserved = true
	command.RequestFingerprint = ""
	command.Normalize()
	require.Equal(t, initialFingerprint, command.RequestFingerprint)
}

// TestPlanBatchImageBillingCapture_SubscriptionFirstAndReleasesRemainder
// supplies a 0.6 USD subscription hold and a 0.4 USD balance hold. Capturing
// 0.45 USD keeps subscription funding alone and records its unused 0.15 USD
// release.
func TestPlanBatchImageBillingCapture_SubscriptionFirstAndReleasesRemainder(t *testing.T) {
	subscriptionID := int64(11)
	planID := int64(22)
	command := &billing.TaskFundsCommand{
		HoldAmount:        1,
		ActualAmount:      0.45,
		BalanceHoldAmount: 0.4,
		SubscriptionHoldAllocations: []billing.BillingAllocation{
			{
				Type:           billing.BillingAllocationTypeSubscription,
				AmountUSD:      0.6,
				SubscriptionID: &subscriptionID,
				PlanID:         &planID,
			},
		}, Task: batchimage.FundingReference(""),
	}

	plan, err := billing.PlanTaskCapture(command)

	require.NoError(t, err)
	// Allow float residuals when comparing kept and refunded dollars.
	require.InDelta(t, 0.45, plan.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0, plan.BalanceAmountUSD, 0.000001)
	require.InDelta(t, 0.4, plan.BalanceHoldAmount, 0.000001)
	require.Len(t, plan.BillingAllocations, 1)
	require.Equal(t, billing.BillingAllocationTypeSubscription, plan.BillingAllocations[0].Type)
	require.Len(t, plan.SubscriptionReleases, 1)
	require.InDelta(t, 0.15, plan.SubscriptionReleases[0].AmountUSD, 0.000001)
}

// TestPlanBatchImageBillingCapture_FallsBackToReservedBalance captures 0.8 USD
// from subscription and balance reservations. The plan keeps the full 0.6 USD
// subscription hold, uses 0.2 USD balance and produces no subscription
// release.
func TestPlanBatchImageBillingCapture_FallsBackToReservedBalance(t *testing.T) {
	subscriptionID := int64(11)
	command := &billing.TaskFundsCommand{
		HoldAmount:        1,
		ActualAmount:      0.8,
		BalanceHoldAmount: 0.4,
		SubscriptionHoldAllocations: []billing.BillingAllocation{
			{
				Type:           billing.BillingAllocationTypeSubscription,
				AmountUSD:      0.6,
				SubscriptionID: &subscriptionID,
			},
		}, Task: batchimage.FundingReference(""),
	}

	plan, err := billing.PlanTaskCapture(command)

	require.NoError(t, err)
	// Allow float residuals in the subscription and balance amount split.
	require.InDelta(t, 0.6, plan.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.2, plan.BalanceAmountUSD, 0.000001)
	require.Len(t, plan.BillingAllocations, 2)
	require.Empty(t, plan.SubscriptionReleases)
}

// TestPlanBatchImageBillingCapture_LegacyJobUsesFullBalanceHold provides a
// legacy monetary hold with no split metadata. The plan treats the entire hold
// as balance funds and retains the supplied smaller actual charge.
func TestPlanBatchImageBillingCapture_LegacyJobUsesFullBalanceHold(t *testing.T) {
	plan, err := billing.PlanTaskCapture(&billing.TaskFundsCommand{
		HoldAmount:   1,
		ActualAmount: 0.25, Task: batchimage.FundingReference(""),
	})

	require.NoError(t, err)
	// Compare held and retained wallet dollars with float tolerance.
	require.InDelta(t, 1, plan.BalanceHoldAmount, 0.000001)
	require.InDelta(t, 0.25, plan.BalanceAmountUSD, 0.000001)
	require.Zero(t, plan.SubscriptionAmountUSD)
}

// TestPlanBatchImageBillingCapture_RejectsActualCostOverReservedFunds supplies
// actual cost above an indicated 0.4 USD balance reservation. Capture must
// return the shared held-funds error instead of treating the larger legacy
// total as available.
func TestPlanBatchImageBillingCapture_RejectsActualCostOverReservedFunds(t *testing.T) {
	_, err := billing.PlanTaskCapture(&billing.TaskFundsCommand{
		HoldAmount:        1,
		ActualAmount:      0.8,
		BalanceHoldAmount: 0.4, Task: batchimage.FundingReference(""),
	})

	require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementCostExceedsHold)
}

// TestPlanBatchImageBillingCapture_UsesPerSourceRates captures one base dollar
// using versioned subscription and balance rates with a half settlement scale.
// It asserts the 0.2 USD source charges and the subscription allocation's
// derived base amount and scaled rate.
func TestPlanBatchImageBillingCapture_UsesPerSourceRates(t *testing.T) {
	subscriptionID := int64(11)
	plan, err := billing.PlanTaskCapture(&billing.TaskFundsCommand{
		PricingSnapshotVersion: 2,
		BaseAmountUSD:          1,
		ActualBaseAmountUSD:    1,
		BalanceRateMultiplier:  2,
		SettlementRateScale:    0.5,
		BalanceHoldAmount:      1.2,
		SubscriptionHoldAllocations: []billing.BillingAllocation{
			{
				Type:           billing.BillingAllocationTypeSubscription,
				AmountUSD:      0.2,
				BaseAmountUSD:  0.4,
				RateMultiplier: 0.5,
				SubscriptionID: &subscriptionID,
			},
		}, Task: batchimage.FundingReference(""),
	})

	require.NoError(t, err)
	// Use a small float tolerance for amounts and reconstructed source rates.
	require.InDelta(t, 0.2, plan.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.2, plan.BalanceAmountUSD, 0.000001)
	require.InDelta(t, 0.4, plan.ActualAmountUSD, 0.000001)
	require.Len(t, plan.BillingAllocations, 2)
	require.InDelta(t, 0.8, plan.BillingAllocations[0].BaseAmountUSD, 0.000001)
	require.InDelta(t, 0.25, plan.BillingAllocations[0].RateMultiplier, 0.000001)
}

// TestPlanTaskCaptureOveragePolicy keeps subscription reservations bounded while
// charging completed excess at the saved wallet rate. Omitted policies reject
// the same overage, and a zero wallet rate preserves free residual usage.
func TestPlanTaskCaptureOveragePolicy(t *testing.T) {
	for _, version := range []int{0, 2} {
		for _, policy := range []billing.TaskOveragePolicy{billing.TaskOverageReject, billing.TaskOverageChargeBalance} {
			command := &billing.TaskFundsCommand{
				PricingSnapshotVersion: version,
				HoldAmount:             12,
				BalanceHoldAmount:      12,
				ActualAmount:           36,
				BaseAmountUSD:          12,
				ActualBaseAmountUSD:    36,
				BalanceRateMultiplier:  1,
				SettlementRateScale:    1,
				OveragePolicy:          policy,
			}
			plan, err := billing.PlanTaskCapture(command)
			if policy == billing.TaskOverageReject {
				require.ErrorIs(t, err, billing.ErrTaskSettlementCostExceedsHold)
				continue
			}
			require.NoError(t, err)
			require.Equal(t, 12.0, plan.BalanceHoldAmount)
			require.Equal(t, 36.0, plan.BalanceAmountUSD)
			require.Equal(t, 36.0, plan.ActualAmountUSD)
			require.Equal(t, 12.0, command.HoldAmount)
		}
	}
	subscriptionID := int64(11)
	command := &billing.TaskFundsCommand{
		PricingSnapshotVersion: 2,
		BaseAmountUSD:          12,
		ActualBaseAmountUSD:    36,
		BalanceRateMultiplier:  2,
		SettlementRateScale:    1,
		BalanceHoldAmount:      8,
		OveragePolicy:          billing.TaskOverageChargeBalance,
		SubscriptionHoldAllocations: []billing.BillingAllocation{{
			Type: billing.BillingAllocationTypeSubscription, SubscriptionID: &subscriptionID,
			AmountUSD: 4, BaseAmountUSD: 8, RateMultiplier: 0.5,
		}},
	}
	plan, err := billing.PlanTaskCapture(command)
	require.NoError(t, err)
	require.Equal(t, 4.0, plan.SubscriptionAmountUSD)
	require.Equal(t, 56.0, plan.BalanceAmountUSD)
	require.Equal(t, 60.0, plan.ActualAmountUSD)
	require.Equal(t, 8.0, plan.BalanceHoldAmount)
	require.Empty(t, plan.SubscriptionReleases)
	require.Equal(t, 28.0, plan.BillingAllocations[1].BaseAmountUSD)
	command.BalanceRateMultiplier = 0
	plan, err = billing.PlanTaskCapture(command)
	require.NoError(t, err)
	require.Equal(t, 4.0, plan.ActualAmountUSD)
	require.Zero(t, plan.BalanceAmountUSD)
}

// TestTaskCaptureOverageFingerprint separates excess-charge authorization from
// bounded captures. Clearing that policy restores the historical fingerprint,
// and mutable allowance flags do not affect an authorized capture replay.
func TestTaskCaptureOverageFingerprint(t *testing.T) {
	command := &billing.TaskFundsCommand{
		UserID: 7, APIKeyID: 9, Task: batchimage.FundingReference("fingerprint"),
		PricingSnapshotVersion: 2, BaseAmountUSD: 12, ActualBaseAmountUSD: 36,
	}
	command.Normalize()
	original := command.RequestFingerprint
	command.OveragePolicy = billing.TaskOverageChargeBalance
	command.RequestFingerprint = ""
	command.Normalize()
	overage := command.RequestFingerprint
	require.NotEqual(t, original, overage)
	command.AllowanceReserved = true
	command.RequestFingerprint = ""
	command.Normalize()
	require.Equal(t, overage, command.RequestFingerprint)
	command.OveragePolicy = billing.TaskOverageReject
	command.RequestFingerprint = ""
	command.Normalize()
	require.Equal(t, original, command.RequestFingerprint)
}
