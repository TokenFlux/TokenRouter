package composite

import "github.com/TokenFlux/TokenRouter/internal/audit"

// ApplyAuditAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyAuditAdminReadSettings(value *audit.AdminReadSettings) {
	s.AuditLogRetentionDays = value.AuditLogRetentionDays
}
