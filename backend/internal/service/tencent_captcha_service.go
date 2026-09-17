// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

var ErrTencentCaptchaVerificationFailed = identity.ErrTencentCaptchaVerificationFailed

var ErrTencentCaptchaNotConfigured = identity.ErrTencentCaptchaNotConfigured

type TencentCaptchaProof = identity.TencentCaptchaProof

type TencentCaptchaCredentials = identity.TencentCaptchaCredentials

const TencentCaptchaRegionCN = identity.TencentCaptchaRegionCN

const TencentCaptchaRegionINTL = identity.TencentCaptchaRegionINTL

type TencentCaptchaVerifyResponse = identity.TencentCaptchaVerifyResponse

type TencentCaptchaVerifier = identity.TencentCaptchaVerifier

type TencentCaptchaService = identity.TencentCaptchaService

// NewTencentCaptchaService 委托所属模块的唯一实现。
func NewTencentCaptchaService(settingService *SettingService, verifier TencentCaptchaVerifier) *TencentCaptchaService {
	var settings identity.CaptchaSettings
	if settingService != nil {
		settings = settingService
	}
	s := identity.NewTencentCaptchaService(settings, verifier)
	s.SetObserver(logger.LegacyPrintf)
	return s
}
