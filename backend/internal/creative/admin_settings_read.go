package creative

// AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	CreativeEnabled       bool
	CreativeModelSettings []CreativeModelSetting
	CreativeWorkerCount   int
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {

	result := &AdminReadSettings{}
	result.CreativeModelSettings = ParseCreativeModelSettings(settings[SettingKeyCreativeModelSettings])
	result.CreativeWorkerCount = ParseCreativeWorkerCount(settings[SettingKeyCreativeWorkerCount])
	result.CreativeEnabled = settings[SettingKeyCreativeEnabled] != "false"

	return result
}
