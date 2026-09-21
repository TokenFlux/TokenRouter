// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	fmt "fmt"
	_ "image/png"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

var (
	ErrBalanceNegative             = billing.ErrBalanceNegative
	ErrInsufficientPerms           = infraerrors.Forbidden("INSUFFICIENT_PERMISSIONS", "insufficient permissions")
	ErrNotifyCodeUserRateLimit     = infraerrors.TooManyRequests("NOTIFY_CODE_USER_RATE_LIMIT", "too many verification codes requested, please try again later")
	ErrAvatarInvalid               = infraerrors.BadRequest("AVATAR_INVALID", "avatar must be a valid image data URL or http(s) URL")
	ErrAvatarTooLarge              = infraerrors.BadRequest("AVATAR_TOO_LARGE", "avatar image must be 100KB or smaller")
	ErrAvatarNotImage              = infraerrors.BadRequest("AVATAR_NOT_IMAGE", "avatar content must be an image")
	ErrProfileEmailChangeForbidden = infraerrors.BadRequest("EMAIL_PROFILE_UPDATE_FORBIDDEN", "email must be changed through verified email binding")
	ErrIdentityProviderInvalid     = infraerrors.BadRequest("IDENTITY_PROVIDER_INVALID", "identity provider is invalid")
	ErrIdentityRedirectInvalid     = infraerrors.BadRequest("IDENTITY_REDIRECT_INVALID", "identity redirect path is invalid")
	ErrUserAPIKeyLimitInvalid      = infraerrors.BadRequest("INVALID_API_KEY_LIMIT", fmt.Sprintf("api key limit must be between 0 and %d", MaxUserAPIKeyLimit))
	ErrIdentityUnbindLastMethod    = infraerrors.Conflict(
		"IDENTITY_UNBIND_LAST_METHOD",
		"bind another sign-in method before unbinding this provider",
	)
)
