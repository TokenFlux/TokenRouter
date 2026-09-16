// 旧构造入口仅绑定唯一账号 HTTP 实现，S15/S16 清理。
package admin

import (
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type QoderOAuthHandler = accounthttp.QoderOAuthHandler
type QoderGenerateAuthURLRequest = accounthttp.QoderGenerateAuthURLRequest
type QoderExchangeCodeRequest = accounthttp.QoderExchangeCodeRequest
type QoderPollRequest = accounthttp.QoderPollRequest

func NewQoderOAuthHandler(s *service.QoderOAuthService) *QoderOAuthHandler {
	return accounthttp.NewQoderOAuthHandler(s.Core)
}
