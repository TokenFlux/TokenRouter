package audit

// AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct{ AuditLogRetentionDays int }

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {

	result := &AdminReadSettings{}
	result.AuditLogRetentionDays = ParseRetentionDays(settings[SettingKeyAuditLogRetentionDays])

	return result
}
