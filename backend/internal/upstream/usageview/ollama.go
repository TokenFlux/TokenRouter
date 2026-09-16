// Ollama 的脱敏数据与 HTTP 观测是只读契约，缓存和浏览器会话仍归账号。
package usageview

import "time"

// OllamaCloudUsageWindow 是单个官方用量窗口的最小化脱敏视图。
type OllamaCloudUsageWindow struct {
	UsedPercent float64    `json:"used_percent"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
	ResetText   string     `json:"reset_text,omitempty"`
}

// OllamaCloudUsageModelWindow 标识模型请求数所属的官方用量窗口。
type OllamaCloudUsageModelWindow string

const (
	OllamaCloudUsageModelWindowFiveHour OllamaCloudUsageModelWindow = "five_hour"
	OllamaCloudUsageModelWindowSevenDay OllamaCloudUsageModelWindow = "seven_day"
)

// OllamaCloudUsageModel 保存 Ollama 用量页面按窗口展示的模型及请求数。
type OllamaCloudUsageModel struct {
	Model    string                      `json:"model"`
	Window   OllamaCloudUsageModelWindow `json:"window"`
	Requests int64                       `json:"requests"`
}

// OllamaCloudUsageData 明确排除原始 HTML 和浏览器会话数据。
type OllamaCloudUsageData struct {
	Plan     string                  `json:"plan,omitempty"`
	FiveHour *OllamaCloudUsageWindow `json:"five_hour,omitempty"`
	SevenDay *OllamaCloudUsageWindow `json:"seven_day,omitempty"`
	Balance  string                  `json:"balance,omitempty"`
	Models   []OllamaCloudUsageModel `json:"models,omitempty"`
}

// OllamaUsageObservation 分离实际 HTTP 观测和账号的失败计数、重试与存储规则。
type OllamaUsageObservation struct {
	Data         *OllamaCloudUsageData
	HTTPStatus   int
	Failure      string
	RetryAfter   time.Duration
	Unauthorized bool
}
