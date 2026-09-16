// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

var (
	ErrUpstreamUsageUnavailable = infraerrors.ServiceUnavailable(
		"UPSTREAM_USAGE_UNAVAILABLE", "upstream usage query service is unavailable",
	)
	ErrUpstreamUsageAccountInvalid = infraerrors.BadRequest(
		"UPSTREAM_USAGE_ACCOUNT_INVALID", "account is not a supported API key account",
	)
	ErrUpstreamUsageAccountDisabled = infraerrors.New(infraerrors.Category(422),
		"UPSTREAM_USAGE_ACCOUNT_DISABLED", "account is disabled",
	)
	ErrUpstreamUsageDisabled = infraerrors.New(infraerrors.Category(422),
		"UPSTREAM_USAGE_DISABLED", "upstream usage query is disabled for this account",
	)
	ErrUpstreamUsageUnsupported       = usageview.ErrUpstreamUsageUnsupported
	ErrUpstreamUsageAuthFailed        = usageview.ErrUpstreamUsageAuthFailed
	ErrUpstreamUsageWalletUnavailable = usageview.ErrUpstreamUsageWalletUnavailable
	ErrUpstreamUsageWalletAuthFailed  = usageview.ErrUpstreamUsageWalletAuthFailed
	ErrUpstreamUsageRateLimited       = usageview.ErrUpstreamUsageRateLimited
	ErrUpstreamUsageTimeout           = usageview.ErrUpstreamUsageTimeout
	ErrUpstreamUsageInvalidResponse   = usageview.ErrUpstreamUsageInvalidResponse
	ErrUpstreamUsageRequestFailed     = usageview.ErrUpstreamUsageRequestFailed
	ErrUpstreamUsageIdentityChanged   = infraerrors.Conflict(
		"UPSTREAM_USAGE_IDENTITY_CHANGED", "account credentials or connection settings changed during the query",
	)
	ErrUpstreamUsageConfigInvalid = usageview.ErrUpstreamUsageConfigInvalid
	ErrUpstreamUsageBatchInvalid  = infraerrors.BadRequest(
		"UPSTREAM_USAGE_BATCH_INVALID", "upstream usage batch request is invalid",
	)
	ErrUpstreamUsageBatchTooLarge = infraerrors.BadRequest(
		"UPSTREAM_USAGE_BATCH_TOO_LARGE", "too many accounts in one upstream usage query",
	)
)
