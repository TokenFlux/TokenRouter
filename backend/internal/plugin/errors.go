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
	ErrPluginsDisabled        = apperror.New(apperror.CategoryServiceUnavailable, "PLUGINS_DISABLED", "Plugins are disabled")
	ErrIndexInvalid           = apperror.BadRequest("PLUGIN_INDEX_INVALID", "Plugin index is invalid")
	ErrIndexSignatureInvalid  = apperror.BadRequest("PLUGIN_INDEX_SIGNATURE_INVALID", "Plugin index signature is invalid")
	ErrConfigInvalid          = apperror.BadRequest("PLUGIN_CONFIG_INVALID", "Plugin configuration is invalid")
	ErrPackageMissing         = apperror.NotFound("PLUGIN_PACKAGE_MISSING", "Plugin package is missing; upload the same version again")
)
