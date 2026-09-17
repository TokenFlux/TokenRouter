// 旧网关中间件只委托 gateway/httpapi，后续随入口清零删除。
package middleware

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

func applyAPIKeyModelRedirect(c *gin.Context, apiKey *service.APIKey) {
	gatewayhttp.ApplyAPIKeyModelRedirect(c, service.APIKeyView(apiKey))
}
