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

	selectionadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/usage"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai/liveattestation"
	"github.com/gin-gonic/gin"
)

const (
	// ChatGPT internal API for OAuth accounts
	chatgptCodexURL = "https://chatgpt.com/backend-api/codex/responses"
	// OpenAI Platform API for API Key accounts (fallback)
	openaiPlatformAPIURL            = "https://api.openai.com/v1/responses"
	openaiPlatformAPIInputTokensURL = "https://api.openai.com/v1/responses/input_tokens"
	openaiStickySessionTTL          = time.Hour // 粘性会话TTL

	// OpenAI WS Mode 失败后的重连次数上限（不含首次尝试）。
	// 与 Codex 客户端保持一致：失败后最多重连 5 次。
	openAIWSReconnectRetryLimit = 5
	// 上游错误体只需要提取错误 JSON/日志摘要，默认 512KiB 避免错误风暴叠加大请求体。
	openAIUpstreamErrorBodyReadLimit int64 = 512 << 10
	// OpenAI WS Mode 重连退避默认值（可由配置覆盖）。
	openAIWSRetryBackoffInitialDefault = 120 * time.Millisecond
	openAIWSRetryBackoffMaxDefault     = 2 * time.Second
	openAIWSRetryJitterRatioDefault    = 0.2

	// Codex 限额快照仅用于后台展示/诊断，不需要每个成功请求都立即落库。
	openAICodexSnapshotPersistMinInterval = 30 * time.Second
	// 配额自动暂停时，超过该时长仍未刷新的 used% 快照视为陈旧，不再据此暂停账号。
	// 被暂停的账号收不到流量，其快照永远不会从上游响应头刷新；该兜底让账号在快照
	// 陈旧时放行一次请求，从而通过正常响应头自愈，而无需等待整个窗口（5h/7d）重置。
)

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

// ErrNoAvailableCompactAccounts indicates the request needs /responses/compact
// support but no compatible account is available.

// OpenAIGatewayService handles OpenAI API gateway operations
type OpenAIGatewayService struct {
	Auxiliary *gatewayhttp.OpenAIAuxiliary
	Text      *gatewayhttp.OpenAITextExecutor
	Requests  *gatewayhttp.OpenAIRequests

	Grok             *gatewayhttp.GrokExecutor
	fastPolicy       *gatewayprovider.ExecutionFastPolicy
	transportFailure *gatewayhttp.UpstreamTransportFailure

	selection   *selectionadapter.Compatible
	cyberBlocks *session.CyberBlocks
	// prompts 直接引用 app 的唯一提示词运行时，WS 不再通过设置聚合取回它。
	prompts       *promptpolicy.Service
	runtimeBlocks atomic.Pointer[accountcore.RuntimeBlockState]

	nativeAttemptActivity func() (func(), error)
	liveObserverMu        sync.Mutex
	liveObserverStopped   bool
	liveObserverCancels   map[string]context.CancelFunc
	liveObserverWG        sync.WaitGroup
	accountRepo           gatewayprovider.ExecutionAccountStore
	usageLogRepo          usage.UsageLogRepository

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

	liveAttestation       liveattestation.Provider
	liveAttestationCipher identity.SecretEncryptor

	openaiWSPoolOnce       sync.Once
	openaiWSPoolMu         sync.Mutex
	openaiWSPoolClosed     bool
	openaiWSStateStoreOnce sync.Once

	openaiProxyStreamCircuitOnce  sync.Once
	openaiWSPassthroughDialerOnce sync.Once
	openaiModelTransientOnce      sync.Once
	agentIdentity                 *gatewayprovider.ExecutionAgentIdentity
	openaiWSPool                  *openai.WSConnPool
	openaiWSStateStore            session.OpenAIWSStateStore

	openaiWSPassthroughDialer  openai.WSClientDialer
	openaiWSSessionPreemptions openAIWSSessionPreemptRegistry
	schedulerStickyStats       atomic.Pointer[scheduler.StickyStats]
	backgroundTasks            func(string, func()) bool
	openaiModelTransient       *accountcore.ModelTransientState
	openaiProxyStreamCircuit   *egress.ProxyStreamCircuit

	openaiWSFallbackUntil sync.Map // key: int64(accountID), value: time.Time
	openaiWSRetryMetrics  openAIWSRetryMetrics
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
	accountRepo gatewayprovider.ExecutionAccountStore,
	usageLogRepo usage.UsageLogRepository,

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
		selection:          choices,
		grokHealth:         grokHealth,
		compactExecutor:    compactExecutor,
		responseOutput:     responseOutput,
		openaiWSStateStore: stateStore,
		turnStateHeaders:   turnStateHeaders,
		prompts:            prompts,
		accountRepo:        accountRepo,
		usageLogRepo:       usageLogRepo,

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

		liveAttestation:       liveattestation.NewProvider(),
		liveAttestationCipher: newLiveAttestationCipher(cfg),
		responseHeaderFilter:  headerFilter,

		openaiModelTransient:     modelTransient,
		openaiProxyStreamCircuit: proxyCircuit,
	}

	if grokHealth != nil {
		svc.codexSnapshotThrottle = grokHealth.Throttle
	} else {
		svc.codexSnapshotThrottle = accountcore.NewWriteThrottle(openAICodexSnapshotPersistMinInterval)
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

func (s *OpenAIGatewayService) isCodexImageGenerationBridgeEnabled(ctx context.Context, account *gatewayprovider.ExecutionAccount, apiKey *apikey.APIKey) bool {
	if group := responsesPolicyGroup(ctx, apiKeyGroup(apiKey)); group != nil {
		switch group.ResponsesImagePolicy {
		case "enabled":
			return true
		case "disabled", "block":
			return false
		}
	}

	if override := gatewayprovider.ExecutionProtocolRecord(account).CodexImageGenerationBridgeOverride(); override != nil {
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

// resolveChannelRoutingModel 返回 OpenAI 账号调度层使用的渠道映射后模型。
func (s *OpenAIGatewayService) resolveChannelRoutingModel(ctx context.Context, groupID *int64, requestedModel string) string {
	if s == nil {
		return requestedModel

	}
	return s.channelService.
		ResolveRoutingModel(ctx, groupID,
			requestedModel,
		)
}

// ResolveOpenAIWSRoutingModelForAccount 为已选定的 WebSocket 账号逐轮解析并校验渠道模型。
// 长连接不能在后续 turn 重新调度账号，因此模型不再适配当前账号时直接拒绝该帧。
func (s *OpenAIGatewayService) ResolveOpenAIWSRoutingModelForAccount(
	ctx context.Context,
	groupID *int64,
	account *gatewayprovider.ExecutionAccount,
	requestedModel string,
	requiredCapability accountcore.OpenAIEndpointCapability,
) (string, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return "", errors.New("websocket request model is empty")
	}
	if s.selection.CheckChannelPricingRestriction(ctx, groupID, requestedModel) {
		return "", fmt.Errorf("model %s is restricted by channel pricing", requestedModel)
	}

	routingModel := strings.TrimSpace(s.resolveChannelRoutingModel(ctx, groupID, requestedModel))
	if routingModel == "" {
		routingModel = requestedModel
	}
	if account == nil || !gatewayprovider.
		CompatibleAccountEligible(
			ctx,
			account,
			account.Record.Platform,
			routingModel,
			false,
			requiredCapability,
		) {
		return "", fmt.Errorf("model %s is not supported by the selected websocket account", requestedModel)
	}
	if s.isOpenAIAccountRequestRuntimeBlocked(account, routingModel) {
		return "", fmt.Errorf("model %s is temporarily unavailable on the selected websocket account", requestedModel)
	}
	if groupID != nil && s.selection.NeedsUpstreamChannelRestriction(ctx, groupID) &&
		s.selection.UpstreamRoutingModelRestricted(ctx, *groupID, account, routingModel, false) {
		return "", fmt.Errorf("model %s is restricted after account mapping", requestedModel)
	}
	return routingModel, nil
}

// ReplaceModelInBody 替换请求体中的 JSON model 字段（通用 gjson/sjson 实现）。
func (s *OpenAIGatewayService) ReplaceModelInBody(body []byte, newModel string) []byte {
	return protocolopenai.ReplaceModelInBody(body, newModel)
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

func (s *OpenAIGatewayService) writeOpenAIWSFallbackErrorResponse(c *gin.Context, account *gatewayprovider.ExecutionAccount, wsErr error) bool {
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
			Platform:           account.Record.Platform,
			AccountID:          account.Record.ID,
			AccountName:        account.Record.Name,
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

// BindNativeAttemptActivity 将平台尝试绑定到应用唯一活动拥有者。
func (s *OpenAIGatewayService) BindNativeAttemptActivity(enter func() (func(), error)) {
	s.nativeAttemptActivity = enter
}

// BindGrokExecution 只在启动前绑定同一执行器及共享传输失败处理器。
func (s *OpenAIGatewayService) BindGrokExecution(value *gatewayhttp.GrokExecutor) {
	s.Grok = value
	s.fastPolicy = value.FastPolicy
	s.transportFailure = value.Failure
	s.openaiWSPassthroughDialer = value.Dialer
}

// BindTextExecution 在启动前固定协议执行器与共用请求构造器。
func (s *OpenAIGatewayService) BindTextExecution(value *gatewayhttp.OpenAITextExecutor) {
	s.Text = value
	s.Requests = value.Requests
}
