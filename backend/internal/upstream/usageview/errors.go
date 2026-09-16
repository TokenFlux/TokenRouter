// 技术查询失败保留原类别、reason、消息及比较身份。
package usageview

import infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

var (
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
	ErrUpstreamUsageConfigInvalid = infraerrors.BadRequest(
		"UPSTREAM_USAGE_CONFIG_INVALID", "upstream usage query configuration is invalid",
	)
)
