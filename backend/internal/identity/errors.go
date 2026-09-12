// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

var (
	ErrInvalidCredentials           = infraerrors.Unauthorized("INVALID_CREDENTIALS", "invalid email or password")
	ErrUserNotActive                = infraerrors.Forbidden("USER_NOT_ACTIVE", "user is not active")
	ErrEmailExists                  = infraerrors.Conflict("EMAIL_EXISTS", "email already exists")
	ErrEmailReserved                = infraerrors.BadRequest("EMAIL_RESERVED", "email is reserved")
	ErrInvalidToken                 = infraerrors.Unauthorized("INVALID_TOKEN", "invalid token")
	ErrTokenExpired                 = infraerrors.Unauthorized("TOKEN_EXPIRED", "token has expired")
	ErrAccessTokenExpired           = infraerrors.Unauthorized("ACCESS_TOKEN_EXPIRED", "access token has expired")
	ErrTokenTooLarge                = infraerrors.BadRequest("TOKEN_TOO_LARGE", "token too large")
	ErrTokenRevoked                 = infraerrors.Unauthorized("TOKEN_REVOKED", "token has been revoked")
	ErrRefreshTokenInvalid          = infraerrors.Unauthorized("REFRESH_TOKEN_INVALID", "invalid refresh token")
	ErrRefreshTokenExpired          = infraerrors.Unauthorized("REFRESH_TOKEN_EXPIRED", "refresh token has expired")
	ErrRefreshTokenReused           = infraerrors.Unauthorized("REFRESH_TOKEN_REUSED", "refresh token has been reused")
	ErrEmailVerifyRequired          = infraerrors.BadRequest("EMAIL_VERIFY_REQUIRED", "email verification is required")
	ErrEmailSuffixNotAllowed        = infraerrors.BadRequest("EMAIL_SUFFIX_NOT_ALLOWED", "email suffix is not allowed")
	ErrEmailDomainRegistrationLimit = infraerrors.BadRequest(
		"EMAIL_DOMAIN_REGISTRATION_LIMIT",
		"this email domain cannot register another account; use a mainstream email or contact support to add the enterprise domain",
	)
	ErrEmailChangeDisabled     = infraerrors.Forbidden("EMAIL_CHANGE_DISABLED", "email changes are disabled")
	ErrRegDisabled             = infraerrors.Forbidden("REGISTRATION_DISABLED", "registration is currently disabled")
	ErrServiceUnavailable      = infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "service temporarily unavailable")
	ErrInvitationCodeRequired  = infraerrors.BadRequest("INVITATION_CODE_REQUIRED", "invitation code is required")
	ErrInvitationCodeInvalid   = infraerrors.BadRequest("INVITATION_CODE_INVALID", "invalid or used invitation code")
	ErrOAuthInvitationRequired = infraerrors.Forbidden("OAUTH_INVITATION_REQUIRED", "invitation code required to complete oauth registration")
	ErrCaptchaProviderConflict = infraerrors.ServiceUnavailable("CAPTCHA_PROVIDER_CONFLICT", "multiple captcha providers are enabled")
)

// ErrPasswordIncorrect 表示当前密码校验失败。
var ErrPasswordIncorrect = infraerrors.BadRequest("PASSWORD_INCORRECT", "current password is incorrect")
