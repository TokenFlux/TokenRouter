package service

import (
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/gemini"

	upstream "github.com/TokenFlux/TokenRouter/internal/upstream"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/gin-gonic/gin"
)

const geminiStickySessionTTL = time.Hour

const (
	geminiMaxRetries     = 5
	geminiRetryBaseDelay = 1 * time.Second
	geminiRetryMaxDelay  = 16 * time.Second
)

const geminiAppliedTempPolicyHeader = "X-TokenRouter-Internal-Temp-Policy-Applied"

// Gemini tool calling now requires `thoughtSignature` in parts that include `functionCall`.
// Many clients don't send it; we inject a known dummy signature to satisfy the validator.
// Ref: https://ai.google.dev/gemini-api/docs/thought-signatures
const geminiDummyThoughtSignature = "skip_thought_signature_validator"

type GeminiMessagesCompatService struct {
	quotaPrecheck             *accountcore.GeminiPrecheck
	nativeAttemptActivity     func() (func(), error)
	accountRepo               gatewayprovider.ExecutionAccountStore
	groupRepo                 routing.GroupRepository
	cache                     session.GatewayCache
	schedulerSnapshot         *scheduler.SnapshotService
	tokenProvider             *accountcore.GeminiTokenSource
	rateLimitService          *RateLimitService
	httpUpstream              httpclient.UpstreamTransport
	antigravityGatewayService *AntigravityGatewayService
	cfg                       *config.Config
	responseHeaderFilter      *egress.CompiledHeaderFilter
	schedulerParameters       *scheduler.Parameters
	advancedAccountStats      *scheduler.RuntimeStats
}

func (s *GeminiMessagesCompatService) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	limit := gatewayUpstreamErrorBodyReadLimit
	if s != nil && s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody && s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	return body
}

func NewGeminiMessagesCompatService(
	accountRepo gatewayprovider.ExecutionAccountStore,
	groupRepo routing.GroupRepository,
	cache session.GatewayCache,
	schedulerSnapshot *scheduler.SnapshotService,
	tokenProvider *accountcore.GeminiTokenSource,
	rateLimitService *RateLimitService,
	httpUpstream httpclient.UpstreamTransport,
	antigravityGatewayService *AntigravityGatewayService,
	cfg *config.Config, headerFilter *egress.CompiledHeaderFilter,
) *GeminiMessagesCompatService {
	return &GeminiMessagesCompatService{
		accountRepo:               accountRepo,
		groupRepo:                 groupRepo,
		cache:                     cache,
		schedulerSnapshot:         schedulerSnapshot,
		tokenProvider:             tokenProvider,
		rateLimitService:          rateLimitService,
		httpUpstream:              httpUpstream,
		antigravityGatewayService: antigravityGatewayService,
		cfg:                       cfg,
		responseHeaderFilter:      headerFilter,
	}
}

// GetTokenProvider returns the token provider for OAuth accounts
func (s *GeminiMessagesCompatService) GetTokenProvider() *accountcore.GeminiTokenSource {
	return s.tokenProvider
}

func (s *GeminiMessagesCompatService) SelectAccountForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*gatewayprovider.ExecutionAccount, error) {
	return s.SelectAccountForModelWithExclusions(ctx, groupID, sessionHash, requestedModel, nil)
}

func (s *GeminiMessagesCompatService) SelectAccountForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.ExecutionAccount, error) {
	// 1. 确定目标平台和调度模式
	platform, useMixedScheduling, hasForcePlatform, group, err := s.resolvePlatformAndSchedulingMode(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group != nil {
		// 后续高级评分必须读取本次解析出的最终分组覆盖，而非再次走可能不同的缓存来源。
		ctx = requeststate.WithGroup(ctx, group)
	}

	cacheKey := "gemini:" + sessionHash
	usesAdvancedScheduler := group != nil && group.UsesAdvancedScheduler()
	advancedSettings := s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID)

	// 2. 尝试粘性会话命中
	// 高级粘性加权会在全部硬过滤后的候选评分中处理缓存绑定，因此不允许旧硬粘性提前返回。
	if !usesAdvancedScheduler || !advancedSettings.StickyWeightedEnabled {
		if account := s.tryStickySessionHit(ctx, groupID, sessionHash, cacheKey, requestedModel, excludedIDs, platform, useMixedScheduling); account != nil {
			return account, nil
		}
	}

	// 3. 查询可调度账户（强制平台模式：优先按分组查找，找不到再查全部）
	accounts, err := s.listSchedulableAccountsOnce(ctx, groupID, platform, hasForcePlatform)
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}
	// 强制平台模式下，分组中找不到账户时回退查询全部
	if len(accounts) == 0 && groupID != nil && hasForcePlatform {
		accounts, err = s.listSchedulableAccountsOnce(ctx, nil, platform, hasForcePlatform)
		if err != nil {
			return nil, fmt.Errorf("query accounts failed: %w", err)
		}
	}

	// 4. 先执行既有硬过滤，再由分组选择基础或高级排序。
	eligible := s.eligibleGeminiAccounts(ctx, accounts, requestedModel, excludedIDs, platform, useMixedScheduling)
	var selected *gatewayprovider.ExecutionAccount
	if usesAdvancedScheduler {
		selected = s.selectAdvancedGeminiAccount(ctx, groupID, sessionHash, cacheKey, eligible, advancedSettings)
	} else {
		selected = s.selectBestGeminiAccountFromEligible(eligible)
	}

	if selected == nil {
		if err := s.groupModelUnsupportedErrorIfApplicable(ctx, accounts, requestedModel, platform, excludedIDs, useMixedScheduling); err != nil {
			return nil, err
		}
		if requestedModel != "" {
			return nil, fmt.Errorf("no available Gemini accounts supporting model: %s", requestedModel)
		}
		return nil, errors.New("no available Gemini accounts")
	}

	// 5. 设置粘性会话绑定
	if sessionHash != "" {
		_ = s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), cacheKey, selected.Record.ID, geminiStickySessionTTL)
	}

	return s.hydrateSelectedAccount(ctx, selected)
}

// resolvePlatformAndSchedulingMode 解析目标平台和调度模式。
// 返回：平台名称、是否使用混合调度、是否强制平台、已解析分组、错误。
func (s *GeminiMessagesCompatService) resolvePlatformAndSchedulingMode(ctx context.Context, groupID *int64) (platform string, useMixedScheduling bool, hasForcePlatform bool, group *routing.Group, err error) {
	// 优先检查 context 中的强制平台（/antigravity 路由）
	forcePlatform, hasForcePlatform := apikey.ForcePlatformFromContext(ctx)
	if hasForcePlatform && forcePlatform != "" {
		if groupID == nil {
			return forcePlatform, false, true, nil, nil
		}
		if ctxGroup, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(ctxGroup) && ctxGroup.ID == *groupID {
			return forcePlatform, false, true, ctxGroup, nil
		}
		group, err = s.groupRepo.GetByIDLite(ctx, *groupID)
		if err != nil {
			return "", false, false, nil, fmt.Errorf("get group failed: %w", err)
		}
		return forcePlatform, false, true, group, nil
	}

	if groupID != nil {
		// 根据分组 platform 决定查询哪种账号
		if ctxGroup, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(ctxGroup) && ctxGroup.ID == *groupID {
			group = ctxGroup
		} else {
			group, err = s.groupRepo.GetByIDLite(ctx, *groupID)
			if err != nil {
				return "", false, false, nil, fmt.Errorf("get group failed: %w", err)
			}
		}
		// gemini 分组支持混合调度（包含启用了 mixed_scheduling 的 antigravity 账户）
		return group.Platform, group.Platform == capability.PlatformGemini, false, group, nil
	}

	// 无分组时只使用原生 gemini 平台
	return capability.PlatformGemini, true, false, nil, nil
}

// tryStickySessionHit 尝试从粘性会话获取账号。
// 如果命中且账号可用则返回账号；如果账号不可用则清理会话并返回 nil。
//
// tryStickySessionHit attempts to get account from sticky session.
// Returns account if hit and usable; clears session and returns nil if account unavailable.
func (s *GeminiMessagesCompatService) tryStickySessionHit(
	ctx context.Context,
	groupID *int64,
	sessionHash, cacheKey, requestedModel string,
	excludedIDs map[int64]struct{},
	platform string,
	useMixedScheduling bool,
) *gatewayprovider.ExecutionAccount {
	if sessionHash == "" {
		return nil
	}

	accountID, err := s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
	if err != nil || accountID <= 0 {
		return nil
	}

	if _, excluded := excludedIDs[accountID]; excluded {
		return nil
	}

	account, err := s.getSchedulableAccount(ctx, accountID)
	if err != nil {
		return nil
	}

	// 检查账号是否需要清理粘性会话
	// Check if sticky session should be cleared
	if shouldClearStickySession(account, requestedModel) {
		_ = s.cache.DeleteSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
		return nil
	}

	// 验证账号是否可用于当前请求
	// Verify account is usable for current request
	if !s.isAccountUsableForRequest(ctx, account, requestedModel, platform, useMixedScheduling) {
		return nil
	}

	// 刷新会话 TTL 并返回账号
	// Refresh session TTL and return account
	_ = s.cache.RefreshSessionTTL(ctx, derefGroupID(groupID), cacheKey, geminiStickySessionTTL)
	return account
}

// isAccountUsableForRequest 检查账号是否可用于当前请求。
// 验证：模型调度、模型支持、平台匹配、速率限制预检。
//
// isAccountUsableForRequest checks if account is usable for current request.
// Validates: model scheduling, model support, platform matching, rate limit precheck.
func (s *GeminiMessagesCompatService) isAccountUsableForRequest(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	requestedModel, platform string,
	useMixedScheduling bool,
) bool {
	return s.isAccountUsableForRequestWithPrecheck(ctx, account, requestedModel, platform, useMixedScheduling, nil)
}

func (s *GeminiMessagesCompatService) isAccountUsableForRequestWithPrecheck(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	requestedModel, platform string,
	useMixedScheduling bool,
	precheckResult map[int64]bool,
) bool {
	// 检查模型调度能力
	// Check model scheduling capability
	if !gatewayprovider.ExecutionModelPolicy(account).Schedulable(ctx, requestedModel) {
		return false
	}

	// 检查模型支持
	// Check model support
	if requestedModel != "" && !s.isModelSupportedByAccount(account, requestedModel) {
		return false
	}

	// 检查平台匹配
	// Check platform matching
	if !s.isAccountValidForPlatform(account, platform, useMixedScheduling) {
		return false
	}

	// 速率限制预检
	// Rate limit precheck
	if !s.passesRateLimitPreCheckWithCache(ctx, account, requestedModel, precheckResult) {
		return false
	}

	return true
}

// isAccountValidForPlatform 检查账号是否匹配目标平台。
// 原生平台直接匹配；混合调度模式下 antigravity 需要启用 mixed_scheduling。
//
// isAccountValidForPlatform checks if account matches target platform.
// Native platform matches directly; mixed scheduling mode requires antigravity to enable mixed_scheduling.
func (s *GeminiMessagesCompatService) isAccountValidForPlatform(account *gatewayprovider.ExecutionAccount, platform string, useMixedScheduling bool) bool {
	if account.Record.Platform == platform {
		return true
	}
	if useMixedScheduling && account.Record.Platform == capability.PlatformAntigravity && account.View().IsMixedSchedulingEnabled() {
		return true
	}
	return false
}

func (s *GeminiMessagesCompatService) passesRateLimitPreCheckWithCache(ctx context.Context, account *gatewayprovider.ExecutionAccount, requestedModel string, precheckResult map[int64]bool) bool {
	if s.quotaPrecheck == nil || requestedModel == "" {
		return true
	}

	if precheckResult != nil {
		if ok, exists := precheckResult[account.Record.ID]; exists {
			return ok
		}
	}

	ok, err := s.quotaPrecheck.PreCheckUsage(ctx, gatewayprovider.ExecutionRecord(account), requestedModel)
	if err != nil {
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini PreCheck] Account %d precheck error: %v", account.Record.ID, err)
	}
	return ok
}

// eligibleGeminiAccounts 在高级和基础调度器前复用 Gemini 现有的全部硬过滤规则。
func (s *GeminiMessagesCompatService) eligibleGeminiAccounts(
	ctx context.Context,
	accounts []gatewayprovider.ExecutionAccount,
	requestedModel string,
	excludedIDs map[int64]struct{},
	platform string,
	useMixedScheduling bool,
) []*gatewayprovider.ExecutionAccount {
	precheckResult := s.buildPreCheckUsageResultMap(ctx, accounts, requestedModel)
	eligible := make([]*gatewayprovider.ExecutionAccount, 0, len(accounts))

	for i := range accounts {
		acc := &accounts[i]

		// 跳过被排除的账号
		if _, excluded := excludedIDs[acc.Record.ID]; excluded {
			continue
		}

		// 检查账号是否可用于当前请求
		if !s.isAccountUsableForRequestWithPrecheck(ctx, acc, requestedModel, platform, useMixedScheduling, precheckResult) {
			continue
		}
		eligible = append(eligible, acc)
	}

	return eligible
}

func (s *GeminiMessagesCompatService) selectBestGeminiAccountFromEligible(eligible []*gatewayprovider.ExecutionAccount) *gatewayprovider.ExecutionAccount {
	var selected *gatewayprovider.ExecutionAccount
	for _, account := range eligible {
		if account == nil {
			continue
		}
		if selected == nil || s.isBetterGeminiAccount(account, selected) {
			selected = account
		}
	}
	return selected
}

// groupUsesAdvancedScheduler 只让最终分组显式选择高级模式；无分组路径保持基础调度。
func (s *GeminiMessagesCompatService) groupUsesAdvancedScheduler(ctx context.Context, groupID *int64, hasForcePlatform bool) bool {
	if s == nil || groupID == nil || *groupID <= 0 {
		return false
	}
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == *groupID {
		return group.UsesAdvancedScheduler()
	}
	if s.schedulerSnapshot != nil {
		if group, err := s.readSchedulingGroup(ctx, *groupID); err == nil && group != nil {
			return group.UsesAdvancedScheduler()
		}
	}
	if s.groupRepo == nil {
		return false
	}
	group, err := s.groupRepo.GetByIDLite(ctx, *groupID)
	return err == nil && group != nil && group.UsesAdvancedScheduler()
}

func (s *GeminiMessagesCompatService) advancedSchedulerStats() *scheduler.RuntimeStats {
	if s == nil {
		return nil
	}
	if s.advancedAccountStats == nil {
		s.advancedAccountStats = scheduler.NewRuntimeStats(time.Now)
	}
	return s.advancedAccountStats
}

// advancedSchedulerEffectiveSettingsForRequest 返回最终分组的高级调度有效配置。
func (s *GeminiMessagesCompatService) advancedSchedulerEffectiveSettingsForRequest(ctx context.Context, id *int64) policy.EffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	group := schedulerRequestGroup(ctx, id, s.schedulerSnapshot != nil, s.readSchedulingGroup)
	return s.schedulerParameters.Effective(ctx, schedulerGroupOverrides(group))
}

// selectAdvancedGeminiAccount 在 Gemini 已完成硬过滤后复用通用高级评分与 Top-K 选择。
func (s *GeminiMessagesCompatService) selectAdvancedGeminiAccount(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	cacheKey string,
	eligible []*gatewayprovider.ExecutionAccount,
	settings policy.EffectiveSettings,
) *gatewayprovider.ExecutionAccount {
	if len(eligible) == 0 {
		return nil
	}
	var stickyAccountID int64
	if sessionHash != "" && s.cache != nil {
		stickyAccountID, _ = s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
	}
	input := scheduler.ScoreInput{
		GroupID:         groupID,
		SessionHash:     cacheKey,
		StickyAccountID: stickyAccountID,
		StickyWeighted:  settings.StickyWeightedEnabled,
		TopK:            settings.TopK,
	}
	values := make([]*scheduler.ScoreAccount, len(eligible))
	source := make(map[*scheduler.ScoreAccount]*gatewayprovider.ExecutionAccount, len(eligible))
	for i, value := range eligible {
		if value == nil {
			continue
		}
		values[i] = &scheduler.ScoreAccount{ID: value.Record.ID, Platform: value.Record.Platform, Priority: value.Record.Priority, SessionWindowEnd: value.Record.SessionWindowEnd}
		source[values[i]] = value
	}
	candidates, _ := scheduler.ScoreCandidates(values, nil, s.advancedSchedulerStats(), settings.Weights, input, time.Now())
	selectionOrder := scheduler.BuildSelectionOrder(candidates, input)
	if len(selectionOrder) == 0 {
		return nil
	}
	return source[selectionOrder[0].Account]
}

// groupModelUnsupportedErrorIfApplicable 在确认是分组模型限制时返回 typed error。
func (s *GeminiMessagesCompatService) groupModelUnsupportedErrorIfApplicable(ctx context.Context, accounts []gatewayprovider.ExecutionAccount, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixedScheduling bool) error {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(accounts) == 0 {
		return nil
	}
	precheckResult := s.buildPreCheckUsageResultMap(ctx, accounts, requestedModel)
	hasRelevantAccount := false
	for i := range accounts {
		acc := &accounts[i]
		if excludedIDs != nil {
			if _, excluded := excludedIDs[acc.Record.ID]; excluded {
				continue
			}
		}
		if !acc.View().IsSchedulable() || !s.isAccountValidForPlatform(acc, platform, useMixedScheduling) || !s.passesRateLimitPreCheckWithCache(ctx, acc, requestedModel, precheckResult) {
			continue
		}
		hasRelevantAccount = true
		if s.isModelSupportedByAccount(acc, requestedModel) {
			return nil
		}
	}
	if !hasRelevantAccount {
		return nil
	}
	return routing.NewGroupModelRejection(platform, requestedModel, modelRejectionSources(accounts))
}

func (s *GeminiMessagesCompatService) buildPreCheckUsageResultMap(ctx context.Context, accounts []gatewayprovider.ExecutionAccount, requestedModel string) map[int64]bool {
	if s.quotaPrecheck == nil || requestedModel == "" || len(accounts) == 0 {
		return nil
	}

	candidates := make([]*gatewayprovider.ExecutionAccount, 0, len(accounts))
	for i := range accounts {
		candidates = append(candidates, &accounts[i])
	}

	result, err := s.quotaPrecheck.PreCheckUsageBatch(ctx, gatewayprovider.ExecutionRecordPointers(candidates), requestedModel)
	if err != nil {
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini PreCheckBatch] failed: %v", err)
	}
	return result
}

// isBetterGeminiAccount 判断 candidate 是否比 current 更优。
// 规则：优先级更高（数值更小）优先；同优先级时，未使用过的优先（OAuth > 非 OAuth），其次是最久未使用的。
//
// isBetterGeminiAccount checks if candidate is better than current.
// Rules: higher priority (lower value) wins; same priority: never used (OAuth > non-OAuth) > least recently used.
func (s *GeminiMessagesCompatService) isBetterGeminiAccount(candidate, current *gatewayprovider.ExecutionAccount) bool {
	// 优先级更高（数值更小）
	if candidate.Record.Priority < current.Record.Priority {
		return true
	}
	if candidate.Record.Priority > current.Record.Priority {
		return false
	}

	// 同优先级，比较最后使用时间
	switch {
	case candidate.Record.LastUsedAt == nil && current.Record.LastUsedAt != nil:
		// candidate 从未使用，优先
		return true
	case candidate.Record.LastUsedAt != nil && current.Record.LastUsedAt == nil:
		// current 从未使用，保持
		return false
	case candidate.Record.LastUsedAt == nil && current.Record.LastUsedAt == nil:
		// 都未使用，优先选择 OAuth 账号（更兼容 Code Assist 流程）
		return candidate.Record.Type == capability.AccountTypeOAuth && current.Record.Type != capability.AccountTypeOAuth
	default:
		// 都使用过，选择最久未使用的
		return candidate.Record.LastUsedAt.Before(*current.Record.LastUsedAt)
	}
}

// isModelSupportedByAccount 根据账户平台检查模型支持
func (s *GeminiMessagesCompatService) isModelSupportedByAccount(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	if account.Record.Platform == capability.PlatformAntigravity {
		if strings.TrimSpace(requestedModel) == "" {
			return true
		}
		return mapAntigravityModel(account, requestedModel) != ""
	}
	return gatewayprovider.ExecutionProtocolRecord(account).IsModelSupported(requestedModel, accountprovider.

		// GetAntigravityGatewayService 返回 AntigravityGatewayService
		ModelDefaults(), accountprovider.ModelRules(gatewayprovider.ExecutionProtocolRecord(account)))
}

func (s *GeminiMessagesCompatService) GetAntigravityGatewayService() *AntigravityGatewayService {
	return s.antigravityGatewayService
}

func (s *GeminiMessagesCompatService) getSchedulableAccount(ctx context.Context, accountID int64) (*gatewayprovider.ExecutionAccount, error) {
	if s.schedulerSnapshot != nil {
		return readSnapshotAccount(ctx, s.schedulerSnapshot, accountID)
	}
	return s.accountRepo.GetByID(ctx, accountID)
}

func (s *GeminiMessagesCompatService) hydrateSelectedAccount(ctx context.Context, account *gatewayprovider.ExecutionAccount) (*gatewayprovider.ExecutionAccount, error) {
	if account == nil || s.schedulerSnapshot == nil {
		return account, nil
	}
	hydrated, err := readSnapshotAccount(ctx, s.schedulerSnapshot, account.Record.ID)
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, fmt.Errorf("selected gemini account %d not found during hydration", account.Record.ID)
	}
	return hydrated, nil
}

func (s *GeminiMessagesCompatService) listSchedulableAccountsOnce(ctx context.Context, groupID *int64, platform string, hasForcePlatform bool) ([]gatewayprovider.ExecutionAccount, error) {
	if s.schedulerSnapshot != nil {
		accounts, _, err := readSnapshotAccounts(ctx, s.schedulerSnapshot, groupID, platform, hasForcePlatform)
		return accounts, err
	}

	useMixedScheduling := platform == capability.PlatformGemini && !hasForcePlatform
	queryPlatforms := []string{platform}
	if useMixedScheduling {
		queryPlatforms = []string{platform, capability.PlatformAntigravity}
	}

	if groupID != nil {
		return s.accountRepo.ListSchedulableByGroupIDAndPlatforms(ctx, *groupID, queryPlatforms)
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return s.accountRepo.ListSchedulableByPlatforms(ctx, queryPlatforms)
	}
	return s.accountRepo.ListSchedulableUngroupedByPlatforms(ctx, queryPlatforms)
}

func (s *GeminiMessagesCompatService) validateUpstreamBaseURL(raw string) (string, error) {
	if s.cfg != nil && !s.cfg.Security.URLAllowlist.Enabled {
		normalized, err := egress.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid base_url: %w", err)
		}
		return normalized, nil
	}
	normalized, err := egress.ValidateHTTPSURL(raw, egress.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}

// HasAntigravityAccounts 检查是否有可用的 antigravity 账户
func (s *GeminiMessagesCompatService) HasAntigravityAccounts(ctx context.Context, groupID *int64) (bool, error) {
	accounts, err := s.listSchedulableAccountsOnce(ctx, groupID, capability.PlatformAntigravity, false)
	if err != nil {
		return false, err
	}
	return len(accounts) > 0, nil
}

// SelectAccountForAIStudioEndpoints selects an account that is likely to succeed against
// generativelanguage.googleapis.com (e.g. GET /v1beta/models).
//
// Preference order:
// 1) API key accounts (AI Studio)
// 2) OAuth accounts without project_id (AI Studio OAuth)
// 3) OAuth accounts explicitly marked as ai_studio
// 4) Any remaining Gemini accounts (fallback)
func (s *GeminiMessagesCompatService) SelectAccountForAIStudioEndpoints(ctx context.Context, groupID *int64) (*gatewayprovider.ExecutionAccount, error) {
	if group, ok := s.resolveAdvancedSchedulerGroup(ctx, groupID); ok {
		ctx = requeststate.WithGroup(ctx, group)
	}
	accounts, err := s.listSchedulableAccountsOnce(ctx, groupID, capability.PlatformGemini, true)
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}
	if len(accounts) == 0 {
		return nil, errors.New("no available Gemini accounts")
	}

	rank := func(a *gatewayprovider.ExecutionAccount) int {
		if a == nil {
			return 999
		}
		switch a.Record.Type {
		case capability.AccountTypeAPIKey:
			if strings.TrimSpace(a.View().GetCredential("api_key")) != "" {
				return 0
			}
			return 9
		case capability.AccountTypeOAuth:
			if strings.TrimSpace(a.View().GetCredential("project_id")) == "" {
				return 1
			}
			if strings.TrimSpace(a.View().GetCredential("oauth_type")) == "ai_studio" {
				return 2
			}
			// Code Assist OAuth tokens often lack AI Studio scopes for models listing.
			return 3
		case capability.AccountTypeServiceAccount:
			// Vertex service accounts use aiplatform.googleapis.com, not the AI Studio
			// endpoint (generativelanguage.googleapis.com), so they cannot serve these requests.
			return 999
		default:
			return 10
		}
	}

	var selected *gatewayprovider.ExecutionAccount
	for i := range accounts {
		acc := &accounts[i]
		if selected == nil {
			selected = acc
			continue
		}

		r1, r2 := rank(acc), rank(selected)
		if r1 < r2 {
			selected = acc
			continue
		}
		if r1 > r2 {
			continue
		}

		if acc.Record.Priority < selected.Record.Priority {
			selected = acc
		} else if acc.Record.Priority == selected.Record.Priority {
			switch {
			case acc.Record.LastUsedAt == nil && selected.Record.LastUsedAt != nil:
				selected = acc
			case acc.Record.LastUsedAt != nil && selected.Record.LastUsedAt == nil:
				// keep selected
			case acc.Record.LastUsedAt == nil && selected.Record.LastUsedAt == nil:
				if acc.Record.Type == capability.AccountTypeOAuth && selected.Record.Type != capability.AccountTypeOAuth {
					selected = acc
				}
			default:
				if acc.Record.LastUsedAt.Before(*selected.Record.LastUsedAt) {
					selected = acc
				}
			}
		}
	}

	if selected == nil {
		return nil, errors.New("no available Gemini accounts")
	}

	// AI Studio 端点的账号类型分级是能力约束，不能被高级调度分数跨级覆盖；
	// 仅在同一能力等级中使用通用高级评分。
	if s.groupUsesAdvancedScheduler(ctx, groupID, false) {
		bestRank := rank(selected)
		if bestRank < 999 {
			eligible := make([]*gatewayprovider.ExecutionAccount, 0, len(accounts))
			for i := range accounts {
				account := &accounts[i]
				if rank(account) == bestRank {
					eligible = append(eligible, account)
				}
			}
			if advanced := s.selectAdvancedGeminiAccount(
				ctx,
				groupID,
				"",
				"",
				eligible,
				s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID),
			); advanced != nil {
				selected = advanced
			}
		}
	}
	return s.hydrateSelectedAccount(ctx, selected)
}

// resolveAdvancedSchedulerGroup 为不经过普通模型选择的 Gemini 入口补齐最终分组。
func (s *GeminiMessagesCompatService) resolveAdvancedSchedulerGroup(ctx context.Context, groupID *int64) (*routing.Group, bool) {
	if s == nil || groupID == nil || *groupID <= 0 {
		return nil, false
	}
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == *groupID {
		return group, true
	}
	if s.schedulerSnapshot != nil {
		if group, err := s.readSchedulingGroup(ctx, *groupID); err == nil && group != nil {
			return group, true
		}
	}
	if s.groupRepo == nil {
		return nil, false
	}
	group, err := s.groupRepo.GetByIDLite(ctx, *groupID)
	return group, err == nil && group != nil
}

func (s *GeminiMessagesCompatService) Forward(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte) (*forwardcore.MessagesResult, error) {
	beginGeminiImageOutputObservation(c)
	startTime := time.Now()

	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("missing model")
	}

	originalModel := req.Model
	// 所有 Gemini 账号类型都执行账号模型映射，OAuth 也必须与调度和可见模型解析保持一致。
	mappedModel := resolveAccountMappedModelForForward(account, req.Model)

	geminiReq, err := convertClaudeMessagesToGeminiGenerateContent(body)
	if err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	geminiReq = ensureGeminiFunctionCallThoughtSignatures(geminiReq)
	originalClaudeBody := body

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	requestIDHeader := "x-request-id"
	switch account.Record.Type {
	case capability.AccountTypeAPIKey, capability.AccountTypeOAuth, capability.AccountTypeServiceAccount:
	default:
		return nil, fmt.Errorf("unsupported account type: %s", account.Record.Type)
	}
	useUpstreamStream := req.Stream
	if account.Record.Type == capability.AccountTypeOAuth && !req.Stream && strings.TrimSpace(account.View().GetCredential("project_id")) != "" {
		useUpstreamStream = true
	}
	plan := s.geminiRequestPlan(account, mappedModel, "", false, req.Stream, useUpstreamStream, false)
	buildReq := func(ctx context.Context) (*http.Request, string, error) {
		return gemininative.BuildRequest(ctx, geminiReq, plan)
	}

	options := s.geminiExchangeOptions(c, ctx, account, mappedModel, geminiExchangeMessages, gemininative.OpenAICompatChatCompletions)
	options.Build = buildReq
	options.RequestIDHeader = requestIDHeader
	options.Do = func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
	}
	options.FilterThinking = func() []byte { return gatewayprovider.FilterThinkingBlocksForRetry(originalClaudeBody, originalModel) }
	options.FilterTools = func() []byte {
		return gatewayprovider.FilterSignatureSensitiveBlocksForRetry(originalClaudeBody, originalModel)
	}
	options.ReplaceBody = func(value []byte) { geminiReq = value }
	var requestID string
	var compatibilityResult *forwardcore.MessagesResult
	stopped := false
	target := &gemininative.Target{AccountID: account.Record.ID, Model: mappedModel, Mode: gemininative.MessagesResponse, Exchange: options, Response: s.geminiResponseAdapter(c).Options, StartedAt: startTime, UpstreamStream: useUpstreamStream, OAuth: account.Record.Type == capability.AccountTypeOAuth, Enter: s.nativeAttemptActivity}
	target.BeforeResponse = func(ctx context.Context, resp *http.Response, requestIDHeader string) (bool, error) {
		var callbackErr error
		compatibilityResult, callbackErr = func() (*forwardcore.MessagesResult, error) {

			if resp.StatusCode >= 400 {
				respBody := s.readUpstreamErrorBody(resp)
				decision := s.applyGeminiUpstreamErrorPolicy(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
				upstreamReqID := resp.Header.Get(requestIDHeader)
				if upstreamReqID == "" {
					upstreamReqID = resp.Header.Get("x-goog-request-id")
				}
				if decision.Policy == accountcore.ErrorPolicyCustomSkipped || decision.Policy == accountcore.ErrorPolicyPoolBypassed {
					if failoverErr := s.skippedErrorPolicyFailoverError(c, account, resp.StatusCode, respBody, upstreamReqID); failoverErr != nil {
						return nil, failoverErr
					}
					if decision.Policy == accountcore.ErrorPolicyCustomSkipped {
						return nil, s.writeGeminiCustomCodeSkippedError(c, account, resp.StatusCode, upstreamReqID, respBody, func() {
							_ = s.writeClaudeError(c, http.StatusInternalServerError, "api_error", geminiCustomCodeSkippedClientMessage)
						})
					}
					return nil, s.writeGeminiMappedError(c, account, resp.StatusCode, upstreamReqID, respBody)
				}
				if decision.ShouldReturnGenericError() {
					genericBody := []byte(`{"error":{"message":"Upstream gateway error"}}`)
					return nil, s.writeGeminiMappedError(c, account, http.StatusInternalServerError, upstreamReqID, genericBody)
				}
				msg400 := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
				googleConfigError := resp.StatusCode == http.StatusBadRequest && upstream.IsGoogleProjectConfigError(msg400)
				defaultFailover := googleConfigError || s.shouldFailoverGeminiUpstreamError(resp.StatusCode)
				if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, defaultFailover) {
					upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
					upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
					upstreamDetail := ""
					if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
						maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
						if maxBytes <= 0 {
							maxBytes = 2048
						}
						upstreamDetail = logredact.TruncateUTF8(string(respBody), maxBytes)
					}
					gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
						Platform:           account.Record.Platform,
						AccountID:          account.Record.ID,
						AccountName:        account.Record.Name,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  upstreamReqID,
						Kind:               "failover",
						Message:            upstreamMsg,
						Detail:             upstreamDetail,
					})
					if googleConfigError {
						log.Printf("[Gemini] status=400 google_config_error failover=true upstream_message=%q account=%d", upstreamMsg, account.Record.ID)
					}
					return nil, &forwardcore.UpstreamFailoverError{
						StatusCode:             resp.StatusCode,
						ResponseBody:           respBody,
						RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
					}
				}
				return nil, s.writeGeminiMappedError(c, account, resp.StatusCode, upstreamReqID, respBody)
			}

			requestID = resp.Header.Get(requestIDHeader)
			if requestID == "" {
				requestID = resp.Header.Get("x-goog-request-id")
			}
			if requestID != "" {
				c.Header("x-request-id", requestID)
			}

			return nil, nil
		}()
		stopped = resp.StatusCode >= 400 || callbackErr != nil || compatibilityResult != nil
		return stopped, callbackErr
	}
	result, executeErr := (gemininative.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocolcore.ProtocolAnthropicMessages, Body: body, Stream: req.Stream, ResponseModel: originalModel, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if stopped {
		return compatibilityResult, executeErr
	}
	if executeErr != nil {
		return nil, executeErr
	}
	requestID = result.RequestID
	usage := &result.Usage
	firstTokenMs := result.FirstTokenMs

	// 图片生成计费
	imageInputSize := s.extractImageInputSize(body)
	imageSize := media.NormalizeImageSizeTier(imageInputSize)
	imageCount := resolveGeminiImageCount(c, originalModel, mappedModel)

	return &forwardcore.MessagesResult{
		RequestID:       requestID,
		UpstreamHeaders: result.UpstreamHeaders,
		Usage:           *usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		Stream:          req.Stream,
		Duration:        result.Duration,
		FirstTokenMs:    firstTokenMs,
		ImageCount:      imageCount,
		ImageSize:       imageSize,
		ImageInputSize:  imageInputSize,
	}, nil
}

func (s *GeminiMessagesCompatService) ForwardNative(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, originalModel string, action string, stream bool, body []byte) (*forwardcore.MessagesResult, error) {
	beginGeminiImageOutputObservation(c)
	startTime := time.Now()

	if strings.TrimSpace(originalModel) == "" {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Missing model in URL")
	}
	if strings.TrimSpace(action) == "" {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Missing action in URL")
	}
	if len(body) == 0 {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Request body is empty")
	}

	// 过滤掉 parts 为空的消息（Gemini API 不接受空 parts）
	if filteredBody, err := gemini.FilterEmptyParts(body); err == nil {
		body = filteredBody
	}

	switch action {
	case "generateContent", "streamGenerateContent", "countTokens":
		// ok
	default:
		return nil, s.writeGoogleError(c, http.StatusNotFound, "Unsupported action: "+action)
	}

	// Some Gemini upstreams validate tool call parts strictly; ensure any `functionCall` part includes a
	// `thoughtSignature` to avoid frequent INVALID_ARGUMENT 400s.
	body = ensureGeminiFunctionCallThoughtSignatures(body)

	// 渠道映射后的模型进入账号后统一解析为最终上游模型，不按凭据类型跳过。
	mappedModel := resolveAccountMappedModelForForward(account, originalModel)

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	useUpstreamStream := stream
	upstreamAction := action
	if account.Record.Type == capability.AccountTypeOAuth && !stream && action == "generateContent" && strings.TrimSpace(account.View().GetCredential("project_id")) != "" {
		// Code Assist's non-streaming generateContent may return no content; use streaming upstream and aggregate.
		useUpstreamStream = true
		upstreamAction = "streamGenerateContent"
	}
	forceAIStudio := action == "countTokens"

	requestIDHeader := "x-request-id"
	switch account.Record.Type {
	case capability.AccountTypeAPIKey, capability.AccountTypeOAuth, capability.AccountTypeServiceAccount:
	default:
		return nil, s.writeGoogleError(c, http.StatusBadGateway, "Unsupported account type: "+account.Record.Type)
	}
	plan := s.geminiRequestPlan(account, mappedModel, upstreamAction, true, stream, useUpstreamStream, forceAIStudio)
	buildReq := func(ctx context.Context) (*http.Request, string, error) {
		return gemininative.BuildRequest(ctx, body, plan)
	}

	options := s.geminiExchangeOptions(c, ctx, account, mappedModel, geminiExchangeNative, gemininative.OpenAICompatChatCompletions)
	options.Build = buildReq
	options.RequestIDHeader = requestIDHeader
	options.Do = func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
	}
	options.CountFallback = action == "countTokens"
	options.EstimateCount = func() int { return gemininative.EstimateGeminiCountTokens(body) }
	var requestID string
	var compatibilityResult *forwardcore.MessagesResult
	stopped := false
	target := &gemininative.Target{AccountID: account.Record.ID, Model: mappedModel, Mode: gemininative.NativeResponse, Exchange: options, Response: s.geminiResponseAdapter(c).Options, StartedAt: startTime, UpstreamStream: useUpstreamStream, OAuth: account.Record.Type == capability.AccountTypeOAuth, Enter: s.nativeAttemptActivity}
	target.BeforeResponse = func(ctx context.Context, resp *http.Response, requestIDHeader string) (bool, error) {
		var callbackErr error
		compatibilityResult, callbackErr = func() (*forwardcore.MessagesResult, error) {

			requestID = resp.Header.Get(requestIDHeader)
			if requestID == "" {
				requestID = resp.Header.Get("x-goog-request-id")
			}
			if requestID != "" {
				c.Header("x-request-id", requestID)
			}

			isOAuth := account.Record.Type == capability.AccountTypeOAuth

			if resp.StatusCode >= 400 {
				respBody := s.readUpstreamErrorBody(resp)
				// Best-effort fallback for OAuth tokens missing AI Studio scopes when calling countTokens.
				// This avoids Gemini SDKs failing hard during preflight token counting.
				// Checked before error policy so it always works regardless of custom error codes.
				if action == "countTokens" && isOAuth && gemininative.IsGeminiInsufficientScope(resp.Header, respBody) {
					estimated := gemininative.EstimateGeminiCountTokens(body)
					c.JSON(http.StatusOK, map[string]any{"totalTokens": estimated})
					return &forwardcore.MessagesResult{
						RequestID:       requestID,
						UpstreamHeaders: resp.Header,
						Usage:           upstream.TokenUsage{},
						Model:           originalModel,
						UpstreamModel:   mappedModel,
						Stream:          false,
						Duration:        time.Since(startTime),
						FirstTokenMs:    nil,
					}, nil
				}

				decision := s.applyGeminiUpstreamErrorPolicy(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
				if decision.Policy == accountcore.ErrorPolicyCustomSkipped || decision.Policy == accountcore.ErrorPolicyPoolBypassed {
					if failoverErr := s.skippedErrorPolicyFailoverError(c, account, resp.StatusCode, respBody, requestID); failoverErr != nil {
						return nil, failoverErr
					}
					if decision.Policy == accountcore.ErrorPolicyCustomSkipped {
						return nil, s.writeGeminiCustomCodeSkippedError(c, account, resp.StatusCode, requestID, respBody, func() {
							_ = s.writeGoogleError(c, http.StatusInternalServerError, geminiCustomCodeSkippedClientMessage)
						})
					}
					return nil, s.writeGeminiNativeUpstreamError(c, account, resp, respBody, requestID, isOAuth)
				}
				if decision.ShouldReturnGenericError() {
					gatewayhttp.MarkResponseCommitted(c)
					c.JSON(http.StatusInternalServerError, gin.H{
						"error": gin.H{
							"code":    http.StatusInternalServerError,
							"message": "Upstream gateway error",
							"status":  "INTERNAL",
						},
					})
					return nil, fmt.Errorf("gemini upstream error: %d (not in custom error codes)", resp.StatusCode)
				}
				msg400 := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
				googleConfigError := resp.StatusCode == http.StatusBadRequest && upstream.IsGoogleProjectConfigError(msg400)
				defaultFailover := googleConfigError || s.shouldFailoverGeminiUpstreamError(resp.StatusCode)
				if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, defaultFailover) {
					evBody := gemininative.UnwrapIfNeeded(isOAuth, respBody)
					upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(evBody))
					upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
					upstreamDetail := ""
					if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
						maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
						if maxBytes <= 0 {
							maxBytes = 2048
						}
						upstreamDetail = logredact.TruncateUTF8(string(evBody), maxBytes)
					}
					gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
						Platform:           account.Record.Platform,
						AccountID:          account.Record.ID,
						AccountName:        account.Record.Name,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  requestID,
						Kind:               "failover",
						Message:            upstreamMsg,
						Detail:             upstreamDetail,
					})
					if googleConfigError {
						log.Printf("[Gemini] status=400 google_config_error failover=true upstream_message=%q account=%d", upstreamMsg, account.Record.ID)
					}
					return nil, &forwardcore.UpstreamFailoverError{
						StatusCode:             resp.StatusCode,
						ResponseBody:           evBody,
						RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
					}
				}

				return nil, s.writeGeminiNativeUpstreamError(c, account, resp, respBody, requestID, isOAuth)
			}

			return nil, nil
		}()
		stopped = resp.StatusCode >= 400 || callbackErr != nil || compatibilityResult != nil
		return stopped, callbackErr
	}
	result, executeErr := (gemininative.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocolcore.ProtocolGeminiGenerateContent, Body: body, Stream: stream, ResponseModel: originalModel, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if stopped {
		return compatibilityResult, executeErr
	}
	if executeErr != nil {
		return nil, executeErr
	}
	if result.EstimatedTokenCount != nil {
		return &forwardcore.MessagesResult{Usage: upstream.TokenUsage{}, Model: originalModel, UpstreamModel: mappedModel, Stream: false, Duration: result.Duration}, nil
	}
	requestID = result.RequestID
	usage := &result.Usage
	firstTokenMs := result.FirstTokenMs

	// 图片生成计费
	imageInputSize := s.extractImageInputSize(body)
	imageSize := media.NormalizeImageSizeTier(imageInputSize)
	imageCount := resolveGeminiImageCount(c, originalModel, mappedModel)

	return &forwardcore.MessagesResult{
		RequestID:       requestID,
		UpstreamHeaders: result.UpstreamHeaders,
		Usage:           *usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		Stream:          result.Stream,
		Duration:        result.Duration,
		FirstTokenMs:    firstTokenMs,
		ImageCount:      imageCount,
		ImageSize:       imageSize,
		ImageInputSize:  imageInputSize,
	}, nil
}

// checkErrorPolicyInLoop 在重试循环内预检查错误策略。
// 返回 true 表示策略已匹配（调用者应 break），resp 已重建可直接使用。
// 返回 false 表示 ErrorPolicyNone，resp 已重建，调用者继续走重试逻辑。
func (s *GeminiMessagesCompatService) checkErrorPolicyInLoop(
	ctx context.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, mappedModel string,
) (matched bool, rebuilt *http.Response) {
	if resp.StatusCode < 400 || s.rateLimitService == nil {
		return false, resp
	}
	body := s.readUpstreamErrorBody(resp)
	_ = resp.Body.Close()
	rebuilt = &http.Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	policy := s.rateLimitService.UpstreamHealth().CheckErrorPolicy(ctx, gatewayprovider.ExecutionRecord(account), gatewayprovider.HealthObservationFromContext(ctx, resp.StatusCode, nil, body, []string{mappedModel}))
	if policy == accountcore.ErrorPolicyTempUnscheduled {
		// CheckErrorPolicy 已写入临时不可调度状态，给最终错误处理留下内部标记，
		// 避免同一个响应再次执行规则并重复写库。
		rebuilt.Header.Set(geminiAppliedTempPolicyHeader, "1")
	}
	// 池模式由 handler 层按账号配置的重试预算处理，不能再叠加 Gemini 固定内部重试。
	return policy != accountcore.ErrorPolicyNone, rebuilt
}

func (s *GeminiMessagesCompatService) shouldRetryGeminiUpstreamError(account *gatewayprovider.ExecutionAccount, statusCode int) bool {
	switch statusCode {
	case 429, 500, 502, 503, 504, 529:
		return true
	case 403:
		// GeminiCli OAuth occasionally returns 403 transiently (activation/quota propagation); allow retry.
		if account == nil || account.Record.Type != capability.AccountTypeOAuth {
			return false
		}
		oauthType := strings.ToLower(strings.TrimSpace(account.View().GetCredential("oauth_type")))
		if oauthType == "" && strings.TrimSpace(account.View().GetCredential("project_id")) != "" {
			// Legacy/implicit Code Assist OAuth accounts.
			oauthType = "code_assist"
		}
		return oauthType == "code_assist"
	default:
		return false
	}
}

func (s *GeminiMessagesCompatService) shouldFailoverGeminiUpstreamError(statusCode int) bool {
	switch statusCode {
	case 401, 403, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

// skippedErrorPolicyFailoverError 处理 ErrorPolicySkipped：跳过账号状态写入不等于跳过换号。
// 可切换的状态码返回 UpstreamFailoverError；池模式仅对配置的状态允许同账号重试。
func (s *GeminiMessagesCompatService) skippedErrorPolicyFailoverError(c *gin.Context, account *gatewayprovider.ExecutionAccount, statusCode int, respBody []byte, upstreamRequestID string) *forwardcore.UpstreamFailoverError {
	if !s.shouldFailoverGeminiUpstreamError(statusCode) {
		return nil
	}
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: statusCode,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "failover",
		Message:            upstreamMsg,
		Detail:             s.upstreamErrorDetail(respBody),
	})
	return &forwardcore.UpstreamFailoverError{
		StatusCode:             statusCode,
		ResponseBody:           respBody,
		RetryableOnSameAccount: account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(statusCode),
	}
}

const geminiCustomCodeSkippedClientMessage = "Upstream gateway error"

// upstreamErrorDetail 按配置截断上游错误体，用于运维日志。
func (s *GeminiMessagesCompatService) upstreamErrorDetail(body []byte) string {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.LogUpstreamErrorBody {
		return ""
	}
	maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
	if maxBytes <= 0 {
		maxBytes = 2048
	}
	return logredact.TruncateUTF8(string(body), maxBytes)
}

// writeGeminiCustomCodeSkippedError 对自定义错误码未命中的请求隐藏上游细节并返回 500。
func (s *GeminiMessagesCompatService) writeGeminiCustomCodeSkippedError(c *gin.Context, account *gatewayprovider.ExecutionAccount, upstreamStatus int, upstreamRequestID string, body []byte, write func()) error {
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	upstreamDetail := s.upstreamErrorDetail(body)
	gatewayhttp.SetOpsUpstreamError(c, upstreamStatus, upstreamMsg, upstreamDetail)
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: upstreamStatus,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	write()
	if upstreamMsg == "" {
		return fmt.Errorf("gemini upstream error: %d (not in custom error codes)", upstreamStatus)
	}
	return fmt.Errorf("gemini upstream error: %d (not in custom error codes) message=%s", upstreamStatus, upstreamMsg)
}

// writeGeminiNativeUpstreamError 按原始状态码和响应体透传不可切换的 Gemini 错误。
func (s *GeminiMessagesCompatService) writeGeminiNativeUpstreamError(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, respBody []byte, requestID string, isOAuth bool) error {
	respBody = gemininative.UnwrapIfNeeded(isOAuth, respBody)
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
	upstreamDetail := s.upstreamErrorDetail(respBody)
	gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  requestID,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	gatewayhttp.MarkResponseCommitted(c)
	c.Data(resp.StatusCode, contentType, respBody)
	if upstreamMsg == "" {
		return fmt.Errorf("gemini upstream error: %d", resp.StatusCode)
	}
	return fmt.Errorf("gemini upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
}

func (s *GeminiMessagesCompatService) writeGeminiMappedError(c *gin.Context, account *gatewayprovider.ExecutionAccount, upstreamStatus int, upstreamRequestID string, body []byte) error {
	gatewayhttp.MarkResponseCommitted(c)
	upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	gatewayhttp.SetOpsUpstreamError(c, upstreamStatus, upstreamMsg, upstreamDetail)
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Record.Platform,
		AccountID:          account.Record.ID,
		AccountName:        account.Record.Name,
		UpstreamStatusCode: upstreamStatus,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini] upstream error %d: %s", upstreamStatus, truncateForLog(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes))
	}

	if status, errType, errMsg, matched := gatewayhttp.ApplyErrorPassthroughRule(
		c,
		capability.PlatformGemini,
		upstreamStatus,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		c.JSON(status, gin.H{
			"type":  "error",
			"error": gin.H{"type": errType, "message": errMsg},
		})
		if upstreamMsg == "" {
			upstreamMsg = errMsg
		}
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d (passthrough rule matched)", upstreamStatus)
		}
		return fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", upstreamStatus, upstreamMsg)
	}

	var statusCode int
	var errType, errMsg string

	if mapped := gatewayhttp.MapGeminiErrorBodyToClaudeError(body); mapped != nil {
		errType = mapped.Type
		if mapped.Message != "" {
			errMsg = mapped.Message
		}
		if mapped.StatusCode > 0 {
			statusCode = mapped.StatusCode
		}
	}

	switch upstreamStatus {
	case 400:
		if statusCode == 0 {
			statusCode = http.StatusBadRequest
		}
		if errType == "" {
			errType = "invalid_request_error"
		}
		if errMsg == "" {
			if upstreamMsg != "" {
				errMsg = upstreamMsg
			} else {
				errMsg = "Invalid request"
			}
		}
	case 401:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			errType = "authentication_error"
		}
		if errMsg == "" {
			errMsg = "Upstream authentication failed, please contact administrator"
		}
	case 403:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			errType = "permission_error"
		}
		if errMsg == "" {
			errMsg = "Upstream access forbidden, please contact administrator"
		}
	case 404:
		if statusCode == 0 {
			statusCode = http.StatusNotFound
		}
		if errType == "" {
			errType = "not_found_error"
		}
		if errMsg == "" {
			errMsg = "Resource not found"
		}
	case 429:
		if statusCode == 0 {
			statusCode = http.StatusTooManyRequests
		}
		if errType == "" {
			errType = "rate_limit_error"
		}
		if errMsg == "" {
			errMsg = "Upstream rate limit exceeded, please retry later"
		}
	case 529:
		if statusCode == 0 {
			statusCode = http.StatusServiceUnavailable
		}
		if errType == "" {
			errType = "overloaded_error"
		}
		if errMsg == "" {
			errMsg = "Upstream service overloaded, please retry later"
		}
	case 500, 502, 503, 504:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			switch upstreamStatus {
			case 504:
				errType = "timeout_error"
			case 503:
				errType = "overloaded_error"
			default:
				errType = "api_error"
			}
		}
		if errMsg == "" {
			errMsg = "Upstream service temporarily unavailable"
		}
	default:
		if statusCode == 0 {
			statusCode = http.StatusBadGateway
		}
		if errType == "" {
			errType = "upstream_error"
		}
		if errMsg == "" {
			errMsg = "Upstream request failed"
		}
	}

	c.JSON(statusCode, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": errMsg},
	})
	if upstreamMsg == "" {
		return fmt.Errorf("upstream error: %d", upstreamStatus)
	}
	return fmt.Errorf("upstream error: %d message=%s", upstreamStatus, upstreamMsg)
}

func (s *GeminiMessagesCompatService) writeClaudeError(c *gin.Context, status int, errType, message string) error {
	gatewayhttp.MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return fmt.Errorf("%s", message)
}

func (s *GeminiMessagesCompatService) writeGoogleError(c *gin.Context, status int, message string) error {
	gatewayhttp.MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  gatewayhttp.HTTPStatusToGoogleStatus(status),
		},
	})
	return fmt.Errorf("%s", message)
}

func (s *GeminiMessagesCompatService) handleNativeNonStreamingResponse(c *gin.Context, resp *http.Response, isOAuth bool) (*upstream.TokenUsage, error) {
	return s.geminiResponseAdapter(c).HandleNativeNonStreamingResponse(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, isOAuth)
}

func (s *GeminiMessagesCompatService) ForwardAIStudioGET(ctx context.Context, account *gatewayprovider.ExecutionAccount, path string) (*gemininative.HTTPResult, error) {
	if account == nil {
		return nil, errors.New("account is nil")
	}
	options := gemininative.ModelGetOptions{Mode: gemininative.CredentialMode(account.Record.Type), BaseURL: func() string { return account.View().GetGeminiBaseURL(geminicli.AIStudioBaseURL) }, APIKey: func() string { return account.View().GetCredential("api_key") }, ValidateURL: s.validateUpstreamBaseURL, Enter: s.nativeAttemptActivity, Do: func(req *http.Request) (*http.Response, error) {
		proxy := ""
		if account.Record.ProxyID != nil && account.Record.Proxy != nil {
			proxy = account.Record.Proxy.URL()
		}
		return s.httpUpstream.Do(req, proxy, account.Record.ID, account.Record.Concurrency)
	}, FilterHeaders: func(header http.Header) http.Header {
		return provider.FilterHeaders(header, s.responseHeaderFilter)
	}}
	if s.tokenProvider != nil {
		options.Token = func(ctx context.Context) (string, error) { return accountToken(ctx, s.tokenProvider, account) }
	}
	return gemininative.ReadAIStudioModel(ctx, path, options)
}

func (s *GeminiMessagesCompatService) handleGeminiUpstreamError(ctx context.Context, account *gatewayprovider.ExecutionAccount, statusCode int, headers http.Header, body []byte) {
	// 遵守自定义错误码策略：未命中则跳过所有限流处理
	if !account.View().ShouldHandleErrorCode(statusCode) {
		return
	}
	if s.rateLimitService != nil && (statusCode == 401 || statusCode == 403 || statusCode == 529) {
		gatewayprovider.ApplyExecutionHealth(ctx, s.rateLimitService.UpstreamHealth(), account, gatewayprovider.HealthObservationFromContext(ctx, statusCode, headers, body, nil))
		return
	}
	if statusCode != 429 {
		return
	}
	// 池模式账号保留在上游账号池中，由请求级重试或切号消化 429；
	// 管理员显式配置的自定义错误策略优先，命中时仍允许写入账号状态。
	if account.View().IsPoolMode() && !account.View().IsCustomErrorCodesEnabled() {
		return
	}

	oauthType := account.View().GeminiOAuthType()
	tierID := account.View().GeminiTierID()
	projectID := strings.TrimSpace(account.View().GetCredential("project_id"))
	isCodeAssist := account.View().IsGeminiCodeAssist()

	if account.View().IsGeminiThirdPartyProvider() {
		// 第三方兼容端点不得解析 Google 官方日配额文案，始终使用通用 429 冷却。
		cooldown := 5 * time.Minute
		if s.quotaPrecheck != nil {
			cooldown = s.quotaPrecheck.GeminiCooldown(ctx, gatewayprovider.ExecutionRecord(account))
		}
		ra := time.Now().Add(cooldown)
		_ = s.accountRepo.SetRateLimited(ctx, account.Record.ID, ra)
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (third-party API Key) rate limited, cooldown=%v", account.Record.ID, time.Until(ra).Truncate(time.Second))
		return
	}

	resetAt := ParseGeminiRateLimitResetTime(body)
	if resetAt == nil {
		// 根据账号类型使用不同的默认重置时间
		var ra time.Time
		if isCodeAssist || oauthType == "google_one" {
			// Gemini CLI / Google One：按层级回退冷却时间
			cooldown := accountcore.GeminiCooldownForTier(tierID)
			if s.quotaPrecheck != nil {
				cooldown = s.quotaPrecheck.GeminiCooldown(ctx, gatewayprovider.ExecutionRecord(account))
			}
			ra = time.Now().Add(cooldown)
			if isCodeAssist {
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (Code Assist, tier=%s, project=%s) rate limited, cooldown=%v", account.Record.ID, tierID, projectID, time.Until(ra).Truncate(time.Second))
			} else {
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (Google One OAuth, tier=%s, project=%s) rate limited, cooldown=%v", account.Record.ID, tierID, projectID, time.Until(ra).Truncate(time.Second))
			}
		} else {
			// API Key / AI Studio OAuth: PST 午夜
			if ts := nextGeminiDailyResetUnix(); ts != nil {
				ra = time.Unix(*ts, 0)
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (API Key/AI Studio, type=%s) rate limited, reset at PST midnight (%v)", account.Record.ID, account.Record.Type, ra)
			} else {
				// 兜底：5 分钟
				ra = time.Now().Add(5 * time.Minute)
				logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d rate limited, fallback to 5min", account.Record.ID)
			}
		}
		_ = s.accountRepo.SetRateLimited(ctx, account.Record.ID, ra)
		return
	}

	// 使用解析到的重置时间
	resetTime := time.Unix(*resetAt, 0)
	_ = s.accountRepo.SetRateLimited(ctx, account.Record.ID, resetTime)
	logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d rate limited until %v (oauth_type=%s, tier=%s)",
		account.Record.ID, resetTime, oauthType, tierID)
}

// applyGeminiUpstreamErrorPolicy 统一 Gemini 三种协议入口的显式策略和默认状态处理。
// 池模式绕过时绝不能继续调用 handleGeminiUpstreamError，否则 429 会写入本地限流。
func (s *GeminiMessagesCompatService) applyGeminiUpstreamErrorPolicy(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	statusCode int,
	headers http.Header,
	body []byte,
	mappedModel string,
) accountcore.UpstreamErrorDecision {
	decision := accountcore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(account), statusCode)
	if s == nil || account == nil {
		return decision
	}
	if headers != nil && headers.Get(geminiAppliedTempPolicyHeader) == "1" {
		headers.Del(geminiAppliedTempPolicyHeader)
		decision.Policy = accountcore.ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		return decision
	}
	if s.rateLimitService != nil {
		decision.Policy = s.rateLimitService.UpstreamHealth().ApplyExplicitErrorPolicy(ctx, gatewayprovider.ExecutionRecord(account), gatewayprovider.HealthObservationFromContext(ctx, statusCode, nil, body, []string{mappedModel}))
		decision.StopScheduling = decision.Policy == accountcore.ErrorPolicyCustomMatched || decision.Policy == accountcore.ErrorPolicyTempUnscheduled
	}
	switch decision.Policy {
	case accountcore.ErrorPolicyCustomMatched, accountcore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case accountcore.ErrorPolicyCustomSkipped, accountcore.ErrorPolicyPoolBypassed:
		return decision
	}
	s.handleGeminiUpstreamError(ctx, account, statusCode, headers, body)
	return decision
}

func ParseGeminiRateLimitResetTime(body []byte) *int64 {
	return gemininative.ParseGeminiRateLimitResetTime(body, nextGeminiDailyResetUnix)
}

func nextGeminiDailyResetUnix() *int64 {
	reset := geminiDailyResetTime(time.Now())
	ts := reset.Unix()
	return &ts
}

// ensureGeminiFunctionCallThoughtSignatures 委托原生 Gemini 方言的纯转换，调用顺序由旧平台保留。
func ensureGeminiFunctionCallThoughtSignatures(body []byte) []byte {
	return bridge.NativeEnsureGeminiFunctionCallThoughtSignatures(bridge.NativeGeminiOptions{DummyThoughtSignature: geminiDummyThoughtSignature}, body)
}

// convertClaudeMessagesToGeminiGenerateContent 委托原生 Gemini 方言的纯转换，调用顺序由旧平台保留。
func convertClaudeMessagesToGeminiGenerateContent(body []byte) ([]byte, error) {
	return bridge.NativeConvertClaudeMessagesToGeminiGenerateContent(bridge.NativeGeminiOptions{DummyThoughtSignature: geminiDummyThoughtSignature}, body)
}

func (s *GeminiMessagesCompatService) extractImageInputSize(body []byte) string {
	var req struct {
		GenerationConfig *struct {
			ImageConfig *struct {
				ImageSize string `json:"imageSize"`
			} `json:"imageConfig"`
		} `json:"generationConfig"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}

	if req.GenerationConfig != nil && req.GenerationConfig.ImageConfig != nil {
		return strings.TrimSpace(req.GenerationConfig.ImageConfig.ImageSize)
	}

	return ""
}

// BindNativeAttemptActivity 只绑定 app 拥有的同步资源屏障。
func (s *GeminiMessagesCompatService) BindNativeAttemptActivity(enter func() (func(), error)) {
	s.nativeAttemptActivity = enter
}

// BindQuotaPrecheck 在开放请求前绑定唯一配额预检和日统计缓存。
func (s *GeminiMessagesCompatService) BindQuotaPrecheck(precheck *accountcore.GeminiPrecheck) {
	s.quotaPrecheck = precheck
}
