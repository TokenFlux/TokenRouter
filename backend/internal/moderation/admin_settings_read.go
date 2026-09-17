package moderation

import (
	"strconv"
	"strings"
)

// AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	CyberSessionBlockEnabled    bool
	CyberSessionBlockTTLSeconds int
	RiskControlEnabled          bool
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {

	result := &AdminReadSettings{}
	result.RiskControlEnabled = settings[SettingKeyRiskControlEnabled] == "true"
	result.CyberSessionBlockEnabled = settings[SettingKeyCyberSessionBlockEnabled] == "true"
	if seconds, err := strconv.Atoi(strings.TrimSpace(settings[SettingKeyCyberSessionBlockTTLSeconds])); err == nil && seconds > 0 {
		result.CyberSessionBlockTTLSeconds = seconds
	} else {
		result.CyberSessionBlockTTLSeconds = 3600
	}
	return result
}
