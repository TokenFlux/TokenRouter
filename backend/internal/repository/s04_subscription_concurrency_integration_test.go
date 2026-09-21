//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"
)

// 只暂停真实仓储的首次读取，让另一个管理员同时延长同一条时间链。
type s04PausedSubscriptionRead struct {
	billing.UserSubscriptionRepository
	read, release chan struct{}
	once          sync.Once
}

func (r *s04PausedSubscriptionRead) GetByID(ctx context.Context, id int64) (*billing.UserSubscription, error) {
	sub, err := r.UserSubscriptionRepository.GetByID(ctx, id)
	r.once.Do(func() {
		close(r.read)
		select {
		case <-r.release:
		case <-ctx.Done():
		}
	})
	return sub, err
}
func TestS04ConcurrentSubscriptionExtensionsUseLockedState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := testEntClient(t)
	user, err := client.User.Create().SetEmail("s04-chain@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("s04 chain").SetPrice(10).SetValidityDays(2).Save(ctx)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(48 * time.Hour)
	sub, err := client.UserSubscription.Create().SetUserID(user.ID).SetPlanID(plan.ID).SetStartsAt(now).SetExpiresAt(expires).SetStatus(billing.SubscriptionStatusActive).SetAssignedAt(now).Save(ctx)
	require.NoError(t, err)
	repo := billingpostgres.NewUserSubscriptionRepository(client)
	paused := &s04PausedSubscriptionRead{UserSubscriptionRepository: repo, read: make(chan struct{}), release: make(chan struct{})}
	first := billing.NewSubscriptionService(subscriptionContractEmptyGroups{}, paused, billingpostgres.NewSubscriptionMutations(client))
	second := billing.NewSubscriptionService(subscriptionContractEmptyGroups{}, repo, billingpostgres.NewSubscriptionMutations(client))
	result1, result2 := make(chan error, 1), make(chan error, 1)
	go func() { _, e := first.ExtendSubscription(ctx, sub.ID, 7); result1 <- e }()
	select {
	case <-paused.read:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() { _, e := second.ExtendSubscription(ctx, sub.ID, 7); result2 <- e }()
	var secondErr error
	finished := false
	select {
	case secondErr = <-result2:
		finished = true
		t.Error("第二次延长绕过了首个操作者的状态锁")
	case <-time.After(30 * time.Millisecond):
	}
	close(paused.release)
	require.NoError(t, <-result1)
	if !finished {
		secondErr = <-result2
	}
	require.NoError(t, secondErr)
	final, err := client.UserSubscription.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.True(t, final.ExpiresAt.Equal(expires.AddDate(0, 0, 14)), "两个 +7 天操作必须累计为 +14 天，不能用旧快照覆盖")
}
