package handler

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/gin-gonic/gin"
)

// AuthEndpoints 保留旧测试的组合形状，路由实现分别由 identity/payment 拥有。
type AuthEndpoints interface {
	identityhttp.AuthEndpoints
	WeChatPaymentOAuthStart(*gin.Context)
	WeChatPaymentOAuthCallback(*gin.Context)
}
