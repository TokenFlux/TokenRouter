//go:build unit

package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/stretchr/testify/require"
)

// transactionTrackingUserSubRepo 记录订阅写操作使用的事务上下文。
type transactionTrackingUserSubRepo struct {
	*subscriptionUserSubRepoStub
	writeContexts []context.Context
}

func (r *transactionTrackingUserSubRepo) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	r.writeContexts = append(r.writeContexts, ctx)
	sub := r.byID[subscriptionID]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.ExpiresAt = newExpiresAt
	return nil
}

func (r *transactionTrackingUserSubRepo) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	r.writeContexts = append(r.writeContexts, ctx)
	sub := r.byID[subscriptionID]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.Status = status
	return nil
}

func (r *transactionTrackingUserSubRepo) Delete(ctx context.Context, subscriptionID int64) error {
	r.writeContexts = append(r.writeContexts, ctx)
	delete(r.byID, subscriptionID)
	r.rebuildIndex()
	return nil
}

func TestExtendSubscriptionReusesCallerTransaction(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	txCtx := dbent.NewTxContext(ctx, tx)

	now := time.Now().UTC()
	repo := &transactionTrackingUserSubRepo{subscriptionUserSubRepoStub: newSubscriptionUserSubRepoStub()}
	repo.seed(&UserSubscription{
		ID: 1, UserID: 7, PlanID: 9, StartsAt: now.Add(-24 * time.Hour),
		ExpiresAt: now.Add(10 * 24 * time.Hour), Status: SubscriptionStatusActive,
	})
	svc := billing.NewSubscriptionService(nil, repo, &subscriptionContextTransactions{SubscriptionMutations: billingpostgres.NewSubscriptionMutations(nil), t: t, tx: tx})

	_, err = svc.ExtendSubscription(txCtx, 1, -1)
	require.NoError(t, err)
	require.NotEmpty(t, repo.writeContexts)
	for _, writeCtx := range repo.writeContexts {
		require.Same(t, tx, dbent.TxFromContext(writeCtx))
	}
}

func TestRevokeSubscriptionReusesCallerTransaction(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	txCtx := dbent.NewTxContext(ctx, tx)

	now := time.Now().UTC()
	repo := &transactionTrackingUserSubRepo{subscriptionUserSubRepoStub: newSubscriptionUserSubRepoStub()}
	repo.seed(&UserSubscription{
		ID: 2, UserID: 8, PlanID: 10, StartsAt: now.Add(-24 * time.Hour),
		ExpiresAt: now.Add(10 * 24 * time.Hour), Status: SubscriptionStatusActive,
	})
	svc := billing.NewSubscriptionService(nil, repo, &subscriptionContextTransactions{SubscriptionMutations: billingpostgres.NewSubscriptionMutations(nil), t: t, tx: tx})

	err = svc.RevokeSubscription(txCtx, 2)
	require.NoError(t, err)
	require.Len(t, repo.writeContexts, 1)
	require.Same(t, tx, dbent.TxFromContext(repo.writeContexts[0]))
}

// SQLite 仅用于此测试的事务身份断言；真实锁与回滚由 S04 PostgreSQL 集成测试验收。
type subscriptionContextTransactions struct {
	*billingpostgres.SubscriptionMutations
	t  *testing.T
	tx *dbent.Tx
}

func (s *subscriptionContextTransactions) LockSubscription(ctx context.Context, _ int64) error {
	require.Same(s.t, s.tx, dbent.TxFromContext(ctx))
	return nil
}
