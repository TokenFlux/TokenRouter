package composite

import "github.com/TokenFlux/TokenRouter/internal/account"

// ApplyAccountAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyAccountAdminReadSettings(value *account.AdminReadSettings) {
	s.AccountQuotaNotifyEmails = value.AccountQuotaNotifyEmails
	s.AccountQuotaNotifyEnabled = value.AccountQuotaNotifyEnabled
	s.AccountSchedulingThresholds = value.AccountSchedulingThresholds
}
