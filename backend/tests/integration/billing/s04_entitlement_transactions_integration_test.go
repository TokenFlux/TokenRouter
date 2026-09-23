//go:build integration

package billing_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/redeemcodeusage"
	"github.com/TokenFlux/TokenRouter/ent/usersubscription"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/stretchr/testify/require"
)

// 外层新建的用户和套餐尚未提交，读得到它们即证明初始读取和锁都在同一连接。
func TestS04SubscriptionParticipantReadsUncommittedAndRollsBack(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			ctx := context.Background()
			client := committedEntitlementClient(t)
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			user, err := tx.Client().User.Create().SetEmail("s04-participant@example.com").SetPasswordHash("hash").SetBalance(10).Save(ctx)
			require.NoError(t, err)
			plan, err := tx.Client().SubscriptionPlan.Create().SetPrice(10).SetName("s04 uncommitted").SetValidityDays(7).Save(ctx)
			require.NoError(t, err)
			callCtx := dbent.NewTxContext(ctx, tx)
			subs := billing.NewSubscriptionService(subscriptionContractEmptyGroups{}, billingpostgres.NewUserSubscriptionRepository(client), billingpostgres.NewSubscriptionMutations(client))
			if explicit {
				subs = billingpostgres.SubscriptionsInTx(tx, nil, billing.DateRuntime{Now: time.Now})
				callCtx = ctx // 显式参与入口无需调用者自行安装 context。
			}
			result, err := subs.AssignSubscription(callCtx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID})
			require.NoError(t, err)
			require.NotZero(t, result.ID)
			require.NoError(t, billingpostgres.RedeemInTx(tx).ApplyBalance(ctx, user.ID, 5))
			inside, err := tx.Client().User.Get(ctx, user.ID)
			require.NoError(t, err)
			require.Equal(t, 15.0, inside.Balance)
			require.Equal(t, 5.0, inside.TotalRecharged)
			_, err = client.User.Get(ctx, user.ID)
			require.True(t, dbent.IsNotFound(err), "参与方法不得提前提交调用方用户")
			require.NoError(t, tx.Rollback())
			_, err = client.UserSubscription.Get(ctx, result.ID)
			require.True(t, dbent.IsNotFound(err), "后续业务失败时权益与资金必须整体回滚")
		})
	}
}

// 此用例也原样在 S03 HEAD 执行，证明旧有效期修改独立提交的历史问题。
func TestS04ValidityChangeParticipatesInOuterTransaction(t *testing.T) {
	ctx := context.Background()
	client := committedEntitlementClient(t)
	user, err := client.User.Create().SetEmail("s04-validity@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetPrice(10).SetName("s04 validity").SetValidityDays(2).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.SubscriptionPlan.DeleteOneID(plan.ID).Exec(context.Background()) })
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(48 * time.Hour)
	sub, err := client.UserSubscription.Create().SetUserID(user.ID).SetPlanID(plan.ID).SetStartsAt(now).SetExpiresAt(expires).SetStatus(billing.SubscriptionStatusActive).SetAssignedAt(now).Save(ctx)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	svc := billing.NewSubscriptionService(subscriptionContractEmptyGroups{}, billingpostgres.NewUserSubscriptionRepository(client), billingpostgres.NewSubscriptionMutations(client))
	_, err = svc.SetSubscriptionValidityDays(dbent.NewTxContext(ctx, tx), sub.ID, 30)
	require.NoError(t, err)
	outside, err := client.UserSubscription.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.True(t, outside.ExpiresAt.Equal(expires), "外层提交前不能看到内部有效期写入")
	require.NoError(t, tx.Rollback())
	outside, err = client.UserSubscription.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.True(t, outside.ExpiresAt.Equal(expires))
}

type failedRedeemUsage struct{ billing.RedeemCodeRepository }

func (r failedRedeemUsage) CreateUsage(ctx context.Context, usage *billing.RedeemCodeUsage) error {
	if err := r.RedeemCodeRepository.CreateUsage(ctx, usage); err != nil {
		return err
	}
	return errors.New("s04 usage audit failed after actual insert")
}

type redeemAuthObservation struct{ count atomic.Int32 }

func (o *redeemAuthObservation) InvalidateAuthCacheByUserID(context.Context, int64) { o.count.Add(1) }

// 每种权益都真实写入后制造 usage 失败，验证余额/订阅/并发数和次数同事务回滚。
func TestS04RedeemEffectsWaitForCommit(t *testing.T) {
	for _, kind := range []string{billing.RedeemTypeBalance, billing.RedeemTypeConcurrency, billing.RedeemTypeSubscription} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			client := committedEntitlementClient(t)
			user, err := client.User.Create().SetEmail("s04-redeem@example.com").SetPasswordHash("hash").SetBalance(10).SetConcurrency(2).Save(ctx)
			require.NoError(t, err)
			plan, err := client.SubscriptionPlan.Create().SetPrice(10).SetName("s04 redeem").SetValidityDays(7).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.SubscriptionPlan.DeleteOneID(plan.ID).Exec(context.Background()) })
			repo := billingpostgres.NewRedeemCodeRepository(client)
			code := &billing.RedeemCode{Code: "S04-" + kind, Type: kind, Value: 5, Status: billing.StatusUnused, MaxUses: 1, PlanID: &plan.ID}
			require.NoError(t, repo.Create(ctx, code))
			auth := &redeemAuthObservation{}
			subs := billing.NewSubscriptionService(subscriptionContractEmptyGroups{}, billingpostgres.NewUserSubscriptionRepository(client), billingpostgres.NewSubscriptionMutations(client))
			makeService := func(store billing.RedeemCodeRepository) *billing.RedeemService {
				return billing.NewRedeemService(store, quotaUsersForContract{identitypostgres.NewUserStore(client, integrationDB)}, subs, nil, nil,
					billingpostgres.NewRedeemMutations(client, billingpostgres.RedeemWriters{Balances: billingpostgres.NewBalanceStore(client), Concurrency: identitypostgres.NewConcurrencyStore(client)}), auth, nil, billing.RedeemRuntime{Now: time.Now})
			}
			_, err = makeService(failedRedeemUsage{repo}).Redeem(ctx, user.ID, code.Code)
			require.ErrorContains(t, err, "s04 usage audit failed")
			require.Zero(t, auth.count.Load(), "写失败不得发布认证失效")
			unchanged, err := client.User.Get(ctx, user.ID)
			require.NoError(t, err)
			require.Equal(t, 10.0, unchanged.Balance)
			require.Zero(t, unchanged.TotalRecharged)
			require.Equal(t, 2, unchanged.Concurrency)
			count, err := client.UserSubscription.Query().Where(usersubscription.UserIDEQ(user.ID)).Count(ctx)
			require.NoError(t, err)
			require.Zero(t, count)
			count, err = client.RedeemCodeUsage.Query().Where(redeemcodeusage.RedeemCodeIDEQ(code.ID)).Count(ctx)
			require.NoError(t, err)
			require.Zero(t, count)
			fresh, err := repo.GetByID(ctx, code.ID)
			require.NoError(t, err)
			require.Zero(t, fresh.UsedCount)
			_, err = makeService(repo).Redeem(ctx, user.ID, code.Code)
			require.NoError(t, err)
			require.Equal(t, int32(1), auth.count.Load())
			_, err = makeService(repo).Redeem(ctx, user.ID, code.Code)
			require.Error(t, err)
			require.Equal(t, int32(1), auth.count.Load())
		})
	}
}

// subscriptionContractEmptyGroups 保留旧未配置分组来源的空读取语义。
type subscriptionContractEmptyGroups struct{}

func (subscriptionContractEmptyGroups) GetByIDLite(context.Context, int64) (*billing.SubscriptionPlanGroup, error) {
	return nil, nil
}
