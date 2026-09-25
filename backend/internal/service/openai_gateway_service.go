package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	selectionadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

const (
	// ChatGPT internal API for OAuth accounts
	chatgptCodexURL = "https://chatgpt.com/backend-api/codex/responses"
	// OpenAI Platform API for API Key accounts (fallback)
	openaiPlatformAPIURL            = "https://api.openai.com/v1/responses"
	openaiPlatformAPIInputTokensURL = "https://api.openai.com/v1/responses/input_tokens"
	openaiStickySessionTTL          = time.Hour // 粘性会话TTL

	// Codex 限额快照仅用于后台展示/诊断，不需要每个成功请求都立即落库。
	openAICodexSnapshotPersistMinInterval = 30 * time.Second
	// 配额自动暂停时，超过该时长仍未刷新的 used% 快照视为陈旧，不再据此暂停账号。
	// 被暂停的账号收不到流量，其快照永远不会从上游响应头刷新；该兜底让账号在快照
	// 陈旧时放行一次请求，从而通过正常响应头自愈，而无需等待整个窗口（5h/7d）重置。
)

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

// ErrNoAvailableCompactAccounts indicates the request needs /responses/compact
// support but no compatible account is available.

// OpenAIGatewayService handles OpenAI API gateway operations
type OpenAIGatewayService struct {
	WebSockets  *gatewayhttp.OpenAIWebSocketExecutor
	Connections *gatewayhttp.OpenAIWSConnections
	Responses   *gatewayhttp.OpenAIResponsesExecutor
	Lineage     *gatewayhttp.OpenAIEncryptedLineage
	Auxiliary   *gatewayhttp.OpenAIAuxiliary
	Text        *gatewayhttp.OpenAITextExecutor
	Requests    *gatewayhttp.OpenAIRequests

	Grok             *gatewayhttp.GrokExecutor
	fastPolicy       *gatewayprovider.ExecutionFastPolicy
	transportFailure *gatewayhttp.UpstreamTransportFailure

	selection   *selectionadapter.Compatible
	cyberBlocks *session.CyberBlocks
	// prompts 直接引用 app 的唯一提示词运行时，WS 不再通过设置聚合取回它。
	prompts       *promptpolicy.Service
	runtimeBlocks atomic.Pointer[accountcore.RuntimeBlockState]

	nativeAttemptActivity func() (func(), error)
	accountRepo           gatewayprovider.ExecutionAccountStore

	cache              session.GatewayCache
	cfg                *config.Config
	codexDetector      accountcore.ClientRestrictionDetector
	concurrencyService *scheduler.ConcurrencyService

	// 用量计费时钟，测试可注入固定时间以覆盖峰值倍率。
	healthObserver *accountprovider.UpstreamHealth

	completionRecorder   *completion.Recorder
	httpUpstream         httpclient.UpstreamTransport
	tlsFPProfileService  *provider.TLSProfiles
	tlsFPRouterService   *egress.TLSFingerprintRouterService
	deferredService      *accountcore.DeferredService
	executionCredentials *accountcore.OpenAIExecutionCredentials

	requestCredentials *gatewayprovider.RequestCredentials
	toolCorrector      *openai.CodexToolCorrector

	resolver       *billing.PriceResolver
	channelService *routing.ChannelService

	settingService *gatewayprovider.RuntimeReaders

	openaiWSStateStoreOnce sync.Once

	openaiProxyStreamCircuitOnce sync.Once
	openaiModelTransientOnce     sync.Once
	agentIdentity                *gatewayprovider.ExecutionAgentIdentity
	openaiWSStateStore           session.OpenAIWSStateStore

	schedulerStickyStats     atomic.Pointer[scheduler.StickyStats]
	backgroundTasks          func(string, func()) bool
	openaiModelTransient     *accountcore.ModelTransientState
	openaiProxyStreamCircuit *egress.ProxyStreamCircuit

	responseHeaderFilter  *egress.CompiledHeaderFilter
	codexSnapshotThrottle *accountcore.WriteThrottle

	anthropicPromptCache atomic.Pointer[session.AnthropicPromptCache]
	// 下游会话最近收到的回合状态签发账号，用于故障转移时剥离跨账号回带状态。
	turnStateHeaders *gatewayhttp.CodexTurnStateHeaders
	grokHealth       *accountprovider.GrokHealth
	compactExecutor  *gatewayhttp.CompactExecutor
	responseOutput   *gatewayhttp.OpenAIResponseOutput
}

// NewOpenAIGatewayService 接入固定执行依赖，剩余协议编排随 S16 退出。
func NewOpenAIGatewayService(
	connections *gatewayhttp.OpenAIWSConnections,
	accountRepo gatewayprovider.ExecutionAccountStore,
	cache session.GatewayCache,
	cfg *config.Config,

	concurrencyService *scheduler.ConcurrencyService,

	healthObserver *accountprovider.UpstreamHealth,
	httpUpstream httpclient.UpstreamTransport,
	tlsFPProfileService *provider.TLSProfiles,
	deferredService *accountcore.DeferredService,
	executionCredentials *accountcore.OpenAIExecutionCredentials,
	requestCredentials *gatewayprovider.RequestCredentials,
	resolver *billing.PriceResolver,
	channelService *routing.ChannelService,

	settingService *gatewayprovider.RuntimeReaders,
	prompts *promptpolicy.Service, headerFilter *egress.CompiledHeaderFilter, stateStore session.OpenAIWSStateStore, turnStateHeaders *gatewayhttp.CodexTurnStateHeaders, modelTransient *accountcore.ModelTransientState, proxyCircuit *egress.ProxyStreamCircuit, choices *selectionadapter.Compatible, grokHealth *accountprovider.GrokHealth, compactExecutor *gatewayhttp.CompactExecutor, responseOutput *gatewayhttp.OpenAIResponseOutput,
	tlsFPRouterServices ...*egress.TLSFingerprintRouterService,
) *OpenAIGatewayService {
	var tlsFPRouterService *egress.TLSFingerprintRouterService
	if len(tlsFPRouterServices) > 0 {
		tlsFPRouterService = tlsFPRouterServices[0]
	}
	if modelTransient == nil {
		modelTransient = accountcore.NewModelTransientState(0)
	}
	var corrector *openai.CodexToolCorrector
	if responseOutput != nil {
		corrector = responseOutput.Corrector
	} else {
		corrector = openai.NewCodexToolCorrector()
	}
	svc := &OpenAIGatewayService{
		Connections:        connections,
		selection:          choices,
		grokHealth:         grokHealth,
		compactExecutor:    compactExecutor,
		responseOutput:     responseOutput,
		openaiWSStateStore: stateStore,
		turnStateHeaders:   turnStateHeaders,
		prompts:            prompts,
		accountRepo:        accountRepo,

		cache: cache,
		cfg:   cfg,

		concurrencyService: concurrencyService,

		healthObserver: healthObserver,

		httpUpstream:         httpUpstream,
		tlsFPProfileService:  tlsFPProfileService,
		tlsFPRouterService:   tlsFPRouterService,
		deferredService:      deferredService,
		executionCredentials: executionCredentials,
		requestCredentials:   requestCredentials,
		toolCorrector:        corrector,

		resolver:       resolver,
		channelService: channelService,

		settingService: settingService,

		responseHeaderFilter: headerFilter,

		openaiModelTransient:     modelTransient,
		openaiProxyStreamCircuit: proxyCircuit,
	}

	if grokHealth != nil {
		svc.codexSnapshotThrottle = grokHealth.Throttle
	} else {
		svc.codexSnapshotThrottle = accountcore.NewWriteThrottle(openAICodexSnapshotPersistMinInterval)
	}
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

// ReplaceModelInBody 替换请求体中的 JSON model 字段（通用 gjson/sjson 实现）。
func (s *OpenAIGatewayService) ReplaceModelInBody(body []byte, newModel string) []byte {
	return protocolopenai.ReplaceModelInBody(body, newModel)
}

func (s *OpenAIGatewayService) SnapshotOpenAICompatibilityFallbackMetrics() OpenAICompatibilityFallbackMetricsSnapshot {
	legacyReadFallbackTotal, legacyReadFallbackHit, legacyDualWriteTotal := s.stickyStats().Snapshot()

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

// BindNativeAttemptActivity 将平台尝试绑定到应用唯一活动拥有者。
func (s *OpenAIGatewayService) BindNativeAttemptActivity(enter func() (func(), error)) {
	s.nativeAttemptActivity = enter
}

// BindGrokExecution 只在启动前绑定同一执行器及共享传输失败处理器。
func (s *OpenAIGatewayService) BindGrokExecution(value *gatewayhttp.GrokExecutor) {
	s.Grok = value
	s.fastPolicy = value.FastPolicy
	s.transportFailure = value.Failure
}

// BindTextExecution 在启动前固定协议执行器与共用请求构造器。
func (s *OpenAIGatewayService) BindTextExecution(value *gatewayhttp.OpenAITextExecutor) {
	s.Text = value
	s.Requests = value.Requests
}

func (s *OpenAIGatewayService) ResponseStateStore() session.OpenAIWSStateStore {
	if s == nil {
		return nil
	}
	s.openaiWSStateStoreOnce.Do(func() {
		if s.openaiWSStateStore == nil {
			s.openaiWSStateStore = session.NewOpenAIWSStateStore(s.cache, gatewayprovider.LogOpenAIWSModeInfo)
		}
	})
	return s.openaiWSStateStore
}
