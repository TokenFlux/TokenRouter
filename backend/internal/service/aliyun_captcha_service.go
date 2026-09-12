// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

var ErrAliyunCaptchaVerificationFailed = identity.ErrAliyunCaptchaVerificationFailed

var ErrAliyunCaptchaNotConfigured = identity.ErrAliyunCaptchaNotConfigured

var ErrCaptchaInvalidCredentials = identity.ErrCaptchaInvalidCredentials

type AliyunCaptchaCredentials = identity.AliyunCaptchaCredentials

type AliyunCaptchaVerifyResult = identity.AliyunCaptchaVerifyResult

type AliyunCaptchaAPIError = identity.AliyunCaptchaAPIError

type AliyunCaptchaVerifier = identity.AliyunCaptchaVerifier

const AliyunCaptchaRegionCN = identity.AliyunCaptchaRegionCN

const AliyunCaptchaRegionSGP = identity.AliyunCaptchaRegionSGP

// normalizeAliyunCaptchaRegion 委托所属模块的唯一实现。
func normalizeAliyunCaptchaRegion(value string) string {
	return identity.NormalizeAliyunCaptchaRegion(value)
}

type AliyunCaptchaService = identity.AliyunCaptchaService

// NewAliyunCaptchaService 委托所属模块的唯一实现。
func NewAliyunCaptchaService(settingService *SettingService, verifier AliyunCaptchaVerifier) *AliyunCaptchaService {
	var settings identity.CaptchaSettings
	if settingService != nil {
		settings = settingService
	}
	s := identity.NewAliyunCaptchaService(settings, verifier)
	s.SetObserver(logger.LegacyPrintf)
	return s
}
