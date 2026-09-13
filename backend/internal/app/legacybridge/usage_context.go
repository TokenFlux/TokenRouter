// 旧网关上下文暂存新资金投影；桥接不执行资格或金额规则，S11 退出。
package legacybridge

import (
	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	"github.com/gin-gonic/gin"
)

func PublicUsageContext() usagehttp.PublicUsageContext {
	return usagehttp.PublicUsageContext{Key: func(c *gin.Context) (*keycore.APIKey, bool) {
		v, ok := middleware.GetAPIKeyFromContext(c)
		return service.APIKeyView(v), ok
	}, Billing: func(c *gin.Context) (*middleware.APIKeyBillingContext, bool) {
		return middleware.GetAPIKeyBillingContext(c)
	}, Subscription: middleware.GetSubscriptionFromContext}
}
