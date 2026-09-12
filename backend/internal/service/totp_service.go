// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

var ErrTotpNotEnabled = identity.ErrTotpNotEnabled

var ErrTotpAlreadyEnabled = identity.ErrTotpAlreadyEnabled

var ErrTotpNotSetup = identity.ErrTotpNotSetup

var ErrTotpInvalidCode = identity.ErrTotpInvalidCode

var ErrTotpSetupExpired = identity.ErrTotpSetupExpired

var ErrTotpTooManyAttempts = identity.ErrTotpTooManyAttempts

var ErrVerifyCodeRequired = identity.ErrVerifyCodeRequired

var ErrPasswordRequired = identity.ErrPasswordRequired

type TotpCache = identity.TotpCache

type SecretEncryptor = identity.SecretEncryptor

type TotpSetupSession = identity.TotpSetupSession

type TotpLoginSession = identity.TotpLoginSession

type PendingOAuthBindLoginSession = identity.PendingOAuthBindLoginSession

type TotpStatus = identity.TotpStatus

type TotpSetupResponse = identity.TotpSetupResponse

type TotpService = identity.TotpService

func NewTotpService(
	userRepo UserRepository,
	encryptor SecretEncryptor,
	cache TotpCache,
	settingService *SettingService,
	emailService *EmailService,
	emailQueueService *EmailQueueService,
) *TotpService {
	return identity.NewTotpService(identitySessionUsers{Repository: userRepo}, encryptor, cache, settingService, emailService, emailQueueService)
}

const StepUpGrantTTL = identity.StepUpGrantTTL

type VerificationMethod = identity.VerificationMethod

func MaskEmail(email string) string { return identity.MaskEmail(email) }
