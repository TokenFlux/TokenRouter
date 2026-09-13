package ops

import "time"

type OpsTokenStatsFilter struct {
	TimeRange string
	StartTime time.Time
	EndTime   time.Time

	Platform string
	GroupID  *int64

	// 分页模式（默认）：page/page_size。
	Page     int
	PageSize int

	// TopN 模式：top_n。
	TopN int
}

func (f *OpsTokenStatsFilter) IsTopNMode() bool {
	return f != nil && f.TopN > 0
}

type OpsTokenStatsItem struct {
	Model                  string   `json:"model"`
	RequestCount           int64    `json:"request_count"`
	AvgTokensPerSec        *float64 `json:"avg_tokens_per_sec"`
	AvgFirstTokenMs        *float64 `json:"avg_first_token_ms"`
	TotalOutputTokens      int64    `json:"total_output_tokens"`
	AvgDurationMs          int64    `json:"avg_duration_ms"`
	RequestsWithFirstToken int64    `json:"requests_with_first_token"`
}

type OpsTokenStatsResponse struct {
	TimeRange string    `json:"time_range"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`

	Platform string `json:"platform,omitempty"`
	GroupID  *int64 `json:"group_id,omitempty"`

	Items []*OpsTokenStatsItem `json:"items"`

	// 分页或 TopN 截断前的模型总数。
	Total int64 `json:"total"`

	// 分页模式元数据。
	Page     int `json:"page,omitempty"`
	PageSize int `json:"page_size,omitempty"`

	// TopN 模式元数据。
	TopN *int `json:"top_n,omitempty"`
}
