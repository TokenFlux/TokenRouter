package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
)

// PanelRateLimitSettings 保留旧调用方的值类型，规则与缓存由 server 拥有。
type PanelRateLimitSettings = runtimeconfig.PanelRateLimitSettings

// DefaultPanelRateLimitSettings 委托唯一默认配置。
func DefaultPanelRateLimitSettings() *PanelRateLimitSettings {
	return runtimeconfig.DefaultPanelRateLimitSettings()
}

// PanelSettings 返回同一生产配置实例，供装配逐步移除旧聚合入口。
func (s *SettingService) PanelSettings() *runtimeconfig.PanelSettings {
	if s == nil {
		return nil
	}
	return s.panelSettings
}

// GetPanelRateLimitSettings 委托管理读取。
func (s *SettingService) GetPanelRateLimitSettings(ctx context.Context) (*PanelRateLimitSettings, error) {
	return s.panelSettings.GetPanelRateLimitSettings(ctx)
}

// SetPanelRateLimitSettings 委托持久化及原有缓存刷新。
func (s *SettingService) SetPanelRateLimitSettings(ctx context.Context, value *PanelRateLimitSettings) error {
	return s.panelSettings.SetPanelRateLimitSettings(ctx, value)
}

// GetPanelRateLimitSettingsCached 委托请求热路径读取。
func (s *SettingService) GetPanelRateLimitSettingsCached(ctx context.Context) PanelRateLimitSettings {
	return s.PanelSettings().GetPanelRateLimitSettingsCached(ctx)
}
