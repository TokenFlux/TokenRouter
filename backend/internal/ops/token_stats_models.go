package ops

import "time"

// token_stats_models.go defines filters and response values for model-grouped
// token statistics.
// Model token statistics group saved usage observations by their execution
// model and rank them by request count. TPS measures average output speed from
// attempt start, while TTFT uses the event selected in each usage snapshot. These
// metrics have separate sample sets; pagination and leading-model limits select
// different response metadata.

// OpsTokenStatsFilter selects model-grouped usage statistics and one
// result-limiting mode.
type OpsTokenStatsFilter struct {
	TimeRange string // response time-range display label
	// StartTime and EndTime select a half-open usage window independently of the
	// TimeRange display label. The service requires nonzero, ordered times and
	// accepts an equal start and end.
	StartTime time.Time // inclusive usage observation start
	EndTime   time.Time // exclusive usage observation end

	Platform string // recorded execution platform filter
	// GroupID selects a positive recorded group identity. GetTokenStats rejects a
	// supplied nonpositive value before querying; nil leaves the group dimension
	// unrestricted.
	GroupID *int64

	// Page and PageSize select pagination when TopN is nonpositive, defaulting to
	// page one and twenty models in GetTokenStats. Positive TopN selects one to
	// one hundred leading models and conflicts with supplied positive pagination
	// values; paginated page size is also limited to one hundred.
	Page     int // one-based requested model page
	PageSize int // requested models per page

	TopN int // requested leading model count
}

// OpsTokenStatsItem carries observation counts and token/latency measurements
// for one saved model.
type OpsTokenStatsItem struct {
	Model        string `json:"model"`         // saved usage model dimension
	RequestCount int64  `json:"request_count"` // model usage observation count
	// AvgTokensPerSec averages eligible per-usage output speeds from attempt start
	// to last useful upstream content. The query rounds the mean to two decimals
	// and returns nil when no complete stream has a valid observed interval.
	AvgTokensPerSec *float64 `json:"avg_tokens_per_sec"`
	// AvgTTFTMs is the mean selected upstream latency in milliseconds, rounded
	// to two decimals. Nil represents the absence of TTFT latency samples
	// for this model.
	AvgTTFTMs         *float64 `json:"avg_ttft_ms"`
	TotalOutputTokens int64    `json:"total_output_tokens"` // summed model output tokens
	// AvgDurationMs is the rounded mean of nonnull recorded durations in
	// milliseconds. Zero is returned when the model has no duration samples;
	// recorded zero durations contribute to the mean.
	AvgDurationMs int64 `json:"avg_duration_ms"`
	// nonnull TTFT latency samples
	RequestsWithFirstToken int64 `json:"requests_with_first_token"`
}

// OpsTokenStatsResponse carries a model statistics page or leading-model
// selection with its total.
type OpsTokenStatsResponse struct {
	TimeRange string    `json:"time_range"` // response time-range display label
	StartTime time.Time `json:"start_time"` // inclusive usage observation start
	EndTime   time.Time `json:"end_time"`   // exclusive usage observation end

	Platform string `json:"platform,omitempty"` // selected recorded execution platform
	GroupID  *int64 `json:"group_id,omitempty"` // optional selected group identity

	Items []*OpsTokenStatsItem `json:"items"` // ordered model statistics entries

	Total int64 `json:"total"` // model count before limiting

	// Page and PageSize contain pagination metadata when TopN is absent. TopN
	// identifies the leading-model mode, whose pagination fields remain zero and
	// are omitted from JSON.
	Page     int `json:"page,omitempty"`      // one-based returned model page
	PageSize int `json:"page_size,omitempty"` // returned models per page

	TopN *int `json:"top_n,omitempty"` // optional requested model limit
}

// IsTopNMode reports whether a positive TopN selects the leading-model result
// limit. Nil filters return false, and storage callers use that result to
// select limit-only or limit-and-offset SQL.
func (f *OpsTokenStatsFilter) IsTopNMode() bool {
	return f != nil && f.TopN > 0
}
