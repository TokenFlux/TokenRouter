// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
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
	ErrUpstreamUsageUnsupported = infraerrors.New(infraerrors.Category(422),
		"UPSTREAM_USAGE_ADAPTER_UNSUPPORTED", "upstream usage adapter is unsupported",
	)
	ErrUpstreamUsageAuthFailed = infraerrors.New(infraerrors.CategoryBadGateway,
		"UPSTREAM_USAGE_AUTH_FAILED", "upstream rejected the account API key",
	)
	ErrUpstreamUsageWalletUnavailable = infraerrors.New(infraerrors.CategoryBadGateway,
		"UPSTREAM_USAGE_WALLET_UNAVAILABLE", "upstream wallet balance is unavailable",
	)
	ErrUpstreamUsageWalletAuthFailed = infraerrors.New(infraerrors.CategoryBadGateway,
		"UPSTREAM_USAGE_WALLET_AUTH_FAILED", "upstream rejected the wallet access token",
	)
	ErrUpstreamUsageRateLimited = infraerrors.ServiceUnavailable(
		"UPSTREAM_USAGE_RATE_LIMITED", "upstream usage query was rate limited",
	)
	ErrUpstreamUsageTimeout = infraerrors.GatewayTimeout(
		"UPSTREAM_USAGE_TIMEOUT", "upstream usage query timed out",
	)
	ErrUpstreamUsageInvalidResponse = infraerrors.New(infraerrors.CategoryBadGateway,
		"UPSTREAM_USAGE_INVALID_RESPONSE", "upstream returned an invalid usage response",
	)
	ErrUpstreamUsageRequestFailed = infraerrors.New(infraerrors.CategoryBadGateway,
		"UPSTREAM_USAGE_REQUEST_FAILED", "upstream usage request failed",
	)
	ErrUpstreamUsageIdentityChanged = infraerrors.Conflict(
		"UPSTREAM_USAGE_IDENTITY_CHANGED", "account credentials or connection settings changed during the query",
	)
	ErrUpstreamUsageConfigInvalid = infraerrors.BadRequest(
		"UPSTREAM_USAGE_CONFIG_INVALID", "upstream usage query configuration is invalid",
	)
	ErrUpstreamUsageBatchInvalid = infraerrors.BadRequest(
		"UPSTREAM_USAGE_BATCH_INVALID", "upstream usage batch request is invalid",
	)
	ErrUpstreamUsageBatchTooLarge = infraerrors.BadRequest(
		"UPSTREAM_USAGE_BATCH_TOO_LARGE", "too many accounts in one upstream usage query",
	)
)
