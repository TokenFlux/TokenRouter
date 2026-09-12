// 本文件维护 accessview 的所属能力；兼容入口复用唯一实现。
package accessview

// GroupAdvancedSchedulerOverrides 保存分组对通用高级调度器的稀疏覆盖。
// nil 表示继承网关通用设置；指针使 false 和 0 可以作为明确覆盖值保存。
type GroupAdvancedSchedulerOverrides struct {
	StickyWeightedEnabled       *bool    `json:"sticky_weighted_enabled,omitempty"`
	SubscriptionPriorityEnabled *bool    `json:"subscription_priority_enabled,omitempty"`
	EWMAErrorRateAlpha          *float64 `json:"ewma_error_rate_alpha,omitempty"`
	EWMATTFTAlpha               *float64 `json:"ewma_ttft_alpha,omitempty"`
	StickyEscapeEnabled         *bool    `json:"sticky_escape_enabled,omitempty"`
	StickyEscapeTTFTMs          *int     `json:"sticky_escape_ttft_ms,omitempty"`
	StickyEscapeErrorRate       *float64 `json:"sticky_escape_error_rate,omitempty"`
	LBTopK                      *int     `json:"lb_top_k,omitempty"`
	WeightPriority              *float64 `json:"weight_priority,omitempty"`
	WeightLoad                  *float64 `json:"weight_load,omitempty"`
	WeightQueue                 *float64 `json:"weight_queue,omitempty"`
	WeightErrorRate             *float64 `json:"weight_error_rate,omitempty"`
	WeightTTFT                  *float64 `json:"weight_ttft,omitempty"`
	WeightReset                 *float64 `json:"weight_reset,omitempty"`
	WeightQuotaHeadroom         *float64 `json:"weight_quota_headroom,omitempty"`
	WeightPreviousResponse      *float64 `json:"weight_previous_response,omitempty"`
	WeightSessionSticky         *float64 `json:"weight_session_sticky,omitempty"`
}
