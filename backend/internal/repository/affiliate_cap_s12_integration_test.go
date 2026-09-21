//go:build integration

package repository

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	promotionpostgres "github.com/TokenFlux/TokenRouter/internal/promotion/postgres"

	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	settingspostgres "github.com/TokenFlux/TokenRouter/internal/settings/postgres"
	"github.com/stretchr/testify/require"
)

// 两个调用在尝试加锁前汇合，锁内读取不得依赖修复前的错误交错。
type s12PlanningAffiliateBarrier struct {
	promotion.AffiliateRepository
	ready chan struct{}
	calls atomic.Int32
}

func (r *s12PlanningAffiliateBarrier) WithLockedInviter(ctx context.Context, id int64, fn func(context.Context) error) error {
	if r.calls.Add(1) == 2 {
		close(r.ready)
	}
	select {
	case <-r.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	return r.AffiliateRepository.WithLockedInviter(ctx, id, fn)
}
func TestAffiliateCapConcurrentAccrualUsesLockedLatestTotal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := testEntClient(t)
	repo := promotionpostgres.NewAffiliateRepository(c, func(tx *dbent.Tx) promotionpostgres.TransferBalance { return billingpostgres.BalanceInTx(tx) })
	inviter, e := c.User.Create().SetEmail("s12-inviter@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, e)
	invitee, e := c.User.Create().SetEmail("s12-invitee@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, e)
	_, e = repo.EnsureUserAffiliate(ctx, inviter.ID)
	require.NoError(t, e)
	_, e = repo.EnsureUserAffiliate(ctx, invitee.ID)
	require.NoError(t, e)
	_, e = repo.BindInviter(ctx, invitee.ID, inviter.ID)
	require.NoError(t, e)
	sr := settings.New(settingspostgres.NewSettingRepository(c))
	keys := []string{promotion.SettingKeyAffiliateEnabled, promotion.SettingKeyAffiliateRebateRate, promotion.SettingKeyAffiliateRebatePerInviteeCap, promotion.SettingKeyAffiliateRebateFreezeHours, promotion.SettingKeyAffiliateRebateDurationDays}
	original, err := sr.GetMultiple(ctx, keys)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, key := range keys {
			require.NoError(t, sr.Delete(context.Background(), key))
		}
		require.NoError(t, sr.SetMultiple(context.Background(), original))
	})
	require.NoError(t, sr.SetMultiple(ctx, map[string]string{promotion.SettingKeyAffiliateEnabled: "true", promotion.SettingKeyAffiliateRebateRate: "100", promotion.SettingKeyAffiliateRebatePerInviteeCap: "10", promotion.SettingKeyAffiliateRebateFreezeHours: "0", promotion.SettingKeyAffiliateRebateDurationDays: "0"}))
	barrier := &s12PlanningAffiliateBarrier{AffiliateRepository: repo, ready: make(chan struct{})}
	svc := promotion.NewAffiliateService(barrier, promotion.NewRuntimeSettings(sr), nil, nil, promotion.Runtime{})
	errs := make(chan error, 2)
	for range 2 {
		go func() { _, e := svc.AccrueInviteRebate(ctx, invitee.ID, 8); errs <- e }()
	}
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	amount, e := repo.GetAccruedRebateFromInvitee(ctx, inviter.ID, invitee.ID)
	require.NoError(t, e)
	t.Logf("cap=10 accrued=%v", amount)
	require.Equal(t, 10.0, amount, "并发计提不能超过单被邀请人上限")
}
