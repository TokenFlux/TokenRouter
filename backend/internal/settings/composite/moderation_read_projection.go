package composite

import "github.com/TokenFlux/TokenRouter/internal/moderation"

// ApplyModerationAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyModerationAdminReadSettings(value *moderation.AdminReadSettings) {
	s.CyberSessionBlockEnabled = value.CyberSessionBlockEnabled
	s.CyberSessionBlockTTLSeconds = value.CyberSessionBlockTTLSeconds
	s.RiskControlEnabled = value.RiskControlEnabled
}
