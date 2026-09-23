package app

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/gin-gonic/gin"
)

// batchImageAccessPorts 仅投影旧认证上下文，任务 Adapter 不导入旧聚合服务。
func batchImageAccessPorts() httpapi.AccessPorts {
	return httpapi.AccessPorts{Key: func(c *gin.Context) (*apikey.APIKey, bool) {
		k, ok := keyhttp.GetAPIKeyFromContext(c)
		return apikey.CopyAPIKey(k), ok
	}, PreferredSubscription: func(c *gin.Context) (*billing.UserSubscription, bool) {
		v, ok := gatewayhttp.GetAPIKeyBillingContext(c)
		if !ok || v == nil || v.Mode != apikey.APIKeyBillingModeSubscription || v.Subscription == nil {
			return nil, false
		}
		return v.Subscription, true
	}, SessionID: gatewayhttp.ExtractClientSessionID}
}
