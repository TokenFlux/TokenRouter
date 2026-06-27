package domain

// GroupAvailabilityProbeConfig 是分组主动可用性探测配置。
// 配置挂在 groups 表上，便于每个分组独立控制探测模型、提示词和频率。
type GroupAvailabilityProbeConfig struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"interval_minutes,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	UserAgent       string `json:"user_agent,omitempty"`
}
