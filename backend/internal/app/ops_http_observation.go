package app

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// provideOpsObservationAccess 只投影观测身份；已加载的失败 Key 不成为鉴权主体。
func provideOpsObservationAccess() gatewayhttp.OpsObservationAccess {
	return gatewayhttp.OpsObservationAccess{
		APIKey: func(c *gin.Context) *apikey.APIKey {
			if key, ok := gatewayhttp.EffectiveAPIKey(c); ok && key != nil {
				return key
			}
			key, _ := middleware.GetOpsFallbackAPIKey(c)
			return key
		},
		Rejected: func(c *gin.Context) bool {
			_, rejected := middleware.GetIngressRejectReason(c)
			return rejected
		},
	}
}
