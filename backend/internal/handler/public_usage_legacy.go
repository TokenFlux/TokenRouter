package handler

import (
	"context"
	"time"

	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	"github.com/gin-gonic/gin"
)

// publicUsageHTTP 仅为旧 Gateway 测试和过渡消费者投影依赖。
func (h *GatewayHandler) publicUsageHTTP() *usagehttp.PublicUsageHandler {
	var k usagehttp.PublicWindowReader
	if h.apiKeyService != nil {
		k = h.apiKeyService
	}
	var balance usagehttp.PublicBalanceReader
	if h.userService != nil {
		balance = usagehttp.PublicBalanceQuery(func(ctx context.Context, id int64) (*usagehttp.PublicUserBalance, error) {
			v, e := h.userService.GetByID(ctx, id)
			if e != nil {
				return nil, e
			}
			return &usagehttp.PublicUserBalance{Balance: v.Balance}, nil
		})
	}
	var settings usagehttp.BalanceUnitReader
	if h.balanceUnit != nil {
		settings = h.balanceUnit
	}
	return usagehttp.NewPublicUsageHandler(h.usageService, k, balance, settings, usagehttp.PublicUsageContext{Key: func(c *gin.Context) (*keycore.APIKey, bool) {
		v, ok := middleware.GetAPIKeyFromContext(c)
		return keycore.CopyAPIKey(v), ok
	}, Billing: func(c *gin.Context) (*middleware.APIKeyBillingContext, bool) {
		return middleware.GetAPIKeyBillingContext(c)
	}, Subscription: middleware.GetSubscriptionFromContext}, timezone.NewCalendar(time.Local))
}
