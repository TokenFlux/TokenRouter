//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	dbent "github.com/BrandonVee/TokenRouter/ent"
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
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)

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
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)

	err = svc.RevokeSubscription(txCtx, 2)
	require.NoError(t, err)
	require.Len(t, repo.writeContexts, 1)
	require.Same(t, tx, dbent.TxFromContext(repo.writeContexts[0]))
}
