package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai/liveattestation"

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

	// Codex 限额快照仅用于后台展示/诊断，不需要每个成功请求都立即落库。
	openAICodexSnapshotPersistMinInterval = 30 * time.Second
	// 配额自动暂停时，超过该时长仍未刷新的 used% 快照视为陈旧，不再据此暂停账号。
	// 被暂停的账号收不到流量，其快照永远不会从上游响应头刷新；该兜底让账号在快照
	// 陈旧时放行一次请求，从而通过正常响应头自愈，而无需等待整个窗口（5h/7d）重置。
)

// OpenAI allowed headers whitelist (for non-passthrough).
var openaiAllowedHeaders = map[string]bool{
	"accept-language": true,
	"content-type":    true,
	"conversation_id": true,
	"user-agent":      true,
	"originator":      true,
	"session_id":      true,
	// Codex 设备/会话标识参与账号 namespace 隔离，必须在进入请求构造器时保留。
	"installation_id":            true,
	"x-codex-installation-id":    true,
	"session-id":                 true,
	"thread_id":                  true,
	"thread-id":                  true,
	"turn_id":                    true,
	"turn-id":                    true,
	"window_id":                  true,
	"window-id":                  true,
	"x-codex-window-id":          true,
	"x-client-request-id":        true,
	"x-codex-beta-features":      true,
	"x-codex-turn-state":         true,
	"x-codex-turn-metadata":      true,
	media.ResponsesLiteHeaderKey: true,
}

// OpenAI passthrough allowed headers whitelist.
// 透传模式下仅放行这些低风险请求头，避免将非标准/环境噪声头传给上游触发风控。
var openaiPassthroughAllowedHeaders = map[string]bool{
	"accept":                     true,
	"accept-language":            true,
	"content-type":               true,
	"conversation_id":            true,
	"openai-beta":                true,
	"user-agent":                 true,
	"originator":                 true,
	"session_id":                 true,
	"installation_id":            true,
	"x-codex-installation-id":    true,
	"session-id":                 true,
	"thread_id":                  true,
	"thread-id":                  true,
	"turn_id":                    true,
	"turn-id":                    true,
	"window_id":                  true,
	"window-id":                  true,
	"x-codex-window-id":          true,
	"x-client-request-id":        true,
	"x-codex-beta-features":      true,
	"x-codex-turn-state":         true,
	"x-codex-turn-metadata":      true,
	media.ResponsesLiteHeaderKey: true,
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

// resolveOpenAITextProtocolForAttempt 解析当前账号的实际文本协议，并在转发前
// 覆盖 attempt 级端点元数据，避免故障转移后沿用上一账号的端点。
func resolveOpenAITextProtocolForAttempt(
	c *gin.Context,
	account *Account,
	preferred accountcore.TextProtocol,
) accountcore.TextProtocol {
	// OAuth 等专用账号始终保留既有 Responses 桥；只有 API Key 账号参与
	// “客户端首选协议 + 路由模式 + 探测状态”的普通文本协议解析。
	protocol := accountcore.TextProtocolResponses
	if account != nil && account.Type == capability.AccountTypeAPIKey {
		protocol = accountcore.ResolveUpstreamTextProtocol(account.Extra, preferred)
	}

	if account != nil && account.attemptRoute.Protocol() == protocolcore.ProtocolOpenAIChatCompletions {
		protocol = accountcore.TextProtocolChatCompletions
	}
	if account != nil && account.attemptRoute.Protocol() == protocolcore.ProtocolOpenAIResponses {
		protocol = accountcore.TextProtocolResponses
	}
	endpoint := "/v1/responses"
	if protocol == accountcore.TextProtocolChatCompletions {
		endpoint = "/v1/chat/completions"
	}
	gatewayhttp.SetActualOpenAIUpstreamEndpoint(c, endpoint)
	return protocol
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

// OpenAIGatewayService handles OpenAI API gateway operations
type OpenAIGatewayService struct {
	nativeAttemptActivity func() (func(), error)
	liveObserverMu        sync.Mutex
	liveObserverStopped   bool
	liveObserverCancels   map[string]context.CancelFunc
	liveObserverWG        sync.WaitGroup
	accountRepo           AccountRepository
	usageLogRepo          usage.UsageLogRepository
	usageBillingRepo      completion.Store
	userRepo              identity.UserRepository
	userSubRepo           billing.UserSubscriptionRepository
	cache                 session.GatewayCache
	cfg                   *config.Config
	codexDetector         accountcore.ClientRestrictionDetector
	schedulerSnapshot     *SchedulerSnapshotService
	concurrencyService    *scheduler.ConcurrencyService
	billingService        *billing.Calculator
	usageBillingNow       func() time.Time // 用量计费时钟，测试可注入固定时间以覆盖峰值倍率。
	rateLimitService      *RateLimitService
	billingCacheService   *billing.Eligibility
	userGroupRateResolver *billing.GroupRateResolver
	httpUpstream          httpclient.UpstreamTransport
	tlsFPProfileService   *provider.TLSProfiles
	tlsFPRouterService    *egress.TLSFingerprintRouterService
	deferredService       *accountcore.DeferredService
	openAITokenProvider   *accountcore.OpenAITokenSource
	openAIAuthorization   *accountcore.OpenAIAuthorization
	grokTokenProvider     *accountcore.GrokTokenSource
	toolCorrector         *openai.CodexToolCorrector

	resolver              *billing.PriceResolver
	channelService        *routing.ChannelService
	balanceNotifyService  *billing.BalanceNotifyService
	settingService        *gatewayprovider.RuntimeReaders
	userPlatformQuotaRepo billing.UserPlatformQuotaRepository
	liveAttestation       liveattestation.Provider
	liveAttestationCipher identity.SecretEncryptor

	openaiWSPoolOnce               sync.Once
	openaiWSPoolMu                 sync.Mutex
	openaiWSPoolClosed             bool
	openaiWSStateStoreOnce         sync.Once
	openaiSchedulerOnce            sync.Once
	openaiProxyStreamCircuitOnce   sync.Once
	openaiWSPassthroughDialerOnce  sync.Once
	openaiModelTransientOnce       sync.Once
	agentIdentityTaskMu            sync.Mutex
	openaiWSPool                   *openai.WSConnPool
	openaiWSStateStore             session.OpenAIWSStateStore
	openaiScheduler                OpenAIAccountScheduler
	openaiWSPassthroughDialer      openai.WSClientDialer
	openaiWSSessionPreemptions     openAIWSSessionPreemptRegistry
	openaiAccountStats             *openAIAccountRuntimeStats
	openaiModelTransient           *accountcore.ModelTransientState
	openaiProxyStreamCircuit       *egress.ProxyStreamCircuit
	openaiProxyStreamFailOpenLogAt atomic.Int64

	openaiWSFallbackUntil               sync.Map // key: int64(accountID), value: time.Time
	refreshFailureBlocks                accountcore.RefreshFailureBlocks
	refreshFailureClearGeneration       sync.Map // 仅显式清理改变刷新失败的发布代次。
	openaiAccountRuntimeBlockUntil      sync.Map // key: int64(accountID), value: time.Time
	openaiAccountRuntimeBlockLocks      sync.Map // key: int64(accountID), value: *sync.Mutex
	openaiAccountRuntimeBlockGeneration sync.Map // key: int64(accountID), value: uint64
	openaiAccountRuntimeBlockSequence   atomic.Uint64
	openaiOAuth429RetryStartedAt        sync.Map // key: int64(accountID), value: time.Time
	grokCredentialMutationLocks         sync.Map // key: int64(accountID), value: *sync.Mutex
	openaiOAuth429WindowStartUnixNano   atomic.Int64
	openaiOAuth429WindowCount           atomic.Int64
	openaiWSRetryMetrics                openAIWSRetryMetrics
	responseHeaderFilter                *egress.CompiledHeaderFilter
	codexSnapshotThrottle               *accountWriteThrottle
	openaiCompatSessionResponses        sync.Map
	openaiCompatAnthropicDigestSessions sync.Map
	// 下游会话最近收到的回合状态签发账号，用于故障转移时剥离跨账号回带状态。
	openaiCodexTurnStateOrigins sync.Map
	openaiCodexTurnStateWrites  atomic.Uint64
}

// NewOpenAIGatewayService creates a new OpenAIGatewayService
func NewOpenAIGatewayService(
	accountRepo AccountRepository,
	usageLogRepo usage.UsageLogRepository,
	usageBillingRepo completion.Store,
	userRepo identity.UserRepository,
	userSubRepo billing.UserSubscriptionRepository,
	userGroupRateRepo billing.UserGroupRateRepository,
	cache session.GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *SchedulerSnapshotService,
	concurrencyService *scheduler.ConcurrencyService,
	billingService *billing.Calculator,
	rateLimitService *RateLimitService,
	billingCacheService *billing.Eligibility,
	httpUpstream httpclient.UpstreamTransport,
	tlsFPProfileService *provider.TLSProfiles,
	deferredService *accountcore.DeferredService,
	openAITokenProvider *accountcore.OpenAITokenSource,
	grokTokenProvider *accountcore.GrokTokenSource,
	resolver *billing.PriceResolver,
	channelService *routing.ChannelService,
	balanceNotifyService *billing.BalanceNotifyService,
	settingService *gatewayprovider.RuntimeReaders,
	userPlatformQuotaRepo billing.UserPlatformQuotaRepository,
	tlsFPRouterServices ...*egress.TLSFingerprintRouterService,
) *OpenAIGatewayService {
	var tlsFPRouterService *egress.TLSFingerprintRouterService
	if len(tlsFPRouterServices) > 0 {
		tlsFPRouterService = tlsFPRouterServices[0]
	}
	svc := &OpenAIGatewayService{
		accountRepo:      accountRepo,
		usageLogRepo:     usageLogRepo,
		usageBillingRepo: usageBillingRepo,
		userRepo:         userRepo,
		userSubRepo:      userSubRepo,
		cache:            cache,
		cfg:              cfg,

		schedulerSnapshot:   schedulerSnapshot,
		concurrencyService:  concurrencyService,
		billingService:      billingService,
		rateLimitService:    rateLimitService,
		billingCacheService: billingCacheService,
		userGroupRateResolver: billing.NewGroupRateResolver(
			userGroupRateRepo,
			nil,
			resolveUserGroupRateCacheTTL(cfg),
			nil,
			"service.openai_gateway", logging.LegacyPrintf,
		),
		httpUpstream:        httpUpstream,
		tlsFPProfileService: tlsFPProfileService,
		tlsFPRouterService:  tlsFPRouterService,
		deferredService:     deferredService,
		openAITokenProvider: openAITokenProvider,
		grokTokenProvider:   grokTokenProvider,
		toolCorrector:       openai.NewCodexToolCorrector(),

		resolver:              resolver,
		channelService:        channelService,
		balanceNotifyService:  balanceNotifyService,
		settingService:        settingService,
		userPlatformQuotaRepo: userPlatformQuotaRepo,
		liveAttestation:       liveattestation.NewProvider(),
		liveAttestationCipher: newLiveAttestationCipher(cfg),
		responseHeaderFilter:  compileResponseHeaderFilter(cfg),
		codexSnapshotThrottle: newAccountWriteThrottle(openAICodexSnapshotPersistMinInterval),
		openaiModelTransient:  accountcore.NewModelTransientState(0),
	}
	if rateLimitService != nil {
		rateLimitService.SetAccountRuntimeBlocker(svc)
	}
	if openAITokenProvider != nil {
		openAITokenProvider.Block = func(record *accountcore.Record, until time.Time, reason string) {
			svc.BlockAccountScheduling(AccountFromRecord(record), until, reason)
		}
	}
	svc.logOpenAIWSModeBootstrap()
	return svc
}

// ResolveChannelMapping 解析渠道级模型映射（代理到 ChannelService）
func (s *OpenAIGatewayService) ResolveChannelMapping(ctx context.Context, groupID int64, model string) routing.ChannelMappingResult {
	if s.channelService == nil {
		return routing.ChannelMappingResult{MappedModel: model}
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
func (s *OpenAIGatewayService) ResolveChannelMappingAndRestrict(ctx context.Context, groupID *int64, model string) (routing.ChannelMappingResult, bool) {
	if s.channelService == nil {
		return modeltrace.WithChannelRedirect((routing.ChannelMappingResult{MappedModel: model}), ctx, model), false
	}
	result, restricted := s.channelService.ResolveChannelMappingAndRestrict(ctx, groupID, model)
	return modeltrace.WithChannelRedirect(result, ctx, model), restricted
}

func (s *OpenAIGatewayService) isCodexImageGenerationBridgeEnabled(ctx context.Context, account *Account, apiKey *apikey.APIKey) bool {
	if group := responsesPolicyGroup(ctx, apiKeyGroup(apiKey)); group != nil {
		switch group.ResponsesImagePolicy {
		case "enabled":
			return true
		case "disabled", "block":
			return false
		}
	}

	if override := account.CodexImageGenerationBridgeOverride(); override != nil {
		return *override
	}
	if s != nil && s.channelService != nil && apiKey != nil && apiKey.GroupID != nil {
		ch, err := s.channelService.GetChannelForGroup(ctx, *apiKey.GroupID)
		if err != nil {
			slog.Warn("failed to resolve codex image generation bridge channel override", "group_id", *apiKey.GroupID, "error", err)
		} else if override := ch.CodexImageGenerationBridgeOverride(capability.PlatformOpenAI); override != nil {
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
	billingModel := routing.BillingModelForRestriction(mapping.BillingModelSource, requestedModel, mapping.MappedModel)
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
	requiredCapability accountcore.OpenAIEndpointCapability,
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
	return ch.BillingModelSource == routing.BillingModelSourceUpstream
}

// ReplaceModelInBody 替换请求体中的 JSON model 字段（通用 gjson/sjson 实现）。
func (s *OpenAIGatewayService) ReplaceModelInBody(body []byte, newModel string) []byte {
	return protocolopenai.ReplaceModelInBody(body, newModel)
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
	if s == nil {
		return
	}
	s.openaiWSPoolMu.Lock()
	s.openaiWSPoolClosed = true
	pool := s.openaiWSPool
	s.openaiWSPoolMu.Unlock()
	if pool != nil {
		pool.Close()
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
	gatewayprovider.LogOpenAIWSModeInfo(
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
		openai.WSMessageReadLimitBytes,
	)
}

func (s *OpenAIGatewayService) getCodexClientRestrictionDetector() accountcore.ClientRestrictionDetector {
	if s != nil && s.codexDetector != nil {
		return s.codexDetector
	}
	var cfg *config.Config
	if s != nil {
		cfg = s.cfg
	}
	return &accountcore.CodexClientDetector{Options: accountcore.CodexClientOptions{ForceCLI: cfg != nil && cfg.Gateway.ForceCodexCLI, OfficialUserAgent: openai.IsCodexOfficialClientRequestStrict, OfficialOriginator: openai.IsCodexOfficialClientOriginator, AllowedClients: openai.MatchAllowedClients}}
}

func classifyOpenAIWSReconnectReason(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var fallbackErr *ws.FallbackError
	if !errors.As(err, &fallbackErr) || fallbackErr == nil {
		return "", false
	}
	reason := strings.TrimSpace(fallbackErr.Reason)
	if reason == "" {
		return "", false
	}

	if warning, ok := forwardcore.WarningFromError(err); ok && gatewayprovider.OpenAIUpstreamWarningIsCyber(warning) {
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
	var policyErr *ws.GenericPolicyError
	if errors.As(err, &policyErr) && policyErr != nil {
		return http.StatusInternalServerError, "upstream_error", "Upstream gateway error", policyErr.Error(), true
	}
	var fallbackErr *ws.FallbackError
	if !errors.As(err, &fallbackErr) || fallbackErr == nil {
		return 0, "", "", "", false
	}

	reason := strings.TrimSpace(fallbackErr.Reason)
	reason = strings.TrimPrefix(reason, "prewarm_")
	if reason == "" {
		return 0, "", "", "", false
	}

	var dialErr *openai.WSDialError
	if fallbackErr.Err != nil && errors.As(fallbackErr.Err, &dialErr) && dialErr != nil {
		if dialErr.StatusCode > 0 {
			statusCode = dialErr.StatusCode
		}
		if dialErr.Err != nil {
			upstreamMessage = logredact.SanitizeUpstreamQueries(strings.TrimSpace(dialErr.Err.Error()))
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
		upstreamMessage = logredact.SanitizeUpstreamQueries(strings.TrimSpace(fallbackErr.Err.Error()))
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
	gatewayhttp.SetOpsUpstreamError(c, statusCode, upstreamMessage, "")
	if account != nil {
		gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
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

	readHitRate := float64(0)
	if legacyReadFallbackTotal > 0 {
		readHitRate = float64(legacyReadFallbackHit) / float64(legacyReadFallbackTotal)
	}

	return OpenAICompatibilityFallbackMetricsSnapshot{
		SessionHashLegacyReadFallbackTotal: legacyReadFallbackTotal,
		SessionHashLegacyReadFallbackHit:   legacyReadFallbackHit,
		SessionHashLegacyDualWriteTotal:    legacyDualWriteTotal,
		SessionHashLegacyReadHitRate:       readHitRate,

		// 旧请求 key 已清零，保留公开指标字段，计数固定为零。
	}
}

func (s *OpenAIGatewayService) detectCodexClientRestriction(c *gin.Context, account *Account, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) accountcore.CodexClientRestrictionDetectionResult {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	return s.detectCodexClientRestrictionForClient(ctx, func() (string, string) {
		if c == nil {
			return "", ""
		}
		return c.GetHeader("User-Agent"), c.GetHeader("originator")
	}, account, tlsRouterMatch)
}

// detectCodexClientRestrictionForClient 保留动态全局设置的读取时机，仅分离客户端数据来源。
func (s *OpenAIGatewayService) detectCodexClientRestrictionForClient(ctx context.Context, readClient func() (string, string), account *Account, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) accountcore.CodexClientRestrictionDetectionResult {
	var globalAllowedClients []string
	if account != nil && account.IsCodexCLIOnlyEnabled() && s != nil && s.settingService != nil {
		if s.settingService.Gateway.IsOpenAIAllowClaudeCodeCodexPluginEnabled(ctx) {
			globalAllowedClients = []string{openai.AllowedClientClaudeCode}
		}
	}
	return s.getCodexClientRestrictionDetector().DetectClient(readClient, AccountRecordView(account), globalAllowedClients, tlsRouterMatch.Matched)
}

func logCodexCLIOnlyDetection(ctx context.Context, c *gin.Context, account *Account, apiKeyID int64, result accountcore.CodexClientRestrictionDetectionResult, body []byte) {
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
	log := logging.FromContext(ctx).With(fields...)
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
		zap.String("request_client_ip", strings.TrimSpace(clientip.GetClientIP(c))),
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
		fields = append(fields, zap.String("request_prompt_cache_key_sha256", upstream.HashSensitiveValueForLog(promptCacheKey)))
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
		result[strings.ToLower(key)] = logredact.TruncateUTF8(value, codexCLIOnlyHeaderValueMaxBytes)
	}
	return result
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
	case capability.AccountTypeOAuth:
		if account.IsOpenAIAgentIdentity() {
			return "", accountcore.OpenAIAuthModeAgentIdentity, nil
		}
		if account.Platform == capability.PlatformGrok {
			if s.grokTokenProvider != nil {
				accessToken, err := s.grokTokenProvider.GetAccessToken(ctx, AccountRecordView(account))
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
			accessToken, err := s.openAITokenProvider.GetAccessToken(ctx, AccountRecordView(account))
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
	case capability.AccountTypeSetupToken:
		if !account.IsOpenAIOAuthLike() {
			return "", "", fmt.Errorf("unsupported account type: %s", account.Type)
		}
		// OpenAI setup tokens are inference-only bearer credentials. They use the
		// Codex OAuth forwarding protocol but have no refresh-token lifecycle.
		accessToken := account.GetOpenAIAccessToken()
		if accessToken == "" {
			return "", "", errors.New("access_token not found in credentials")
		}
		return accessToken, "oauth", nil
	case capability.AccountTypeAPIKey:
		if account.Platform == capability.PlatformGrok {
			apiKey := strings.TrimSpace(account.GetCredential("api_key"))
			if apiKey == "" {
				return "", "", errors.New("api_key not found in credentials")
			}
			return apiKey, "apikey", nil
		}
		apiKey := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
		if apiKey == "" {
			return "", "", errors.New("api_key not found in credentials")
		}
		return apiKey, "apikey", nil
	default:
		return "", "", fmt.Errorf("unsupported account type: %s", account.Type)
	}
}

// EnforceOpenAIClientPolicyForRequest 在非 /responses 主入口上复用 OpenAI OAuth 客户端访问策略。
func (s *OpenAIGatewayService) EnforceOpenAIClientPolicyForRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) error {
	result := s.detectCodexClientRestriction(c, account, tlsRouterMatch)
	apiKeyID := gatewayhttp.APIKeyIDFromContext(c)
	logCodexCLIOnlyDetection(ctx, c, account, apiKeyID, result, body)
	if !result.Enabled || result.Matched {
		return nil
	}
	gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	if c != nil && gatewayhttp.GetOpenAIClientTransport(c) != gatewayhttp.OpenAIClientTransportWS {
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
func (s *OpenAIGatewayService) MatchOpenAITLSFingerprintRouterForRequest(c *gin.Context, account *Account) egress.TLSFingerprintRouterMatchResult {
	return s.matchTLSFingerprintRouter(c, account)
}

// applyOpenAIUpstreamUserAgentHeader 在 WebSocket 握手头上复用 HTTP 上游 UA 规则。
func (s *OpenAIGatewayService) applyOpenAIUpstreamUserAgentHeader(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	headers http.Header,
	passthrough bool,
	routerMatch ...egress.TLSFingerprintRouterMatchResult,
) {
	if headers == nil {
		return
	}
	req := &http.Request{Header: headers}
	s.applyOpenAIUpstreamUserAgent(ctx, c, account, req, passthrough, routerMatch...)
}

func (s *OpenAIGatewayService) matchTLSFingerprintRouter(c *gin.Context, account *Account) egress.TLSFingerprintRouterMatchResult {
	return s.matchTLSFingerprintRouterForClient(func() string {
		if c == nil {
			return ""
		}
		return c.GetHeader("User-Agent")
	}, account)
}

// matchTLSFingerprintRouterForClient 在账号确有 Router 后才读取 User-Agent。
func (s *OpenAIGatewayService) matchTLSFingerprintRouterForClient(readUserAgent func() string, account *Account) egress.TLSFingerprintRouterMatchResult {
	if s == nil || s.tlsFPRouterService == nil || account == nil || account.GetTLSFingerprintRouterID() <= 0 {
		return egress.TLSFingerprintRouterMatchResult{}
	}
	userAgent := readUserAgent()
	return s.tlsFPRouterService.MatchUserAgent(account.GetTLSFingerprintRouterID(), userAgent)
}

// resolveOpenAITLSProfile 保留未装配时的短路，选择规则唯一归 egress。
func (s *OpenAIGatewayService) resolveOpenAITLSProfile(value *Account, routerMatch ...egress.TLSFingerprintRouterMatchResult) *tlsfingerprint.Profile {
	if s == nil || s.tlsFPProfileService == nil {
		return nil
	}
	return s.tlsFPProfileService.ResolveRequestTLS(accountTLSSelection(value, routerMatch))
}

func (s *OpenAIGatewayService) resolveOpenAIWSTLSProfile(account *Account, routerMatch ...egress.TLSFingerprintRouterMatchResult) (*tlsfingerprint.Profile, string) {
	profile := s.resolveOpenAITLSProfile(account, routerMatch...)
	if profile == nil {
		return nil, ""
	}
	// Responses WebSocket 是 HTTP/1.1 Upgrade，连接池键也按剥离 h2 后的模板隔离。
	profile = tlsfingerprint.HTTP1OnlyProfile(profile)
	return profile, egress.WebSocketTLSIdentity(accountTLSSelection(account, routerMatch), true, tlsfingerprint.CacheKey(profile))
}

// hasOpenAIUltraReasoningSuffix 仅识别 OpenAI GPT 模型，避免误伤其它平台的 Ultra 命名。
func hasOpenAIUltraReasoningSuffix(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(capability.LastOpenAIModelSegment(model)))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	return strings.HasPrefix(normalized, "gpt-") && strings.HasSuffix(normalized, "-ultra")
}

func openAIClientPolicyForbiddenMessage(result accountcore.CodexClientRestrictionDetectionResult) string {
	// 按策略返回更明确的拒绝原因，同时保留旧 codex_cli_only 测试和客户端提示语义。
	if result.Policy == accountcore.OpenAIOAuthClientPolicyCodexOnly {
		return "This account only allows Codex official clients"
	}
	if result.Policy == accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly {
		return "This account only allows clients matched by the configured TLS router"
	}
	return "This account only allows configured OpenAI OAuth clients"
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

// ExpireRuntimeCaches 由应用拥有的时间轮调用，保留原缓存到期清理频率。
func (s *OpenAIGatewayService) ExpireRuntimeCaches() {
	if s != nil {
		if s.userGroupRateResolver != nil {
			s.userGroupRateResolver.DeleteExpired()
		}
	}
}

// BindNativeAttemptActivity 将平台尝试绑定到应用唯一活动拥有者。
func (s *OpenAIGatewayService) BindNativeAttemptActivity(enter func() (func(), error)) {
	s.nativeAttemptActivity = enter
}
