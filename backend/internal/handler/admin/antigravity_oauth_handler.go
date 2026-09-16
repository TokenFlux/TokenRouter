// 管理路由兼容构造指向账号 HTTP Adapter，S15/S16 清理旧入口。
package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type AntigravityOAuthHandler = httpapi.AntigravityOAuthHandler
type AntigravityGenerateAuthURLRequest = httpapi.AntigravityGenerateAuthURLRequest
type AntigravityExchangeCodeRequest = httpapi.AntigravityExchangeCodeRequest
type AntigravityRefreshTokenRequest = httpapi.AntigravityRefreshTokenRequest

func NewAntigravityOAuthHandler(s *service.AntigravityOAuthService) *AntigravityOAuthHandler {
	return httpapi.NewAntigravityOAuthHandler(s)
}
