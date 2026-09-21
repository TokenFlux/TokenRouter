//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package middleware

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	gin "github.com/gin-gonic/gin"
)

// requestSessionBinding 委托身份 HTTP 适配。
func requestSessionBinding(c *gin.Context) *identity.SessionBinding {
	return identityhttp.RequestSessionBinding(c)
}
