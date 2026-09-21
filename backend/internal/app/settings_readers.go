package app

import (
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/creative"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// 配置读取器共享唯一 Store；构造不回源，各模块保持原缓存作用域与读取时机。
// @project-doc docs/interfaces/configuration.md#runtime_settings
func providePanelSettings(store *settings.Store) *runtimeconfig.PanelSettings {
	return runtimeconfig.NewPanelSettings(store)
}

func providePromotionSettings(store *settings.Store) *promotion.RuntimeSettings {
	return promotion.NewRuntimeSettings(store)
}

func provideAccountSettings(store *settings.Store) *account.RuntimeSettings {
	return account.NewRuntimeSettings(store, settings.ErrSettingNotFound)
}

func provideUsageSettings(store *settings.Store) *usage.RuntimeSettings {
	return usage.NewRuntimeSettings(store)
}

func provideAuditSettings(store *settings.Store) *audit.RetentionSettings {
	return audit.NewRetentionSettings(store)
}

func provideIdentitySettings(store *settings.Store) *identity.RuntimeSettings {
	return identity.NewRuntimeSettings(store, settings.ErrSettingNotFound)
}

func provideRoutingSettings(store *settings.Store) *routing.RuntimeSettings {
	return routing.NewRuntimeSettings(store)
}

func provideBackendMode(store *settings.Store) *admission.BackendMode {
	return admission.NewBackendMode(store, slog.Warn)
}

// provideCreativeRuntimeSettings 直接共享设置 Store，保持请求时读取，不增加缓存。
func provideCreativeRuntimeSettings(store *settings.Store) *creative.RuntimeSettings {
	return creative.NewRuntimeSettings(store, settings.ErrSettingNotFound)
}
