package team

// PublicSettings 返回运行设置在进程许可范围内的公开团队能力。
func PublicSettings(values map[string]string, enabled, selfService bool) (bool, bool) {
	return values[SettingKeyTeamEnabled] != "false" && enabled, selfService
}
