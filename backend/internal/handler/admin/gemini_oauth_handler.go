// 旧管理路由构造转接账号 HTTP Adapter，URL 和 DTO 不变。
package admin

import (
	accountapi "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type GeminiOAuthHandler = accountapi.GeminiOAuthHandler

func NewGeminiOAuthHandler(source *service.GeminiOAuthService) *GeminiOAuthHandler {
	return accountapi.NewGeminiOAuthHandler(source)
}

type GeminiGenerateAuthURLRequest = accountapi.GeminiGenerateAuthURLRequest
type GeminiExchangeCodeRequest = accountapi.GeminiExchangeCodeRequest
