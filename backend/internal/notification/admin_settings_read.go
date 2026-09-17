package notification

import (
	"strconv"
) // AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	SMTPFrom               string
	SMTPFromName           string
	SMTPHost               string
	SMTPPassword           string
	SMTPPasswordConfigured bool
	SMTPPort               int
	SMTPUseTLS             bool
	SMTPUsername           string
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {

	result := &AdminReadSettings{}
	result.SMTPHost = settings[SettingKeySMTPHost]
	result.SMTPUsername = settings[SettingKeySMTPUsername]
	result.SMTPFrom = settings[SettingKeySMTPFrom]
	result.SMTPFromName = settings[SettingKeySMTPFromName]
	result.SMTPUseTLS = settings[SettingKeySMTPUseTLS] == "true"
	result.SMTPPasswordConfigured = settings[SettingKeySMTPPassword] != ""
	if port, err := strconv.Atoi(settings[SettingKeySMTPPort]); err == nil {
		result.SMTPPort = port
	} else {
		result.SMTPPort = 587
	}
	result.SMTPPassword = settings[SettingKeySMTPPassword]
	return result
}
