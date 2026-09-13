package service

// 本文件由 openai_gateway_service.go 纯移动拆分而来：粘性会话哈希、账号选择与
// 负载感知调度、配额自动暂停判定、并发槽位获取。仅做代码搬迁，无任何行为变更。

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openCodeSessionAffinityHeader = "X-Session-Affinity"
	openCodeSessionIDHeader       = "X-Session-Id"
	openCodeNativeSessionHeader   = "X-OpenCode-Session"
	codeBuddyConversationHeader   = "X-Conversation-ID"
)

var explicitOpenAIHeaderSessionNames = []string{
	"session-id",
	"session_id",
	"conversation_id",
	openCodeSessionAffinityHeader,
	openCodeSessionIDHeader,
	openCodeNativeSessionHeader,
	codeBuddyConversationHeader,
}

// explicitOpenAIHeaderSessionID 提取 OpenAI 兼容客户端发送的稳定会话标识。
// 这里只接收会话级字段；每轮变化的请求或消息 ID 会破坏粘性路由和上游提示缓存。
func explicitOpenAIHeaderSessionID(c *gin.Context) string {
	if c == nil {
		return ""
	}

	for _, header := range explicitOpenAIHeaderSessionNames {
		if sessionID := strings.TrimSpace(c.GetHeader(header)); sessionID != "" {
			return sessionID
		}
	}
	return ""
}

// ExtractSessionID extracts the raw session ID from headers or body without hashing.
// Used by ForwardAsAnthropic to pass as prompt_cache_key for upstream cache.
func (s *OpenAIGatewayService) ExtractSessionID(c *gin.Context, body []byte) string {
	return explicitOpenAIRequestSessionID(c, body)
}

func explicitOpenAISessionID(c *gin.Context, body []byte) string {
	if c == nil {
		return ""
	}

	sessionID := explicitOpenAIHeaderSessionID(c)
	if sessionID == "" && len(body) > 0 {
		// WS response.create 将会话字段包在 response 内，先解包再读取统一信号。
		sessionID = strings.TrimSpace(openAIRequestPayloadView(body).Get("prompt_cache_key").String())
	}
	return sessionID
}

// explicitOpenAIRequestSessionID 仅对认证到 Grok 分组的请求，将 Grok 原生会话头加入
// 通用 OpenAI 会话信号，避免无关的 x-grok-conv-id 改变非 Grok 分组的调度或上游会话行为。
func explicitOpenAIRequestSessionID(c *gin.Context, body []byte) string {
	if c == nil {
		return ""
	}

	sessionID := explicitOpenAIHeaderSessionID(c)
	if sessionID == "" && isGrokRequestContext(c) {
		sessionID = strings.TrimSpace(c.GetHeader(grokConversationIDHeader))
	}
	if sessionID == "" && len(body) > 0 {
		sessionID = strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String())
	}
	if sessionID == "" && isGrokRequestContext(c) && len(body) > 0 {
		sessionID = grokPreviousResponseSessionSeed(body)
	}
	return sessionID
}

// grokPreviousResponseSessionSeed 根据 Responses 的 previous_response_id 返回稳定的粘性种子。
// 仅接受 resp_* 响应 ID，消息 ID 与未知格式不得固定粘性路由或提示缓存身份。
func grokPreviousResponseSessionSeed(body []byte) string {
	id := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	if id == "" {
		return ""
	}
	if ClassifyOpenAIPreviousResponseIDKind(id) != OpenAIPreviousResponseIDKindResponseID {
		return ""
	}
	// 增加命名空间，避免内容派生种子与响应 ID 冲突。
	return "grok-prev-resp:" + id
}

// GenerateExplicitSessionHash generates a sticky-session hash only from explicit
// client session signals. It intentionally skips content-derived fallback and is
// used by stateless endpoints such as /v1/images.
func (s *OpenAIGatewayService) GenerateExplicitSessionHash(c *gin.Context, body []byte) string {
	sessionID := explicitOpenAIRequestSessionID(c, body)
	if sessionID == "" {
		return ""
	}

	currentHash, legacyHash := deriveOpenAISessionHashes(sessionID)
	attachOpenAILegacySessionHashToGin(c, legacyHash)
	return currentHash
}

// GenerateSessionHash 为 OpenAI 请求生成粘性会话哈希。
// 优先级依次为：session-id/session_id、conversation_id、OpenCode 会话头、
// CodeBuddy 会话头、Grok 分组会话头、prompt_cache_key，最后才使用内容回退。
func (s *OpenAIGatewayService) GenerateSessionHash(c *gin.Context, body []byte) string {
	if c == nil {
		return ""
	}

	sessionID := explicitOpenAIRequestSessionID(c, body)
	if sessionID == "" && len(body) > 0 {
		sessionID = deriveOpenAIContentSessionSeed(body)
	}
	if sessionID == "" {
		return ""
	}

	if isGrokRequestContext(c) {
		sessionID = grokStickyAffinitySeed(sessionID, body)
	}

	currentHash, legacyHash := deriveOpenAISessionHashes(sessionID)
	attachOpenAILegacySessionHashToGin(c, legacyHash)
	return currentHash
}

// grokStickyAffinitySeed 按模型隔离粘性路由，同时不改变
// applyGrokResponsesCacheIdentity 写入的上游 prompt_cache_key。
func grokStickyAffinitySeed(sessionID string, body []byte) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	model := ""
	if len(body) > 0 {
		model = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "model").String()))
	}
	if model == "" {
		return "grok-affinity:v1:" + sessionID
	}
	return "grok-affinity:v1:" + model + ":" + sessionID
}

// GenerateSessionHashWithFallback 先按常规信号生成会话哈希；
// 当未携带 session_id/conversation_id/prompt_cache_key 时，使用 fallbackSeed 生成稳定哈希。
// 该方法用于 WS ingress，避免会话信号缺失时发生跨账号漂移。
func (s *OpenAIGatewayService) GenerateSessionHashWithFallback(c *gin.Context, body []byte, fallbackSeed string) string {
	sessionHash := s.GenerateSessionHash(c, body)
	if sessionHash != "" {
		return sessionHash
	}

	seed := strings.TrimSpace(fallbackSeed)
	if seed == "" {
		return ""
	}

	currentHash, legacyHash := deriveOpenAISessionHashes(seed)
	attachOpenAILegacySessionHashToGin(c, legacyHash)
	return currentHash
}

func resolveOpenAIUpstreamOriginator(c *gin.Context, isOfficialClient bool, routerMatch ...TLSFingerprintRouterMatchResult) string {
	return resolveOpenAIUpstreamOriginatorForClient(func() string {
		if c == nil {
			return ""
		}
		return c.GetHeader("originator")
	}, isOfficialClient, routerMatch...)
}

// resolveOpenAIUpstreamOriginatorForClient 保持 Router、显式来源和官方默认值优先级。
func resolveOpenAIUpstreamOriginatorForClient(readOriginator func() string, isOfficialClient bool, routerMatch ...TLSFingerprintRouterMatchResult) string {
	if len(routerMatch) > 0 && routerMatch[0].Matched {
		if originator := strings.TrimSpace(routerMatch[0].UpstreamOriginator); originator != "" {
			return originator
		}
	}
	if originator := strings.TrimSpace(readOriginator()); originator != "" {
		return originator
	}
	if isOfficialClient {
		return resolveCodexOutboundIdentity("").originator
	}
	return "opencode"
}

// BindStickySession sets session -> account binding with standard TTL.
func (s *OpenAIGatewayService) BindStickySession(ctx context.Context, groupID *int64, sessionHash string, accountID int64) error {
	if sessionHash == "" || accountID <= 0 {
		return nil
	}
	if preserveOpenAIGuardianParentBinding(ctx, sessionHash) {
		return nil
	}
	ttl := openaiStickySessionTTL
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds > 0 {
		ttl = time.Duration(s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds) * time.Second
	}
	return s.setStickySessionAccountID(ctx, groupID, sessionHash, accountID, ttl)
}

// SelectAccount selects an OpenAI account with sticky session support
func (s *OpenAIGatewayService) SelectAccount(ctx context.Context, groupID *int64, sessionHash string) (*Account, error) {
	return s.SelectAccountForModel(ctx, groupID, sessionHash, "")
}

// SelectAccountForModel selects an account supporting the requested model
func (s *OpenAIGatewayService) SelectAccountForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*Account, error) {
	return s.SelectAccountForModelWithExclusions(ctx, groupID, sessionHash, requestedModel, nil)
}

// SelectAccountForModelWithExclusions selects an account supporting the requested model while excluding specified accounts.
// SelectAccountForModelWithExclusions 选择支持指定模型的账号，同时排除指定的账号。
func (s *OpenAIGatewayService) SelectAccountForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*Account, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	resolvedCtx, resolvedGroupID, err := s.resolveOpenAISchedulerGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	ctx = resolvedCtx
	groupID = resolvedGroupID
	if s.groupUsesAdvancedScheduler(ctx, groupID) {
		selection, _, selectErr := s.SelectAccountWithScheduler(
			withAdvancedSchedulerNoSlotSelection(ctx),
			groupID,
			"",
			sessionHash,
			requestedModel,
			excludedIDs,
			OpenAIUpstreamTransportAny,
			false,
		)
		if selectErr != nil {
			return nil, selectErr
		}
		if selection == nil || selection.Account == nil {
			return nil, ErrNoAvailableAccounts
		}
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		return selection.Account, nil
	}
	return s.selectAccountForModelWithExclusions(ctx, groupID, PlatformOpenAI, sessionHash, requestedModel, excludedIDs, false, 0, "")
}

func shouldUseGroupModelUnsupportedError(ctx context.Context, accounts []Account, requestedModel string) bool {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(accounts) == 0 {
		return false
	}
	hasRelevantAccount := false
	for i := range accounts {
		acc := &accounts[i]
		if !acc.IsOpenAI() || !acc.IsSchedulable() {
			continue
		}
		hasRelevantAccount = true
		if openAIAccountSupportsRoutingModel(ctx, acc, requestedModel) {
			return false
		}
	}
	return hasRelevantAccount
}

// NormalizeOpenAICompatiblePlatform 保留 OpenAI 网关正式支持的平台标识，
// 其它输入回退 OpenAI，避免把未知平台带入账号查询。
// SelectAccountForTokenCount selects an account for a non-billable token-count
// request. It applies the normal platform, model, capability, and runtime
// eligibility checks without acquiring or waiting for a generation slot.
func (s *OpenAIGatewayService) SelectAccountForTokenCount(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	requiredCapability OpenAIEndpointCapability,
	platform string,
) (*Account, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	return s.selectAccountForModelWithExclusions(
		ctx,
		groupID,
		platform,
		sessionHash,
		requestedModel,
		nil,
		false,
		0,
		requiredCapability,
	)
}

func NormalizeOpenAICompatiblePlatform(platform string) string {
	return routing.NormalizeOpenAICompatiblePlatform(platform)
}

// noAvailableOpenAISelectionErrorForRouting 使用账号层模型 C/D 判断能力，同时保留 R 的对外错误语义。
func noAvailableOpenAISelectionErrorForRouting(ctx context.Context, requestedModel string, routingModel string, compactBlocked bool, accounts ...[]Account) error {
	return noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, requestedModel, routingModel, compactBlocked, "", accounts...)
}

// noAvailableOpenAISelectionErrorForRoutingWithDetails 仅在通用无账号错误中追加调度诊断；
// compact 能力错误和 fork 的模型业务错误继续保留原有类型与消息。
func noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx context.Context, requestedModel string, routingModel string, compactBlocked bool, details string, accounts ...[]Account) error {
	if compactBlocked {
		return ErrNoAvailableCompactAccounts
	}
	if len(accounts) > 0 && shouldUseGroupModelUnsupportedError(ctx, accounts[0], routingModel) {
		if err := newGroupModelUnsupportedError(PlatformOpenAI, requestedModel, accounts[0]); err != nil {
			return err
		}
	}
	message := "no available OpenAI accounts"
	if requestedModel != "" {
		message = fmt.Sprintf("no available OpenAI accounts supporting model: %s", requestedModel)
	}
	if details != "" {
		message += " (" + details + ")"
	}
	return openAINoAvailableSelectionError{message: message}
}

// openAINoAvailableSelectionError 保留原有可读消息，同时支持 errors.Is 统一分类。
type openAINoAvailableSelectionError struct {
	message string
}

func (e openAINoAvailableSelectionError) Error() string {
	return e.message
}

func (e openAINoAvailableSelectionError) Unwrap() error {
	return ErrNoAvailableAccounts
}

// openAIAccountSupportsRoutingModel 按当前入口的真实转发模式检查账号模型规则。
// HTTP Responses 自动透传只替换认证，因此不会执行账号普通映射和最终白名单。
func openAIAccountSupportsRoutingModel(ctx context.Context, account *Account, routingModel string) bool {
	routingModel = strings.TrimSpace(routingModel)
	if routingModel == "" {
		return true
	}
	if openAIHTTPPassthroughRoutingFromContext(ctx) && account != nil && account.IsOpenAIPassthroughEnabled() {
		return true
	}
	return account != nil && account.IsModelSupported(routingModel)
}

// allowsOpenAICompatibleCompact 统一读取 OpenAI 管理员开关和 Grok 固有的 Compact 资格。
func allowsOpenAICompatibleCompact(account *Account) bool {
	return account != nil && (account.IsGrok() || account.AllowsOpenAICompact())
}

// isOpenAICompatibleAccountEligibleForRequest 判断 OpenAI 兼容账号是否满足本次请求的调度条件。
// 检查内容包括：平台匹配、账号可用性、quota 自动暂停、spark 路由限制、模型支持及端点能力。
//
// 注意：对 spark 影子账号，调用方还须额外调用 parentHealthyForShadow(account, lookup)
// 检查母账号凭据可用性；该检查未内置于本函数，以避免注入 DB 依赖。
func isOpenAICompatibleAccountEligibleForRequest(ctx context.Context, account *Account, platform string, requestedModel string, requireCompact bool, requiredCapability OpenAIEndpointCapability) bool {
	return openAICompatibleAccountEligibilityFailureReason(ctx, account, platform, requestedModel, requireCompact, requiredCapability) == ""
}

// openAICompatibleAccountEligibilityFailureReason 在保留旧布尔判定的同时返回首个拦截原因。
// 负载批处理只使用该原因生成服务端无账号诊断，不改变实际准入行为。
func openAICompatibleAccountEligibilityFailureReason(ctx context.Context, account *Account, platform string, requestedModel string, requireCompact bool, requiredCapability OpenAIEndpointCapability) string {
	// fork 已按产品决策跳过 group profit control，这里只返回普通资格门的首个原因。
	return openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx, account, platform, requestedModel, requireCompact, requiredCapability)
}

func openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx context.Context, account *Account, platform string, requestedModel string, requireCompact bool, requiredCapability OpenAIEndpointCapability) string {
	platform = NormalizeOpenAICompatiblePlatform(platform)
	if account == nil {
		return "account_nil"
	}
	if account.Platform != platform || !account.IsOpenAICompatible() {
		return "platform_mismatch"
	}
	if !account.IsSchedulableForModelWithContext(ctx, requestedModel) {
		if account.IsSchedulable() {
			return "model_rate_limited"
		}
		return "not_schedulable"
	}
	if account.IsOpenAI() {
		if paused, reason := shouldAutoPauseOpenAIAccountByQuota(ctx, account); paused {
			// Debug level: this fires per-candidate on the scheduling hot path, so Info
			// would amplify into log spam once several accounts cross the threshold.
			slog.Debug("account_auto_paused_by_quota",
				"account_id", account.ID,
				"window", reason.window,
				"threshold", reason.threshold,
				"utilization", reason.utilization,
			)
			if reason.window != "" {
				return "quota_auto_pause_" + reason.window
			}
			return "quota_auto_pause"
		}
	}
	if account.IsGrok() {
		if paused, reason := shouldAutoPauseGrokAccountByQuota(account); paused {
			slog.Debug("grok_account_auto_paused_by_quota",
				"account_id", account.ID,
				"window", reason.window,
				"threshold", reason.threshold,
				"utilization", reason.utilization,
			)
			if reason.window != "" {
				return "quota_auto_pause_" + reason.window
			}
			return "quota_auto_pause"
		}
	}
	if !openAIAccountSupportsRoutingModel(ctx, account, requestedModel) {
		return "model_not_supported"
	}
	if !supportsOpenAIRequestCapability(ctx, account, requiredCapability) {
		if account.IsGrok() && requiredCapability == OpenAIEndpointCapabilityGrokMediaGeneration {
			_, reason := account.GrokMediaGenerationEligibility()
			slog.Debug("grok_media_account_ineligible", "account_id", account.ID, "reason", reason)
		}
		return "capability_mismatch"
	}
	if requireCompact && !allowsOpenAICompatibleCompact(account) {
		return "compact_unsupported"
	}
	return ""
}

type openAIQuotaAutoPauseDecision struct {
	window      string
	threshold   float64
	utilization float64
}

func shouldAutoPauseGrokAccountByQuota(account *Account) (bool, openAIQuotaAutoPauseDecision) {
	if account == nil || !account.IsGrok() || account.Type != AccountTypeOAuth {
		return false, openAIQuotaAutoPauseDecision{}
	}
	snapshot, err := grokQuotaSnapshotFromExtra(account.Extra)
	if err != nil || snapshot == nil {
		return false, openAIQuotaAutoPauseDecision{}
	}
	now := time.Now()
	if grokQuotaSnapshotStaleForPause(snapshot, now) {
		return false, openAIQuotaAutoPauseDecision{}
	}
	if grokQuotaRetryAfterActive(snapshot, now) {
		return true, openAIQuotaAutoPauseDecision{window: "retry_after", threshold: 1, utilization: 1}
	}
	if paused, decision := shouldAutoPauseGrokQuotaWindow("requests", snapshot.Requests, now); paused {
		return true, decision
	}
	if paused, decision := shouldAutoPauseGrokQuotaWindow("tokens", snapshot.Tokens, now); paused {
		return true, decision
	}
	return false, openAIQuotaAutoPauseDecision{}
}

func grokQuotaRetryAfterActive(snapshot *xai.QuotaSnapshot, now time.Time) bool {
	if snapshot == nil || snapshot.RetryAfterSeconds == nil || *snapshot.RetryAfterSeconds <= 0 {
		return false
	}
	if strings.TrimSpace(snapshot.UpdatedAt) == "" {
		return true
	}
	updatedAt, err := parseTime(snapshot.UpdatedAt)
	if err != nil {
		return true
	}
	retryAfterUntil := updatedAt.Add(time.Duration(*snapshot.RetryAfterSeconds) * time.Second)
	return now.Before(retryAfterUntil)
}

func shouldAutoPauseGrokQuotaWindow(name string, window *xai.QuotaWindow, now time.Time) (bool, openAIQuotaAutoPauseDecision) {
	if window == nil || window.Limit == nil || window.Remaining == nil || *window.Limit <= 0 {
		return false, openAIQuotaAutoPauseDecision{}
	}
	if window.ResetUnix != nil && *window.ResetUnix > 0 && !now.Before(time.Unix(*window.ResetUnix, 0)) {
		return false, openAIQuotaAutoPauseDecision{}
	}
	utilization := float64(*window.Limit-*window.Remaining) / float64(*window.Limit)
	if *window.Remaining <= 0 || utilization >= 1 {
		return true, openAIQuotaAutoPauseDecision{window: name, threshold: 1, utilization: utilization}
	}
	return false, openAIQuotaAutoPauseDecision{}
}

func grokQuotaSnapshotStaleForPause(snapshot *xai.QuotaSnapshot, now time.Time) bool {
	if snapshot == nil || strings.TrimSpace(snapshot.UpdatedAt) == "" {
		return false
	}
	updatedAt, err := parseTime(snapshot.UpdatedAt)
	if err != nil {
		return false
	}
	return now.Sub(updatedAt) >= openAICodexAutoPauseStaleAfter
}

func shouldAutoPauseOpenAIAccountByQuota(ctx context.Context, account *Account) (bool, openAIQuotaAutoPauseDecision) {
	return evaluateOpenAIQuotaAutoPause(ctx, account, time.Now())
}

// EvaluateOpenAIQuotaAutoPause 返回 OpenAI 账号当前是否因 5h/7d 配额阈值被自动暂停。
// 这是给展示层和容量统计使用的无副作用派生判断，不能在这里写数据库或修改调度缓存。
func EvaluateOpenAIQuotaAutoPause(ctx context.Context, account *Account) bool {
	paused, _ := evaluateOpenAIQuotaAutoPause(ctx, account, time.Now())
	return paused
}

// evaluateOpenAIQuotaAutoPause 只转换旧账号与请求设置，不复制健康裁决。
func evaluateOpenAIQuotaAutoPause(ctx context.Context, v *Account, now time.Time) (bool, openAIQuotaAutoPauseDecision) {
	if v == nil {
		return false, openAIQuotaAutoPauseDecision{}
	}
	paused, d := accountcore.EvaluateQuotaAutoPause(v.Platform, v.Extra, openAIQuotaAutoPauseSettingsFromContext(ctx), now)
	return paused, openAIQuotaAutoPauseDecision{window: d.Window, threshold: d.Threshold, utilization: d.Utilization}
}

// resolveAccountExtraNumber 委托账号健康纯规则。
func resolveAccountExtraNumber(extra map[string]any, keys ...string) (float64, bool) {
	return accountcore.ResolveAccountExtraNumber(extra, keys...)
}

// openAIQuotaWindowReset 委托账号健康纯规则。
func openAIQuotaWindowReset(extra map[string]any, window string, now time.Time) bool {
	return accountcore.OpenAIQuotaWindowReset(extra, window, now)
}

type openAIQuotaAutoPauseCtxKey struct{}

// WithOpenAIQuotaAutoPauseSettings 把 OpenAI 配额自动暂停全局设置放进 context，
// 让调度、展示和容量统计复用完全一致的阈值解析逻辑。
func WithOpenAIQuotaAutoPauseSettings(ctx context.Context, settings OpsOpenAIAccountQuotaAutoPauseSettings) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIQuotaAutoPauseCtxKey{}, settings)
}

func withOpenAIQuotaAutoPauseSettings(ctx context.Context, settings OpsOpenAIAccountQuotaAutoPauseSettings) context.Context {
	return WithOpenAIQuotaAutoPauseSettings(ctx, settings)
}

func openAIQuotaAutoPauseSettingsFromContext(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings {
	if ctx == nil {
		return OpsOpenAIAccountQuotaAutoPauseSettings{}
	}
	settings, _ := ctx.Value(openAIQuotaAutoPauseCtxKey{}).(OpsOpenAIAccountQuotaAutoPauseSettings)
	return settings
}

func (s *OpenAIGatewayService) withOpenAIQuotaAutoPauseContext(ctx context.Context) context.Context {
	if s == nil || s.settingService == nil {
		return ctx
	}
	return withOpenAIQuotaAutoPauseSettings(ctx, s.settingService.GetOpenAIQuotaAutoPauseSettings(ctx))
}

// prioritizeEnabledOpenAICompactAccounts 先尝试已启用的账号。
// 快照显示关闭的账号仍保留在末尾，最终以数据库复核结果决定资格。

type openAIHTTPPassthroughRoutingContextKey struct{}

// WithOpenAIHTTPPassthroughRouting 标记当前请求会按账号配置进入 HTTP Responses 自动透传分支。
func WithOpenAIHTTPPassthroughRouting(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIHTTPPassthroughRoutingContextKey{}, true)
}

func openAIHTTPPassthroughRoutingFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(openAIHTTPPassthroughRoutingContextKey{}).(bool)
	return enabled
}

// resolveOpenAIAccountUpstreamModelForRequest 按真实转发顺序解析 OpenAI 最终上游模型。
// HTTP 自动透传只执行 compact 专属映射；其它入口依次执行账号映射、compact 映射和 OAuth 模型归一化。
func resolveOpenAIAccountUpstreamModelForRequest(account *Account, requestedModel string, requireCompact bool, passthrough ...bool) string {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ""
	}
	allowHTTPPassthrough := len(passthrough) > 0 && passthrough[0]
	if shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		upstreamModel := resolveOpenAIForwardModel(account, requestedModel, "")
		return normalizeOpenAIModelForUpstream(account, upstreamModel)
	}
	if account != nil && account.IsOpenAIPassthroughEnabled() {
		if requireCompact {
			return resolveOpenAICompactForwardModel(account, requestedModel)
		}
		return requestedModel
	}
	if allowHTTPPassthrough && account != nil && account.IsOpenAIPassthroughEnabled() {
		return requestedModel
	}
	if requireCompact && account != nil {
		if compactModel, matched := account.ResolveCompactMappedModel(requestedModel); matched {
			if compactModel = strings.TrimSpace(compactModel); compactModel != "" {
				return compactModel
			}
		}
	}

	upstreamModel := strings.TrimSpace(resolveOpenAIForwardModel(account, requestedModel, ""))
	if upstreamModel == "" {
		return ""
	}
	if requireCompact {
		compactModel := strings.TrimSpace(resolveOpenAICompactForwardModel(account, upstreamModel))
		if compactModel != "" && compactModel != upstreamModel {
			return compactModel
		}
	}
	return strings.TrimSpace(normalizeOpenAIModelForUpstream(account, upstreamModel))
}

// ResolveOpenAIAccountUpstreamModelForRequest 暴露调度层实际采用的账号模型。
func ResolveOpenAIAccountUpstreamModelForRequest(account *Account, requestedModel string, requireCompact bool) string {
	return resolveOpenAIAccountUpstreamModelForRequest(account, requestedModel, requireCompact)
}

// resolveOpenAIForwardMappedModels 返回计费模型与最终上游模型，保持两条链路一致。
func resolveOpenAIForwardMappedModels(account *Account, requestedModel string, requireCompact bool) (billingModel, upstreamModel string) {
	requestedModel = strings.TrimSpace(requestedModel)
	if account != nil && account.IsOpenAIPassthroughEnabled() {
		billingModel = requestedModel
	} else if account != nil {
		billingModel = strings.TrimSpace(account.GetMappedModel(requestedModel))
	}
	if billingModel == "" {
		billingModel = requestedModel
	}
	upstreamModel = resolveOpenAIAccountUpstreamModelForRequest(account, requestedModel, requireCompact)
	if strings.TrimSpace(upstreamModel) == "" {
		upstreamModel = billingModel
	}
	return billingModel, upstreamModel
}

// resolveOpenAIErrorSchedulingModel 优先使用已观测的真实上游模型。
func resolveOpenAIErrorSchedulingModel(billingModel, upstreamModel string) string {
	if upstream := strings.TrimSpace(upstreamModel); upstream != "" {
		return upstream
	}
	return strings.TrimSpace(billingModel)
}

func (s *OpenAIGatewayService) selectAccountForModelWithExclusions(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, requireCompact bool, stickyAccountID int64, requiredCapability OpenAIEndpointCapability) (*Account, error) {
	routingModel := s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	return s.selectAccountForModelWithExclusionsForRouting(ctx, groupID, platform, sessionHash, requestedModel, routingModel, excludedIDs, requireCompact, stickyAccountID, requiredCapability)
}

// selectAccountForModelWithExclusionsForRouting 使用已解析的账号层模型执行旧版调度。
func (s *OpenAIGatewayService) selectAccountForModelWithExclusionsForRouting(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, stickyAccountID int64, requiredCapability OpenAIEndpointCapability) (*Account, error) {
	core, scope := (&defaultOpenAIAccountScheduler{service: s}).platformSelector()
	selected, err := core.SelectBasicOnly(ctx, scheduler.PlatformSelectionInput{GroupID: groupID, Platform: platform, SessionHash: sessionHash, RequestedModel: requestedModel, RoutingModel: routingModel, ExcludedIDs: excludedIDs, RequireCompact: requireCompact, StickyAccountID: stickyAccountID, RequiredCapability: requiredCapability})
	return scope.oldAccount(selected), err
}

// tryStickySessionHit 尝试从粘性会话获取账号。
// 如果命中且账号可用则返回账号；如果账号不可用则清理会话并返回 nil。
//
// tryStickySessionHit attempts to get account from sticky session.
// Returns account if hit and usable; clears session and returns nil if account is unavailable.

// selectBestAccount 从候选账号中选择最佳账号（优先级 + LRU）。
// 返回 nil 表示无可用账号。
//
// selectBestAccount selects the best account from candidates (priority + LRU).
// Returns nil if no available account. The second return reports whether at
// least one candidate was filtered out solely because it lacks compact support
// (only meaningful when requireCompact=true).

// isBetterAccount 判断 candidate 是否比 current 更优。
// 规则：优先级更高（数值更小）优先；同优先级时，未使用过的优先，其次是最久未使用的。
//
// isBetterAccount checks if candidate is better than current.
// Rules: higher priority (lower value) wins; same priority: never used > least recently used.

// SelectAccountWithLoadAwareness selects an account with load-awareness and wait plan.
func (s *OpenAIGatewayService) SelectAccountWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*AccountSelectionResult, error) {
	return s.selectAccountWithLoadAwareness(s.withOpenAIQuotaAutoPauseContext(ctx), groupID, PlatformOpenAI, sessionHash, requestedModel, excludedIDs, false, "")
}

func (s *OpenAIGatewayService) selectAccountWithLoadAwareness(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, requireCompact bool, requiredCapability OpenAIEndpointCapability) (*AccountSelectionResult, error) {
	routingModel := s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	return s.selectAccountWithLoadAwarenessForRouting(ctx, groupID, platform, sessionHash, requestedModel, routingModel, excludedIDs, requireCompact, requiredCapability)
}

// selectAccountWithLoadAwarenessForRouting 使用已解析的账号层模型执行负载感知调度。
func (s *OpenAIGatewayService) selectAccountWithLoadAwarenessForRouting(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, requiredCapability OpenAIEndpointCapability) (*AccountSelectionResult, error) {
	core, scope := (&defaultOpenAIAccountScheduler{service: s}).platformSelector()
	selected, err := core.SelectBasic(ctx, scheduler.PlatformSelectionInput{GroupID: groupID, Platform: platform, SessionHash: sessionHash, RequestedModel: requestedModel, RoutingModel: routingModel, ExcludedIDs: excludedIDs, RequireCompact: requireCompact, RequiredCapability: requiredCapability})
	return scope.restore(selected), err
}

func (s *OpenAIGatewayService) listSchedulableAccounts(ctx context.Context, groupID *int64, platform string) ([]Account, error) {
	platform = NormalizeOpenAICompatiblePlatform(platform)
	if s.schedulerSnapshot != nil {
		accounts, _, err := s.schedulerSnapshot.ListSchedulableAccounts(ctx, groupID, platform, false)
		if err != nil {
			return accounts, err
		}
		accounts = s.filterOpenAIAccountsBySchedulingThreshold(ctx, accounts)
		if platform == PlatformGrok {
			accounts = s.filterGrokFreeQuotaAccountsForOpenAI(ctx, accounts)
		}
		return accounts, nil
	}
	var accounts []Account
	var err error
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		accounts, err = s.accountRepo.ListSchedulableByPlatform(ctx, platform)
	} else if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	} else {
		accounts, err = s.accountRepo.ListSchedulableUngroupedByPlatform(ctx, platform)
	}
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}
	accounts = s.filterOpenAIAccountsBySchedulingThreshold(ctx, accounts)
	if platform == PlatformGrok {
		accounts = s.filterGrokFreeQuotaAccountsForOpenAI(ctx, accounts)
	}
	return accounts, nil
}

func (s *OpenAIGatewayService) tryAcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (*AcquireResult, error) {
	if isAdvancedSchedulerNoSlotSelection(ctx) {
		return &AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	if s.concurrencyService == nil {
		return &AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	return s.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
}

func (s *OpenAIGatewayService) resolveFreshSchedulableOpenAIAccount(ctx context.Context, account *Account, platform string, requestedModel string, requireCompact bool, requiredCapability OpenAIEndpointCapability) *Account {
	if account == nil {
		return nil
	}
	platform = NormalizeOpenAICompatiblePlatform(platform)

	fresh := account
	if s.schedulerSnapshot != nil {
		current, err := s.getSchedulableAccount(ctx, account.ID)
		if err != nil || current == nil {
			return nil
		}
		fresh = current
	}

	if !isOpenAICompatibleAccountEligibleForRequest(ctx, fresh, platform, requestedModel, requireCompact, requiredCapability) {
		return nil
	}
	if !s.shadowProtocolsAllowed(ctx, fresh) || !parentHealthyForShadow(fresh, s.parentAccountLookup(ctx)) {
		return nil
	}
	if s.isOpenAIAccountRequestRuntimeBlocked(fresh, requestedModel) {
		return nil
	}
	if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, fresh) {
		return nil
	}
	if s.isOpenAIProxyStreamQuarantined(ctx, fresh) {
		return nil
	}
	return fresh
}

// parentAccountLookup 返回供 parentHealthyForShadow 使用的母账号解析闭包:经 accountRepo
// 按 ID 取当前 Account(repo 为空时 fail-closed 返回 nil)。统一调度/粘连各路径的母账号解析,
// 取代各调用点重复内联的同一闭包(历史上 recheck 等路径还漏写过 accountRepo==nil 守卫)。
// L2 候选循环改用带 per-pass 缓存的 parentLookupL2,不走此方法。
func (s *OpenAIGatewayService) parentAccountLookup(ctx context.Context) func(int64) *Account {
	return func(id int64) *Account {
		if s.accountRepo == nil {
			return nil
		}
		a, _ := s.accountRepo.GetByID(ctx, id)
		return a
	}
}

func (s *OpenAIGatewayService) recheckSelectedOpenAIAccountFromDB(ctx context.Context, account *Account, groupID *int64, platform string, requestedModel string, requireCompact bool, requiredCapability OpenAIEndpointCapability) *Account {
	if account == nil {
		return nil
	}
	platform = NormalizeOpenAICompatiblePlatform(platform)
	if s.schedulerSnapshot == nil || s.accountRepo == nil {
		if !s.openAIAccountPassesPrivacyRequirement(ctx, groupID, account) {
			return nil
		}
		if !isOpenAICompatibleAccountEligibleForRequest(ctx, account, platform, requestedModel, requireCompact, requiredCapability) {
			return nil
		}
		if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, account) {
			return nil
		}
		if !s.shadowProtocolsAllowed(ctx, account) || !parentHealthyForShadow(account, s.parentAccountLookup(ctx)) {
			return nil
		}
		if s.isOpenAIProxyStreamQuarantined(ctx, account) {
			return nil
		}
		return account
	}

	latest, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil || latest == nil {
		return nil
	}
	if !s.openAIAccountMatchesSchedulingGroup(latest, groupID) {
		return nil
	}
	if !s.openAIAccountPassesPrivacyRequirement(ctx, groupID, latest) {
		return nil
	}
	if !isOpenAICompatibleAccountEligibleForRequest(ctx, latest, platform, requestedModel, requireCompact, requiredCapability) {
		return nil
	}
	if !s.shadowProtocolsAllowed(ctx, latest) || !parentHealthyForShadow(latest, s.parentAccountLookup(ctx)) {
		return nil
	}
	if s.isOpenAIAccountRequestRuntimeBlocked(latest, requestedModel) {
		return nil
	}
	if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, latest) {
		return nil
	}
	if s.isOpenAIProxyStreamQuarantined(ctx, latest) {
		return nil
	}
	return latest
}

func (s *OpenAIGatewayService) openAIAccountMatchesSchedulingGroup(account *Account, groupID *int64) bool {
	if s != nil && s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return account != nil
	}
	return openAIStickyAccountMatchesGroup(account, groupID)
}

// openAIAccountPassesPrivacyRequirement 判断账号是否满足当前分组的隐私资格。
func (s *OpenAIGatewayService) openAIAccountPassesPrivacyRequirement(ctx context.Context, groupID *int64, account *Account) bool {
	return account != nil && (!s.openAIGroupRequiresPrivacySet(ctx, groupID) || account.IsPrivacySet())
}

func (s *OpenAIGatewayService) getSchedulableAccount(ctx context.Context, accountID int64) (*Account, error) {
	var (
		account *Account
		err     error
	)
	if s.schedulerSnapshot != nil {
		account, err = s.schedulerSnapshot.GetAccount(ctx, accountID)
	} else {
		account, err = s.accountRepo.GetByID(ctx, accountID)
	}
	if err != nil || account == nil {
		return account, err
	}
	if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, account) {
		return nil, nil
	}
	// 即使关闭高级调度器，旧版粘性路由仍必须对 Grok OAuth 执行免费层门禁。
	if account.IsGrok() {
		if gated := s.filterGrokFreeQuotaAccountsForOpenAI(ctx, []Account{*account}); len(gated) == 0 {
			return nil, nil
		}
	}
	return account, nil
}

// filterGrokFreeQuotaAccountsForOpenAI 为 OpenAI 兼容旧版选择路径应用与
// GatewayService 和高级调度器一致的本地免费层软性门禁。
func (s *OpenAIGatewayService) filterGrokFreeQuotaAccountsForOpenAI(ctx context.Context, accounts []Account) []Account {
	if s == nil {
		return accounts
	}
	return filterGrokFreeQuotaAccountsCore(ctx, s.cfg, s.usageLogRepo, &openaiGrokFreeQuotaGateCache, accounts)
}

func (s *OpenAIGatewayService) filterOpenAIAccountsBySchedulingThreshold(ctx context.Context, accounts []Account) []Account {
	if len(accounts) == 0 {
		return accounts
	}

	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, &accounts[i]) {
			continue
		}
		filtered = append(filtered, accounts[i])
	}
	return filtered
}

func (s *OpenAIGatewayService) isOpenAIAccountBlockedBySchedulingThreshold(ctx context.Context, account *Account) bool {
	if s == nil || s.rateLimitService == nil || account == nil {
		return false
	}
	return s.rateLimitService.ApplyAccountSchedulingThreshold(ctx, account)
}

func (s *OpenAIGatewayService) hydrateSelectedAccount(ctx context.Context, account *Account) (*Account, error) {
	if account == nil || s.schedulerSnapshot == nil {
		return account, nil
	}
	hydrated, err := s.schedulerSnapshot.GetAccount(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, fmt.Errorf("selected openai account %d not found during hydration", account.ID)
	}
	return hydrated, nil
}

func (s *OpenAIGatewayService) newSelectionResult(ctx context.Context, account *Account, acquired bool, release func(), waitPlan *AccountWaitPlan) (*AccountSelectionResult, error) {
	hydrated, err := s.hydrateSelectedAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	return &AccountSelectionResult{
		Account:     hydrated,
		Acquired:    acquired,
		ReleaseFunc: release,
		WaitPlan:    waitPlan,
	}, nil
}

func (s *OpenAIGatewayService) newAcquiredSelectionResult(ctx context.Context, account *Account, release func()) (*AccountSelectionResult, error) {
	selection, err := s.newSelectionResult(ctx, account, true, release, nil)
	if err != nil && release != nil {
		release()
	}
	return selection, err
}

func (s *OpenAIGatewayService) schedulingConfig() config.GatewaySchedulingConfig {
	if s.cfg != nil {
		return s.cfg.Gateway.Scheduling
	}
	return config.GatewaySchedulingConfig{
		StickySessionMaxWaiting:  3,
		StickySessionWaitTimeout: 45 * time.Second,
		FallbackWaitTimeout:      30 * time.Second,
		FallbackMaxWaiting:       100,
		LoadBatchEnabled:         true,
		SlotCleanupInterval:      30 * time.Second,
	}
}
