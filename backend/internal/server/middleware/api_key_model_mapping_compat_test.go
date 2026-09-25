// 中间件测试辅助函数委托 gateway/httpapi。
package middleware

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
)

func applyAPIKeyModelRedirect(c *gin.Context, apiKey *apikey.APIKey) {
	gatewayhttp.ApplyAPIKeyModelRedirect(c, apikey.CopyAPIKey(apiKey))
}
