package textattempt

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// shouldUseAntigravityCompat 判断账号是否需要走 Antigravity 原生兼容桥。
func shouldUseAntigravityCompat(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil &&
		account.Record.Platform == capability.PlatformAntigravity &&
		account.Record.Type == capability.AccountTypeOAuth
}
