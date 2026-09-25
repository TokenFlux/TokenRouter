// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

type APIKey[G any] struct {
	ID                int64  `json:"id"`
	UserID            int64  `json:"user_id"`
	TeamID            *int64 `json:"team_id"`
	Scope             string `json:"scope"`
	TeamOwnerDisabled bool   `json:"team_owner_disabled"` // 告知成员该团队 Key 只能由 Owner 恢复。
	Key               string `json:"key"`
	Name              string `json:"name"`
	GroupID           *int64 `json:"group_id"`
	IsComposite       bool   `json:"is_composite"`
	// CompositeGroups 按用户设置顺序返回复合 Key 的分组映射。
	CompositeGroups         []APIKeyCompositeGroup[G] `json:"composite_groups"`
	Status                  string                    `json:"status"`
	FastModePolicy          string                    `json:"fast_mode_policy"`
	BillingMode             string                    `json:"billing_mode"`
	PreferredSubscriptionID *int64                    `json:"preferred_subscription_id"`
	ModelMapping            map[string]string         `json:"model_mapping"`
	IPWhitelist             []string                  `json:"ip_whitelist"`
	IPBlacklist             []string                  `json:"ip_blacklist"`
	LastUsedAt              *time.Time                `json:"last_used_at"`
	LastUsedIP              *string                   `json:"last_used_ip"` // 最近一条带 IP 的用量日志。
	Quota                   float64                   `json:"quota"`        // Quota limit in USD (0 = unlimited)
	QuotaUsed               float64                   `json:"quota_used"`   // Used quota amount in USD
	ExpiresAt               *time.Time                `json:"expires_at"`   // Expiration time (nil = never expires)
	CreatedAt               time.Time                 `json:"created_at"`
	UpdatedAt               time.Time                 `json:"updated_at"`
	// 绑定分组不可用时是否自动回退到同平台默认分组。
	FallbackToDefaultGroupWhenUnavailable bool `json:"fallback_to_default_group_when_unavailable"`
	// CurrentConcurrency 表示当前 API Key 的实时活跃请求数。
	CurrentConcurrency int `json:"current_concurrency"`

	// Rate limit fields
	RateLimit5h   float64    `json:"rate_limit_5h"`
	RateLimit1d   float64    `json:"rate_limit_1d"`
	RateLimit7d   float64    `json:"rate_limit_7d"`
	Usage5h       float64    `json:"usage_5h"`
	Usage1d       float64    `json:"usage_1d"`
	Usage7d       float64    `json:"usage_7d"`
	Window5hStart *time.Time `json:"window_5h_start"`
	Window1dStart *time.Time `json:"window_1d_start"`
	Window7dStart *time.Time `json:"window_7d_start"`
	Reset5hAt     *time.Time `json:"reset_5h_at,omitempty"`
	Reset1dAt     *time.Time `json:"reset_1d_at,omitempty"`
	Reset7dAt     *time.Time `json:"reset_7d_at,omitempty"`

	// API Key 响应不能携带用户对象，避免团队 Key 暴露付款 Owner 的资产信息。
	Group *G `json:"group,omitempty"`
}

// APIKeyCompositeGroup 是复合 API Key 的公开映射结构。
type APIKeyCompositeGroup[G any] struct {
	GroupID int64  `json:"group_id"`
	Prefix  string `json:"prefix"`
	Group   *G     `json:"group,omitempty"`
}

func APIKeyFromKey[G any](k *apikey.APIKey, group func(*routing.Group) *G) *APIKey[G] {
	if k == nil {
		return nil
	}
	out := &APIKey[G]{
		ID:                                    k.ID,
		UserID:                                k.UserID,
		TeamID:                                k.TeamID,
		TeamOwnerDisabled:                     k.TeamOwnerDisabled,
		Key:                                   k.Key,
		Name:                                  k.Name,
		GroupID:                               k.GroupID,
		IsComposite:                           k.IsComposite,
		Status:                                k.Status,
		FastModePolicy:                        k.FastModePolicy,
		BillingMode:                           k.BillingMode,
		PreferredSubscriptionID:               k.PreferredSubscriptionID,
		ModelMapping:                          apikey.CloneModelMapping(k.ModelMapping),
		IPWhitelist:                           k.IPWhitelist,
		IPBlacklist:                           k.IPBlacklist,
		LastUsedAt:                            k.LastUsedAt,
		LastUsedIP:                            k.LastUsedIP,
		Quota:                                 k.Quota,
		QuotaUsed:                             k.QuotaUsed,
		ExpiresAt:                             k.ExpiresAt,
		CreatedAt:                             k.CreatedAt,
		UpdatedAt:                             k.UpdatedAt,
		RateLimit5h:                           k.RateLimit5h,
		RateLimit1d:                           k.RateLimit1d,
		RateLimit7d:                           k.RateLimit7d,
		Usage5h:                               k.EffectiveUsage5h(),
		Usage1d:                               k.EffectiveUsage1d(),
		Usage7d:                               k.EffectiveUsage7d(),
		Window5hStart:                         k.Window5hStart,
		Window1dStart:                         k.Window1dStart,
		Window7dStart:                         k.Window7dStart,
		FallbackToDefaultGroupWhenUnavailable: k.FallbackToDefaultGroupWhenUnavailable,
		CurrentConcurrency:                    k.CurrentConcurrency,
		Group:                                 group(k.Group),
	}
	out.CompositeGroups = make([]APIKeyCompositeGroup[G], 0, len(k.CompositeGroups))
	for _, binding := range k.CompositeGroups {
		out.CompositeGroups = append(out.CompositeGroups, APIKeyCompositeGroup[G]{
			GroupID: binding.GroupID,
			Prefix:  binding.Prefix,
			Group:   group(binding.Group),
		})
	}
	if k.TeamID != nil {
		out.Scope = "team"
	} else {
		out.Scope = "personal"
	}
	if k.Window5hStart != nil && !apikey.IsWindowExpired(k.Window5hStart, apikey.RateLimitWindow5h) {
		t := k.Window5hStart.Add(apikey.RateLimitWindow5h)
		out.Reset5hAt = &t
	}
	if k.Window1dStart != nil && !apikey.IsWindowExpired(k.Window1dStart, apikey.RateLimitWindow1d) {
		t := k.Window1dStart.Add(apikey.RateLimitWindow1d)
		out.Reset1dAt = &t
	}
	if k.Window7dStart != nil && !apikey.IsWindowExpired(k.Window7dStart, apikey.RateLimitWindow7d) {
		t := k.Window7dStart.Add(apikey.RateLimitWindow7d)
		out.Reset7dAt = &t
	}
	return out
}
