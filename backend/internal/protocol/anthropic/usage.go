// Anthropic OAuth usage 报文，仅表达上游 wire 字段。
package anthropic

// ClaudeUsageWindow Anthropic /api/oauth/usage 返回的单个用量窗口
type ClaudeUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

// ClaudeUsageResponse Anthropic API返回的usage结构
type ClaudeUsageResponse struct {
	FiveHour struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"five_hour"`
	SevenDay struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"seven_day"`
	SevenDaySonnet struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"seven_day_sonnet"`
	// Fable 专属 7d 窗口（对应响应头 7d_oi，claim 名为 seven_day_overage_included，
	// 见 anthropic-ratelimit-unified-representative-claim 头）。上游 usage API
	// 若不下发该字段，GetUsage 会用被动采样数据回填。
	SevenDayOverageIncluded ClaudeUsageWindow `json:"seven_day_overage_included"`
}
