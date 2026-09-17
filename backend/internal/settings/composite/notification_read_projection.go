package composite

import "github.com/TokenFlux/TokenRouter/internal/notification"

// ApplyNotificationAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyNotificationAdminReadSettings(value *notification.AdminReadSettings) {
	s.SMTPFrom = value.SMTPFrom
	s.SMTPFromName = value.SMTPFromName
	s.SMTPHost = value.SMTPHost
	s.SMTPPassword = value.SMTPPassword
	s.SMTPPasswordConfigured = value.SMTPPasswordConfigured
	s.SMTPPort = value.SMTPPort
	s.SMTPUseTLS = value.SMTPUseTLS
	s.SMTPUsername = value.SMTPUsername
}
