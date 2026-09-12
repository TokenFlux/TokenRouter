// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

var ErrTurnstileVerificationFailed = identity.ErrTurnstileVerificationFailed

var ErrTurnstileNotConfigured = identity.ErrTurnstileNotConfigured

var ErrTurnstileInvalidSecretKey = identity.ErrTurnstileInvalidSecretKey

type TurnstileVerifier = identity.TurnstileVerifier

type TurnstileService = identity.TurnstileService

type TurnstileVerifyResponse = identity.TurnstileVerifyResponse

// NewTurnstileService 委托所属模块的唯一实现。
func NewTurnstileService(settingService *SettingService, verifier TurnstileVerifier) *TurnstileService {
	var settings identity.CaptchaSettings
	if settingService != nil {
		settings = settingService
	}
	s := identity.NewTurnstileService(settings, verifier)
	s.SetObserver(logger.LegacyPrintf)
	return s
}
