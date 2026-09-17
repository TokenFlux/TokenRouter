package composite

import "github.com/TokenFlux/TokenRouter/internal/creative"

// ApplyCreativeAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyCreativeAdminReadSettings(value *creative.AdminReadSettings) {
	s.CreativeEnabled = value.CreativeEnabled
	s.CreativeModelSettings = value.CreativeModelSettings
	s.CreativeWorkerCount = value.CreativeWorkerCount
}
