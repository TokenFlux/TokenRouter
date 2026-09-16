// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	time "time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

const (
	OllamaCloudUsageStatusOK           = "ok"
	OllamaCloudUsageStatusUnauthorized = "unauthorized"
	OllamaCloudUsageStatusFailed       = "failed"
)

// OllamaCloudUsageSettings 控制可选的请求驱动刷新任务。
//
// IntervalMinutes 是最大等待上限：模型请求持续到达并不断推迟尾随防抖时，
// 超过该时长会强制刷新。DebounceMinutes 是分组最近一次请求后的静默期。
type OllamaCloudUsageSettings struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"` // 请求持续到达时的最大等待时间
	DebounceMinutes int  `json:"debounce_minutes"` // 最近一次请求后的尾随静默期
}

type OllamaCloudUsageWindow = usageview.OllamaCloudUsageWindow

type OllamaCloudUsageModelWindow = usageview.OllamaCloudUsageModelWindow

const OllamaCloudUsageModelWindowFiveHour = usageview.OllamaCloudUsageModelWindowFiveHour
const OllamaCloudUsageModelWindowSevenDay = usageview.OllamaCloudUsageModelWindowSevenDay

type OllamaCloudUsageModel = usageview.OllamaCloudUsageModel

type OllamaCloudUsageData = usageview.OllamaCloudUsageData

// OllamaCloudUsageSnapshot 是账号 extra 中唯一持久化的用量观测数据。
//
// NextRefreshAt 作为兼容字段继续持久化。状态为 ok 时，它只标记最大等待边界；
// 成功后的自动刷新由模型请求活动（分组 last_used_at + 防抖/最大等待）驱动，
// 不会仅由该字段触发。状态为 failed/unauthorized 时，它表示失败后的最早重试时间
// （Retry-After 或指数退避），实际到期时间取 activityDue 与 NextRefreshAt 的较晚者。
type OllamaCloudUsageSnapshot struct {
	Status        string                `json:"status"`
	Data          *OllamaCloudUsageData `json:"data,omitempty"`
	FetchedAt     *time.Time            `json:"fetched_at,omitempty"`
	LastAttemptAt time.Time             `json:"last_attempt_at"`
	NextRefreshAt time.Time             `json:"next_refresh_at"`
	FailureCount  int                   `json:"failure_count,omitempty"`
	HTTPStatus    int                   `json:"http_status,omitempty"`
	LastError     string                `json:"last_error,omitempty"`
}

// OllamaCloudUsageState 是向管理员暴露的专用 DTO。
type OllamaCloudUsageState struct {
	AccountID               int64                     `json:"account_id"`
	Eligible                bool                      `json:"eligible"`
	Configured              bool                      `json:"configured"`
	AutoRefreshEnabled      bool                      `json:"auto_refresh_enabled"`
	EncryptionKeyConfigured bool                      `json:"encryption_key_configured"`
	Snapshot                *OllamaCloudUsageSnapshot `json:"snapshot,omitempty"`
}

const OllamaCloudUsageSessionExtraKey = "ollama_cloud_usage_session"
const OllamaCloudUsageAutoRefreshExtraKey = "ollama_cloud_usage_auto_refresh"
const OllamaCloudUsageMinFetchInterval = 15 * time.Minute

var (
	ErrOllamaCloudUsageUnavailable = infraerrors.ServiceUnavailable(
		"OLLAMA_CLOUD_USAGE_UNAVAILABLE", "Ollama Cloud usage is unavailable",
	)
	ErrOllamaCloudUsageAccountInvalid = infraerrors.BadRequest(
		"OLLAMA_CLOUD_USAGE_ACCOUNT_INVALID", "account must be an OpenAI or Anthropic API key account using https://ollama.com",
	)
	ErrOllamaCloudUsageSessionRequired = infraerrors.BadRequest(
		"OLLAMA_CLOUD_USAGE_SESSION_REQUIRED", "an Ollama web session must be configured first",
	)
	ErrOllamaCloudUsageEncryptionKey = infraerrors.BadRequest(
		"OLLAMA_CLOUD_USAGE_ENCRYPTION_KEY_NOT_CONFIGURED", "cannot store an Ollama web session without a fixed TOTP_ENCRYPTION_KEY",
	)
	ErrOllamaCloudUsageIdentityChanged = infraerrors.Conflict(
		"OLLAMA_CLOUD_USAGE_IDENTITY_CHANGED", "account identity or Ollama web session changed during refresh; retry",
	)
	ErrOllamaCloudUsageRefreshRateLimited = infraerrors.TooManyRequests(
		"OLLAMA_CLOUD_USAGE_REFRESH_RATE_LIMITED", "Ollama Cloud usage can be refreshed manually once every 30 seconds",
	)
)
