package account

import "time"

// OllamaUsageFetchInput 仅用于受控的出站端口，不作为管理响应或普通日志字段。
type OllamaUsageFetchInput struct {
	AccountID   int64
	Concurrency int
	ProxyURL    string `json:"-"`
	Cookie      string `json:"-"`
	ObservedAt  time.Time
}

// OllamaUsageObservation 分离实际 HTTP 观测和账号的失败计数、重试与存储规则。
type OllamaUsageObservation struct {
	Data         *OllamaCloudUsageData
	HTTPStatus   int
	Failure      string
	RetryAfter   time.Duration
	Unauthorized bool
}
