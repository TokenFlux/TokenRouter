package service

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type RequestType = usage.RequestType

const BillingTypeBalance = usage.BillingTypeBalance
const BillingTypeSubscription = usage.BillingTypeSubscription
const RequestTypeUnknown = usage.RequestTypeUnknown
const RequestTypeSync = usage.RequestTypeSync
const RequestTypeStream = usage.RequestTypeStream
const RequestTypeWSV2 = usage.RequestTypeWSV2
const RequestTypeCyberBlocked = usage.RequestTypeCyberBlocked
const RequestTypeLive = usage.RequestTypeLive

var RequestTypeFromInt16 = usage.RequestTypeFromInt16
var ParseUsageRequestType = usage.ParseUsageRequestType
var RequestTypeFromLegacy = usage.RequestTypeFromLegacy
var ApplyLegacyRequestFields = usage.ApplyLegacyRequestFields

type UsageLog struct {
	ID            int64
	UserID        int64
	BillingUserID int64
	TeamID        *int64
	APIKeyID      int64
	AccountID     int64
	RequestID     string
	Model         string
	// RequestedModel is the client-requested model name recorded for stable user/admin display.
	// Empty should be treated as Model for backward compatibility with historical rows.
	RequestedModel string
	// UpstreamModel is the actual model sent to the upstream provider after mapping.
	// Nil means no mapping was applied (requested model was used as-is).
	UpstreamModel *string
	// ChannelID 渠道 ID
	ChannelID *int64
	// ModelMappingChain 模型映射链，如 "a→b→c"
	ModelMappingChain *string
	// BillingTier 计费层级标签（per_request/image 模式）
	BillingTier *string
	// BillingMode 计费模式：token/image
	BillingMode *string
	// ServiceTier 记录归一化后的计费层级；Claude Fast 同样使用 "priority"。
	ServiceTier *string
	// ReasoningEffort 是最终转发给上游的推理档位，可能已经经过分组策略或模型族归一化。
	// OpenAI: "low" / "medium" / "high" / "xhigh" / "max"；Claude: "low" / "medium" / "high" / "max"。
	// Nil 表示未提供或不适用。
	ReasoningEffort *string
	// RequestedReasoningEffort 是客户端在策略改写前请求的推理档位。
	// 历史记录可能为空，此时展示层回退到 ReasoningEffort。
	RequestedReasoningEffort *string
	// InboundEndpoint is the client-facing API endpoint path, e.g. /v1/chat/completions.
	InboundEndpoint *string
	// UpstreamEndpoint is the normalized upstream endpoint path, e.g. /v1/responses.
	UpstreamEndpoint *string

	GroupID        *int64
	SubscriptionID *int64

	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int

	CacheCreation5mTokens int `gorm:"column:cache_creation_5m_tokens"`
	CacheCreation1hTokens int `gorm:"column:cache_creation_1h_tokens"`

	ImageInputTokens  int
	ImageInputCost    float64
	ImageOutputTokens int
	ImageOutputCost   float64

	InputCost                 float64
	OutputCost                float64
	CacheCreationCost         float64
	CacheReadCost             float64
	TotalCost                 float64
	ActualCost                float64
	SubscriptionAmountUSD     float64
	BalanceAmountUSD          float64
	BillingAllocations        []domain.BillingAllocation
	RateMultiplier            float64
	LongContextBillingApplied bool // 长上下文规则是否实际增加费用
	// AccountRateMultiplier 账号计费倍率快照（nil 表示历史数据，按 1.0 处理）
	AccountRateMultiplier *float64
	// AccountStatsCost 账号统计定价预计算基数（nil 时回退 total_cost，之后再乘账号倍率）
	AccountStatsCost *float64

	BillingType  int8
	RequestType  RequestType
	Stream       bool
	OpenAIWSMode bool
	// NativeCompactionV2 表示运行时识别出的 OpenAI 原生远程 compaction v2 请求。
	NativeCompactionV2 bool
	DurationMs         *int
	FirstTokenMs       *int
	UserAgent          *string
	IPAddress          *string
	// SessionID 是客户端显式提供的请求关联标识，例如 session_id 或 X-Session-Id
	// 请求头；客户端未提供有效值时为 nil，且绝不从 prompt_cache_key 或内容派生。
	SessionID *string
	// UpstreamRequestID 是直接上游在响应头中声明的请求标识，只读账户
	// extra.upstream_request_id_header 指定的头；账户未指定头名、WS 轮次
	// 与上游没有该头的路径为 nil。
	UpstreamRequestID *string

	// Cache TTL Override 标记（管理员强制替换了缓存 TTL 计费）
	CacheTTLOverridden bool

	// 图片生成字段
	ImageCount         int
	ImageSize          *string
	ImageInputSize     *string
	ImageOutputSize    *string
	ImageSizeSource    *string
	ImageSizeBreakdown map[string]int
	MediaType          *string

	// 视频生成字段（Grok 视频按秒计费；video_count>0 的行不要求 image_size）
	VideoCount           int
	VideoResolution      *string
	VideoDurationSeconds *int

	CreatedAt time.Time

	User         *User
	APIKey       *APIKey
	Account      *Account
	Group        *Group
	Subscription *UserSubscription
}

// usageActorUserID 返回实际发起请求的用户；个人 Key 的调用者与付款人相同。
func usageActorUserID(apiKey *APIKey, billingUser *User) int64 {
	if apiKey != nil && apiKey.ActorUser != nil && apiKey.ActorUser.ID > 0 {
		return apiKey.ActorUser.ID
	}
	if apiKey != nil && apiKey.UserID > 0 {
		return apiKey.UserID
	}
	if billingUser != nil {
		return billingUser.ID
	}
	return 0
}

func (u *UsageLog) TotalTokens() int {
	return (&usage.UsageLog{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheCreationTokens: u.CacheCreationTokens, CacheReadTokens: u.CacheReadTokens}).TotalTokens()
}

func (u *UsageLog) EffectiveRequestType() RequestType {
	if u == nil {
		return (*usage.UsageLog)(nil).EffectiveRequestType()
	}
	return (&usage.UsageLog{RequestType: u.RequestType, Stream: u.Stream, OpenAIWSMode: u.OpenAIWSMode}).EffectiveRequestType()
}

func (u *UsageLog) SyncRequestTypeAndLegacyFields() {
	if u == nil {
		return
	}
	v := &usage.UsageLog{RequestType: u.RequestType, Stream: u.Stream, OpenAIWSMode: u.OpenAIWSMode}
	v.SyncRequestTypeAndLegacyFields()
	u.RequestType = v.RequestType
	u.Stream = v.Stream
	u.OpenAIWSMode = v.OpenAIWSMode
}
