package routing

import "strconv"

// AdminSettings 只包含路由回退和市场观测窗口配置。
type AdminSettings struct {
	AllowUngroupedKeyScheduling          bool   `json:"allow_ungrouped_key_scheduling"`
	EnableModelFallback                  bool   `json:"enable_model_fallback"`
	FallbackModelAnthropic               string `json:"fallback_model_anthropic"`
	FallbackModelAntigravity             string `json:"fallback_model_antigravity"`
	FallbackModelGemini                  string `json:"fallback_model_gemini"`
	FallbackModelOpenAI                  string `json:"fallback_model_openai"`
	MarketplaceAvailabilityBucketMinutes int    `json:"marketplace_availability_bucket_minutes"`
	MarketplaceAvailabilityWindowDays    int    `json:"marketplace_availability_window_days"`
}

// 路由配置继续使用已有持久键。
const (
	SettingKeyAllowUngroupedKeyScheduling = "allow_ungrouped_key_scheduling"
	SettingKeyEnableModelFallback         = "enable_model_fallback"
	SettingKeyFallbackModelAnthropic      = "fallback_model_anthropic"
	SettingKeyFallbackModelAntigravity    = "fallback_model_antigravity"
	SettingKeyFallbackModelGemini         = "fallback_model_gemini"
	SettingKeyFallbackModelOpenAI         = "fallback_model_openai"
)

// PrepareAdminSettings 复用市场窗口规则，不改变候选选择、映射或回退算法。
func PrepareAdminSettings(settings *AdminSettings) map[string]string {
	updates := map[string]string{}
	settings.MarketplaceAvailabilityWindowDays, settings.MarketplaceAvailabilityBucketMinutes = NormalizeMarketplaceAvailabilityWindow(
		settings.MarketplaceAvailabilityWindowDays,
		settings.MarketplaceAvailabilityBucketMinutes,
	)
	updates[SettingKeyMarketplaceAvailabilityWindowDays] = strconv.Itoa(settings.MarketplaceAvailabilityWindowDays)
	updates[SettingKeyMarketplaceAvailabilityBucketMinutes] = strconv.Itoa(settings.MarketplaceAvailabilityBucketMinutes)
	updates[SettingKeyEnableModelFallback] = strconv.FormatBool(settings.EnableModelFallback)
	updates[SettingKeyFallbackModelAnthropic] = settings.FallbackModelAnthropic
	updates[SettingKeyFallbackModelOpenAI] = settings.FallbackModelOpenAI
	updates[SettingKeyFallbackModelGemini] = settings.FallbackModelGemini
	updates[SettingKeyFallbackModelAntigravity] = settings.FallbackModelAntigravity
	updates[SettingKeyAllowUngroupedKeyScheduling] = strconv.FormatBool(settings.AllowUngroupedKeyScheduling)
	return updates
}
