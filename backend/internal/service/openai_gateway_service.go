package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/ip"
	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"github.com/BrandonVee/TokenRouter/internal/pkg/openai"
	"github.com/BrandonVee/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/BrandonVee/TokenRouter/internal/platform/liveattestation"
	"github.com/BrandonVee/TokenRouter/internal/util/responseheaders"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	// ChatGPT internal API for OAuth accounts
	chatgptCodexURL = "https://chatgpt.com/backend-api/codex/responses"
	// OpenAI Platform API for API Key accounts (fallback)
	openaiPlatformAPIURL            = "https://api.openai.com/v1/responses"
	openaiPlatformAPIInputTokensURL = "https://api.openai.com/v1/responses/input_tokens"
	openaiStickySessionTTL          = time.Hour // 粘性会话TTL
	// 与真实 Codex TUI 的 User-Agent 结构对齐：
	// {originator}/{version} ({OS} {OS_version}; {arch}) {terminal}
	// 缺少 OS/架构/终端后缀的形态易被上游指纹识别为非官方客户端。
	codexCLIUserAgent = openai.CodexDefaultOriginator + "/" + codexCLIVersion + " (Ubuntu 22.4.0; x86_64) xterm-256color"
	// codex_cli_only 拒绝时单个请求头日志长度上限（字符）
	codexCLIOnlyHeaderValueMaxBytes = 256

	// OpenAI WS Mode 失败后的重连次数上限（不含首次尝试）。
	// 与 Codex 客户端保持一致：失败后最多重连 5 次。
	openAIWSReconnectRetryLimit = 5
	// 上游错误体只需要提取错误 JSON/日志摘要，默认 512KiB 避免错误风暴叠加大请求体。
	openAIUpstreamErrorBodyReadLimit int64 = 512 << 10
	// OpenAI WS Mode 重连退避默认值（可由配置覆盖）。
	openAIWSRetryBackoffInitialDefault = 120 * time.Millisecond
	openAIWSRetryBackoffMaxDefault     = 2 * time.Second
	openAIWSRetryJitterRatioDefault    = 0.2
	openAICompactSessionSeedKey        = "openai_compact_session_seed"
	openAIUpstreamEndpointContextKey   = "openai_actual_upstream_endpoint"
	codexCLIVersion                    = "0.144.1"
	// Codex 限额快照仅用于后台展示/诊断，不需要每个成功请求都立即落库。
	openAICodexSnapshotPersistMinInterval = 30 * time.Second
	// 配额自动暂停时，超过该时长仍未刷新的 used% 快照视为陈旧，不再据此暂停账号。
	// 被暂停的账号收不到流量，其快照永远不会从上游响应头刷新；该兜底让账号在快照
	// 陈旧时放行一次请求，从而通过正常响应头自愈，而无需等待整个窗口（5h/7d）重置。
	openAICodexAutoPauseStaleAfter = 2 * time.Hour
)

// OpenAI allowed headers whitelist (for non-passthrough).
var openaiAllowedHeaders = map[string]bool{
	"accept-language":       true,
	"content-type":          true,
	"conversation_id":       true,
	"user-agent":            true,
	"originator":            true,
	"session_id":            true,
	"x-codex-beta-features": true,
	"x-codex-turn-state":    true,
	"x-codex-turn-metadata": true,
	responsesLiteHeaderKey:  true,
}

// OpenAI passthrough allowed headers whitelist.
// 透传模式下仅放行这些低风险请求头，避免将非标准/环境噪声头传给上游触发风控。
var openaiPassthroughAllowedHeaders = map[string]bool{
	"accept":                true,
	"accept-language":       true,
	"content-type":          true,
	"conversation_id":       true,
	"openai-beta":           true,
	"user-agent":            true,
	"originator":            true,
	"session_id":            true,
	"x-codex-beta-features": true,
	"x-codex-turn-state":    true,
	"x-codex-turn-metadata": true,
	responsesLiteHeaderKey:  true,
}

// codex_cli_only 拒绝时记录的请求头白名单（仅用于诊断日志，不参与上游透传）
var codexCLIOnlyDebugHeaderWhitelist = []string{
	"User-Agent",
	"Content-Type",
	"Accept",
	"Accept-Language",
	"OpenAI-Beta",
	"Originator",
	"Session_ID",
	"Conversation_ID",
	"X-Request-ID",
	"X-Client-Request-ID",
	"X-Forwarded-For",
	"X-Real-IP",
}

// OpenAICodexUsageSnapshot represents Codex API usage limits from response headers
type OpenAICodexUsageSnapshot struct {
	PrimaryUsedPercent          *float64 `json:"primary_used_percent,omitempty"`
	PrimaryResetAfterSeconds    *int     `json:"primary_reset_after_seconds,omitempty"`
	PrimaryWindowMinutes        *int     `json:"primary_window_minutes,omitempty"`
	SecondaryUsedPercent        *float64 `json:"secondary_used_percent,omitempty"`
	SecondaryResetAfterSeconds  *int     `json:"secondary_reset_after_seconds,omitempty"`
	SecondaryWindowMinutes      *int     `json:"secondary_window_minutes,omitempty"`
	PrimaryOverSecondaryPercent *float64 `json:"primary_over_secondary_percent,omitempty"`
	UpdatedAt                   string   `json:"updated_at,omitempty"`
}

// NormalizedCodexLimits contains normalized 5h/7d rate limit data
type NormalizedCodexLimits struct {
	Used5hPercent   *float64
	Reset5hSeconds  *int
	Window5hMinutes *int
	Used7dPercent   *float64
	Reset7dSeconds  *int
	Window7dMinutes *int
}

// Normalize converts primary/secondary fields to canonical 5h/7d fields.
// Strategy: Compare window_minutes to determine which is 5h vs 7d.
// Returns nil if snapshot is nil or has no useful data.
func (s *OpenAICodexUsageSnapshot) Normalize() *NormalizedCodexLimits {
	if s == nil {
		return nil
	}

	result := &NormalizedCodexLimits{}

	primaryMins := 0
	secondaryMins := 0
	hasPrimaryWindow := false
	hasSecondaryWindow := false

	if s.PrimaryWindowMinutes != nil {
		primaryMins = *s.PrimaryWindowMinutes
		hasPrimaryWindow = true
	}
	if s.SecondaryWindowMinutes != nil {
		secondaryMins = *s.SecondaryWindowMinutes
		hasSecondaryWindow = true
	}

	// Determine mapping based on window_minutes
	use5hFromPrimary := false
	use7dFromPrimary := false

	if hasPrimaryWindow && hasSecondaryWindow {
		// Both known: smaller window is 5h, larger is 7d
		if primaryMins < secondaryMins {
			use5hFromPrimary = true
		} else {
			use7dFromPrimary = true
		}
	} else if hasPrimaryWindow {
		// Only primary known: classify by threshold (<=360 min = 6h -> 5h window)
		if primaryMins <= 360 {
			use5hFromPrimary = true
		} else {
			use7dFromPrimary = true
		}
	} else if hasSecondaryWindow {
		// Only secondary known: classify by threshold
		if secondaryMins <= 360 {
			// 5h from secondary, so primary (if any data) is 7d
			use7dFromPrimary = true
		} else {
			// 7d from secondary, so primary (if any data) is 5h
			use5hFromPrimary = true
		}
	} else {
		// No window_minutes: fall back to legacy assumption (primary=7d, secondary=5h)
		use7dFromPrimary = true
	}

	// Assign values
	if use5hFromPrimary {
		result.Used5hPercent = s.PrimaryUsedPercent
		result.Reset5hSeconds = s.PrimaryResetAfterSeconds
		result.Window5hMinutes = s.PrimaryWindowMinutes
		result.Used7dPercent = s.SecondaryUsedPercent
		result.Reset7dSeconds = s.SecondaryResetAfterSeconds
		result.Window7dMinutes = s.SecondaryWindowMinutes
	} else if use7dFromPrimary {
		result.Used7dPercent = s.PrimaryUsedPercent
		result.Reset7dSeconds = s.PrimaryResetAfterSeconds
		result.Window7dMinutes = s.PrimaryWindowMinutes
		result.Used5hPercent = s.SecondaryUsedPercent
		result.Reset5hSeconds = s.SecondaryResetAfterSeconds
		result.Window5hMinutes = s.SecondaryWindowMinutes
	}

	return result
}

// OpenAIUsage represents OpenAI API response usage
type OpenAIUsage struct {
	InputTokens              int `json:"input_tokens"`
	ImageInputTokens         int `json:"image_input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	ImageOutputTokens        int `json:"image_output_tokens,omitempty"`
}

// OpenAIForwardResult represents the result of forwarding
type OpenAIForwardResult struct {
	RequestID  string
	ResponseID string
	Usage      OpenAIUsage
	Model      string // 原始模型（用于响应和日志显示）
	// BillingModel is the model used for cost calculation.
	// When non-empty, CalculateCost uses this instead of Model.
	// This is set by the Anthropic Messages conversion path where
	// the mapped upstream model differs from the client-facing model.
	BillingModel string
	// UpstreamModel is the actual model sent to the upstream provider after mapping.
	// Empty when no mapping was applied (requested model was used as-is).
	UpstreamModel string
	// UpstreamEndpoint 是该请求实际使用的上游 API 路径，避免同一下游协议可选择
	// 多个上游端点时只能依赖推断。
	UpstreamEndpoint string
	// ServiceTier records the OpenAI Responses API service tier, e.g. "priority" / "flex".
	// Nil means the request did not specify a recognized tier.
	ServiceTier *string
	// ReasoningEffort is extracted from request body (reasoning.effort) or derived from model suffix.
	// Stored for usage records display; nil means not provided / not applicable.
	ReasoningEffort *string
	Stream          bool
	OpenAIWSMode    bool
	// UpstreamTerminalEvent 记录 Responses WebSocket 请求观测到的规范化终止事件；
	// 空值保持旧调用方和非 WebSocket 请求的成功语义。
	UpstreamTerminalEvent string
	ResponseHeaders       http.Header
	ResponseBody          []byte // 成功响应体，用于数据共享提取 assistant 输出。
	DataShareSessionID    string // 数据共享聚合使用的稳定会话标识。
	Duration              time.Duration
	FirstTokenMs          *int
	ClientDisconnect      bool
	ImageCount            int
	ImageSize             string
	ImageInputSize        string
	ImageOutputSize       string
	ImageOutputSizes      []string
	ImageSizeSource       string
	ImageSizeBreakdown    map[string]int
	// UpstreamWarning 仅在上游成功完成传输但 terminal 事件携带风控拒绝时填充。
	UpstreamWarning *OpenAIUpstreamWarning
	VideoCount      int
	VideoResolution string
	// VideoDurationSeconds 是提交时请求的生成时长（xAI 按输出秒数计费），已归一化到 1-15 秒。
	VideoDurationSeconds int
	// WebSearchCalls 是 Codex alpha/search 网页搜索调用次数（每次成功请求为 1）。
	// 上游不返回 usage 字段，>0 时走按次计费（分组单价 × 次数 × 倍率）。
	WebSearchCalls int
	// SearchCount 是 Grok 原生 web_search 或工具搜索调用次数，按每千次计价。
	SearchCount int
	// AudioUsage 在有值时携带 Voice 计费单位。
	AudioUsage *AudioUsage

	wsReplayInput       []json.RawMessage
	wsReplayInputExists bool
}

// SucceededForScheduling 判断转发结果能否作为上游调度成功，并清除模型级短暂状态。
// 零值继续保持现有非 WebSocket 调用方的成功语义。
func (r *OpenAIForwardResult) SucceededForScheduling() bool {
	if r == nil || !r.OpenAIWSMode || r.UpstreamTerminalEvent == "" {
		return true
	}
	switch r.UpstreamTerminalEvent {
	case "response.completed", "response.done":
		return true
	default:
		return false
	}
}

// SetActualOpenAIUpstreamEndpoint 记录当前转发尝试选择的端点，供无法取得
// OpenAIForwardResult 的错误路径记录用量和运维日志。
func SetActualOpenAIUpstreamEndpoint(c *gin.Context, endpoint string) {
	if c == nil {
		return
	}
	if endpoint = strings.TrimSpace(endpoint); endpoint != "" {
		c.Set(openAIUpstreamEndpointContextKey, endpoint)
	}
}

// GetActualOpenAIUpstreamEndpoint 返回该请求最近一次转发尝试记录的端点。
func GetActualOpenAIUpstreamEndpoint(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, exists := c.Get(openAIUpstreamEndpointContextKey)
	if !exists {
		return ""
	}
	endpoint, _ := value.(string)
	return strings.TrimSpace(endpoint)
}

type OpenAIWSRetryMetricsSnapshot struct {
	RetryAttemptsTotal            int64 `json:"retry_attempts_total"`
	RetryBackoffMsTotal           int64 `json:"retry_backoff_ms_total"`
	RetryExhaustedTotal           int64 `json:"retry_exhausted_total"`
	NonRetryableFastFallbackTotal int64 `json:"non_retryable_fast_fallback_total"`
}

type OpenAICompatibilityFallbackMetricsSnapshot struct {
	SessionHashLegacyReadFallbackTotal int64   `json:"session_hash_legacy_read_fallback_total"`
	SessionHashLegacyReadFallbackHit   int64   `json:"session_hash_legacy_read_fallback_hit"`
	SessionHashLegacyDualWriteTotal    int64   `json:"session_hash_legacy_dual_write_total"`
	SessionHashLegacyReadHitRate       float64 `json:"session_hash_legacy_read_hit_rate"`

	MetadataLegacyFallbackIsMaxTokensOneHaikuTotal int64 `json:"metadata_legacy_fallback_is_max_tokens_one_haiku_total"`
	MetadataLegacyFallbackThinkingEnabledTotal     int64 `json:"metadata_legacy_fallback_thinking_enabled_total"`
	MetadataLegacyFallbackPrefetchedStickyAccount  int64 `json:"metadata_legacy_fallback_prefetched_sticky_account_total"`
	MetadataLegacyFallbackPrefetchedStickyGroup    int64 `json:"metadata_legacy_fallback_prefetched_sticky_group_total"`
	MetadataLegacyFallbackSingleAccountRetryTotal  int64 `json:"metadata_legacy_fallback_single_account_retry_total"`
	MetadataLegacyFallbackAccountSwitchCountTotal  int64 `json:"metadata_legacy_fallback_account_switch_count_total"`
	MetadataLegacyFallbackTotal                    int64 `json:"metadata_legacy_fallback_total"`
}

type openAIWSRetryMetrics struct {
	retryAttempts            atomic.Int64
	retryBackoffMs           atomic.Int64
	retryExhausted           atomic.Int64
	nonRetryableFastFallback atomic.Int64
}

type accountWriteThrottle struct {
	minInterval time.Duration
	mu          sync.Mutex
	lastByID    map[int64]time.Time
}

func newAccountWriteThrottle(minInterval time.Duration) *accountWriteThrottle {
	return &accountWriteThrottle{
		minInterval: minInterval,
		lastByID:    make(map[int64]time.Time),
	}
}

func (t *accountWriteThrottle) Allow(id int64, now time.Time) bool {
	if t == nil || id <= 0 || t.minInterval <= 0 {
		return true
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if last, ok := t.lastByID[id]; ok && now.Sub(last) < t.minInterval {
		return false
	}
	t.lastByID[id] = now

	if len(t.lastByID) > 4096 {
		cutoff := now.Add(-4 * t.minInterval)
		for accountID, writtenAt := range t.lastByID {
			if writtenAt.Before(cutoff) {
				delete(t.lastByID, accountID)
			}
		}
	}

	return true
}

var defaultOpenAICodexSnapshotPersistThrottle = newAccountWriteThrottle(openAICodexSnapshotPersistMinInterval)

// ErrNoAvailableCompactAccounts indicates the request needs /responses/compact
// support but no compatible account is available.
var ErrNoAvailableCompactAccounts = errors.New("no available accounts support /responses/compact")

// OpenAIGatewayService handles OpenAI API gateway operations
type OpenAIGatewayService struct {
	accountRepo           AccountRepository
	usageLogRepo          UsageLogRepository
	usageBillingRepo      UsageBillingRepository
	userRepo              UserRepository
	userSubRepo           UserSubscriptionRepository
	cache                 GatewayCache
	cfg                   *config.Config
	codexDetector         CodexClientRestrictionDetector
	schedulerSnapshot     *SchedulerSnapshotService
	concurrencyService    *ConcurrencyService
	billingService        *BillingService
	usageBillingNow       func() time.Time // 用量计费时钟，测试可注入固定时间以覆盖峰值倍率。
	rateLimitService      *RateLimitService
	billingCacheService   *BillingCacheService
	userGroupRateResolver *userGroupRateResolver
	httpUpstream          HTTPUpstream
	tlsFPProfileService   *TLSFingerprintProfileService
	tlsFPRouterService    *TLSFingerprintRouterService
	deferredService       *DeferredService
	openAITokenProvider   *OpenAITokenProvider
	grokTokenProvider     *GrokTokenProvider
	toolCorrector         *CodexToolCorrector
	openaiWSResolver      OpenAIWSProtocolResolver
	resolver              *ModelPricingResolver
	channelService        *ChannelService
	balanceNotifyService  *BalanceNotifyService
	settingService        *SettingService
	userPlatformQuotaRepo UserPlatformQuotaRepository
	dataSharingService    *DataSharingService
	liveAttestation       liveattestation.Provider
	liveAttestationCipher SecretEncryptor

	openaiWSPoolOnce               sync.Once
	openaiWSStateStoreOnce         sync.Once
	openaiSchedulerOnce            sync.Once
	openaiProxyStreamCircuitOnce   sync.Once
	openaiWSPassthroughDialerOnce  sync.Once
	openaiModelTransientOnce       sync.Once
	agentIdentityTaskMu            sync.Mutex
	openaiWSPool                   *openAIWSConnPool
	openaiWSStateStore             OpenAIWSStateStore
	openaiScheduler                OpenAIAccountScheduler
	openaiWSPassthroughDialer      openAIWSClientDialer
	openaiAccountStats             *openAIAccountRuntimeStats
	openaiModelTransient           *openAIAccountModelTransientState
	openaiProxyStreamCircuit       *openAIProxyStreamCircuit
	openaiProxyStreamFailOpenLogAt atomic.Int64

	openaiWSFallbackUntil               sync.Map // key: int64(accountID), value: time.Time
	openaiAccountRuntimeBlockUntil      sync.Map // key: int64(accountID), value: time.Time
	openaiAccountRuntimeBlockLocks      sync.Map // key: int64(accountID), value: *sync.Mutex
	openaiAccountRuntimeBlockGeneration sync.Map // key: int64(accountID), value: uint64
	openaiAccountRuntimeBlockSequence   atomic.Uint64
	grokCredentialMutationLocks         sync.Map // key: int64(accountID), value: *sync.Mutex
	openaiOAuth429WindowStartUnixNano   atomic.Int64
	openaiOAuth429WindowCount           atomic.Int64
	openaiWSRetryMetrics                openAIWSRetryMetrics
	responseHeaderFilter                *responseheaders.CompiledHeaderFilter
	codexSnapshotThrottle               *accountWriteThrottle
	openaiCompatSessionResponses        sync.Map
	openaiCompatAnthropicDigestSessions sync.Map
}

// NewOpenAIGatewayService creates a new OpenAIGatewayService
func NewOpenAIGatewayService(
	accountRepo AccountRepository,
	usageLogRepo UsageLogRepository,
	usageBillingRepo UsageBillingRepository,
	userRepo UserRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *SchedulerSnapshotService,
	concurrencyService *ConcurrencyService,
	billingService *BillingService,
	rateLimitService *RateLimitService,
	billingCacheService *BillingCacheService,
	httpUpstream HTTPUpstream,
	tlsFPProfileService *TLSFingerprintProfileService,
	deferredService *DeferredService,
	openAITokenProvider *OpenAITokenProvider,
	grokTokenProvider *GrokTokenProvider,
	resolver *ModelPricingResolver,
	channelService *ChannelService,
	balanceNotifyService *BalanceNotifyService,
	settingService *SettingService,
	userPlatformQuotaRepo UserPlatformQuotaRepository,
	dataSharingService *DataSharingService,
	tlsFPRouterServices ...*TLSFingerprintRouterService,
) *OpenAIGatewayService {
	var tlsFPRouterService *TLSFingerprintRouterService
	if len(tlsFPRouterServices) > 0 {
		tlsFPRouterService = tlsFPRouterServices[0]
	}
	svc := &OpenAIGatewayService{
		accountRepo:         accountRepo,
		usageLogRepo:        usageLogRepo,
		usageBillingRepo:    usageBillingRepo,
		userRepo:            userRepo,
		userSubRepo:         userSubRepo,
		cache:               cache,
		cfg:                 cfg,
		codexDetector:       NewOpenAICodexClientRestrictionDetector(cfg),
		schedulerSnapshot:   schedulerSnapshot,
		concurrencyService:  concurrencyService,
		billingService:      billingService,
		rateLimitService:    rateLimitService,
		billingCacheService: billingCacheService,
		userGroupRateResolver: newUserGroupRateResolver(
			userGroupRateRepo,
			nil,
			resolveUserGroupRateCacheTTL(cfg),
			nil,
			"service.openai_gateway",
		),
		httpUpstream:          httpUpstream,
		tlsFPProfileService:   tlsFPProfileService,
		tlsFPRouterService:    tlsFPRouterService,
		deferredService:       deferredService,
		openAITokenProvider:   openAITokenProvider,
		grokTokenProvider:     grokTokenProvider,
		toolCorrector:         NewCodexToolCorrector(),
		openaiWSResolver:      NewOpenAIWSProtocolResolver(cfg),
		resolver:              resolver,
		channelService:        channelService,
		balanceNotifyService:  balanceNotifyService,
		settingService:        settingService,
		userPlatformQuotaRepo: userPlatformQuotaRepo,
		dataSharingService:    dataSharingService,
		liveAttestation:       liveattestation.NewProvider(),
		liveAttestationCipher: newLiveAttestationCipher(cfg),
		responseHeaderFilter:  compileResponseHeaderFilter(cfg),
		codexSnapshotThrottle: newAccountWriteThrottle(openAICodexSnapshotPersistMinInterval),
		openaiModelTransient:  newOpenAIAccountModelTransientState(openAIModelTransientDefaultMax),
	}
	if rateLimitService != nil {
		rateLimitService.SetAccountRuntimeBlocker(svc)
	}
	if openAITokenProvider != nil {
		openAITokenProvider.SetAccountRuntimeBlocker(svc)
	}
	svc.logOpenAIWSModeBootstrap()
	return svc
}

// ResolveChannelMapping 解析渠道级模型映射（代理到 ChannelService）
func (s *OpenAIGatewayService) ResolveChannelMapping(ctx context.Context, groupID int64, model string) ChannelMappingResult {
	if s.channelService == nil {
		return ChannelMappingResult{MappedModel: model}
	}
	return s.channelService.ResolveChannelMapping(ctx, groupID, model)
}

// IsModelRestricted 检查模型是否被渠道限制（代理到 ChannelService）
func (s *OpenAIGatewayService) IsModelRestricted(ctx context.Context, groupID int64, model string) bool {
	if s.channelService == nil {
		return false
	}
	return s.channelService.IsModelRestricted(ctx, groupID, model)
}

// ResolveChannelMappingAndRestrict 解析渠道映射。
// 模型限制检查已移至调度阶段，restricted 始终返回 false。
func (s *OpenAIGatewayService) ResolveChannelMappingAndRestrict(ctx context.Context, groupID *int64, model string) (ChannelMappingResult, bool) {
	if s.channelService == nil {
		return (ChannelMappingResult{MappedModel: model}).WithAPIKeyModelRedirect(ctx, model), false
	}
	result, restricted := s.channelService.ResolveChannelMappingAndRestrict(ctx, groupID, model)
	return result.WithAPIKeyModelRedirect(ctx, model), restricted
}

func (s *OpenAIGatewayService) isCodexImageGenerationBridgeEnabled(ctx context.Context, account *Account, apiKey *APIKey) bool {
	if override := account.CodexImageGenerationBridgeOverride(); override != nil {
		return *override
	}
	if s != nil && s.channelService != nil && apiKey != nil && apiKey.GroupID != nil {
		ch, err := s.channelService.GetChannelForGroup(ctx, *apiKey.GroupID)
		if err != nil {
			slog.Warn("failed to resolve codex image generation bridge channel override", "group_id", *apiKey.GroupID, "error", err)
		} else if override := ch.CodexImageGenerationBridgeOverride(PlatformOpenAI); override != nil {
			return *override
		}
	}
	return s != nil && s.cfg != nil && s.cfg.Gateway.CodexImageGenerationBridgeEnabled
}

func (s *OpenAIGatewayService) checkChannelPricingRestriction(ctx context.Context, groupID *int64, requestedModel string) bool {
	if groupID == nil || s.channelService == nil || requestedModel == "" {
		return false
	}
	mapping := s.channelService.ResolveChannelMapping(ctx, *groupID, requestedModel)
	billingModel := billingModelForRestriction(mapping.BillingModelSource, requestedModel, mapping.MappedModel)
	if billingModel == "" {
		return false
	}
	return s.channelService.IsModelRestricted(ctx, *groupID, billingModel)
}

// resolveChannelRoutingModel 返回 OpenAI 账号调度层使用的渠道映射后模型。
func (s *OpenAIGatewayService) resolveChannelRoutingModel(ctx context.Context, groupID *int64, requestedModel string) string {
	if groupID == nil || s == nil || s.channelService == nil || strings.TrimSpace(requestedModel) == "" {
		return requestedModel
	}
	mapping := s.channelService.ResolveChannelMapping(ctx, *groupID, requestedModel)
	if mappedModel := strings.TrimSpace(mapping.MappedModel); mappedModel != "" {
		return mappedModel
	}
	return requestedModel
}

// ResolveOpenAIWSRoutingModelForAccount 为已选定的 WebSocket 账号逐轮解析并校验渠道模型。
// 长连接不能在后续 turn 重新调度账号，因此模型不再适配当前账号时直接拒绝该帧。
func (s *OpenAIGatewayService) ResolveOpenAIWSRoutingModelForAccount(
	ctx context.Context,
	groupID *int64,
	account *Account,
	requestedModel string,
	requiredCapability OpenAIEndpointCapability,
) (string, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return "", errors.New("websocket request model is empty")
	}
	if s.checkChannelPricingRestriction(ctx, groupID, requestedModel) {
		return "", fmt.Errorf("model %s is restricted by channel pricing", requestedModel)
	}

	routingModel := strings.TrimSpace(s.resolveChannelRoutingModel(ctx, groupID, requestedModel))
	if routingModel == "" {
		routingModel = requestedModel
	}
	if account == nil || !isOpenAICompatibleAccountEligibleForRequest(
		ctx,
		account,
		account.Platform,
		routingModel,
		false,
		requiredCapability,
	) {
		return "", fmt.Errorf("model %s is not supported by the selected websocket account", requestedModel)
	}
	if s.isOpenAIAccountRequestRuntimeBlocked(account, routingModel) {
		return "", fmt.Errorf("model %s is temporarily unavailable on the selected websocket account", requestedModel)
	}
	if groupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, groupID) &&
		s.isUpstreamRoutingModelRestrictedByChannel(ctx, *groupID, account, routingModel, false) {
		return "", fmt.Errorf("model %s is restricted after account mapping", requestedModel)
	}
	return routingModel, nil
}

func (s *OpenAIGatewayService) isUpstreamModelRestrictedByChannel(ctx context.Context, groupID int64, account *Account, requestedModel string, requireCompact bool) bool {
	if s.channelService == nil {
		return false
	}
	routingModel := s.resolveChannelRoutingModel(ctx, &groupID, requestedModel)
	return s.isUpstreamRoutingModelRestrictedByChannel(ctx, groupID, account, routingModel, requireCompact)
}

// isUpstreamRoutingModelRestrictedByChannel 使用已经完成渠道及分组映射的账号层模型检查最终上游模型。
func (s *OpenAIGatewayService) isUpstreamRoutingModelRestrictedByChannel(ctx context.Context, groupID int64, account *Account, routingModel string, requireCompact bool) bool {
	if s.channelService == nil {
		return false
	}
	upstreamModel := resolveOpenAIAccountUpstreamModelForRequest(
		account,
		routingModel,
		requireCompact,
		openAIHTTPPassthroughRoutingFromContext(ctx),
	)
	if upstreamModel == "" {
		return false
	}
	return s.channelService.IsModelRestricted(ctx, groupID, upstreamModel)
}

func (s *OpenAIGatewayService) needsUpstreamChannelRestrictionCheck(ctx context.Context, groupID *int64) bool {
	if groupID == nil || s.channelService == nil {
		return false
	}
	ch, err := s.channelService.GetChannelForGroup(ctx, *groupID)
	if err != nil {
		slog.Warn("failed to check openai channel upstream restriction", "group_id", *groupID, "error", err)
		return false
	}
	if ch == nil || !ch.RestrictModels {
		return false
	}
	return ch.BillingModelSource == BillingModelSourceUpstream
}

// ReplaceModelInBody 替换请求体中的 JSON model 字段（通用 gjson/sjson 实现）。
func (s *OpenAIGatewayService) ReplaceModelInBody(body []byte, newModel string) []byte {
	return ReplaceModelInBody(body, newModel)
}

func (s *OpenAIGatewayService) getCodexSnapshotThrottle() *accountWriteThrottle {
	if s != nil && s.codexSnapshotThrottle != nil {
		return s.codexSnapshotThrottle
	}
	return defaultOpenAICodexSnapshotPersistThrottle
}

func (s *OpenAIGatewayService) billingDeps() *billingDeps {
	return &billingDeps{
		accountRepo:           s.accountRepo,
		userRepo:              s.userRepo,
		userSubRepo:           s.userSubRepo,
		billingCacheService:   s.billingCacheService,
		deferredService:       s.deferredService,
		balanceNotifyService:  s.balanceNotifyService,
		userPlatformQuotaRepo: s.userPlatformQuotaRepo,
	}
}

// CloseOpenAIWSPool 关闭 OpenAI WebSocket 连接池的后台 worker 和空闲连接。
// 应在应用优雅关闭时调用。
func (s *OpenAIGatewayService) CloseOpenAIWSPool() {
	if s != nil && s.openaiWSPool != nil {
		s.openaiWSPool.Close()
	}
}

func (s *OpenAIGatewayService) InvalidateAgentIdentityWSConnections(accountID int64) {
	if pool := s.getOpenAIWSConnPool(); pool != nil {
		pool.ClearAccount(accountID)
	}
}

func (s *OpenAIGatewayService) logOpenAIWSModeBootstrap() {
	if s == nil || s.cfg == nil {
		return
	}
	wsCfg := s.cfg.Gateway.OpenAIWS
	logOpenAIWSModeInfo(
		"bootstrap enabled=%v oauth_enabled=%v apikey_enabled=%v force_http=%v responses_websockets_v2=%v responses_websockets=%v payload_log_sample_rate=%.3f event_flush_batch_size=%d event_flush_interval_ms=%d prewarm_cooldown_ms=%d retry_backoff_initial_ms=%d retry_backoff_max_ms=%d retry_jitter_ratio=%.3f retry_total_budget_ms=%d ws_read_limit_bytes=%d",
		wsCfg.Enabled,
		wsCfg.OAuthEnabled,
		wsCfg.APIKeyEnabled,
		wsCfg.ForceHTTP,
		wsCfg.ResponsesWebsocketsV2,
		wsCfg.ResponsesWebsockets,
		wsCfg.PayloadLogSampleRate,
		wsCfg.EventFlushBatchSize,
		wsCfg.EventFlushIntervalMS,
		wsCfg.PrewarmCooldownMS,
		wsCfg.RetryBackoffInitialMS,
		wsCfg.RetryBackoffMaxMS,
		wsCfg.RetryJitterRatio,
		wsCfg.RetryTotalBudgetMS,
		openAIWSMessageReadLimitBytes,
	)
}

func (s *OpenAIGatewayService) getCodexClientRestrictionDetector() CodexClientRestrictionDetector {
	if s != nil && s.codexDetector != nil {
		return s.codexDetector
	}
	var cfg *config.Config
	if s != nil {
		cfg = s.cfg
	}
	return NewOpenAICodexClientRestrictionDetector(cfg)
}

func (s *OpenAIGatewayService) getOpenAIWSProtocolResolver() OpenAIWSProtocolResolver {
	if s != nil && s.openaiWSResolver != nil {
		return s.openaiWSResolver
	}
	var cfg *config.Config
	if s != nil {
		cfg = s.cfg
	}
	return NewOpenAIWSProtocolResolver(cfg)
}

func classifyOpenAIWSReconnectReason(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var fallbackErr *openAIWSFallbackError
	if !errors.As(err, &fallbackErr) || fallbackErr == nil {
		return "", false
	}
	reason := strings.TrimSpace(fallbackErr.Reason)
	if reason == "" {
		return "", false
	}

	if warning, ok := ExtractOpenAIUpstreamWarning(err); ok && openAIUpstreamWarningIsCyber(warning) {
		// 已收到上游 terminal 风控拒绝，重试只会覆盖原始拒绝原因。
		return reason, false
	}

	baseReason := strings.TrimPrefix(reason, "prewarm_")

	switch baseReason {
	case "policy_violation",
		"message_too_big",
		"upgrade_required",
		"ws_unsupported",
		"auth_failed",
		"invalid_encrypted_content",
		"previous_response_not_found":
		return reason, false
	}

	switch baseReason {
	case "read_event",
		"write_request",
		"write",
		"acquire_timeout",
		"acquire_conn",
		"conn_queue_full",
		"dial_failed",
		"upstream_5xx",
		"event_error",
		"error_event",
		"upstream_error_event",
		"ws_connection_limit_reached",
		"missing_final_response":
		return reason, true
	default:
		return reason, false
	}
}

func resolveOpenAIWSFallbackErrorResponse(err error) (statusCode int, errType string, clientMessage string, upstreamMessage string, ok bool) {
	if err == nil {
		return 0, "", "", "", false
	}
	var policyErr *openAIWSGenericPolicyError
	if errors.As(err, &policyErr) && policyErr != nil {
		return http.StatusInternalServerError, "upstream_error", "Upstream gateway error", policyErr.Error(), true
	}
	var fallbackErr *openAIWSFallbackError
	if !errors.As(err, &fallbackErr) || fallbackErr == nil {
		return 0, "", "", "", false
	}

	reason := strings.TrimSpace(fallbackErr.Reason)
	reason = strings.TrimPrefix(reason, "prewarm_")
	if reason == "" {
		return 0, "", "", "", false
	}

	var dialErr *openAIWSDialError
	if fallbackErr.Err != nil && errors.As(fallbackErr.Err, &dialErr) && dialErr != nil {
		if dialErr.StatusCode > 0 {
			statusCode = dialErr.StatusCode
		}
		if dialErr.Err != nil {
			upstreamMessage = sanitizeUpstreamErrorMessage(strings.TrimSpace(dialErr.Err.Error()))
		}
	}

	switch reason {
	case "invalid_encrypted_content":
		if statusCode == 0 {
			statusCode = http.StatusBadRequest
		}
		errType = "invalid_request_error"
		if upstreamMessage == "" {
			upstreamMessage = "encrypted content could not be verified"
		}
	case "previous_response_not_found":
		if statusCode == 0 {
			statusCode = http.StatusBadRequest
		}
		errType = "invalid_request_error"
		if upstreamMessage == "" {
			upstreamMessage = "previous response not found"
		}
	case "upgrade_required":
		if statusCode == 0 {
			statusCode = http.StatusUpgradeRequired
		}
	case "ws_unsupported":
		if statusCode == 0 {
			statusCode = http.StatusBadRequest
		}
	case "auth_failed":
		if statusCode == 0 {
			statusCode = http.StatusUnauthorized
		}
	case "upstream_rate_limited":
		if statusCode == 0 {
			statusCode = http.StatusTooManyRequests
		}
	default:
		if statusCode == 0 {
			return 0, "", "", "", false
		}
	}

	if upstreamMessage == "" && fallbackErr.Err != nil {
		upstreamMessage = sanitizeUpstreamErrorMessage(strings.TrimSpace(fallbackErr.Err.Error()))
	}
	if upstreamMessage == "" {
		switch reason {
		case "upgrade_required":
			upstreamMessage = "upstream websocket upgrade required"
		case "ws_unsupported":
			upstreamMessage = "upstream websocket not supported"
		case "auth_failed":
			upstreamMessage = "upstream authentication failed"
		case "upstream_rate_limited":
			upstreamMessage = "upstream rate limit exceeded, please retry later"
		default:
			upstreamMessage = "Upstream request failed"
		}
	}

	if errType == "" {
		if statusCode == http.StatusTooManyRequests {
			errType = "rate_limit_error"
		} else {
			errType = "upstream_error"
		}
	}
	clientMessage = upstreamMessage
	return statusCode, errType, clientMessage, upstreamMessage, true
}

func (s *OpenAIGatewayService) writeOpenAIWSFallbackErrorResponse(c *gin.Context, account *Account, wsErr error) bool {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return false
	}
	statusCode, errType, clientMessage, upstreamMessage, ok := resolveOpenAIWSFallbackErrorResponse(wsErr)
	if !ok {
		return false
	}
	if strings.TrimSpace(clientMessage) == "" {
		clientMessage = "Upstream request failed"
	}
	if strings.TrimSpace(upstreamMessage) == "" {
		upstreamMessage = clientMessage
	}

	setOpsUpstreamError(c, statusCode, upstreamMessage, "")
	if account != nil {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: statusCode,
			Kind:               "ws_error",
			Message:            upstreamMessage,
		})
	}
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": clientMessage,
		},
	})
	return true
}

func (s *OpenAIGatewayService) openAIWSRetryBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	initial := openAIWSRetryBackoffInitialDefault
	maxBackoff := openAIWSRetryBackoffMaxDefault
	jitterRatio := openAIWSRetryJitterRatioDefault
	if s != nil && s.cfg != nil {
		wsCfg := s.cfg.Gateway.OpenAIWS
		if wsCfg.RetryBackoffInitialMS > 0 {
			initial = time.Duration(wsCfg.RetryBackoffInitialMS) * time.Millisecond
		}
		if wsCfg.RetryBackoffMaxMS > 0 {
			maxBackoff = time.Duration(wsCfg.RetryBackoffMaxMS) * time.Millisecond
		}
		if wsCfg.RetryJitterRatio >= 0 {
			jitterRatio = wsCfg.RetryJitterRatio
		}
	}
	if initial <= 0 {
		return 0
	}
	if maxBackoff <= 0 {
		maxBackoff = initial
	}
	if maxBackoff < initial {
		maxBackoff = initial
	}
	if jitterRatio < 0 {
		jitterRatio = 0
	}
	if jitterRatio > 1 {
		jitterRatio = 1
	}

	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	backoff := initial
	if shift > 0 {
		backoff = initial * time.Duration(1<<shift)
	}
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	if jitterRatio <= 0 {
		return backoff
	}
	jitter := time.Duration(float64(backoff) * jitterRatio)
	if jitter <= 0 {
		return backoff
	}
	delta := time.Duration(rand.Int63n(int64(jitter)*2+1)) - jitter
	withJitter := backoff + delta
	if withJitter < 0 {
		return 0
	}
	return withJitter
}

func (s *OpenAIGatewayService) openAIWSRetryTotalBudget() time.Duration {
	if s != nil && s.cfg != nil {
		ms := s.cfg.Gateway.OpenAIWS.RetryTotalBudgetMS
		if ms <= 0 {
			return 0
		}
		return time.Duration(ms) * time.Millisecond
	}
	return 0
}

func (s *OpenAIGatewayService) recordOpenAIWSRetryAttempt(backoff time.Duration) {
	if s == nil {
		return
	}
	s.openaiWSRetryMetrics.retryAttempts.Add(1)
	if backoff > 0 {
		s.openaiWSRetryMetrics.retryBackoffMs.Add(backoff.Milliseconds())
	}
}

func (s *OpenAIGatewayService) recordOpenAIWSRetryExhausted() {
	if s == nil {
		return
	}
	s.openaiWSRetryMetrics.retryExhausted.Add(1)
}

func (s *OpenAIGatewayService) recordOpenAIWSNonRetryableFastFallback() {
	if s == nil {
		return
	}
	s.openaiWSRetryMetrics.nonRetryableFastFallback.Add(1)
}

func (s *OpenAIGatewayService) SnapshotOpenAIWSRetryMetrics() OpenAIWSRetryMetricsSnapshot {
	if s == nil {
		return OpenAIWSRetryMetricsSnapshot{}
	}
	return OpenAIWSRetryMetricsSnapshot{
		RetryAttemptsTotal:            s.openaiWSRetryMetrics.retryAttempts.Load(),
		RetryBackoffMsTotal:           s.openaiWSRetryMetrics.retryBackoffMs.Load(),
		RetryExhaustedTotal:           s.openaiWSRetryMetrics.retryExhausted.Load(),
		NonRetryableFastFallbackTotal: s.openaiWSRetryMetrics.nonRetryableFastFallback.Load(),
	}
}

func SnapshotOpenAICompatibilityFallbackMetrics() OpenAICompatibilityFallbackMetricsSnapshot {
	legacyReadFallbackTotal, legacyReadFallbackHit, legacyDualWriteTotal := openAIStickyCompatStats()
	isMaxTokensOneHaiku, thinkingEnabled, prefetchedStickyAccount, prefetchedStickyGroup, singleAccountRetry, accountSwitchCount := RequestMetadataFallbackStats()

	readHitRate := float64(0)
	if legacyReadFallbackTotal > 0 {
		readHitRate = float64(legacyReadFallbackHit) / float64(legacyReadFallbackTotal)
	}
	metadataFallbackTotal := isMaxTokensOneHaiku + thinkingEnabled + prefetchedStickyAccount + prefetchedStickyGroup + singleAccountRetry + accountSwitchCount

	return OpenAICompatibilityFallbackMetricsSnapshot{
		SessionHashLegacyReadFallbackTotal: legacyReadFallbackTotal,
		SessionHashLegacyReadFallbackHit:   legacyReadFallbackHit,
		SessionHashLegacyDualWriteTotal:    legacyDualWriteTotal,
		SessionHashLegacyReadHitRate:       readHitRate,

		MetadataLegacyFallbackIsMaxTokensOneHaikuTotal: isMaxTokensOneHaiku,
		MetadataLegacyFallbackThinkingEnabledTotal:     thinkingEnabled,
		MetadataLegacyFallbackPrefetchedStickyAccount:  prefetchedStickyAccount,
		MetadataLegacyFallbackPrefetchedStickyGroup:    prefetchedStickyGroup,
		MetadataLegacyFallbackSingleAccountRetryTotal:  singleAccountRetry,
		MetadataLegacyFallbackAccountSwitchCountTotal:  accountSwitchCount,
		MetadataLegacyFallbackTotal:                    metadataFallbackTotal,
	}
}

func (s *OpenAIGatewayService) detectCodexClientRestriction(c *gin.Context, account *Account, tlsRouterMatch TLSFingerprintRouterMatchResult) CodexClientRestrictionDetectionResult {
	var globalAllowedClients []string
	if account != nil && account.IsCodexCLIOnlyEnabled() && s != nil && s.settingService != nil {
		ctx := context.Background()
		if c != nil && c.Request != nil {
			ctx = c.Request.Context()
		}
		if s.settingService.IsOpenAIAllowClaudeCodeCodexPluginEnabled(ctx) {
			globalAllowedClients = []string{openai.AllowedClientClaudeCode}
		}
	}
	return s.getCodexClientRestrictionDetector().Detect(c, account, globalAllowedClients, tlsRouterMatch)
}

func getAPIKeyIDFromContext(c *gin.Context) int64 {
	if c == nil {
		return 0
	}
	v, exists := c.Get("api_key")
	if !exists {
		return 0
	}
	apiKey, ok := v.(*APIKey)
	if !ok || apiKey == nil {
		return 0
	}
	return apiKey.ID
}

// isolateOpenAISessionID 将 apiKeyID 混入 session 标识符，
// 确保不同 API Key 的用户即使使用相同的原始 session_id/conversation_id，
// 到达上游的标识符也不同，防止跨用户会话碰撞。
func isolateOpenAISessionID(apiKeyID int64, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	h := xxhash.New()
	_, _ = fmt.Fprintf(h, "k%d:", apiKeyID)
	_, _ = h.WriteString(raw)
	return fmt.Sprintf("%016x", h.Sum64())
}

func logCodexCLIOnlyDetection(ctx context.Context, c *gin.Context, account *Account, apiKeyID int64, result CodexClientRestrictionDetectionResult, body []byte) {
	if !result.Enabled {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.Bool("codex_cli_only_enabled", result.Enabled),
		zap.Bool("codex_official_client_match", result.Matched),
		zap.String("reject_reason", result.Reason),
	}
	if apiKeyID > 0 {
		fields = append(fields, zap.Int64("api_key_id", apiKeyID))
	}
	if !result.Matched {
		fields = appendCodexCLIOnlyRejectedRequestFields(fields, c, body)
	}
	log := logger.FromContext(ctx).With(fields...)
	if result.Matched {
		log.Info("OpenAI codex_cli_only 放行请求")
		return
	}
	log.Warn("OpenAI codex_cli_only 拒绝非官方客户端请求")
}

func appendCodexCLIOnlyRejectedRequestFields(fields []zap.Field, c *gin.Context, body []byte) []zap.Field {
	if c == nil || c.Request == nil {
		return fields
	}

	req := c.Request
	requestModel, requestStream, promptCacheKey := extractOpenAIRequestMetaFromBody(body)
	fields = append(fields,
		zap.String("request_method", strings.TrimSpace(req.Method)),
		zap.String("request_path", strings.TrimSpace(req.URL.Path)),
		zap.String("request_query", strings.TrimSpace(req.URL.RawQuery)),
		zap.String("request_host", strings.TrimSpace(req.Host)),
		zap.String("request_client_ip", strings.TrimSpace(ip.GetClientIP(c))),
		zap.String("request_remote_addr", strings.TrimSpace(req.RemoteAddr)),
		zap.String("request_user_agent", strings.TrimSpace(req.Header.Get("User-Agent"))),
		zap.String("request_content_type", strings.TrimSpace(req.Header.Get("Content-Type"))),
		zap.Int64("request_content_length", req.ContentLength),
		zap.Bool("request_stream", requestStream),
	)
	if requestModel != "" {
		fields = append(fields, zap.String("request_model", requestModel))
	}
	if promptCacheKey != "" {
		fields = append(fields, zap.String("request_prompt_cache_key_sha256", hashSensitiveValueForLog(promptCacheKey)))
	}

	if headers := snapshotCodexCLIOnlyHeaders(req.Header); len(headers) > 0 {
		fields = append(fields, zap.Any("request_headers", headers))
	}
	fields = append(fields, zap.Int("request_body_size", len(body)))
	return fields
}

func snapshotCodexCLIOnlyHeaders(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	result := make(map[string]string, len(codexCLIOnlyDebugHeaderWhitelist))
	for _, key := range codexCLIOnlyDebugHeaderWhitelist {
		value := strings.TrimSpace(header.Get(key))
		if value == "" {
			continue
		}
		result[strings.ToLower(key)] = truncateString(value, codexCLIOnlyHeaderValueMaxBytes)
	}
	return result
}

func hashSensitiveValueForLog(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

// GetAccessToken gets the access token for an OpenAI account
func (s *OpenAIGatewayService) GetAccessToken(ctx context.Context, account *Account) (string, string, error) {
	if account.IsShadow() {
		credAccount, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return "", "", err
		}
		account = credAccount
	}
	switch account.Type {
	case AccountTypeOAuth:
		if account.IsOpenAIAgentIdentity() {
			return "", OpenAIAuthModeAgentIdentity, nil
		}
		if account.Platform == PlatformGrok {
			if s.grokTokenProvider != nil {
				accessToken, err := s.grokTokenProvider.GetAccessToken(ctx, account)
				if err != nil {
					return "", "", err
				}
				return accessToken, "oauth", nil
			}
			accessToken := account.GetGrokAccessToken()
			if accessToken == "" {
				return "", "", errors.New("access_token not found in credentials")
			}
			return accessToken, "oauth", nil
		}
		// 使用 TokenProvider 获取缓存的 token
		if s.openAITokenProvider != nil {
			accessToken, err := s.openAITokenProvider.GetAccessToken(ctx, account)
			if err != nil {
				return "", "", err
			}
			return accessToken, "oauth", nil
		}
		// 降级：TokenProvider 未配置时直接从账号读取
		accessToken := account.GetOpenAIAccessToken()
		if accessToken == "" {
			return "", "", errors.New("access_token not found in credentials")
		}
		return accessToken, "oauth", nil
	case AccountTypeAPIKey:
		if account.Platform == PlatformGrok {
			apiKey := strings.TrimSpace(account.GetCredential("api_key"))
			if apiKey == "" {
				return "", "", errors.New("api_key not found in credentials")
			}
			return apiKey, "apikey", nil
		}
		apiKey := account.GetOpenAIApiKey()
		if apiKey == "" {
			return "", "", errors.New("api_key not found in credentials")
		}
		return apiKey, "apikey", nil
	default:
		return "", "", fmt.Errorf("unsupported account type: %s", account.Type)
	}
}

// EnforceOpenAIClientPolicyForRequest 在非 /responses 主入口上复用 OpenAI OAuth 客户端访问策略。
func (s *OpenAIGatewayService) EnforceOpenAIClientPolicyForRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, tlsRouterMatch TLSFingerprintRouterMatchResult) error {
	result := s.detectCodexClientRestriction(c, account, tlsRouterMatch)
	apiKeyID := getAPIKeyIDFromContext(c)
	logCodexCLIOnlyDetection(ctx, c, account, apiKeyID, result, body)
	if !result.Enabled || result.Matched {
		return nil
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	if c != nil && GetOpenAIClientTransport(c) != OpenAIClientTransportWS {
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"type":    "forbidden_error",
				"message": openAIClientPolicyForbiddenMessage(result),
			},
		})
	}
	return errors.New("openai oauth client policy restriction: client is not allowed")
}

// MatchOpenAITLSFingerprintRouterForRequest 暴露给 OpenAI handler，用于在选中账号后统一执行
// UA 路由匹配，并把结果传入各转发分支。
func (s *OpenAIGatewayService) MatchOpenAITLSFingerprintRouterForRequest(c *gin.Context, account *Account) TLSFingerprintRouterMatchResult {
	return s.matchTLSFingerprintRouter(c, account)
}

// applyOpenAIUpstreamUserAgent 按路由规则、账号配置与全局兜底优先级设置上游 UA。
func (s *OpenAIGatewayService) applyOpenAIUpstreamUserAgent(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	req *http.Request,
	passthrough bool,
	routerMatch ...TLSFingerprintRouterMatchResult,
) {
	if req == nil {
		return
	}
	if len(routerMatch) > 0 && routerMatch[0].Matched {
		if ua := strings.TrimSpace(routerMatch[0].UpstreamUserAgent); ua != "" {
			req.Header.Set("user-agent", ua)
			return
		}
	}
	if account != nil {
		if customUA := strings.TrimSpace(account.GetOpenAIUserAgent()); customUA != "" {
			req.Header.Set("user-agent", customUA)
		}
	}
	if s != nil && s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		req.Header.Set("user-agent", codexCLIUserAgent)
		return
	}
	wasBrowserUA := account != nil && account.Type == AccountTypeOAuth && openai.IsBrowserUserAgent(req.Header.Get("user-agent"))
	s.overrideBrowserUserAgent(ctx, account, req)
	if passthrough && account != nil && account.Type == AccountTypeOAuth && !wasBrowserUA && !openai.IsCodexOfficialClientRequest(req.Header.Get("user-agent")) {
		// OAuth 安全透传：非浏览器、非官方 Codex UA 使用标准 Codex TUI 兜底。
		req.Header.Set("user-agent", codexCLIUserAgent)
	}
}

// applyOpenAIUpstreamUserAgentHeader 在 WebSocket 握手头上复用 HTTP 上游 UA 规则。
func (s *OpenAIGatewayService) applyOpenAIUpstreamUserAgentHeader(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	headers http.Header,
	passthrough bool,
	routerMatch ...TLSFingerprintRouterMatchResult,
) {
	if headers == nil {
		return
	}
	req := &http.Request{Header: headers}
	s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, passthrough, routerMatch...)
}

func (s *OpenAIGatewayService) matchTLSFingerprintRouter(c *gin.Context, account *Account) TLSFingerprintRouterMatchResult {
	if s == nil || s.tlsFPRouterService == nil || account == nil || account.GetTLSFingerprintRouterID() <= 0 {
		return TLSFingerprintRouterMatchResult{}
	}
	userAgent := ""
	if c != nil {
		userAgent = c.GetHeader("User-Agent")
	}
	return s.tlsFPRouterService.MatchUserAgent(account.GetTLSFingerprintRouterID(), userAgent)
}

func (s *OpenAIGatewayService) resolveOpenAITLSProfile(account *Account, routerMatch ...TLSFingerprintRouterMatchResult) *tlsfingerprint.Profile {
	if s == nil || s.tlsFPProfileService == nil {
		return nil
	}
	if len(routerMatch) > 0 && routerMatch[0].Matched {
		if profile, ok := s.tlsFPProfileService.ResolveRoutableTLSProfileByID(account, routerMatch[0].TLSFingerprintProfileID); ok {
			return profile
		}
	}
	// ResolveTLSProfile 内部会按账号类型兜底，OpenAI API Key 即使手写 extra 也不会生效。
	return s.tlsFPProfileService.ResolveTLSProfile(account)
}

func (s *OpenAIGatewayService) resolveOpenAIWSTLSProfile(account *Account, routerMatch ...TLSFingerprintRouterMatchResult) (*tlsfingerprint.Profile, string) {
	profile := s.resolveOpenAITLSProfile(account, routerMatch...)
	if profile == nil {
		return nil, ""
	}
	// Responses WebSocket 是 HTTP/1.1 Upgrade，连接池键也按剥离 h2 后的模板隔离。
	profile = tlsfingerprint.HTTP1OnlyProfile(profile)
	if len(routerMatch) > 0 && routerMatch[0].Matched {
		if routerMatch[0].TLSFingerprintProfileID == -1 {
			return profile, "tls-router-random"
		}
		return profile, "tls-router-" + strconv.FormatInt(routerMatch[0].RouterID, 10) + "-" + strconv.FormatInt(routerMatch[0].TLSFingerprintProfileID, 10)
	}
	if account != nil && account.GetTLSFingerprintProfileID() == -1 {
		// WS 连接需要在多轮 continuation 间保持同一连接可复用，随机模板使用稳定配置键隔离连接池。
		return profile, "tls-random"
	}
	return profile, tlsfingerprint.CacheKey(profile)
}

func (e *openAIUpstreamWarningError) Error() string {
	if e == nil || e.err == nil {
		return "openai upstream warning"
	}
	return e.err.Error()
}

func (e *openAIUpstreamWarningError) OpenAIUpstreamWarning() *OpenAIUpstreamWarning {
	if e == nil {
		return nil
	}
	return e.warning
}

func (e *openAIUpstreamWarningError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// ExtractOpenAICyberWarningMessage 提取可直接回传给下游客户端的 cyber 风控提示。
func ExtractOpenAICyberWarningMessage(responseBody []byte, warningText string) string {
	if hit, _, message := detectOpenAICyberPolicy(responseBody); hit && strings.TrimSpace(message) != "" {
		return truncateForLog([]byte(sanitizeUpstreamErrorMessage(message)), 2048)
	}
	for _, candidate := range []string{
		strings.TrimSpace(warningText),
		strings.TrimSpace(extractCyberWarningText(responseBody)),
	} {
		if IsOpenAICyberWarningText(candidate) {
			return truncateForLog([]byte(sanitizeUpstreamErrorMessage(candidate)), 2048)
		}
	}
	if fallback := strings.TrimSpace(warningText); fallback != "" {
		return truncateForLog([]byte(sanitizeUpstreamErrorMessage(fallback)), 2048)
	}
	if fallback := strings.TrimSpace(extractCyberWarningText(responseBody)); fallback != "" && !strings.HasPrefix(fallback, "{") {
		return truncateForLog([]byte(sanitizeUpstreamErrorMessage(fallback)), 2048)
	}
	return "OpenAI rejected this request because it may violate cyber safety policy."
}

// ExtractOpenAIUpstreamWarning 从转发错误链中提取上游 cyber 风控警告。
func ExtractOpenAIUpstreamWarning(err error) (*OpenAIUpstreamWarning, bool) {
	if err == nil {
		return nil, false
	}
	var carrier OpenAIUpstreamWarningCarrier
	if !errors.As(err, &carrier) || carrier == nil {
		return nil, false
	}
	warning := carrier.OpenAIUpstreamWarning()
	if warning == nil {
		return nil, false
	}
	return cloneOpenAIUpstreamWarning(warning), true
}

// IsOpenAICyberWarningPayload 判断上游响应体或错误文本是否属于 OpenAI cyber 风控拒绝。
func IsOpenAICyberWarningPayload(responseBody []byte, warningText string) bool {
	if IsOpenAICyberWarningText(warningText) {
		return true
	}
	if len(responseBody) == 0 {
		return false
	}
	if hit, _, _ := detectOpenAICyberPolicy(responseBody); hit {
		return true
	}
	return IsOpenAICyberWarningText(extractCyberWarningText(responseBody)) ||
		IsOpenAICyberWarningText(string(responseBody))
}

func cloneOpenAIUpstreamWarning(warning *OpenAIUpstreamWarning) *OpenAIUpstreamWarning {
	if warning == nil {
		return nil
	}
	cloned := *warning
	if warning.ResponseBody != nil {
		cloned.ResponseBody = append([]byte(nil), warning.ResponseBody...)
	}
	return &cloned
}

// hasOpenAIUltraReasoningSuffix 仅识别 OpenAI GPT 模型，避免误伤其它平台的 Ultra 命名。
func hasOpenAIUltraReasoningSuffix(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(lastOpenAIModelSegment(model)))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	return strings.HasPrefix(normalized, "gpt-") && strings.HasSuffix(normalized, "-ultra")
}

func normalizeOpenAIReasoningEffortForModel(raw string, model string) string {
	value := normalizeOpenAIReasoningEffort(raw)
	switch value {
	case "max":
		if !openAIModelSupportsReasoningEffort(model, value) {
			return ""
		}
	}
	return value
}

func openAIClientPolicyForbiddenMessage(result CodexClientRestrictionDetectionResult) string {
	// 按策略返回更明确的拒绝原因，同时保留旧 codex_cli_only 测试和客户端提示语义。
	if result.Policy == OpenAIOAuthClientPolicyCodexOnly {
		return "This account only allows Codex official clients"
	}
	if result.Policy == OpenAIOAuthClientPolicyTLSRouterMatchedOnly {
		return "This account only allows clients matched by the configured TLS router"
	}
	return "This account only allows configured OpenAI OAuth clients"
}

func openAIUpstreamWarningIsCyber(warning *OpenAIUpstreamWarning) bool {
	if warning == nil {
		return false
	}
	// 保持 WS 重试决策与 cyber 落库识别规则一致，避免重试覆盖可统计的上游风控拒绝。
	return IsOpenAICyberWarningPayload(warning.ResponseBody, warning.Message)
}

// validateOpenAIReasoningEffort 拒绝 Codex 客户端专用的 Ultra 模式。
// Ultra 在 Codex 内部表示 max 推理加主动多代理，不是 OpenAI 上游协议档位。
func validateOpenAIReasoningEffort(body []byte, requestedModel string) error {
	efforts := []string{
		gjson.GetBytes(body, "reasoning.effort").String(),
		gjson.GetBytes(body, "reasoning_effort").String(),
		gjson.GetBytes(body, "output_config.effort").String(),
		gjson.GetBytes(body, "response.reasoning.effort").String(),
		gjson.GetBytes(body, "response.reasoning_effort").String(),
		gjson.GetBytes(body, "session.reasoning.effort").String(),
		gjson.GetBytes(body, "session.reasoning_effort").String(),
	}
	for _, effort := range efforts {
		if strings.EqualFold(strings.TrimSpace(effort), "ultra") {
			return errors.New(`reasoning effort "ultra" is not supported; use "max"`)
		}
	}

	models := []string{
		requestedModel,
		gjson.GetBytes(body, "model").String(),
		gjson.GetBytes(body, "session.model").String(),
	}
	for _, model := range models {
		if hasOpenAIUltraReasoningSuffix(model) {
			return errors.New(`model reasoning suffix "ultra" is not supported; use "max"`)
		}
	}
	return nil
}

func wrapOpenAIUpstreamWarningIfCyber(statusCode int, responseBody []byte, message string, err error) error {
	if err == nil {
		return nil
	}
	warning := &OpenAIUpstreamWarning{
		StatusCode:   statusCode,
		ResponseBody: append([]byte(nil), responseBody...),
		Message:      strings.TrimSpace(message),
	}
	if !openAIUpstreamWarningIsCyber(warning) {
		return err
	}
	return &openAIUpstreamWarningError{warning: warning, err: err}
}

// OpenAIUpstreamWarning 表示上游返回的非计费风控警告事件。
type OpenAIUpstreamWarning struct {
	StatusCode   int
	ResponseBody []byte
	Message      string
}

// OpenAIUpstreamWarningCarrier 允许错误链携带可落库的上游风控 warning。
type OpenAIUpstreamWarningCarrier interface {
	OpenAIUpstreamWarning() *OpenAIUpstreamWarning
}

type openAIUpstreamWarningError struct {
	warning *OpenAIUpstreamWarning
	err     error
}
