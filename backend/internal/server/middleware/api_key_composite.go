// 旧网关中间件只委托 gateway/httpapi，后续随入口清零删除。
package middleware

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
)

const compositeKeyNoGroupContextKey = gatewayhttp.CompositeKeyNoGroupContextKey

func SetCompositeModelContext(c *gin.Context, clientModel, actualModel string) {
	gatewayhttp.SetCompositeModelContext(c, clientModel, actualModel)
}
func GetCompositeModelFromContext(c *gin.Context) (clientModel, actualModel string, ok bool) {
	return gatewayhttp.GetCompositeModelFromContext(c)
}
