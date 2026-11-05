package plugin

import "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

// errors.go assigns stable application reasons to plugin management and process
// failures. HTTP adapters preserve these reasons for clients, while callers use
// error-chain comparison to distinguish unavailable generations, retryable drain
// windows, invalid packages, and administrator decisions that require confirmation.

var (
	// Plugin errors keep installation, distribution, and process failures distinguishable without exposing internal transport or filesystem diagnostics to clients.
	ErrPluginNotFound         = apperror.NotFound("PLUGIN_NOT_FOUND", "Plugin not found")
	ErrPluginAlreadyInstalled = apperror.Conflict("PLUGIN_ALREADY_INSTALLED", "Plugin is already installed")
	ErrPluginIncompatible     = apperror.BadRequest("PLUGIN_INCOMPATIBLE", "Plugin is incompatible with this host")
	ErrPermissionsNotAccepted = apperror.BadRequest("PLUGIN_PERMISSIONS_NOT_ACCEPTED", "Confirm all requested plugin permissions")
	ErrPackageInvalid         = apperror.BadRequest("PLUGIN_PACKAGE_INVALID", "Plugin package is invalid")
	ErrDownloadFailed         = apperror.New(apperror.CategoryBadGateway, "PLUGIN_DOWNLOAD_FAILED", "Plugin download failed")
	ErrChecksumMismatch       = apperror.BadRequest("PLUGIN_CHECKSUM_MISMATCH", "Plugin package checksum does not match")
	ErrStartFailed            = apperror.New(apperror.CategoryServiceUnavailable, "PLUGIN_START_FAILED", "Plugin failed to start")
	ErrUpdateFailed           = apperror.New(apperror.CategoryServiceUnavailable, "PLUGIN_UPDATE_FAILED", "Plugin update failed")
	ErrPluginUnavailable      = apperror.New(apperror.CategoryServiceUnavailable, "PLUGIN_UNAVAILABLE", "Plugin is unavailable")
	ErrPluginBusy             = apperror.New(apperror.CategoryServiceUnavailable, "PLUGIN_BUSY", "Plugin request capacity is full")
	ErrPluginRestarting       = apperror.New(apperror.CategoryServiceUnavailable, "PLUGIN_RESTARTING", "Plugin is restarting")
	ErrRouteNotFound          = apperror.NotFound("PLUGIN_ROUTE_NOT_FOUND", "Plugin route not found")
	ErrPluginTimeout          = apperror.New(apperror.CategoryGatewayTimeout, "PLUGIN_TIMEOUT", "Plugin request timed out")
	ErrPluginRequestFailed    = apperror.New(apperror.CategoryBadGateway, "PLUGIN_UNAVAILABLE", "Plugin request failed")
	ErrPluginsDisabled        = apperror.New(apperror.CategoryServiceUnavailable, "PLUGINS_DISABLED", "Plugins are disabled")
	ErrIndexInvalid           = apperror.BadRequest("PLUGIN_INDEX_INVALID", "Plugin index is invalid")
	ErrIndexNotFound          = apperror.NotFound("PLUGIN_INDEX_NOT_FOUND", "Plugin index not found")
	ErrSecretKeyMissing       = apperror.BadRequest("PLUGIN_SECRET_KEY_REQUIRED", "Configure a persistent security.secret_encryption_key before saving plugin secrets")
	ErrLimitsInvalid          = apperror.BadRequest("PLUGIN_LIMITS_INVALID", "Plugin limits are invalid")
	ErrMaxRunning             = apperror.Conflict("MAX_RUNNING_EXCEEDED", "Plugin process limit reached")
	ErrIndexSignatureInvalid  = apperror.BadRequest("PLUGIN_INDEX_SIGNATURE_INVALID", "Plugin index signature is invalid")
	ErrConfigInvalid          = apperror.BadRequest("PLUGIN_CONFIG_INVALID", "Plugin configuration is invalid")
	ErrPackageMissing         = apperror.NotFound("PLUGIN_PACKAGE_MISSING", "Plugin package is missing; upload the same version again")

	// Host capability errors expose protocol reasons while keeping database failures and submitted authorization tickets outside caller-visible diagnostics.
	ErrInvalidArgument      = apperror.BadRequest("INVALID_ARGUMENT", "Invalid plugin capability arguments")
	ErrGrantInvalid         = apperror.Forbidden("GRANT_INVALID", "Plugin caller grant is invalid or expired")
	ErrPermissionNotGranted = apperror.Forbidden("PERMISSION_NOT_GRANTED", "Plugin permission is not granted")
	ErrLimitExceeded        = apperror.New(apperror.CategoryTooManyRequests, "LIMIT_EXCEEDED", "Plugin capability limit exceeded")
	ErrInsufficientBalance  = apperror.BadRequest("INSUFFICIENT_BALANCE", "User balance is insufficient")
	ErrHostUserNotFound     = apperror.NotFound("USER_NOT_FOUND", "User not found")
	ErrPlanNotAllowed       = apperror.BadRequest("PLAN_NOT_ALLOWED", "Subscription plan is not allowed for this plugin")
	ErrHostUnavailable      = apperror.New(apperror.CategoryServiceUnavailable, "HOST_UNAVAILABLE", "Plugin host operation is unavailable")
	ErrEmailDeliveryUnknown = apperror.Conflict("EMAIL_DELIVERY_UNKNOWN", "Email delivery outcome is unknown")
	ErrVersionConflict      = apperror.Conflict("VERSION_CONFLICT", "Plugin storage version does not match")
	ErrKVQuotaExceeded      = apperror.New(apperror.CategoryTooManyRequests, "KV_QUOTA_EXCEEDED", "Plugin storage limit exceeded")
)
