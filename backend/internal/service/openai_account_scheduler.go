package service

import (
	context "context"
	errors "errors"
	fmt "fmt"
	slog "log/slog"
	strings "strings"
	sync "sync"
	time "time"

	config "github.com/TokenFlux/TokenRouter/internal/config"
	ctxkey "github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

const (
	openAIAccountScheduleLayerPreviousResponse = "previous_response_id"
	openAIAccountScheduleLayerGuardianParent   = "guardian_parent"
	openAIAccountScheduleLayerSessionSticky    = "session_hash"
	openAIAccountScheduleLayerLoadBalance      = "load_balance"
)

// quota headroom 只影响调度偏好，不应像自动暂停那样强制屏蔽账号。
// 缺少或陈旧快照时使用中性分，避免没有 header 数据的账号被错误降权。
const (
	openAIQuotaHeadroomNeutralFactor      = 0.5
	openAIQuotaHeadroomSecondaryLowRemain = 0.10
	openAIQuotaHeadroomSnapshotStaleAfter = 8 * time.Hour
)

type advancedSchedulerRuntimeSettings struct {
	stickyWeightedEnabled       bool
	subscriptionPriorityEnabled bool
	lbTopKOverride              int
	weightOverrides             map[string]float64
	ewmaErrorRateAlpha          float64
	ewmaErrorRateAlphaSet       bool
	ewmaTTFTAlpha               float64
	ewmaTTFTAlphaSet            bool
	stickyEscapeEnabled         bool
	stickyEscapeEnabledSet      bool
	stickyEscapeTTFTMs          float64
	stickyEscapeTTFTMsSet       bool
	stickyEscapeErrorRate       float64
	stickyEscapeErrorRateSet    bool
	stickyEscape                advancedStickyEscapeConfig
}

type OpenAIAccountScheduleRequest struct {
	GroupID                 *int64
	Platform                string
	SessionHash             string
	StickyAccountID         int64
	GuardianParentAccountID int64
	StickyPreviousAccountID int64
	StickyWeighted          bool
	SubscriptionPriority    bool
	PreserveStickyBinding   bool
	RequirePrivacySet       bool
	PreviousResponseID      string
	PreviousResponseCanMove bool
	RequestedModel          string // 客户端请求模型 R，用于限制、错误和会话语义。
	RoutingModel            string // 账号层模型：普通请求为 C，Messages 为分组映射后的 D。
	RequiredTransport       OpenAIUpstreamTransport
	RequiredCapability      OpenAIEndpointCapability
	RequiredImageCapability OpenAIImagesCapability
	RequireCompact          bool
	ExcludedIDs             map[int64]struct{}
	// AdvancedSchedulerFeedbackConfig 与 StickyEscapeConfig 固定本次请求使用的有效策略。
	AdvancedSchedulerFeedbackConfig advancedSchedulerFeedbackConfig
	StickyEscapeConfig              advancedStickyEscapeConfig
}

// routingModel 返回账号调度层使用的模型，并兼容未设置 RoutingModel 的旧调用方。
func (r OpenAIAccountScheduleRequest) routingModel() string {
	if model := strings.TrimSpace(r.RoutingModel); model != "" {
		return model
	}
	return r.RequestedModel
}

type OpenAIAccountScheduleDecision = scheduler.PlatformDecision

type OpenAIAccountSchedulerMetricsSnapshot = scheduler.PlatformMetricsSnapshot

type OpenAIAccountScheduler interface {
	Select(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error)
	ReportResult(accountID int64, success bool, firstTokenMs *int, feedback ...advancedSchedulerFeedbackConfig)
	ReportSwitch()
	SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot
}

type openAIAccountSchedulerMetrics struct{ scheduler.PlatformMetrics }

func (m *openAIAccountSchedulerMetrics) recordSelect(v OpenAIAccountScheduleDecision) {
	if m != nil {
		m.RecordSelect(v)
	}
}
func (m *openAIAccountSchedulerMetrics) recordSwitch() {
	if m != nil {
		m.RecordSwitch()
	}
}

// 兼容内部调用点的别名：OpenAI 是通用高级调度器的能力适配者之一。
type openAIAccountRuntimeStats = advancedAccountRuntimeStats

func newOpenAIAccountRuntimeStats() *advancedAccountRuntimeStats {
	return newAdvancedAccountRuntimeStats()
}

type defaultOpenAIAccountScheduler struct {
	service                *OpenAIGatewayService
	metrics                openAIAccountSchedulerMetrics
	stats                  *openAIAccountRuntimeStats
	grokFreeQuotaGateCache sync.Map // key: int64(accountID), value: grokFreeQuotaGateCacheEntry
}

type advancedStickyEscapeConfig struct {
	enabled   bool
	ttftMs    float64
	errorRate float64
}

func newDefaultOpenAIAccountScheduler(service *OpenAIGatewayService, stats *openAIAccountRuntimeStats) OpenAIAccountScheduler {
	if stats == nil {
		stats = newOpenAIAccountRuntimeStats()
	}
	return &defaultOpenAIAccountScheduler{
		service: service,
		stats:   stats,
	}
}

func (s *defaultOpenAIAccountScheduler) Select(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	core, scope := s.platformSelector()
	v, decision, err := core.Select(ctx, platformSelectionInput(req))
	return scope.restore(v), decision, err
}

// hasOpenAIAccountGroupMetadata 判断账号是否声明了分组归属信息。
func hasOpenAIAccountGroupMetadata(account *Account) bool {
	return account != nil && (len(account.GroupIDs) > 0 || len(account.AccountGroups) > 0)
}

// openAIStickyAccountMatchesGroup 校验粘性会话账号是否仍属于当前请求分组。
func openAIStickyAccountMatchesGroup(account *Account, groupID *int64) bool {
	if account == nil {
		return false
	}
	if groupID == nil {
		return len(account.AccountGroups) == 0 && len(account.GroupIDs) == 0
	}
	for _, accountGroupID := range account.GroupIDs {
		if accountGroupID == *groupID {
			return true
		}
	}
	for _, accountGroup := range account.AccountGroups {
		if accountGroup.GroupID == *groupID {
			return true
		}
	}
	return false
}

func shouldEscapeAdvancedStickyAccount(stats *advancedAccountRuntimeStats, id int64, cfg advancedStickyEscapeConfig) (string, float64, float64, bool) {
	return scheduler.ShouldEscapeSticky(schedulerStats(stats), id, policy.StickyEscapeConfig{Enabled: cfg.enabled, TtftMs: cfg.ttftMs, ErrorRate: cfg.errorRate})
}

func (s *defaultOpenAIAccountScheduler) shouldEscapeStickyAccount(accountID int64, cfg advancedStickyEscapeConfig) (reason string, errorRate float64, ttft float64, shouldEscape bool) {
	if s == nil {
		return "", 0, 0, false
	}
	return shouldEscapeAdvancedStickyAccount(s.stats, accountID, cfg)
}

// 以下别名保留 OpenAI 适配层的局部语义；底层实现由通用高级调度核心共享。
type openAIAccountCandidateScore = advancedSchedulerCandidateScore

func isOpenAIAccountCandidateBetter(left, right openAIAccountCandidateScore) bool {
	return isAdvancedSchedulerCandidateBetter(left, right)
}

func selectTopKOpenAICandidates(candidates []openAIAccountCandidateScore, topK int) []openAIAccountCandidateScore {
	return selectTopKAdvancedSchedulerCandidates(candidates, topK)
}

func buildOpenAIWeightedSelectionOrder(candidates []openAIAccountCandidateScore, req OpenAIAccountScheduleRequest) []openAIAccountCandidateScore {
	return buildAdvancedWeightedSelectionOrder(candidates, advancedSchedulerSelectionInput{
		GroupID:                 req.GroupID,
		SessionHash:             req.SessionHash,
		PreviousResponseID:      req.PreviousResponseID,
		RequestedModel:          req.RequestedModel,
		StickyAccountID:         req.StickyAccountID,
		StickyPreviousAccountID: req.StickyPreviousAccountID,
		StickyWeighted:          req.StickyWeighted,
	})
}

// 兼容旧测试和基准的命名，生产代码使用通用高级调度器随机源。
type openAISelectionRNG = advancedSchedulerRNG

func newOpenAISelectionRNG(seed uint64) openAISelectionRNG {
	return newAdvancedSchedulerRNG(seed)
}

func deriveOpenAISelectionSeed(req OpenAIAccountScheduleRequest) uint64 {
	return deriveAdvancedSchedulerSelectionSeed(advancedSchedulerSelectionInput{
		GroupID:                 req.GroupID,
		SessionHash:             req.SessionHash,
		PreviousResponseID:      req.PreviousResponseID,
		RequestedModel:          req.RequestedModel,
		StickyAccountID:         req.StickyAccountID,
		StickyPreviousAccountID: req.StickyPreviousAccountID,
		StickyWeighted:          req.StickyWeighted,
	})
}

// reasons 延迟分配，正常选中账号的热路径不会产生额外 map 分配。

// summary 生成顺序稳定的排除统计，便于日志聚合和问题定位。

func (s *defaultOpenAIAccountScheduler) isAccountTransportCompatible(account *Account, requiredTransport OpenAIUpstreamTransport) bool {
	if requiredTransport == OpenAIUpstreamTransportAny || requiredTransport == OpenAIUpstreamTransportHTTPSSE {
		return true
	}
	if s == nil || s.service == nil {
		return false
	}
	return s.service.isOpenAIAccountTransportCompatible(account, requiredTransport)
}

func (s *defaultOpenAIAccountScheduler) lookupShadowParentAccount(ctx context.Context, id int64) *Account {
	if s == nil || s.service == nil {
		return nil
	}
	if s.service.schedulerSnapshot != nil {
		if account, err := s.service.schedulerSnapshot.GetAccount(ctx, id); err == nil && account != nil {
			return account
		}
	}
	if s.service.accountRepo == nil {
		return nil
	}
	account, _ := s.service.accountRepo.GetByID(ctx, id)
	return account
}

func (s *defaultOpenAIAccountScheduler) isAccountRequestCompatible(ctx context.Context, account *Account, req OpenAIAccountScheduleRequest) bool {
	compatible, _ := s.isAccountRequestCompatibleReason(ctx, account, req)
	return compatible
}

// isAccountRequestCompatibleReason 返回账号是否兼容，并在拒绝时标明具体门禁原因。
func (s *defaultOpenAIAccountScheduler) isAccountRequestCompatibleReason(ctx context.Context, account *Account, req OpenAIAccountScheduleRequest) (bool, string) {
	if s != nil && s.service != nil && !s.service.shadowProtocolsAllowed(ctx, account) {
		return false, "parent_protocol_unavailable"
	}
	if !account.allowsProtocolRequest(ctx) {
		return false, "protocol_unavailable"
	}
	if account == nil {
		return false, "account_nil"
	}
	if req.RequirePrivacySet && !account.IsPrivacySet() {
		return false, "privacy_not_set"
	}
	if s != nil && s.service != nil && s.service.isOpenAIAccountRequestRuntimeBlocked(account, req.routingModel()) {
		return false, "runtime_blocked"
	}
	if s != nil && s.service != nil && s.service.isOpenAIProxyStreamQuarantined(ctx, account) {
		return false, "proxy_stream_quarantined"
	}
	// 配额自动暂停也必须在初始过滤阶段执行。否则 TopK 候选池可能被已暂停账号占满，
	// 后续 fresh/DB 复查无法触达落在 TopK 之外的健康账号，最终在存在健康账号时
	// 仍表现为“无可用账号”。
	if paused, decision := shouldAutoPauseOpenAIAccountByQuota(ctx, account); paused {
		reason := "quota_auto_pause"
		if decision.window != "" {
			reason += "_" + decision.window
		}
		return false, reason
	}
	// 母账号健康联动：影子账号的凭据来自母账号，母账号不可调度时影子也不应被选中。
	// Parent-health gate: shadow borrows the parent's credentials; an unschedulable
	// parent must block the shadow across all scheduler paths.
	if !parentHealthyForShadow(account, func(id int64) *Account {
		return s.lookupShadowParentAccount(ctx, id)
	}) {
		return false, "shadow_parent_unhealthy"
	}
	if !openAIAccountSupportsRoutingModel(ctx, account, req.routingModel()) {
		return false, "model_not_supported"
	}
	if req.GroupID != nil && s != nil && s.service != nil &&
		s.service.needsUpstreamChannelRestrictionCheck(ctx, req.GroupID) &&
		s.service.isUpstreamRoutingModelRestrictedByChannel(ctx, *req.GroupID, account, req.routingModel(), req.RequireCompact) {
		return false, "channel_upstream_restricted"
	}
	if !accountSupportsOpenAICapabilities(ctx, account, req.RequiredCapability, req.RequiredImageCapability) {
		return false, "capability_mismatch"
	}
	return true, ""
}

func (s *defaultOpenAIAccountScheduler) ReportResult(accountID int64, success bool, firstTokenMs *int, feedback ...advancedSchedulerFeedbackConfig) {
	if s == nil || s.stats == nil {
		return
	}
	s.stats.report(accountID, success, firstTokenMs, feedback...)
}

func (s *defaultOpenAIAccountScheduler) ReportSwitch() {
	if s == nil {
		return
	}
	s.metrics.recordSwitch()
}

func (s *defaultOpenAIAccountScheduler) SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	if s == nil {
		return OpenAIAccountSchedulerMetricsSnapshot{}
	}
	return s.metrics.Snapshot(s.stats.size())
}

func (s *OpenAIGatewayService) advancedSchedulerSettingRepo() SettingRepository {
	if s == nil || s.rateLimitService == nil || s.rateLimitService.settingService == nil {
		return nil
	}
	return s.rateLimitService.settingService.settingRepo
}

func (s *OpenAIGatewayService) advancedSchedulerRuntimeSettings(ctx context.Context) advancedSchedulerRuntimeSettings {
	return legacySchedulerRuntimeSettings(SchedulerSettingsRuntime().Load(ctx, s.advancedSchedulerSettingRepo(), schedulerRuntimeSettings(s.advancedSchedulerProcessRuntimeSettings())))
}

// advancedSchedulerProcessRuntimeSettings 返回高级调度器的进程级默认运行参数。
func (s *OpenAIGatewayService) advancedSchedulerProcessRuntimeSettings() advancedSchedulerRuntimeSettings {
	settings := advancedSchedulerRuntimeSettings{
		ewmaErrorRateAlpha: defaultAdvancedSchedulerErrorRateAlpha,
		ewmaTTFTAlpha:      defaultAdvancedSchedulerTTFTAlpha,
		stickyEscape:       resolveAdvancedStickyEscapeConfig(nil),
	}
	if s != nil && s.cfg != nil {
		settings.ewmaErrorRateAlpha = s.cfg.Gateway.AdvancedScheduler.EWMAErrorRateAlpha
		settings.ewmaTTFTAlpha = s.cfg.Gateway.AdvancedScheduler.EWMATTFTAlpha
		settings.stickyEscape = resolveAdvancedStickyEscapeConfig(s.cfg)
	}
	return settings
}

func parseAdvancedSchedulerAlphaOverride(raw string, fallback float64) (float64, bool) {
	return scheduler.ParseAdvancedSchedulerAlphaOverride(raw, fallback)
}

func parseAdvancedSchedulerPositiveFloatOverride(raw string, fallback float64) (float64, bool) {
	return scheduler.ParseAdvancedSchedulerPositiveFloatOverride(raw, fallback)
}

func parseAdvancedSchedulerRateOverride(raw string, fallback float64) (float64, bool) {
	return scheduler.ParseAdvancedSchedulerRateOverride(raw, fallback)
}

func (s *OpenAIGatewayService) isAdvancedSchedulerStickyWeightedEnabled(ctx context.Context) bool {
	return s.advancedSchedulerRuntimeSettings(ctx).stickyWeightedEnabled
}

func advancedSchedulerRuntimeSettingKeys() []string {
	return scheduler.AdvancedSchedulerRuntimeSettingKeys()
}

func parsePositiveIntOverride(raw string) int { return scheduler.ParsePositiveIntOverride(raw) }

func parseAdvancedSchedulerWeightOverrides(values map[string]string) map[string]float64 {
	return scheduler.ParseAdvancedSchedulerWeightOverrides(values)
}

// groupUsesAdvancedScheduler 只依据最终解析后的分组决定调度模式。
func (s *OpenAIGatewayService) groupUsesAdvancedScheduler(ctx context.Context, groupID *int64) bool {
	if s == nil || groupID == nil || *groupID <= 0 {
		return false
	}
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) && group.ID == *groupID {
		return group.UsesAdvancedScheduler()
	}
	if s.schedulerSnapshot == nil {
		return false
	}
	group, err := s.schedulerSnapshot.GetGroupByID(ctx, *groupID)
	return err == nil && group != nil && group.UsesAdvancedScheduler()
}

func (s *OpenAIGatewayService) getOpenAIAccountScheduler(ctx context.Context, groupID *int64) OpenAIAccountScheduler {
	if s == nil {
		return nil
	}
	if !s.groupUsesAdvancedScheduler(ctx, groupID) {
		return nil
	}
	s.openaiSchedulerOnce.Do(func() {
		if s.rateLimitService != nil {
			s.openaiAccountStats = s.rateLimitService.AdvancedSchedulerRuntimeStats()
		}
		if s.openaiAccountStats == nil {
			s.openaiAccountStats = newOpenAIAccountRuntimeStats()
		}
		if s.openaiScheduler == nil {
			s.openaiScheduler = newDefaultOpenAIAccountScheduler(s, s.openaiAccountStats)
		}
	})
	return s.openaiScheduler
}

// ensureOpenAIAccountScheduler 仅供结果统计和诊断初始化调度器实例。
// 实际账号选择仍必须经 getOpenAIAccountScheduler 按分组模式门控。
func (s *OpenAIGatewayService) ensureOpenAIAccountScheduler() OpenAIAccountScheduler {
	if s == nil {
		return nil
	}
	s.openaiSchedulerOnce.Do(func() {
		if s.rateLimitService != nil {
			s.openaiAccountStats = s.rateLimitService.AdvancedSchedulerRuntimeStats()
		}
		if s.openaiAccountStats == nil {
			s.openaiAccountStats = newOpenAIAccountRuntimeStats()
		}
		if s.openaiScheduler == nil {
			s.openaiScheduler = newDefaultOpenAIAccountScheduler(s, s.openaiAccountStats)
		}
	})
	return s.openaiScheduler
}

func resetAdvancedSchedulerSettingCacheForTest() {
	schedulerSettingsRuntime.Store(scheduler.NewSettingsRuntime(LegacySchedulerDiagnostics()))
}

func (s *OpenAIGatewayService) SelectAccountWithScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requireCompact bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	return s.selectAccountWithScheduler(ctx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs, requiredTransport, "", "", requireCompact, PlatformOpenAI, false)
}

// SelectAccountWithSchedulerForCapability 按能力要求调度账号。
// previousResponseCanMove 表示首包 input 可自行重建工具续链，previous_response_id 允许跨账号迁移
// （粘性加权模式下改为加权偏好而非硬粘连）。
func (s *OpenAIGatewayService) SelectAccountWithSchedulerForCapability(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	platformOverride ...string,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	platform := PlatformOpenAI
	if len(platformOverride) > 0 {
		platform = platformOverride[0]
	}
	return s.selectAccountWithScheduler(ctx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs, requiredTransport, requiredCapability, "", requireCompact, platform, previousResponseCanMove)
}

// SelectAccountWithSchedulerForCapabilityAndRoutingModel 同时保留客户端模型 R 与已解析的账号层模型。
// 该入口供 /v1/messages 使用，使渠道限制按 R/C 检查，账号能力与账号映射按 D 检查。
func (s *OpenAIGatewayService) SelectAccountWithSchedulerForCapabilityAndRoutingModel(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	platformOverride ...string,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	platform := PlatformOpenAI
	if len(platformOverride) > 0 {
		platform = platformOverride[0]
	}
	routingModel = strings.TrimSpace(routingModel)
	if routingModel == "" {
		routingModel = s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	}
	return s.selectAccountWithSchedulerForRouting(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, "", requireCompact, platform, previousResponseCanMove)
}

func (s *OpenAIGatewayService) SelectAccountWithSchedulerForImages(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredCapability OpenAIImagesCapability,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	selection, decision, err := s.selectAccountWithScheduler(ctx, groupID, "", sessionHash, requestedModel, excludedIDs, OpenAIUpstreamTransportHTTPSSE, "", requiredCapability, false, PlatformOpenAI, false)
	if err == nil && selection != nil && selection.Account != nil {
		return selection, decision, nil
	}
	// 如果要求 native 能力（如指定了模型）但没有可用的 APIKey 账号，回退到 basic（OAuth 账号）
	if requiredCapability == OpenAIImagesCapabilityNative {
		return s.selectAccountWithScheduler(ctx, groupID, "", sessionHash, requestedModel, excludedIDs, OpenAIUpstreamTransportHTTPSSE, "", OpenAIImagesCapabilityBasic, false, PlatformOpenAI, false)
	}
	return selection, decision, err
}

func (s *OpenAIGatewayService) selectAccountWithScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requiredImageCapability OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	routingModel := s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	return s.selectAccountWithSchedulerForRouting(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
}

// selectAccountWithSchedulerForRouting 首次调度仍把隔离代理当作不可用；只有因容量耗尽
// 失败且熔断器确实在隔离代理时，才用同一账号层模型重跑一次并忽略隔离。这样健康代理
// 始终优先，同时避免共享代理场景被熔断器清空全部容量。
func (s *OpenAIGatewayService) selectAccountWithSchedulerForRouting(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requiredImageCapability OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	originalGroupID := derefGroupID(groupID)
	resolvedCtx, resolvedGroupID, err := s.resolveOpenAISchedulerGroup(ctx, groupID)
	if err != nil {
		return nil, OpenAIAccountScheduleDecision{}, err
	}
	ctx = resolvedCtx
	groupID = resolvedGroupID
	if derefGroupID(groupID) != originalGroupID {
		// 回退后的分组可能有不同渠道映射，必须重新解析账号层模型。
		routingModel = s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	}
	selection, decision, err := s.selectAccountWithSchedulerForRoutingOnce(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
	if err == nil || openAIProxyStreamQuarantineBypassed(ctx) {
		return selection, decision, err
	}
	if !errors.Is(err, ErrNoAvailableAccounts) && !errors.Is(err, ErrNoAvailableCompactAccounts) {
		return selection, decision, err
	}
	// 断流熔断器只隔离 OpenAI 平台账号，其他兼容平台不应触发第二次调度。
	if NormalizeOpenAICompatiblePlatform(platform) != PlatformOpenAI {
		return selection, decision, err
	}
	blocked := s.getOpenAIProxyStreamCircuit().activeBlockCount(time.Now())
	if blocked == 0 {
		return selection, decision, err
	}
	s.logOpenAIProxyStreamQuarantineFailOpen(requestedModel, blocked)
	return s.selectAccountWithSchedulerForRoutingOnce(withOpenAIProxyStreamQuarantineBypass(ctx), groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
}

// resolveOpenAISchedulerGroup 解析 OpenAI 路径最终实际使用的分组。
// 与通用网关保持一致：Claude Code 专属分组会在非 Claude Code 请求时沿回退链解析，
// 因此高级调度器模式、渠道映射和粘性缓存均绑定到最终目标分组。
func (s *OpenAIGatewayService) resolveOpenAISchedulerGroup(ctx context.Context, groupID *int64) (context.Context, *int64, error) {
	if groupID == nil || *groupID <= 0 {
		return ctx, groupID, nil
	}
	if forcePlatform, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok && strings.TrimSpace(forcePlatform) != "" {
		return ctx, groupID, nil
	}

	currentID := *groupID
	visited := map[int64]struct{}{}
	for {
		if _, seen := visited[currentID]; seen {
			return ctx, nil, fmt.Errorf("fallback group cycle detected")
		}
		visited[currentID] = struct{}{}

		var group *Group
		if contextual, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(contextual) && contextual.ID == currentID {
			group = contextual
		} else if s != nil && s.schedulerSnapshot != nil {
			resolved, err := s.schedulerSnapshot.GetGroupByID(ctx, currentID)
			if err != nil {
				return ctx, nil, err
			}
			group = resolved
		}
		if group == nil {
			// 没有可用分组快照时保持已有选择语义；高级模式也会安全回退到基础路径。
			return ctx, &currentID, nil
		}
		if !group.ClaudeCodeOnly || IsClaudeCodeClient(ctx) {
			return context.WithValue(ctx, ctxkey.Group, group), &currentID, nil
		}
		if group.FallbackGroupID == nil || *group.FallbackGroupID <= 0 {
			return ctx, nil, ErrClaudeCodeOnly
		}
		currentID = *group.FallbackGroupID
	}
}

type openAIGroupPrivacyRequirementContextKey struct{}

type openAIGroupPrivacyRequirement struct {
	groupID  int64
	required bool
}

// withOpenAIGroupPrivacyRequirement 在一次调度请求内缓存分组隐私资格，避免重试重复查询。
func (s *OpenAIGatewayService) withOpenAIGroupPrivacyRequirement(ctx context.Context, groupID *int64) context.Context {
	return context.WithValue(ctx, openAIGroupPrivacyRequirementContextKey{}, openAIGroupPrivacyRequirement{
		groupID:  derefGroupID(groupID),
		required: s.loadOpenAIGroupRequiresPrivacySet(ctx, groupID),
	})
}

func (s *OpenAIGatewayService) openAIGroupRequiresPrivacySet(ctx context.Context, groupID *int64) bool {
	if cached, ok := ctx.Value(openAIGroupPrivacyRequirementContextKey{}).(openAIGroupPrivacyRequirement); ok && cached.groupID == derefGroupID(groupID) {
		return cached.required
	}
	return s.loadOpenAIGroupRequiresPrivacySet(ctx, groupID)
}

func (s *OpenAIGatewayService) loadOpenAIGroupRequiresPrivacySet(ctx context.Context, groupID *int64) bool {
	if s == nil || groupID == nil || s.schedulerSnapshot == nil {
		return false
	}
	group, err := s.schedulerSnapshot.GetGroupByID(ctx, *groupID)
	if err != nil {
		// 隐私资格查询失败时收紧当前请求，避免错误地把未确认账号用于审查任务。
		return true
	}
	return group != nil && group.RequirePrivacySet
}

func (s *OpenAIGatewayService) selectAccountWithSchedulerForRoutingOnce(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requiredImageCapability OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, groupID)
	platform = NormalizeOpenAICompatiblePlatform(platform)
	decision := OpenAIAccountScheduleDecision{}
	preserveGuardianParentBinding := preserveOpenAIGuardianParentBinding(ctx, sessionHash)
	guardianParentAccountID := int64(0)
	if strings.TrimSpace(previousResponseID) == "" {
		guardianParentAccountID = s.resolveOpenAIGuardianParentAccountID(ctx, groupID)
	}
	scheduler := s.getOpenAIAccountScheduler(ctx, groupID)
	if scheduler == nil {
		decision.Layer = openAIAccountScheduleLayerLoadBalance
		if guardianParentAccountID > 0 {
			fallbackScheduler := &defaultOpenAIAccountScheduler{service: s, stats: newOpenAIAccountRuntimeStats()}
			selection, _, err := fallbackScheduler.selectBySessionHash(ctx, OpenAIAccountScheduleRequest{
				GroupID:                 groupID,
				Platform:                platform,
				SessionHash:             sessionHash,
				StickyAccountID:         guardianParentAccountID,
				PreserveStickyBinding:   true,
				RequestedModel:          requestedModel,
				RoutingModel:            routingModel,
				RequiredTransport:       requiredTransport,
				RequiredCapability:      requiredCapability,
				RequiredImageCapability: requiredImageCapability,
				RequireCompact:          requireCompact,
				RequirePrivacySet:       s.openAIGroupRequiresPrivacySet(ctx, groupID),
				ExcludedIDs:             excludedIDs,
			})
			if err != nil {
				return nil, decision, err
			}
			if selection != nil && selection.Account != nil {
				decision.Layer = openAIAccountScheduleLayerGuardianParent
				decision.StickySessionHit = true
				decision.SelectedAccountID = selection.Account.ID
				decision.SelectedAccountType = selection.Account.Type
				return selection, decision, nil
			}
		}
		legacySessionHash := sessionHash
		if preserveGuardianParentBinding {
			legacySessionHash = ""
		}
		if requiredTransport == OpenAIUpstreamTransportAny || requiredTransport == OpenAIUpstreamTransportHTTPSSE {
			effectiveExcludedIDs := cloneExcludedAccountIDs(excludedIDs)
			for {
				selection, err := s.selectAccountWithLoadAwarenessForRouting(ctx, groupID, platform, legacySessionHash, requestedModel, routingModel, effectiveExcludedIDs, requireCompact, requiredCapability)
				if err != nil {
					return nil, decision, err
				}
				if selection == nil || selection.Account == nil {
					return selection, decision, nil
				}
				if accountSupportsOpenAICapabilities(ctx, selection.Account, requiredCapability, requiredImageCapability) {
					return selection, decision, nil
				}
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				if effectiveExcludedIDs == nil {
					effectiveExcludedIDs = make(map[int64]struct{})
				}
				if _, exists := effectiveExcludedIDs[selection.Account.ID]; exists {
					return nil, decision, ErrNoAvailableAccounts
				}
				effectiveExcludedIDs[selection.Account.ID] = struct{}{}
			}
		}

		effectiveExcludedIDs := cloneExcludedAccountIDs(excludedIDs)
		for {
			selection, err := s.selectAccountWithLoadAwarenessForRouting(ctx, groupID, platform, legacySessionHash, requestedModel, routingModel, effectiveExcludedIDs, requireCompact, requiredCapability)
			if err != nil {
				return nil, decision, err
			}
			if selection == nil || selection.Account == nil {
				return selection, decision, nil
			}
			if s.isOpenAIAccountTransportCompatible(selection.Account, requiredTransport) &&
				accountSupportsOpenAICapabilities(ctx, selection.Account, requiredCapability, requiredImageCapability) {
				return selection, decision, nil
			}
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			if effectiveExcludedIDs == nil {
				effectiveExcludedIDs = make(map[int64]struct{})
			}
			if _, exists := effectiveExcludedIDs[selection.Account.ID]; exists {
				return nil, decision, ErrNoAvailableAccounts
			}
			effectiveExcludedIDs[selection.Account.ID] = struct{}{}
		}
	}

	if s.checkChannelPricingRestriction(ctx, groupID, requestedModel) {
		slog.Warn("channel pricing restriction blocked request",
			"group_id", derefGroupID(groupID),
			"model", requestedModel)
		return nil, decision, fmt.Errorf("%w supporting model: %s (channel pricing restriction)", ErrNoAvailableAccounts, requestedModel)
	}

	var stickyAccountID int64
	if sessionHash != "" && s.cache != nil {
		if accountID, err := s.getStickySessionAccountID(ctx, groupID, sessionHash); err == nil && accountID > 0 {
			stickyAccountID = accountID
		}
	}
	effectiveSettings := s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID)
	stickyWeighted := effectiveSettings.stickyWeightedEnabled
	subscriptionPriority := effectiveSettings.subscriptionPriorityEnabled
	stickyPreviousAccountID := int64(0)
	if stickyWeighted && previousResponseCanMove && strings.TrimSpace(previousResponseID) != "" && platform == PlatformOpenAI {
		stickyPreviousAccountID = s.ResolveAccountIDByPreviousResponseIDForScheduler(ctx, groupID, previousResponseID, routingModel, excludedIDs, requiredCapability, requireCompact)
	}

	selection, decision, selectErr := scheduler.Select(ctx, OpenAIAccountScheduleRequest{
		GroupID:                 groupID,
		Platform:                platform,
		SessionHash:             sessionHash,
		StickyAccountID:         stickyAccountID,
		GuardianParentAccountID: guardianParentAccountID,
		StickyPreviousAccountID: stickyPreviousAccountID,
		StickyWeighted:          stickyWeighted,
		SubscriptionPriority:    subscriptionPriority,
		PreserveStickyBinding:   preserveGuardianParentBinding,
		RequirePrivacySet:       s.openAIGroupRequiresPrivacySet(ctx, groupID),
		PreviousResponseID:      previousResponseID,
		PreviousResponseCanMove: previousResponseCanMove,
		RequestedModel:          requestedModel,
		RoutingModel:            routingModel,
		RequiredTransport:       requiredTransport,
		RequiredCapability:      requiredCapability,
		RequiredImageCapability: requiredImageCapability,
		RequireCompact:          requireCompact,
		ExcludedIDs:             excludedIDs,
	})
	if selection != nil {
		// 只有进入 OpenAI 高级适配器的选择才允许写入高级运行时反馈。
		selection.AdvancedScheduler = true
		feedback := effectiveSettings.feedback
		selection.AdvancedSchedulerFeedback = &feedback
	}
	return selection, decision, selectErr
}

func accountSupportsOpenAICapabilities(ctx context.Context, account *Account, requiredCapability OpenAIEndpointCapability, requiredImageCapability OpenAIImagesCapability) bool {
	if account == nil {
		return false
	}
	return supportsOpenAIRequestCapability(ctx, account, requiredCapability) &&
		account.SupportsOpenAIImageCapability(requiredImageCapability)
}

func cloneExcludedAccountIDs(excludedIDs map[int64]struct{}) map[int64]struct{} {
	if len(excludedIDs) == 0 {
		return nil
	}
	cloned := make(map[int64]struct{}, len(excludedIDs))
	for id := range excludedIDs {
		cloned[id] = struct{}{}
	}
	return cloned
}

func (s *OpenAIGatewayService) isOpenAIAccountTransportCompatible(account *Account, requiredTransport OpenAIUpstreamTransport) bool {
	if requiredTransport == OpenAIUpstreamTransportAny || requiredTransport == OpenAIUpstreamTransportHTTPSSE {
		return true
	}
	if s == nil || account == nil {
		return false
	}
	if requiredTransport == OpenAIUpstreamTransportResponsesWebsocketV2Ingress {
		// Grok WS ingress 在转发层固定走 HTTP bridge，不依赖仅适用于 OpenAI 账号的 WSv2 mode。
		if account.IsGrok() {
			return true
		}
		if s.cfg == nil || !s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled {
			return s.getOpenAIWSProtocolResolver().Resolve(account).Transport == OpenAIUpstreamTransportResponsesWebsocketV2
		}
		switch account.ResolveOpenAIResponsesWebSocketV2Mode(s.cfg.Gateway.OpenAIWS.IngressModeDefault) {
		case OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough, OpenAIWSIngressModeHTTPBridge, OpenAIWSIngressModeShared, OpenAIWSIngressModeDedicated:
			return true
		default:
			return false
		}
	}
	return s.getOpenAIWSProtocolResolver().Resolve(account).Transport == requiredTransport
}

func (s *OpenAIGatewayService) ReportOpenAIAccountScheduleResult(accountOrID any, model string, success bool, firstTokenMs *int, observedErr ...error) bool {
	var account *Account
	var accountID int64
	switch value := accountOrID.(type) {
	case *Account:
		account = value
		if account != nil {
			accountID = account.ID
		}
	case int64:
		accountID = value
	case int:
		accountID = int64(value)
	}
	if account == nil && accountID == 0 {
		return false
	}

	healthTripped := false
	if account != nil && s != nil && s.rateLimitService != nil {
		if success {
			s.rateLimitService.ObserveOpenAIAPIKeyHealthSuccess(context.Background(), account)
		} else if len(observedErr) > 0 && observedErr[0] != nil {
			healthTripped = s.rateLimitService.ObserveOpenAIAPIKeyHealthFailure(context.Background(), account, observedErr[0])
		}
	}
	if success {
		s.openaiOAuth429RetryStartedAt.Delete(accountID)
		s.clearOpenAIAccountModelTransientState(accountID, normalizeOpenAIAccountModelTransientModel(model))
	}
	if account == nil {
		// 旧调用点不携带本次选择模式，不能据此写入高级统计。
		s.reportOpenAIAccountScheduleResult(false, accountID, model, success, firstTokenMs)
		return healthTripped
	}
	if s == nil || s.rateLimitService == nil {
		return healthTripped
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return healthTripped
	}
	scheduler.ReportResult(accountID, success, firstTokenMs)
	return healthTripped
}

// ObserveOpenAIAccountHealthFailure 记录已经写出响应后无法进入调度反馈的失败。
func (s *OpenAIGatewayService) ObserveOpenAIAccountHealthFailure(ctx context.Context, account *Account, observedErr error) bool {
	if s == nil || s.rateLimitService == nil || account == nil || observedErr == nil {
		return false
	}
	return s.rateLimitService.ObserveOpenAIAPIKeyHealthFailure(ctx, account, observedErr)
}

// ReportOpenAIAccountScheduleResultForSelection 按本次实际选择模式写入反馈。
// 基础分组仍清理请求临时状态，但不会污染高级调度统计。
func (s *OpenAIGatewayService) ReportOpenAIAccountScheduleResultForSelection(selection *AccountSelectionResult, accountID int64, model string, success bool, firstTokenMs *int) {
	if selection != nil && selection.AdvancedScheduler && selection.AdvancedSchedulerFeedback != nil {
		s.reportOpenAIAccountScheduleResultWithFeedback(accountID, model, success, firstTokenMs, *selection.AdvancedSchedulerFeedback)
		return
	}
	s.reportOpenAIAccountScheduleResult(selection != nil && selection.AdvancedScheduler, accountID, model, success, firstTokenMs)
}

func (s *OpenAIGatewayService) reportOpenAIAccountScheduleResult(advanced bool, accountID int64, model string, success bool, firstTokenMs *int) {
	if success {
		s.clearOpenAIAccountModelTransientState(accountID, normalizeOpenAIAccountModelTransientModel(model))
	}
	if !advanced {
		return
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportResult(accountID, success, firstTokenMs)
}

func (s *OpenAIGatewayService) reportOpenAIAccountScheduleResultWithFeedback(accountID int64, model string, success bool, firstTokenMs *int, feedback advancedSchedulerFeedbackConfig) {
	if success {
		s.clearOpenAIAccountModelTransientState(accountID, normalizeOpenAIAccountModelTransientModel(model))
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportResult(accountID, success, firstTokenMs, feedback)
}

func (s *OpenAIGatewayService) RecordOpenAIAccountSwitch() {
	// 旧调用点没有分组上下文，仅在高级调度依赖已装配时记录，避免空服务污染指标。
	if s == nil || s.rateLimitService == nil {
		return
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler != nil {
		scheduler.ReportSwitch()
	}
}

// RecordOpenAIAccountSwitchForSelection 只记录高级调度请求的账号切换。
func (s *OpenAIGatewayService) RecordOpenAIAccountSwitchForSelection(selection *AccountSelectionResult) {
	s.recordOpenAIAccountSwitch(selection != nil && selection.AdvancedScheduler)
}

func (s *OpenAIGatewayService) recordOpenAIAccountSwitch(advanced bool) {
	if !advanced {
		return
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportSwitch()
}

func (s *OpenAIGatewayService) SnapshotOpenAIAccountSchedulerMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return OpenAIAccountSchedulerMetricsSnapshot{}
	}
	return scheduler.SnapshotMetrics()
}

func (s *OpenAIGatewayService) openAIWSSessionStickyTTL() time.Duration {
	if s != nil && s.cfg != nil && s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds > 0 {
		return time.Duration(s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds) * time.Second
	}
	return openaiStickySessionTTL
}

func (s *OpenAIGatewayService) openAIWSLBTopK() int {
	if s != nil && s.cfg != nil && s.cfg.Gateway.AdvancedScheduler.LBTopK > 0 {
		return s.cfg.Gateway.AdvancedScheduler.LBTopK
	}
	return 7
}

func (s *OpenAIGatewayService) openAIWSLBTopKForRequest(ctx context.Context) int {
	base := s.openAIWSLBTopK()
	settings := s.advancedSchedulerRuntimeSettings(ctx)
	if settings.lbTopKOverride > 0 {
		return settings.lbTopKOverride
	}
	return base
}

func resolveAdvancedStickyEscapeConfig(appConfig *config.Config) advancedStickyEscapeConfig {
	if appConfig != nil {
		cfg := appConfig.Gateway.AdvancedScheduler
		return normalizeAdvancedStickyEscapeConfig(advancedStickyEscapeConfig{
			enabled:   cfg.StickyEscapeEnabled,
			ttftMs:    float64(cfg.StickyEscapeTTFTMs),
			errorRate: cfg.StickyEscapeErrorRate,
		})
	}
	return normalizeAdvancedStickyEscapeConfig(advancedStickyEscapeConfig{
		enabled:   true,
		ttftMs:    15000,
		errorRate: 0.5,
	})
}

func normalizeAdvancedStickyEscapeConfig(value advancedStickyEscapeConfig) advancedStickyEscapeConfig {
	v := policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: value.enabled, TtftMs: value.ttftMs, ErrorRate: value.errorRate})
	return advancedStickyEscapeConfig{enabled: v.Enabled, ttftMs: v.TtftMs, errorRate: v.ErrorRate}
}

func (s *OpenAIGatewayService) openAIWSSchedulerWeights() GatewayAdvancedSchedulerScoreWeightsView {
	if s != nil && s.cfg != nil {
		return GatewayAdvancedSchedulerScoreWeightsView{
			Priority:      s.cfg.Gateway.AdvancedScheduler.ScoreWeights.Priority,
			Load:          s.cfg.Gateway.AdvancedScheduler.ScoreWeights.Load,
			Queue:         s.cfg.Gateway.AdvancedScheduler.ScoreWeights.Queue,
			ErrorRate:     s.cfg.Gateway.AdvancedScheduler.ScoreWeights.ErrorRate,
			TTFT:          s.cfg.Gateway.AdvancedScheduler.ScoreWeights.TTFT,
			Reset:         s.cfg.Gateway.AdvancedScheduler.ScoreWeights.Reset,
			QuotaHeadroom: s.cfg.Gateway.AdvancedScheduler.ScoreWeights.QuotaHeadroom,
			Previous:      s.cfg.Gateway.AdvancedScheduler.ScoreWeights.PreviousResponse,
			SessionSticky: s.cfg.Gateway.AdvancedScheduler.ScoreWeights.SessionSticky,
		}
	}
	return GatewayAdvancedSchedulerScoreWeightsView{
		Priority:      1.0,
		Load:          1.0,
		Queue:         0.7,
		ErrorRate:     0.8,
		TTFT:          0.5,
		Reset:         0.0,
		QuotaHeadroom: 0.0,
		Previous:      5.0,
		SessionSticky: 3.0,
	}
}

func (s *OpenAIGatewayService) openAIWSSchedulerWeightsForRequest(ctx context.Context) GatewayAdvancedSchedulerScoreWeightsView {
	weights := s.openAIWSSchedulerWeights()
	settings := s.advancedSchedulerRuntimeSettings(ctx)
	overridden := applyAdvancedSchedulerWeightOverrides(weights, settings.weightOverrides)
	if !overridden.configWeights().IsValid() {
		return weights
	}
	return overridden
}

func applyAdvancedSchedulerWeightOverrides(
	weights GatewayAdvancedSchedulerScoreWeightsView,
	overrides map[string]float64,
) GatewayAdvancedSchedulerScoreWeightsView {
	return GatewayAdvancedSchedulerScoreWeightsView(policy.ApplyGlobalWeightOverrides(policy.ScoreWeights(weights), overrides))
}

// 旧视图只保留 config 投影方法，值字段由纯 policy 拥有。
type GatewayAdvancedSchedulerScoreWeightsView policy.ScoreWeights

func (w GatewayAdvancedSchedulerScoreWeightsView) configWeights() config.GatewayAdvancedSchedulerScoreWeights {
	return config.GatewayAdvancedSchedulerScoreWeights{
		Priority:         w.Priority,
		Load:             w.Load,
		Queue:            w.Queue,
		ErrorRate:        w.ErrorRate,
		TTFT:             w.TTFT,
		Reset:            w.Reset,
		QuotaHeadroom:    w.QuotaHeadroom,
		PreviousResponse: w.Previous,
		SessionSticky:    w.SessionSticky,
	}
}

// BuildAdvancedAccountSchedulerScoreSnapshot 按运行时设置生成通用高级调度评分快照。
func (s *RateLimitService) BuildAdvancedAccountSchedulerScoreSnapshot(
	ctx context.Context,
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	return s.BuildAdvancedAccountSchedulerScoreSnapshotForGroup(ctx, nil, accounts, loadMap)
}

// BuildAdvancedAccountSchedulerScoreSnapshotForGroup 按指定高级分组的有效配置生成评分快照。
// 该入口供管理端展示使用，必须与实际调度的分组覆盖优先级保持一致。
func (s *RateLimitService) BuildAdvancedAccountSchedulerScoreSnapshotForGroup(
	ctx context.Context,
	group *Group,
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	gateway := &OpenAIGatewayService{cfg: nil, rateLimitService: s}
	var stats *advancedAccountRuntimeStats
	if s != nil {
		gateway.cfg = s.cfg
		stats = s.AdvancedSchedulerRuntimeStats()
	}
	effectiveSettings := gateway.advancedSchedulerEffectiveSettingsForGroup(ctx, group)
	return buildAdvancedAccountSchedulerScoreSnapshot(
		accounts,
		loadMap,
		stats,
		group,
		effectiveSettings.weights,
		effectiveSettings.stickyWeightedEnabled,
		openAIQuotaHeadroomFactor,
	)
}

// BuildAdvancedAccountSchedulerScoreSnapshot 按默认配置生成通用高级调度评分快照。
func BuildAdvancedAccountSchedulerScoreSnapshot(
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	return BuildAdvancedAccountSchedulerScoreSnapshotForGroup(nil, accounts, loadMap)
}

// BuildAdvancedAccountSchedulerScoreSnapshotForGroup 按静态默认值和分组覆盖生成评分。
// 未注入设置服务的管理端测试路径使用该函数。
func BuildAdvancedAccountSchedulerScoreSnapshotForGroup(
	group *Group,
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	gateway := &OpenAIGatewayService{}
	effectiveSettings := gateway.advancedSchedulerEffectiveSettingsForGroup(context.Background(), group)
	return buildAdvancedAccountSchedulerScoreSnapshot(
		accounts,
		loadMap,
		nil,
		group,
		effectiveSettings.weights,
		effectiveSettings.stickyWeightedEnabled,
		openAIQuotaHeadroomFactor,
	)
}

// openAIQuotaHeadroomFactor 把 Codex quota 快照转换成 0..1 的调度因子。
// 7d/primary 剩余额度越高分越高；5h/secondary 接近耗尽时会折扣该分值。
func openAIQuotaHeadroomFactor(account *Account, now time.Time) float64 {
	if account == nil || len(account.Extra) == 0 || openAIQuotaHeadroomSnapshotStale(account.Extra, now) {
		return openAIQuotaHeadroomNeutralFactor
	}
	primaryUsedPercent, ok := resolveAccountExtraNumber(account.Extra, "codex_primary_used_percent", "codex_7d_used_percent")
	if !ok || openAIQuotaWindowResetAny(account.Extra, now, "primary", "7d") {
		return openAIQuotaHeadroomNeutralFactor
	}

	factor := 1 - clamp01(primaryUsedPercent/100)
	if secondaryUsedPercent, ok := resolveAccountExtraNumber(account.Extra, "codex_secondary_used_percent", "codex_5h_used_percent"); ok &&
		!openAIQuotaWindowResetAny(account.Extra, now, "secondary", "5h") {
		secondaryRemaining := 1 - clamp01(secondaryUsedPercent/100)
		if secondaryRemaining < openAIQuotaHeadroomSecondaryLowRemain {
			factor *= openAIQuotaHeadroomNeutralFactor
		}
	}
	return factor
}

// openAIQuotaHeadroomSnapshotStale 判断 quota 快照是否过旧到只能按中性分参与调度。
func openAIQuotaHeadroomSnapshotStale(extra map[string]any, now time.Time) bool {
	updatedRaw, ok := extra["codex_usage_updated_at"]
	if !ok {
		return true
	}
	updatedAt, err := parseTime(fmt.Sprint(updatedRaw))
	if err != nil {
		return true
	}
	return now.Sub(updatedAt) >= openAIQuotaHeadroomSnapshotStaleAfter
}

// openAIQuotaWindowResetAny 支持同时检查 primary/7d 或 secondary/5h 兼容字段。
func openAIQuotaWindowResetAny(extra map[string]any, now time.Time, windows ...string) bool {
	for _, window := range windows {
		if openAIQuotaWindowReset(extra, window, now) {
			return true
		}
	}
	return false
}

// 数值归一及负载偏斜委托唯一调度纯实现。
func clamp01(value float64) float64 { return scheduler.Clamp01(value) }
func calcLoadSkewByMoments(sum, sumSquares float64, count int) float64 {
	return scheduler.LoadSkewByMoments(sum, sumSquares, count)
}

// 基础回退只委托同一粘性核心，不维护第二条绑定或等待策略。
func (s *defaultOpenAIAccountScheduler) selectBySessionHash(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, bool, error) {
	core, scope := s.platformSelector()
	value, escaped, err := core.SelectBySessionHash(ctx, platformSelectionInput(req))
	return scope.restore(value), escaped, err
}

// 旧基础选择的只读诊断状态与新核心使用同一类型。
