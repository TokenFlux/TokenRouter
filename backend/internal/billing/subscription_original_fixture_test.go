package billing_test

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
)

// subscriptionSelectionGroupFixture 保留原测试对意外分组回源的拒绝。
type subscriptionSelectionGroupFixture struct{}

func (subscriptionSelectionGroupFixture) GetByIDLite(context.Context, int64) (*billing.SubscriptionPlanGroup, error) {
	panic("unexpected GetByIDLite call")
}

// newOriginalSubscriptionService 只绑定原生用例与原无数据库事务适配。
func newOriginalSubscriptionService(repo billing.UserSubscriptionRepository) *billing.SubscriptionService {
	return billing.NewSubscriptionService(subscriptionSelectionGroupFixture{}, repo, billingpostgres.NewSubscriptionMutations(nil), originalSubscriptionClock())
}

// originalSubscriptionClock 保留原 service 测试进程的 UTC 约定，改为实例注入而不写 time.Local。
func originalSubscriptionClock() billing.DateRuntime {
	calendar := timezone.NewCalendar(time.UTC)
	return billing.DateRuntime{Now: func() time.Time { return time.Now().UTC() }, Calendar: &calendar}
}
