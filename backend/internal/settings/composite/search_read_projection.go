package composite

import "github.com/TokenFlux/TokenRouter/internal/search"

// ApplySearchAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplySearchAdminReadSettings(value *search.AdminReadSettings) {
	s.WebSearchEmulationEnabled = value.WebSearchEmulationEnabled
}
