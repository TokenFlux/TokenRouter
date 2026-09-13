// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	time "time"
)

const GeminiQuotaPolicySettingKey = "gemini_quota_policy"

type GeminiTierQuotaOverride struct {
	ProRPD          *int64 `json:"pro_rpd"`
	FlashRPD        *int64 `json:"flash_rpd"`
	CooldownMinutes *int   `json:"cooldown_minutes"`
}

// GeminiQuotaOptions 区分静态配置和按原 TTL 加载的动态设置。
type GeminiQuotaOptions struct {
	StaticTiers  map[string]GeminiTierQuotaOverride
	StaticPolicy string
	LoadPolicy   func(context.Context) (string, error)
	NotFound     error
	Now          func() time.Time
	Log          func(string, ...any)
}

func cloneGeminiTierOverrides(values map[string]GeminiTierQuotaOverride) map[string]GeminiTierQuotaOverride {
	if values == nil {
		return nil
	}
	out := make(map[string]GeminiTierQuotaOverride, len(values))
	for id, v := range values {
		v.ProRPD = clonePointer(v.ProRPD)
		v.FlashRPD = clonePointer(v.FlashRPD)
		v.CooldownMinutes = clonePointer(v.CooldownMinutes)
		out[id] = v
	}
	return out
}
