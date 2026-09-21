// 公开用量直接接入新用量实现，身份与窗口只提供只读投影。
package app

import (
	"context"

	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	"github.com/gin-gonic/gin"
)

// publicBalanceUnit 只投影 billing 的单键展示读取，不创建独立缓存。
type publicBalanceUnit struct{ store *settings.Store }

func (r publicBalanceUnit) GetBalanceUnitName(ctx context.Context) string {
	return billing.ReadBalanceUnitName(ctx, r.store)
}

func providePublicUsage(u *usage.UsageService, k *keycore.APIKeyService, users *identity.UserService, store *settings.Store, calendar timezone.Calendar) *usagehttp.PublicUsageHandler {
	return usagehttp.NewPublicUsageHandler(u, k, usagehttp.PublicBalanceQuery(func(ctx context.Context, id int64) (*usagehttp.PublicUserBalance, error) {
		v, e := users.GetByID(ctx, id)
		if e != nil {
			return nil, e
		}
		return &usagehttp.PublicUserBalance{Balance: v.Balance}, nil
	}), publicBalanceUnit{store}, usagehttp.PublicUsageContext{
		Key: func(c *gin.Context) (*keycore.APIKey, bool) {
			value, ok := middleware.GetAPIKeyFromContext(c)
			return keycore.CopyAPIKey(value), ok
		},
		Billing: func(c *gin.Context) (*billing.APIKeyBillingContext, bool) {
			return middleware.GetAPIKeyBillingContext(c)
		},
		Subscription: func(c *gin.Context) (*billing.UserSubscription, bool) {
			return middleware.GetSubscriptionFromContext(c)
		},
	}, calendar)
}

// providePublicBalanceUnit 让旧 HTTP 消费者直接使用唯一 billing 展示规则。
func providePublicBalanceUnit(store *settings.Store) usagehttp.BalanceUnitReader {
	return publicBalanceUnit{store}
}
