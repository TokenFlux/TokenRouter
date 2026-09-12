//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package middleware

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

// requestSessionBinding 委托身份 HTTP 适配。
func requestSessionBinding(c *gin.Context) *service.SessionBinding {
	return identityhttp.RequestSessionBinding(c)
}
