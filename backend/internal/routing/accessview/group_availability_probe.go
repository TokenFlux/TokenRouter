// 本文件维护 accessview 的所属能力；兼容入口复用唯一实现。
package accessview

// GroupAvailabilityProbeConfig 是分组主动可用性探测配置。
// 配置挂在 groups 表上，便于每个分组独立控制探测模型、提示词和频率。
type GroupAvailabilityProbeConfig struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"interval_minutes,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	// MaxRetries 表示首次探测失败后允许重试的最大次数；nil 使用服务端默认值。
	MaxRetries *int   `json:"max_retries,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
}
