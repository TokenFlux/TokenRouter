package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// shouldUseAntigravityCompat 判断账号是否需要走 Antigravity 原生兼容桥。
func shouldUseAntigravityCompat(account *service.Account) bool {
	return account != nil &&
		account.Platform == capability.PlatformAntigravity &&
		account.Type == capability.AccountTypeOAuth
}
