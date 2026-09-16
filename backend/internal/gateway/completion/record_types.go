package completion

import (
	"context"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// Result 仅保留完成处理需要的已观测结果，不携带响应体、HTTP Header 或平台执行器。
type Result struct {
	RequestID, ResponseID, Model, BillingModel, UpstreamModel                    string
	UpstreamRequestID                                                            *string
	Usage                                                                        TokenUsage
	ServiceTier, ReasoningEffort, RequestedReasoningEffort                       *string
	UpstreamResponseServiceTier                                                  string
	Stream, OpenAIWSMode                                                         bool
	Duration                                                                     time.Duration
	FirstTokenMs                                                                 *int
	ImageCount, VideoCount, VideoDurationSeconds, WebSearchCalls, SearchCount    int
	ImageSize, ImageInputSize, ImageOutputSize, ImageSizeSource, VideoResolution string
	ImageOutputSizes                                                             []string
	ImageSizeBreakdown                                                           map[string]int
	AudioUsage                                                                   *AudioUsage
}

// TokenUsage 在完成边界区分普通输入与供应商返回的总输入，具体桶转换由对应入口执行。
type TokenUsage struct {
	InputTokens, OutputTokens, CacheCreationInputTokens, CacheReadInputTokens         int
	CacheCreation5mTokens, CacheCreation1hTokens, ImageInputTokens, ImageOutputTokens int
	Speed                                                                             string
}
type AudioUsage struct {
	Mode            string
	DurationOrUnits float64
}

// PayerSnapshot 不含身份凭据，资金主体与行为主体分别显式提供。
type PayerSnapshot struct {
	ID           int64
	Balance      float64
	Notification *billing.UserSummary
}

// AccountSnapshot 不携带凭据或健康可变字段。
type AccountSnapshot struct {
	CacheTTLOverrideEnabled                                     bool
	CacheTTLOverrideTarget                                      string
	AnthropicOAuthOrSetupToken                                  bool
	Platform                                                    string
	ID                                                          int64
	Type                                                        string
	RateMultiplier                                              float64
	OpenAI, CNProvider, OAuthLike, QuotaEligible, HasQuotaLimit bool
	// CredentialAccountID 仅供账户端口按原时机读取影子母账号。
	CredentialAccountID *int64
	Notification        *billing.QuotaNotifyAccount
}

// KeySnapshot 冻结请求取得的资金绑定；行为用户和付款用户不能互相替代。
type KeySnapshot struct {
	ActorUserPresent                         bool
	ID                                       int64
	Key                                      string
	ActorUserID                              int64
	GroupID, TeamID, PreferredSubscriptionID *int64
	BillingMode                              string
	Quota                                    float64
	HasRateLimits                            bool
	Group                                    *GroupSnapshot
}

// GroupSnapshot 只含完成计费字段，不接收路由/调度实体。
type GroupSnapshot struct {
	ID                                      int64
	Platform                                string
	Price                                   *billing.PriceGroup
	RateMultiplier                          float64
	PeakRateEnabled                         bool
	PeakStart, PeakEnd                      string
	PeakRateMultiplier                      float64
	Location                                *time.Location
	FreeOpenAIFast, SupportsOpenAIFast      bool
	WebSearchPricePerCall, SearchPricePer1k *float64
	AudioPrice                              *pricing.AudioPriceConfig
}

func (g *GroupSnapshot) PeakMultiplierAt(at time.Time) float64 {
	if g.Location != nil {
		at = at.In(g.Location)
	}
	return (&routing.Group{PeakRateEnabled: g.PeakRateEnabled, PeakStart: g.PeakStart, PeakEnd: g.PeakEnd, PeakRateMultiplier: g.PeakRateMultiplier}).PeakMultiplierAt(at)
}

// Input 是异步完成快照，调用方通过 Snapshot 后提交队列；不保留原请求体。
type Input struct {
	Result                                                                   *Result
	APIKey                                                                   *KeySnapshot
	User                                                                     *PayerSnapshot
	Account                                                                  *AccountSnapshot
	Subscription                                                             *billing.UserSubscription
	InboundEndpoint, UpstreamEndpoint, UserAgent, IPAddress, ClientSessionID string
	RequestID, RequestPayloadHash, QuotaPlatform, CacheOverrideTarget        string
	RequestedReasoningEffort                                                 *string
	ForceCacheBilling, QuotaUpdates, CyberBlocked, NativeCompactionV2        bool
	PricingAt                                                                time.Time
	ChannelUsageFields
}

type ChannelUsageFields = routing.ChannelUsageFields
type UsageLog = usage.UsageLog
type UsageTokens = pricing.UsageTokens
type CostBreakdown = pricing.CostBreakdown
type ResolvedPricing = pricing.ResolvedPricing
type CostInput = billing.CostInput
type PricingInput = billing.PricingInput

const (
	BillingModeToken                = pricing.BillingModeToken
	BillingModeImage                = pricing.BillingModeImage
	BillingModeVideo                = pricing.BillingModeVideo
	BillingModePerRequest           = pricing.BillingModePerRequest
	BillingTypeBalance              = usage.BillingTypeBalance
	BillingTypeSubscription         = usage.BillingTypeSubscription
	RequestTypeCyberBlocked         = usage.RequestTypeCyberBlocked
	BillingModelSourceRequested     = routing.BillingModelSourceRequested
	BillingModelSourceUpstream      = routing.BillingModelSourceUpstream
	BillingModelSourceChannelMapped = routing.BillingModelSourceChannelMapped
)

var ErrModelPricingUnavailable = pricing.ErrModelPricingUnavailable

type PricingOptions struct{ PricingAt time.Time }

// Store 保持普通结算闭合事务，不把环境中的外层事务隐式传给资金参与者。
type Store interface {
	Apply(context.Context, *billing.UsageBillingCommand) (*billing.UsageBillingApplyResult, error)
}
type SubscriptionReader interface {
	ResolveUsableSubscriptionForGroup(context.Context, int64, int64) (*billing.UserSubscription, error)
}
type RateReader interface {
	Resolve(context.Context, int64, int64, float64) float64
}
type AccountReader interface {
	CredentialAccount(context.Context, AccountSnapshot) (*AccountSnapshot, error)
}
type HealthObserver interface{ ResetOpenAI403Counter(context.Context, int64) }
type ModelCandidates interface {
	Candidates(string, ...string) []string
}
type LogWriter interface {
	Create(context.Context, *usage.UsageLog) (bool, error)
}
type BestEffortLogWriter interface {
	CreateBestEffort(context.Context, *usage.UsageLog) error
}
type AccountStats interface {
	ResolveAccountStats(context.Context, billing.AccountStatsCostInput) *float64
}

// Effects 只消费已提交资金结果，具体缓存及通知由原领域能力持有。
type Effects interface {
	AccountUsed(int64)
	InvalidateAuth(context.Context, string)
	Settled(SettlementInput, *billing.UsageBillingApplyResult)
}

// Dependencies 只绑定窄读取/写入端口及 billing 唯一计算器，不包含旧网关对象。
type CacheInjectionPolicy interface{ IsAnthropicCacheTTL1hInjectionEnabled(context.Context) bool }

// BillingEvent 把原日志字段交给技术适配器，核心不持有 logger 或可变字段容器。
type BillingEvent struct {
	Kind, Component, RequestID, RequestedModel, MappedModel, UpstreamModel, Model, Platform string
	RequestedTier, ObservedTier, BilledTier                                                 string
	Models                                                                                  []string
	KeyID, AccountID                                                                        int64
	GroupID                                                                                 *int64
	SearchCount                                                                             int
	Err                                                                                     error
}
type Dependencies struct {
	Emit           func(BillingEvent)
	CacheInjection CacheInjectionPolicy
	Calculator     *billing.Calculator
	Prices         *billing.PriceResolver
	AccountStats   AccountStats
	Funds          Store
	Subscriptions  SubscriptionReader
	Rates          RateReader
	Accounts       AccountReader
	Health         HealthObserver
	Models         ModelCandidates
	Logs           LogWriter
	Effects        Effects
	Observe        func(string, string)
}
type RecorderOptions struct {
	Simple            bool
	DefaultMultiplier float64
	Now               func() time.Time
}
type Recorder struct {
	emit              func(BillingEvent)
	cacheInjection    CacheInjectionPolicy
	billingService    *billing.Calculator
	resolver          *billing.PriceResolver
	stats             AccountStats
	store             Store
	subscriptions     SubscriptionReader
	rates             RateReader
	accounts          AccountReader
	health            HealthObserver
	models            ModelCandidates
	logs              LogWriter
	effects           Effects
	observe           func(string, string)
	simple            bool
	defaultMultiplier float64
	now               func() time.Time
}

// NewRecorder 不创建缓存、队列或后台任务，所有状态沿用装配传入的唯一实例。
func NewRecorder(d Dependencies, o RecorderOptions) *Recorder {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Recorder{
		emit:              d.Emit,
		cacheInjection:    d.CacheInjection,
		billingService:    d.Calculator,
		resolver:          d.Prices,
		stats:             d.AccountStats,
		store:             d.Funds,
		subscriptions:     d.Subscriptions,
		rates:             d.Rates,
		accounts:          d.Accounts,
		health:            d.Health,
		models:            d.Models,
		logs:              d.Logs,
		effects:           d.Effects,
		observe:           d.Observe,
		simple:            o.Simple,
		defaultMultiplier: o.DefaultMultiplier,
		now:               o.Now,
	}
}

// Record 是普通完成入口；openAI 标记原有总输入桶和媒体计价分支，不改变部分结果的提交资格。
func (s *Recorder) Record(ctx context.Context, input *Input, openAI bool) error {
	input = Snapshot(input)
	if openAI {
		return s.RecordOpenAI(ctx, input)
	}
	return s.RecordAnthropic(ctx, input, &PricingOptions{})
}
func (s *Recorder) printf(component, format string, args ...any) {
	if s.observe != nil {
		s.observe(component, fmt.Sprintf(format, args...))
	}
}
