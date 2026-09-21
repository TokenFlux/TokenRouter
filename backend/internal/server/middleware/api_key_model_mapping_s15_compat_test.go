// 旧网关中间件只委托 gateway/httpapi，后续随入口清零删除。
package middleware

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
)

func applyAPIKeyModelRedirect(c *gin.Context, apiKey *apikey.APIKey) {
	gatewayhttp.ApplyAPIKeyModelRedirect(c, apikey.CopyAPIKey(apiKey))
}
