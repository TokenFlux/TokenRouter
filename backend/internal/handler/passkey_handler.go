// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type PasskeyHandler = identityhttp.PasskeyHandler

func NewPasskeyHandler(
	passkeys *service.PasskeyService,
	authService *service.AuthService,
	settingService *service.SettingService,
) *PasskeyHandler {
	var core *identity.PasskeyService
	if passkeys != nil {
		core = passkeys.PasskeyService
	}
	var settings identityhttp.BackendSettings
	if settingService != nil {
		settings = settingService
	}
	return identityhttp.NewPasskeyHandler(core, authService.IdentityCore(), settings)
}
