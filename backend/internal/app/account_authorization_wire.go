//go:build wireinject

package app

import (
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/google/wire"
)

// accountAuthorizationHTTPProviders 绑定实际授权端口；平台旧适配随账号能力清理。
var accountAuthorizationHTTPProviders = wire.NewSet(
	provideClaudeAuthorizationHTTP,
	provideQoderAuthorizationHTTP,
	accounthttp.NewGeminiOAuthHandler,
	accounthttp.NewAntigravityOAuthHandler,
	accounthttp.NewCodexInviteResetHandler,
	identityhttp.NewUserAttributeHandler,
)
