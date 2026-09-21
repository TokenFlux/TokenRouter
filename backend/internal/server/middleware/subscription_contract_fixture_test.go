package middleware

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
)

// subscriptionAuthGroups 延续旧构造未配置分组来源时的空读取结果。
type subscriptionAuthGroups struct{}

func (subscriptionAuthGroups) GetByIDLite(context.Context, int64) (*billing.SubscriptionPlanGroup, error) {
	return nil, nil
}

// newSubscriptionAuthFixture 直接组装认证契约所用的唯一订阅实现。
func newSubscriptionAuthFixture(repo billing.UserSubscriptionRepository) *billing.SubscriptionService {
	return billing.NewSubscriptionService(subscriptionAuthGroups{}, repo, billingpostgres.NewSubscriptionMutations(nil))
}
