package service

import (
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// SetSchedulerAdminDefaults 仅由 app 在构造时注入不可变默认值。
func (s *SettingService) SetSchedulerAdminDefaults(value *scheduler.AdminDefaults) {
	s.schedulerAdminDefaults = value
}

// SchedulerAdminDefaults 为旧构造保留参数投影；生产由 app 提供。
func (s *SettingService) SchedulerAdminDefaults() scheduler.AdminDefaults {
	if s != nil && s.schedulerAdminDefaults != nil {
		return *s.schedulerAdminDefaults
	}
	value := scheduler.DefaultAdminSettingsDefaults()
	if s != nil && s.cfg != nil {
		cfg := s.cfg.Gateway.AdvancedScheduler
		value.TopK = cfg.LBTopK
		value.Weights = cfg.ScoreWeights
		value.Process.EwmaErrorRateAlpha = cfg.EWMAErrorRateAlpha
		value.Process.EwmaTTFTAlpha = cfg.EWMATTFTAlpha
		value.Process.StickyEscape = policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: cfg.StickyEscapeEnabled, TtftMs: float64(cfg.StickyEscapeTTFTMs), ErrorRate: cfg.StickyEscapeErrorRate})
	}
	return value
}
