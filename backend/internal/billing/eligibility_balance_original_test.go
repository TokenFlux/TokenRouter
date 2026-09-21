//go:build unit

package billing

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type balanceEligibilityCacheStub struct {
	billingCacheWorkerStub

	balance                  float64
	cacheMissAfterInvalidate bool
	invalidated              atomic.Bool
	deductCalls              atomic.Int64
	invalidateCalls          atomic.Int64
}

func (s *balanceEligibilityCacheStub) GetUserBalance(context.Context, int64) (float64, error) {
	if s.cacheMissAfterInvalidate && s.invalidated.Load() {
		return 0, errors.New("cache miss")
	}
	return s.balance, nil
}

func (s *balanceEligibilityCacheStub) DeductUserBalance(context.Context, int64, float64) error {
	s.deductCalls.Add(1)
	return nil
}

func (s *balanceEligibilityCacheStub) InvalidateUserBalance(context.Context, int64) error {
	s.invalidateCalls.Add(1)
	s.invalidated.Store(true)
	return nil
}

func TestCheckBillingEligibilityRejectsBalanceBelowMinimumReserve(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.005}
	cfg := &EligibilityOptions{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := newOriginalEligibility(cache, nil, nil, nil, cfg)
	svc.Start()
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &UserSummary{ID: 1}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)
}

func TestCheckBillingEligibilityAllowsBalanceAtMinimumReserve(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.01}
	cfg := &EligibilityOptions{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := newOriginalEligibility(cache, nil, nil, nil, cfg)
	svc.Start()
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(context.Background(), &UserSummary{ID: 1}, nil, nil, nil, "")
	require.NoError(t, err)
}

func TestSyncBalanceCacheAfterDeductionInvalidatesExhaustedBalance(t *testing.T) {
	cache := &balanceEligibilityCacheStub{
		balance:                  0.50,
		cacheMissAfterInvalidate: true,
	}
	userRepo := &balanceLoadUserRepoStub{balance: -0.25}
	cfg := &EligibilityOptions{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := newOriginalEligibility(cache, userRepo, nil, nil, cfg)
	svc.Start()
	t.Cleanup(svc.Stop)

	newBalance := -0.25
	(SettlementEffects{Cache: svc}).SyncBalance(context.Background(), 1, &UsageBillingApplyResult{
		NewBalance:       &newBalance,
		BalanceAmountUSD: 0.75,
	})

	require.Equal(t, int64(1), cache.invalidateCalls.Load())
	require.Equal(t, int64(0), cache.deductCalls.Load())

	err := svc.CheckBillingEligibility(context.Background(), &UserSummary{ID: 1}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.Equal(t, int64(1), userRepo.calls.Load())
}

func TestSyncBalanceCacheAfterDeductionInvalidatesWhenBalanceFallsBelowReserve(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.50}
	cfg := &EligibilityOptions{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := newOriginalEligibility(cache, nil, nil, nil, cfg)
	svc.Start()
	t.Cleanup(svc.Stop)

	newBalance := 0.005
	(SettlementEffects{Cache: svc}).SyncBalance(context.Background(), 1, &UsageBillingApplyResult{
		NewBalance:       &newBalance,
		BalanceAmountUSD: 0.495,
	})

	require.Equal(t, int64(1), cache.invalidateCalls.Load())
	require.Equal(t, int64(0), cache.deductCalls.Load())
}

func TestSyncBalanceCacheAfterDeductionQueuesDeductWhenBalanceStillEligible(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 1}
	cfg := &EligibilityOptions{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	svc := newOriginalEligibility(cache, nil, nil, nil, cfg)
	svc.Start()
	t.Cleanup(svc.Stop)

	newBalance := 0.75
	(SettlementEffects{Cache: svc}).SyncBalance(context.Background(), 1, &UsageBillingApplyResult{
		NewBalance:       &newBalance,
		BalanceAmountUSD: 0.25,
	})

	require.Equal(t, int64(0), cache.invalidateCalls.Load())
	require.Eventually(t, func() bool {
		return cache.deductCalls.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)
}
