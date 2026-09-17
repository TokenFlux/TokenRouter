package ops

import (
	"strconv"
	"strings"

	settingvalues "github.com/TokenFlux/TokenRouter/internal/settings"
)

// AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	OpenAIQuotaAutoPauseSettings OpsOpenAIAccountQuotaAutoPauseSettings
	OpsMetricsIntervalSeconds    int
	OpsMonitoringEnabled         bool
	OpsRealtimeMonitoringEnabled bool
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {

	result := &AdminReadSettings{}

	result.OpsMonitoringEnabled = !settingvalues.IsExplicitFalse(settings[SettingKeyOpsMonitoringEnabled])
	result.OpsRealtimeMonitoringEnabled = !settingvalues.IsExplicitFalse(settings[SettingKeyOpsRealtimeMonitoringEnabled])
	result.OpsMetricsIntervalSeconds = 60
	if raw := strings.TrimSpace(settings[SettingKeyOpsMetricsIntervalSeconds]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			if v < 60 {
				v = 60
			}
			if v > 3600 {
				v = 3600
			}
			result.OpsMetricsIntervalSeconds = v
		}
	}
	result.OpenAIQuotaAutoPauseSettings = ParseQuotaAutoPauseSettings(settings[SettingKeyOpsAdvancedSettings])
	return result
}
