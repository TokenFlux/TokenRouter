package routing

import (
	settingvalues "github.com/TokenFlux/TokenRouter/internal/settings"
) // AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	AllowUngroupedKeyScheduling          bool
	EnableModelFallback                  bool
	FallbackModelAnthropic               string
	FallbackModelAntigravity             string
	FallbackModelGemini                  string
	FallbackModelOpenAI                  string
	MarketplaceAvailabilityBucketMinutes int
	MarketplaceAvailabilityWindowDays    int
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {

	result := &AdminReadSettings{}

	result.MarketplaceAvailabilityWindowDays, result.MarketplaceAvailabilityBucketMinutes = ParseMarketplaceAvailabilityWindowSettings(settings)
	result.EnableModelFallback = settings[SettingKeyEnableModelFallback] == "true"
	result.FallbackModelAnthropic = settingvalues.StringOrDefault(settings, SettingKeyFallbackModelAnthropic, "claude-3-5-sonnet-20241022")
	result.FallbackModelOpenAI = settingvalues.StringOrDefault(settings, SettingKeyFallbackModelOpenAI, "gpt-4o")
	result.FallbackModelGemini = settingvalues.StringOrDefault(settings, SettingKeyFallbackModelGemini, "gemini-2.5-pro")
	result.FallbackModelAntigravity = settingvalues.StringOrDefault(settings, SettingKeyFallbackModelAntigravity, "gemini-2.5-pro")
	result.AllowUngroupedKeyScheduling = settings[SettingKeyAllowUngroupedKeyScheduling] == "true"
	return result
}
