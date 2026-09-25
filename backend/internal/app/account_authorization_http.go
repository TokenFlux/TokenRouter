package app

import (
	accountauth "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"

	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
)

// provideClaudeAuthorizationHTTP 直接绑定已有授权用例，不再经管理 handler 转接。
func provideClaudeAuthorizationHTTP(source *accountauth.ClaudeAuthorization) *accounthttp.ClaudeOAuthHandler {
	return accounthttp.NewClaudeOAuthHandler(source)
}

func provideQoderAuthorizationHTTP(source *provider.QoderAuthorization) *accounthttp.QoderOAuthHandler {
	return accounthttp.NewQoderOAuthHandler(source)
}
