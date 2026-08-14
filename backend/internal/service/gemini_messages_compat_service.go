package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	mathrand "math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/geminicli"
	"github.com/TokenFlux/TokenRouter/internal/pkg/googleapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"
	"github.com/TokenFlux/TokenRouter/internal/util/urlvalidator"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
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
	accountRepo               AccountRepository
	groupRepo                 GroupRepository
	cache                     GatewayCache
	schedulerSnapshot         *SchedulerSnapshotService
	tokenProvider             *GeminiTokenProvider
	rateLimitService          *RateLimitService
	httpUpstream              HTTPUpstream
	antigravityGatewayService *AntigravityGatewayService
	cfg                       *config.Config
	responseHeaderFilter      *responseheaders.CompiledHeaderFilter
	advancedAccountStats      *advancedAccountRuntimeStats
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
	accountRepo AccountRepository,
	groupRepo GroupRepository,
	cache GatewayCache,
	schedulerSnapshot *SchedulerSnapshotService,
	tokenProvider *GeminiTokenProvider,
	rateLimitService *RateLimitService,
	httpUpstream HTTPUpstream,
	antigravityGatewayService *AntigravityGatewayService,
	cfg *config.Config,
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
		responseHeaderFilter:      compileResponseHeaderFilter(cfg),
		advancedAccountStats:      newAdvancedAccountRuntimeStats(),
	}
}

// GetTokenProvider returns the token provider for OAuth accounts
func (s *GeminiMessagesCompatService) GetTokenProvider() *GeminiTokenProvider {
	return s.tokenProvider
}

func (s *GeminiMessagesCompatService) SelectAccountForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*Account, error) {
	return s.SelectAccountForModelWithExclusions(ctx, groupID, sessionHash, requestedModel, nil)
}

func (s *GeminiMessagesCompatService) SelectAccountForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*Account, error) {
	// 1. 确定目标平台和调度模式
	platform, useMixedScheduling, hasForcePlatform, group, err := s.resolvePlatformAndSchedulingMode(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group != nil {
		// 后续高级评分必须读取本次解析出的最终分组覆盖，而非再次走可能不同的缓存来源。
		ctx = context.WithValue(ctx, ctxkey.Group, group)
	}

	cacheKey := "gemini:" + sessionHash
	usesAdvancedScheduler := group != nil && group.UsesAdvancedScheduler()
	advancedSettings := s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID)

	// 2. 尝试粘性会话命中
	// 高级粘性加权会在全部硬过滤后的候选评分中处理缓存绑定，因此不允许旧硬粘性提前返回。
	if !usesAdvancedScheduler || !advancedSettings.stickyWeightedEnabled {
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
	var selected *Account
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
		_ = s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), cacheKey, selected.ID, geminiStickySessionTTL)
	}

	return s.hydrateSelectedAccount(ctx, selected)
}

// resolvePlatformAndSchedulingMode 解析目标平台和调度模式。
// 返回：平台名称、是否使用混合调度、是否强制平台、已解析分组、错误。
func (s *GeminiMessagesCompatService) resolvePlatformAndSchedulingMode(ctx context.Context, groupID *int64) (platform string, useMixedScheduling bool, hasForcePlatform bool, group *Group, err error) {
	// 优先检查 context 中的强制平台（/antigravity 路由）
	forcePlatform, hasForcePlatform := ctx.Value(ctxkey.ForcePlatform).(string)
	if hasForcePlatform && forcePlatform != "" {
		if groupID == nil {
			return forcePlatform, false, true, nil, nil
		}
		if ctxGroup, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(ctxGroup) && ctxGroup.ID == *groupID {
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
		if ctxGroup, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(ctxGroup) && ctxGroup.ID == *groupID {
			group = ctxGroup
		} else {
			group, err = s.groupRepo.GetByIDLite(ctx, *groupID)
			if err != nil {
				return "", false, false, nil, fmt.Errorf("get group failed: %w", err)
			}
		}
		// gemini 分组支持混合调度（包含启用了 mixed_scheduling 的 antigravity 账户）
		return group.Platform, group.Platform == PlatformGemini, false, group, nil
	}

	// 无分组时只使用原生 gemini 平台
	return PlatformGemini, true, false, nil, nil
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
) *Account {
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
	account *Account,
	requestedModel, platform string,
	useMixedScheduling bool,
) bool {
	return s.isAccountUsableForRequestWithPrecheck(ctx, account, requestedModel, platform, useMixedScheduling, nil)
}

func (s *GeminiMessagesCompatService) isAccountUsableForRequestWithPrecheck(
	ctx context.Context,
	account *Account,
	requestedModel, platform string,
	useMixedScheduling bool,
	precheckResult map[int64]bool,
) bool {
	// 检查模型调度能力
	// Check model scheduling capability
	if !account.IsSchedulableForModelWithContext(ctx, requestedModel) {
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
func (s *GeminiMessagesCompatService) isAccountValidForPlatform(account *Account, platform string, useMixedScheduling bool) bool {
	if account.Platform == platform {
		return true
	}
	if useMixedScheduling && account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled() {
		return true
	}
	return false
}

func (s *GeminiMessagesCompatService) passesRateLimitPreCheckWithCache(ctx context.Context, account *Account, requestedModel string, precheckResult map[int64]bool) bool {
	if s.rateLimitService == nil || requestedModel == "" {
		return true
	}

	if precheckResult != nil {
		if ok, exists := precheckResult[account.ID]; exists {
			return ok
		}
	}

	ok, err := s.rateLimitService.PreCheckUsage(ctx, account, requestedModel)
	if err != nil {
		logger.LegacyPrintf("service.gemini_messages_compat", "[Gemini PreCheck] Account %d precheck error: %v", account.ID, err)
	}
	return ok
}

// eligibleGeminiAccounts 在高级和基础调度器前复用 Gemini 现有的全部硬过滤规则。
func (s *GeminiMessagesCompatService) eligibleGeminiAccounts(
	ctx context.Context,
	accounts []Account,
	requestedModel string,
	excludedIDs map[int64]struct{},
	platform string,
	useMixedScheduling bool,
) []*Account {
	precheckResult := s.buildPreCheckUsageResultMap(ctx, accounts, requestedModel)
	eligible := make([]*Account, 0, len(accounts))

	for i := range accounts {
		acc := &accounts[i]

		// 跳过被排除的账号
		if _, excluded := excludedIDs[acc.ID]; excluded {
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

func (s *GeminiMessagesCompatService) selectBestGeminiAccountFromEligible(eligible []*Account) *Account {
	var selected *Account
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
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) && group.ID == *groupID {
		return group.UsesAdvancedScheduler()
	}
	if s.schedulerSnapshot != nil {
		if group, err := s.schedulerSnapshot.GetGroupByID(ctx, *groupID); err == nil && group != nil {
			return group.UsesAdvancedScheduler()
		}
	}
	if s.groupRepo == nil {
		return false
	}
	group, err := s.groupRepo.GetByIDLite(ctx, *groupID)
	return err == nil && group != nil && group.UsesAdvancedScheduler()
}

func (s *GeminiMessagesCompatService) advancedSchedulerStats() *advancedAccountRuntimeStats {
	if s == nil {
		return nil
	}
	if s.rateLimitService != nil {
		if stats := s.rateLimitService.AdvancedSchedulerRuntimeStats(); stats != nil {
			return stats
		}
	}
	if s.advancedAccountStats == nil {
		s.advancedAccountStats = newAdvancedAccountRuntimeStats()
	}
	return s.advancedAccountStats
}

// advancedSchedulerEffectiveSettingsForRequest 返回最终分组的高级调度有效配置。
func (s *GeminiMessagesCompatService) advancedSchedulerEffectiveSettingsForRequest(ctx context.Context, groupID *int64) advancedSchedulerEffectiveSettings {
	gateway := &OpenAIGatewayService{cfg: s.cfg, rateLimitService: s.rateLimitService, schedulerSnapshot: s.schedulerSnapshot}
	return gateway.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID)
}

// selectAdvancedGeminiAccount 在 Gemini 已完成硬过滤后复用通用高级评分与 Top-K 选择。
func (s *GeminiMessagesCompatService) selectAdvancedGeminiAccount(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	cacheKey string,
	eligible []*Account,
	settings advancedSchedulerEffectiveSettings,
) *Account {
	if len(eligible) == 0 {
		return nil
	}
	var stickyAccountID int64
	if sessionHash != "" && s.cache != nil {
		stickyAccountID, _ = s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
	}
	input := advancedSchedulerSelectionInput{
		GroupID:         groupID,
		SessionHash:     cacheKey,
		StickyAccountID: stickyAccountID,
		StickyWeighted:  settings.stickyWeightedEnabled,
		TopK:            settings.topK,
	}
	candidates, _ := scoreAdvancedSchedulerCandidates(
		eligible,
		nil,
		s.advancedSchedulerStats(),
		settings.weights,
		input,
		time.Now(),
	)
	selectionOrder := buildAdvancedSchedulerSelectionOrder(candidates, input)
	if len(selectionOrder) == 0 {
		return nil
	}
	return selectionOrder[0].account
}

// groupModelUnsupportedErrorIfApplicable 在确认是分组模型限制时返回 typed error。
func (s *GeminiMessagesCompatService) groupModelUnsupportedErrorIfApplicable(ctx context.Context, accounts []Account, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixedScheduling bool) error {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(accounts) == 0 {
		return nil
	}
	precheckResult := s.buildPreCheckUsageResultMap(ctx, accounts, requestedModel)
	hasRelevantAccount := false
	for i := range accounts {
		acc := &accounts[i]
		if excludedIDs != nil {
			if _, excluded := excludedIDs[acc.ID]; excluded {
				continue
			}
		}
		if !acc.IsSchedulable() || !s.isAccountValidForPlatform(acc, platform, useMixedScheduling) || !s.passesRateLimitPreCheckWithCache(ctx, acc, requestedModel, precheckResult) {
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
	return newGroupModelUnsupportedError(platform, requestedModel, accounts)
}

func (s *GeminiMessagesCompatService) buildPreCheckUsageResultMap(ctx context.Context, accounts []Account, requestedModel string) map[int64]bool {
	if s.rateLimitService == nil || requestedModel == "" || len(accounts) == 0 {
		return nil
	}

	candidates := make([]*Account, 0, len(accounts))
	for i := range accounts {
		candidates = append(candidates, &accounts[i])
	}

	result, err := s.rateLimitService.PreCheckUsageBatch(ctx, candidates, requestedModel)
	if err != nil {
		logger.LegacyPrintf("service.gemini_messages_compat", "[Gemini PreCheckBatch] failed: %v", err)
	}
	return result
}

// isBetterGeminiAccount 判断 candidate 是否比 current 更优。
// 规则：优先级更高（数值更小）优先；同优先级时，未使用过的优先（OAuth > 非 OAuth），其次是最久未使用的。
//
// isBetterGeminiAccount checks if candidate is better than current.
// Rules: higher priority (lower value) wins; same priority: never used (OAuth > non-OAuth) > least recently used.
func (s *GeminiMessagesCompatService) isBetterGeminiAccount(candidate, current *Account) bool {
	// 优先级更高（数值更小）
	if candidate.Priority < current.Priority {
		return true
	}
	if candidate.Priority > current.Priority {
		return false
	}

	// 同优先级，比较最后使用时间
	switch {
	case candidate.LastUsedAt == nil && current.LastUsedAt != nil:
		// candidate 从未使用，优先
		return true
	case candidate.LastUsedAt != nil && current.LastUsedAt == nil:
		// current 从未使用，保持
		return false
	case candidate.LastUsedAt == nil && current.LastUsedAt == nil:
		// 都未使用，优先选择 OAuth 账号（更兼容 Code Assist 流程）
		return candidate.Type == AccountTypeOAuth && current.Type != AccountTypeOAuth
	default:
		// 都使用过，选择最久未使用的
		return candidate.LastUsedAt.Before(*current.LastUsedAt)
	}
}

// isModelSupportedByAccount 根据账户平台检查模型支持
func (s *GeminiMessagesCompatService) isModelSupportedByAccount(account *Account, requestedModel string) bool {
	if account.Platform == PlatformAntigravity {
		if strings.TrimSpace(requestedModel) == "" {
			return true
		}
		return mapAntigravityModel(account, requestedModel) != ""
	}
	return account.IsModelSupported(requestedModel)
}

// GetAntigravityGatewayService 返回 AntigravityGatewayService
func (s *GeminiMessagesCompatService) GetAntigravityGatewayService() *AntigravityGatewayService {
	return s.antigravityGatewayService
}

func (s *GeminiMessagesCompatService) getSchedulableAccount(ctx context.Context, accountID int64) (*Account, error) {
	if s.schedulerSnapshot != nil {
		return s.schedulerSnapshot.GetAccount(ctx, accountID)
	}
	return s.accountRepo.GetByID(ctx, accountID)
}

func (s *GeminiMessagesCompatService) hydrateSelectedAccount(ctx context.Context, account *Account) (*Account, error) {
	if account == nil || s.schedulerSnapshot == nil {
		return account, nil
	}
	hydrated, err := s.schedulerSnapshot.GetAccount(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, fmt.Errorf("selected gemini account %d not found during hydration", account.ID)
	}
	return hydrated, nil
}

func (s *GeminiMessagesCompatService) listSchedulableAccountsOnce(ctx context.Context, groupID *int64, platform string, hasForcePlatform bool) ([]Account, error) {
	if s.schedulerSnapshot != nil {
		accounts, _, err := s.schedulerSnapshot.ListSchedulableAccounts(ctx, groupID, platform, hasForcePlatform)
		return accounts, err
	}

	useMixedScheduling := platform == PlatformGemini && !hasForcePlatform
	queryPlatforms := []string{platform}
	if useMixedScheduling {
		queryPlatforms = []string{platform, PlatformAntigravity}
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
		normalized, err := urlvalidator.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid base_url: %w", err)
		}
		return normalized, nil
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
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
	accounts, err := s.listSchedulableAccountsOnce(ctx, groupID, PlatformAntigravity, false)
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
func (s *GeminiMessagesCompatService) SelectAccountForAIStudioEndpoints(ctx context.Context, groupID *int64) (*Account, error) {
	if group, ok := s.resolveAdvancedSchedulerGroup(ctx, groupID); ok {
		ctx = context.WithValue(ctx, ctxkey.Group, group)
	}
	accounts, err := s.listSchedulableAccountsOnce(ctx, groupID, PlatformGemini, true)
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}
	if len(accounts) == 0 {
		return nil, errors.New("no available Gemini accounts")
	}

	rank := func(a *Account) int {
		if a == nil {
			return 999
		}
		switch a.Type {
		case AccountTypeAPIKey:
			if strings.TrimSpace(a.GetCredential("api_key")) != "" {
				return 0
			}
			return 9
		case AccountTypeOAuth:
			if strings.TrimSpace(a.GetCredential("project_id")) == "" {
				return 1
			}
			if strings.TrimSpace(a.GetCredential("oauth_type")) == "ai_studio" {
				return 2
			}
			// Code Assist OAuth tokens often lack AI Studio scopes for models listing.
			return 3
		case AccountTypeServiceAccount:
			// Vertex service accounts use aiplatform.googleapis.com, not the AI Studio
			// endpoint (generativelanguage.googleapis.com), so they cannot serve these requests.
			return 999
		default:
			return 10
		}
	}

	var selected *Account
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

		if acc.Priority < selected.Priority {
			selected = acc
		} else if acc.Priority == selected.Priority {
			switch {
			case acc.LastUsedAt == nil && selected.LastUsedAt != nil:
				selected = acc
			case acc.LastUsedAt != nil && selected.LastUsedAt == nil:
				// keep selected
			case acc.LastUsedAt == nil && selected.LastUsedAt == nil:
				if acc.Type == AccountTypeOAuth && selected.Type != AccountTypeOAuth {
					selected = acc
				}
			default:
				if acc.LastUsedAt.Before(*selected.LastUsedAt) {
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
			eligible := make([]*Account, 0, len(accounts))
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
func (s *GeminiMessagesCompatService) resolveAdvancedSchedulerGroup(ctx context.Context, groupID *int64) (*Group, bool) {
	if s == nil || groupID == nil || *groupID <= 0 {
		return nil, false
	}
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) && group.ID == *groupID {
		return group, true
	}
	if s.schedulerSnapshot != nil {
		if group, err := s.schedulerSnapshot.GetGroupByID(ctx, *groupID); err == nil && group != nil {
			return group, true
		}
	}
	if s.groupRepo == nil {
		return nil, false
	}
	group, err := s.groupRepo.GetByIDLite(ctx, *groupID)
	return group, err == nil && group != nil
}

func (s *GeminiMessagesCompatService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
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
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	var requestIDHeader string
	var buildReq func(ctx context.Context) (*http.Request, string, error)
	useUpstreamStream := req.Stream
	if account.Type == AccountTypeOAuth && !req.Stream && strings.TrimSpace(account.GetCredential("project_id")) != "" {
		// Code Assist's non-streaming generateContent may return no content; use streaming upstream and aggregate.
		useUpstreamStream = true
	}

	switch account.Type {
	case AccountTypeAPIKey:
		buildReq = func(ctx context.Context) (*http.Request, string, error) {
			apiKey := account.GetCredential("api_key")
			if strings.TrimSpace(apiKey) == "" {
				return nil, "", errors.New("gemini api_key not configured")
			}

			baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
			normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, "", err
			}

			action := "generateContent"
			if req.Stream {
				action = "streamGenerateContent"
			}
			fullURL, err := buildGeminiAIStudioModelActionURL(normalizedBaseURL, mappedModel, action, req.Stream)
			if err != nil {
				return nil, "", err
			}

			restGeminiReq := normalizeGeminiRequestForAIStudio(geminiReq)
			upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(restGeminiReq))
			if err != nil {
				return nil, "", err
			}
			upstreamReq.Header.Set("Content-Type", "application/json")
			upstreamReq.Header.Set("x-goog-api-key", apiKey)
			return upstreamReq, "x-request-id", nil
		}
		requestIDHeader = "x-request-id"

	case AccountTypeOAuth:
		buildReq = func(ctx context.Context) (*http.Request, string, error) {
			if s.tokenProvider == nil {
				return nil, "", errors.New("gemini token provider not configured")
			}
			accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
			if err != nil {
				return nil, "", err
			}

			projectID := strings.TrimSpace(account.GetCredential("project_id"))

			action := "generateContent"
			if useUpstreamStream {
				action = "streamGenerateContent"
			}

			// Two modes for OAuth:
			// 1. With project_id -> Code Assist API (wrapped request)
			// 2. Without project_id -> AI Studio API (direct OAuth, like API key but with Bearer token)
			if projectID != "" {
				// Mode 1: Code Assist API
				baseURL, err := s.validateUpstreamBaseURL(geminicli.GeminiCliBaseURL)
				if err != nil {
					return nil, "", err
				}
				fullURL := fmt.Sprintf("%s/v1internal:%s", strings.TrimRight(baseURL, "/"), action)
				if useUpstreamStream {
					fullURL += "?alt=sse"
				}

				wrapped := map[string]any{
					"model":   mappedModel,
					"project": projectID,
				}
				var inner any
				if err := json.Unmarshal(geminiReq, &inner); err != nil {
					return nil, "", fmt.Errorf("failed to parse gemini request: %w", err)
				}
				wrapped["request"] = inner
				wrappedBytes, _ := json.Marshal(wrapped)

				upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(wrappedBytes))
				if err != nil {
					return nil, "", err
				}
				upstreamReq.Header.Set("Content-Type", "application/json")
				upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
				upstreamReq.Header.Set("User-Agent", geminicli.GeminiCLIUserAgent)
				return upstreamReq, "x-request-id", nil
			} else {
				// Mode 2: AI Studio API with OAuth (like API key mode, but using Bearer token)
				baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
				normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
				if err != nil {
					return nil, "", err
				}

				fullURL, err := buildGeminiAIStudioModelActionURL(normalizedBaseURL, mappedModel, action, useUpstreamStream)
				if err != nil {
					return nil, "", err
				}

				restGeminiReq := normalizeGeminiRequestForAIStudio(geminiReq)
				upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(restGeminiReq))
				if err != nil {
					return nil, "", err
				}
				upstreamReq.Header.Set("Content-Type", "application/json")
				upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
				return upstreamReq, "x-request-id", nil
			}
		}
		requestIDHeader = "x-request-id"

	case AccountTypeServiceAccount:
		buildReq = func(ctx context.Context) (*http.Request, string, error) {
			if s.tokenProvider == nil {
				return nil, "", errors.New("gemini token provider not configured")
			}
			accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
			if err != nil {
				return nil, "", err
			}

			action := "generateContent"
			if req.Stream {
				action = "streamGenerateContent"
			}
			fullURL, err := buildVertexGeminiURL(account.VertexProjectID(), account.VertexLocation(mappedModel), mappedModel, action, req.Stream)
			if err != nil {
				return nil, "", err
			}

			restGeminiReq := normalizeGeminiRequestForAIStudio(geminiReq)
			upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(restGeminiReq))
			if err != nil {
				return nil, "", err
			}
			upstreamReq.Header.Set("Content-Type", "application/json")
			upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
			return upstreamReq, "x-request-id", nil
		}
		requestIDHeader = "x-request-id"

	default:
		return nil, fmt.Errorf("unsupported account type: %s", account.Type)
	}

	var resp *http.Response
	signatureRetryStage := 0
	for attempt := 1; attempt <= geminiMaxRetries; attempt++ {
		upstreamReq, idHeader, err := buildReq(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			// Local build error: don't retry.
			if strings.Contains(err.Error(), "missing project_id") {
				return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			}
			return nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", err.Error())
		}
		requestIDHeader = idHeader

		resp, err = s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
		if err != nil {
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			if attempt < geminiMaxRetries {
				logger.LegacyPrintf("service.gemini_messages_compat", "Gemini account %d: upstream request failed, retry %d/%d: %v", account.ID, attempt, geminiMaxRetries, err)
				sleepGeminiBackoff(attempt)
				continue
			}
			setOpsUpstreamError(c, 0, safeErr, "")
			return nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed after retries: "+safeErr)
		}

		// Special-case: signature/thought_signature validation errors are not transient, but may be fixed by
		// downgrading Claude thinking/tool history to plain text (conservative two-stage retry).
		if resp.StatusCode == http.StatusBadRequest && signatureRetryStage < 2 {
			respBody := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()

			if isGeminiSignatureRelatedError(respBody) {
				upstreamReqID := resp.Header.Get(requestIDHeader)
				if upstreamReqID == "" {
					upstreamReqID = resp.Header.Get("x-goog-request-id")
				}
				upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
				upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
				upstreamDetail := ""
				if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
					maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
					if maxBytes <= 0 {
						maxBytes = 2048
					}
					upstreamDetail = truncateString(string(respBody), maxBytes)
				}
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  upstreamReqID,
					Kind:               "signature_error",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})

				var strippedClaudeBody []byte
				stageName := ""
				switch signatureRetryStage {
				case 0:
					// Stage 1: disable thinking + thinking->text
					strippedClaudeBody = FilterThinkingBlocksForRetry(originalClaudeBody, originalModel)
					stageName = "thinking-only"
					signatureRetryStage = 1
				default:
					// Stage 2: additionally downgrade tool_use/tool_result blocks to text
					strippedClaudeBody = FilterSignatureSensitiveBlocksForRetry(originalClaudeBody, originalModel)
					stageName = "thinking+tools"
					signatureRetryStage = 2
				}
				retryGeminiReq, txErr := convertClaudeMessagesToGeminiGenerateContent(strippedClaudeBody)
				if txErr == nil {
					logger.LegacyPrintf("service.gemini_messages_compat", "Gemini account %d: detected signature-related 400, retrying with downgraded Claude blocks (%s)", account.ID, stageName)
					geminiReq = retryGeminiReq
					// Consume one retry budget attempt and continue with the updated request payload.
					sleepGeminiBackoff(1)
					continue
				}
			}

			// Restore body for downstream error handling.
			resp = &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     resp.Header.Clone(),
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}
			break
		}

		// 错误策略优先：匹配则跳过重试直接处理。
		if matched, rebuilt := s.checkErrorPolicyInLoop(ctx, account, resp, mappedModel); matched {
			resp = rebuilt
			break
		} else {
			resp = rebuilt
		}

		if resp.StatusCode >= 400 && s.shouldRetryGeminiUpstreamError(account, resp.StatusCode) {
			respBody := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			// Don't treat insufficient-scope as transient.
			if resp.StatusCode == 403 && isGeminiInsufficientScope(resp.Header, respBody) {
				resp = &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(respBody)),
				}
				break
			}
			if resp.StatusCode == 429 {
				// Mark as rate-limited early so concurrent requests avoid this account.
				s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			if attempt < geminiMaxRetries {
				upstreamReqID := resp.Header.Get(requestIDHeader)
				if upstreamReqID == "" {
					upstreamReqID = resp.Header.Get("x-goog-request-id")
				}
				upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
				upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
				upstreamDetail := ""
				if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
					maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
					if maxBytes <= 0 {
						maxBytes = 2048
					}
					upstreamDetail = truncateString(string(respBody), maxBytes)
				}
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  upstreamReqID,
					Kind:               "retry",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})

				logger.LegacyPrintf("service.gemini_messages_compat", "Gemini account %d: upstream status %d, retry %d/%d", account.ID, resp.StatusCode, attempt, geminiMaxRetries)
				sleepGeminiBackoff(attempt)
				continue
			}
			// Final attempt: surface the upstream error body (mapped below) instead of a generic retry error.
			resp = &http.Response{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}
			break
		}

		break
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		decision := s.applyGeminiUpstreamErrorPolicy(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
		upstreamReqID := resp.Header.Get(requestIDHeader)
		if upstreamReqID == "" {
			upstreamReqID = resp.Header.Get("x-goog-request-id")
		}
		if decision.ShouldReturnGenericError() {
			genericBody := []byte(`{"error":{"message":"Upstream gateway error"}}`)
			return nil, s.writeGeminiMappedError(c, account, http.StatusInternalServerError, upstreamReqID, genericBody)
		}
		msg400 := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		googleConfigError := resp.StatusCode == http.StatusBadRequest && isGoogleProjectConfigError(msg400)
		defaultFailover := googleConfigError || s.shouldFailoverGeminiUpstreamError(resp.StatusCode)
		if decision.ShouldFailover(account, resp.StatusCode, defaultFailover) {
			upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
			upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
			upstreamDetail := ""
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
				if maxBytes <= 0 {
					maxBytes = 2048
				}
				upstreamDetail = truncateString(string(respBody), maxBytes)
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  upstreamReqID,
				Kind:               "failover",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})
			if googleConfigError {
				log.Printf("[Gemini] status=400 google_config_error failover=true upstream_message=%q account=%d", upstreamMsg, account.ID)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
			}
		}
		return nil, s.writeGeminiMappedError(c, account, resp.StatusCode, upstreamReqID, respBody)
	}

	requestID := resp.Header.Get(requestIDHeader)
	if requestID == "" {
		requestID = resp.Header.Get("x-goog-request-id")
	}
	if requestID != "" {
		c.Header("x-request-id", requestID)
	}

	var usage *ClaudeUsage
	var firstTokenMs *int
	var responseBody []byte
	if req.Stream {
		streamRes, err := s.handleStreamingResponse(c, resp, startTime, originalModel)
		if err != nil {
			return nil, err
		}
		usage = streamRes.usage
		firstTokenMs = streamRes.firstTokenMs
		responseBody = streamRes.responseBody
	} else {
		if useUpstreamStream {
			collected, usageObj, err := collectGeminiSSE(resp.Body, true)
			if err != nil {
				return nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to read upstream stream")
			}
			collectedBytes, _ := json.Marshal(collected)
			observeGeminiImageOutputs(c, collectedBytes)
			claudeResp, usageObj2 := convertGeminiToClaudeMessage(collected, originalModel, collectedBytes, false)
			if body, err := json.Marshal(claudeResp); err == nil {
				responseBody = cloneDataSharingRequestBody(body)
				c.Data(http.StatusOK, "application/json; charset=utf-8", body)
			} else {
				c.JSON(http.StatusOK, claudeResp)
			}
			usage = usageObj2
			if usageObj != nil && (usageObj.InputTokens > 0 || usageObj.OutputTokens > 0) {
				usage = usageObj
			}
		} else {
			var body []byte
			usage, body, err = s.handleNonStreamingResponse(c, resp, originalModel)
			if err != nil {
				return nil, err
			}
			responseBody = body
		}
	}

	// 图片生成计费
	imageInputSize := s.extractImageInputSize(body)
	imageSize := normalizeOpenAIImageSizeTier(imageInputSize)
	imageCount := resolveGeminiImageCount(c, originalModel, mappedModel)

	return &ForwardResult{
		RequestID:      requestID,
		Usage:          *usage,
		Model:          originalModel,
		UpstreamModel:  mappedModel,
		Stream:         req.Stream,
		Duration:       time.Since(startTime),
		FirstTokenMs:   firstTokenMs,
		ResponseBody:   cloneDataSharingRequestBody(responseBody),
		ImageCount:     imageCount,
		ImageSize:      imageSize,
		ImageInputSize: imageInputSize,
	}, nil
}

func isGeminiSignatureRelatedError(respBody []byte) bool {
	msg := strings.ToLower(strings.TrimSpace(extractAntigravityErrorMessage(respBody)))
	if msg == "" {
		msg = strings.ToLower(string(respBody))
	}
	return strings.Contains(msg, "thought_signature") || strings.Contains(msg, "signature")
}

func (s *GeminiMessagesCompatService) ForwardNative(ctx context.Context, c *gin.Context, account *Account, originalModel string, action string, stream bool, body []byte) (*ForwardResult, error) {
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
	if filteredBody, err := filterEmptyPartsFromGeminiRequest(body); err == nil {
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
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	useUpstreamStream := stream
	upstreamAction := action
	if account.Type == AccountTypeOAuth && !stream && action == "generateContent" && strings.TrimSpace(account.GetCredential("project_id")) != "" {
		// Code Assist's non-streaming generateContent may return no content; use streaming upstream and aggregate.
		useUpstreamStream = true
		upstreamAction = "streamGenerateContent"
	}
	forceAIStudio := action == "countTokens"

	var requestIDHeader string
	var buildReq func(ctx context.Context) (*http.Request, string, error)

	switch account.Type {
	case AccountTypeAPIKey:
		buildReq = func(ctx context.Context) (*http.Request, string, error) {
			apiKey := account.GetCredential("api_key")
			if strings.TrimSpace(apiKey) == "" {
				return nil, "", errors.New("gemini api_key not configured")
			}

			baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
			normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, "", err
			}

			fullURL, err := buildGeminiAIStudioModelActionURL(normalizedBaseURL, mappedModel, upstreamAction, useUpstreamStream)
			if err != nil {
				return nil, "", err
			}

			upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
			if err != nil {
				return nil, "", err
			}
			upstreamReq.Header.Set("Content-Type", "application/json")
			upstreamReq.Header.Set("x-goog-api-key", apiKey)
			return upstreamReq, "x-request-id", nil
		}
		requestIDHeader = "x-request-id"

	case AccountTypeOAuth:
		buildReq = func(ctx context.Context) (*http.Request, string, error) {
			if s.tokenProvider == nil {
				return nil, "", errors.New("gemini token provider not configured")
			}
			accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
			if err != nil {
				return nil, "", err
			}

			projectID := strings.TrimSpace(account.GetCredential("project_id"))

			// Two modes for OAuth:
			// 1. With project_id -> Code Assist API (wrapped request)
			// 2. Without project_id -> AI Studio API (direct OAuth, like API key but with Bearer token)
			if projectID != "" && !forceAIStudio {
				// Mode 1: Code Assist API
				baseURL, err := s.validateUpstreamBaseURL(geminicli.GeminiCliBaseURL)
				if err != nil {
					return nil, "", err
				}
				fullURL := fmt.Sprintf("%s/v1internal:%s", strings.TrimRight(baseURL, "/"), upstreamAction)
				if useUpstreamStream {
					fullURL += "?alt=sse"
				}

				wrapped := map[string]any{
					"model":   mappedModel,
					"project": projectID,
				}
				var inner any
				if err := json.Unmarshal(body, &inner); err != nil {
					return nil, "", fmt.Errorf("failed to parse gemini request: %w", err)
				}
				wrapped["request"] = inner
				wrappedBytes, _ := json.Marshal(wrapped)

				upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(wrappedBytes))
				if err != nil {
					return nil, "", err
				}
				upstreamReq.Header.Set("Content-Type", "application/json")
				upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
				upstreamReq.Header.Set("User-Agent", geminicli.GeminiCLIUserAgent)
				return upstreamReq, "x-request-id", nil
			} else {
				// Mode 2: AI Studio API with OAuth (like API key mode, but using Bearer token)
				baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
				normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
				if err != nil {
					return nil, "", err
				}

				fullURL, err := buildGeminiAIStudioModelActionURL(normalizedBaseURL, mappedModel, upstreamAction, useUpstreamStream)
				if err != nil {
					return nil, "", err
				}

				upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
				if err != nil {
					return nil, "", err
				}
				upstreamReq.Header.Set("Content-Type", "application/json")
				upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
				return upstreamReq, "x-request-id", nil
			}
		}
		requestIDHeader = "x-request-id"

	case AccountTypeServiceAccount:
		buildReq = func(ctx context.Context) (*http.Request, string, error) {
			if s.tokenProvider == nil {
				return nil, "", errors.New("gemini token provider not configured")
			}
			accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
			if err != nil {
				return nil, "", err
			}

			fullURL, err := buildVertexGeminiURL(account.VertexProjectID(), account.VertexLocation(mappedModel), mappedModel, upstreamAction, useUpstreamStream)
			if err != nil {
				return nil, "", err
			}

			upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
			if err != nil {
				return nil, "", err
			}
			upstreamReq.Header.Set("Content-Type", "application/json")
			upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
			return upstreamReq, "x-request-id", nil
		}
		requestIDHeader = "x-request-id"

	default:
		return nil, s.writeGoogleError(c, http.StatusBadGateway, "Unsupported account type: "+account.Type)
	}

	var resp *http.Response
	for attempt := 1; attempt <= geminiMaxRetries; attempt++ {
		upstreamReq, idHeader, err := buildReq(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			// Local build error: don't retry.
			if strings.Contains(err.Error(), "missing project_id") {
				return nil, s.writeGoogleError(c, http.StatusBadRequest, err.Error())
			}
			return nil, s.writeGoogleError(c, http.StatusBadGateway, err.Error())
		}
		requestIDHeader = idHeader

		resp, err = s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
		if err != nil {
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			if attempt < geminiMaxRetries {
				logger.LegacyPrintf("service.gemini_messages_compat", "Gemini account %d: upstream request failed, retry %d/%d: %v", account.ID, attempt, geminiMaxRetries, err)
				sleepGeminiBackoff(attempt)
				continue
			}
			if action == "countTokens" {
				estimated := estimateGeminiCountTokens(body)
				c.JSON(http.StatusOK, map[string]any{"totalTokens": estimated})
				return &ForwardResult{
					RequestID:     "",
					Usage:         ClaudeUsage{},
					Model:         originalModel,
					UpstreamModel: mappedModel,
					Stream:        false,
					Duration:      time.Since(startTime),
					FirstTokenMs:  nil,
				}, nil
			}
			setOpsUpstreamError(c, 0, safeErr, "")
			return nil, s.writeGoogleError(c, http.StatusBadGateway, "Upstream request failed after retries: "+safeErr)
		}

		// 错误策略优先：匹配则跳过重试直接处理。
		if matched, rebuilt := s.checkErrorPolicyInLoop(ctx, account, resp, mappedModel); matched {
			resp = rebuilt
			break
		} else {
			resp = rebuilt
		}

		if resp.StatusCode >= 400 && s.shouldRetryGeminiUpstreamError(account, resp.StatusCode) {
			respBody := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			// Don't treat insufficient-scope as transient.
			if resp.StatusCode == 403 && isGeminiInsufficientScope(resp.Header, respBody) {
				resp = &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(respBody)),
				}
				break
			}
			if resp.StatusCode == 429 {
				s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			if attempt < geminiMaxRetries {
				upstreamReqID := resp.Header.Get(requestIDHeader)
				if upstreamReqID == "" {
					upstreamReqID = resp.Header.Get("x-goog-request-id")
				}
				upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
				upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
				upstreamDetail := ""
				if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
					maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
					if maxBytes <= 0 {
						maxBytes = 2048
					}
					upstreamDetail = truncateString(string(respBody), maxBytes)
				}
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  upstreamReqID,
					Kind:               "retry",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})

				logger.LegacyPrintf("service.gemini_messages_compat", "Gemini account %d: upstream status %d, retry %d/%d", account.ID, resp.StatusCode, attempt, geminiMaxRetries)
				sleepGeminiBackoff(attempt)
				continue
			}
			if action == "countTokens" {
				estimated := estimateGeminiCountTokens(body)
				c.JSON(http.StatusOK, map[string]any{"totalTokens": estimated})
				return &ForwardResult{
					RequestID:     "",
					Usage:         ClaudeUsage{},
					Model:         originalModel,
					UpstreamModel: mappedModel,
					Stream:        false,
					Duration:      time.Since(startTime),
					FirstTokenMs:  nil,
				}, nil
			}
			// Final attempt: surface the upstream error body (passed through below) instead of a generic retry error.
			resp = &http.Response{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}
			break
		}

		break
	}
	defer func() { _ = resp.Body.Close() }()

	requestID := resp.Header.Get(requestIDHeader)
	if requestID == "" {
		requestID = resp.Header.Get("x-goog-request-id")
	}
	if requestID != "" {
		c.Header("x-request-id", requestID)
	}

	isOAuth := account.Type == AccountTypeOAuth

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		// Best-effort fallback for OAuth tokens missing AI Studio scopes when calling countTokens.
		// This avoids Gemini SDKs failing hard during preflight token counting.
		// Checked before error policy so it always works regardless of custom error codes.
		if action == "countTokens" && isOAuth && isGeminiInsufficientScope(resp.Header, respBody) {
			estimated := estimateGeminiCountTokens(body)
			c.JSON(http.StatusOK, map[string]any{"totalTokens": estimated})
			return &ForwardResult{
				RequestID:     requestID,
				Usage:         ClaudeUsage{},
				Model:         originalModel,
				UpstreamModel: mappedModel,
				Stream:        false,
				Duration:      time.Since(startTime),
				FirstTokenMs:  nil,
			}, nil
		}

		decision := s.applyGeminiUpstreamErrorPolicy(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
		if decision.ShouldReturnGenericError() {
			MarkResponseCommitted(c)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"code":    http.StatusInternalServerError,
					"message": "Upstream gateway error",
					"status":  "INTERNAL",
				},
			})
			return nil, fmt.Errorf("gemini upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		msg400 := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		googleConfigError := resp.StatusCode == http.StatusBadRequest && isGoogleProjectConfigError(msg400)
		defaultFailover := googleConfigError || s.shouldFailoverGeminiUpstreamError(resp.StatusCode)
		if decision.ShouldFailover(account, resp.StatusCode, defaultFailover) {
			evBody := unwrapIfNeeded(isOAuth, respBody)
			upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(evBody))
			upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
			upstreamDetail := ""
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
				if maxBytes <= 0 {
					maxBytes = 2048
				}
				upstreamDetail = truncateString(string(evBody), maxBytes)
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  requestID,
				Kind:               "failover",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})
			if googleConfigError {
				log.Printf("[Gemini] status=400 google_config_error failover=true upstream_message=%q account=%d", upstreamMsg, account.ID)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           evBody,
				RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
			}
		}

		respBody = unwrapIfNeeded(isOAuth, respBody)
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		upstreamDetail := ""
		if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
			maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
			if maxBytes <= 0 {
				maxBytes = 2048
			}
			upstreamDetail = truncateString(string(respBody), maxBytes)
			logger.LegacyPrintf("service.gemini_messages_compat", "[Gemini] native upstream error %d: %s", resp.StatusCode, truncateForLog(respBody, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes))
		}
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
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
		MarkResponseCommitted(c)
		c.Data(resp.StatusCode, contentType, respBody)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("gemini upstream error: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("gemini upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	var usage *ClaudeUsage
	var firstTokenMs *int
	var responseBody []byte

	if stream {
		streamRes, err := s.handleNativeStreamingResponse(c, resp, startTime, isOAuth)
		if err != nil {
			return nil, err
		}
		usage = streamRes.usage
		firstTokenMs = streamRes.firstTokenMs
		responseBody = streamRes.responseBody
	} else {
		if useUpstreamStream {
			collected, usageObj, err := collectGeminiSSE(resp.Body, isOAuth)
			if err != nil {
				return nil, s.writeGoogleError(c, http.StatusBadGateway, "Failed to read upstream stream")
			}
			b, _ := json.Marshal(collected)
			responseBody = cloneDataSharingRequestBody(b)
			observeGeminiImageOutputs(c, b)
			c.Data(http.StatusOK, "application/json", b)
			usage = usageObj
		} else {
			usageResp, body, err := s.handleNativeNonStreamingResponse(c, resp, isOAuth)
			if err != nil {
				return nil, err
			}
			usage = usageResp
			responseBody = body
		}
	}

	if usage == nil {
		usage = &ClaudeUsage{}
	}

	// 图片生成计费
	imageInputSize := s.extractImageInputSize(body)
	imageSize := normalizeOpenAIImageSizeTier(imageInputSize)
	imageCount := resolveGeminiImageCount(c, originalModel, mappedModel)

	return &ForwardResult{
		RequestID:      requestID,
		Usage:          *usage,
		Model:          originalModel,
		UpstreamModel:  mappedModel,
		Stream:         stream,
		Duration:       time.Since(startTime),
		FirstTokenMs:   firstTokenMs,
		ResponseBody:   cloneDataSharingRequestBody(responseBody),
		ImageCount:     imageCount,
		ImageSize:      imageSize,
		ImageInputSize: imageInputSize,
	}, nil
}

// checkErrorPolicyInLoop 在重试循环内预检查错误策略。
// 返回 true 表示策略已匹配（调用者应 break），resp 已重建可直接使用。
// 返回 false 表示 ErrorPolicyNone，resp 已重建，调用者继续走重试逻辑。
func (s *GeminiMessagesCompatService) checkErrorPolicyInLoop(
	ctx context.Context, account *Account, resp *http.Response, mappedModel string,
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
	policy := s.rateLimitService.CheckErrorPolicy(ctx, account, resp.StatusCode, body, mappedModel)
	if policy == ErrorPolicyTempUnscheduled {
		// CheckErrorPolicy 已写入临时不可调度状态，给最终错误处理留下内部标记，
		// 避免同一个响应再次执行规则并重复写库。
		rebuilt.Header.Set(geminiAppliedTempPolicyHeader, "1")
	}
	// 池模式由 handler 层按账号配置的重试预算处理，不能再叠加 Gemini 固定内部重试。
	return policy != ErrorPolicyNone, rebuilt
}

func (s *GeminiMessagesCompatService) shouldRetryGeminiUpstreamError(account *Account, statusCode int) bool {
	switch statusCode {
	case 429, 500, 502, 503, 504, 529:
		return true
	case 403:
		// GeminiCli OAuth occasionally returns 403 transiently (activation/quota propagation); allow retry.
		if account == nil || account.Type != AccountTypeOAuth {
			return false
		}
		oauthType := strings.ToLower(strings.TrimSpace(account.GetCredential("oauth_type")))
		if oauthType == "" && strings.TrimSpace(account.GetCredential("project_id")) != "" {
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

func sleepGeminiBackoff(attempt int) {
	delay := geminiRetryBaseDelay * time.Duration(1<<uint(attempt-1))
	if delay > geminiRetryMaxDelay {
		delay = geminiRetryMaxDelay
	}

	// +/- 20% jitter
	r := mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	jitter := time.Duration(float64(delay) * 0.2 * (r.Float64()*2 - 1))
	sleepFor := delay + jitter
	if sleepFor < 0 {
		sleepFor = 0
	}
	time.Sleep(sleepFor)
}

var (
	sensitiveQueryParamRegex = regexp.MustCompile(`(?i)([?&](?:key|client_secret|access_token|refresh_token)=)[^&"\s]+`)
	retryInRegex             = regexp.MustCompile(`Please retry in ([0-9.]+)s`)
)

func sanitizeUpstreamErrorMessage(msg string) string {
	if msg == "" {
		return msg
	}
	return sensitiveQueryParamRegex.ReplaceAllString(msg, `$1***`)
}

func (s *GeminiMessagesCompatService) writeGeminiMappedError(c *gin.Context, account *Account, upstreamStatus int, upstreamRequestID string, body []byte) error {
	MarkResponseCommitted(c)
	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, upstreamStatus, upstreamMsg, upstreamDetail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: upstreamStatus,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logger.LegacyPrintf("service.gemini_messages_compat", "[Gemini] upstream error %d: %s", upstreamStatus, truncateForLog(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes))
	}

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		PlatformGemini,
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

	if mapped := mapGeminiErrorBodyToClaudeError(body); mapped != nil {
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
			errMsg = "Invalid request"
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

type claudeErrorMapping struct {
	Type       string
	Message    string
	StatusCode int
}

func mapGeminiErrorBodyToClaudeError(body []byte) *claudeErrorMapping {
	if len(body) == 0 {
		return nil
	}

	var parsed struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	if strings.TrimSpace(parsed.Error.Status) == "" && parsed.Error.Code == 0 && strings.TrimSpace(parsed.Error.Message) == "" {
		return nil
	}

	mapped := &claudeErrorMapping{
		Type:    mapGeminiStatusToClaudeErrorType(parsed.Error.Status),
		Message: "",
	}
	if mapped.Type == "" {
		mapped.Type = "upstream_error"
	}

	switch strings.ToUpper(strings.TrimSpace(parsed.Error.Status)) {
	case "INVALID_ARGUMENT":
		mapped.StatusCode = http.StatusBadRequest
	case "NOT_FOUND":
		mapped.StatusCode = http.StatusNotFound
	case "RESOURCE_EXHAUSTED":
		mapped.StatusCode = http.StatusTooManyRequests
	default:
		// Keep StatusCode unset and let HTTP status mapping decide.
	}

	// Keep messages generic by default; upstream error message can be long or include sensitive fragments.
	return mapped
}

func mapGeminiStatusToClaudeErrorType(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "INVALID_ARGUMENT":
		return "invalid_request_error"
	case "PERMISSION_DENIED":
		return "permission_error"
	case "NOT_FOUND":
		return "not_found_error"
	case "RESOURCE_EXHAUSTED":
		return "rate_limit_error"
	case "UNAUTHENTICATED":
		return "authentication_error"
	case "UNAVAILABLE":
		return "overloaded_error"
	case "INTERNAL":
		return "api_error"
	case "DEADLINE_EXCEEDED":
		return "timeout_error"
	default:
		return ""
	}
}

type geminiStreamResult struct {
	usage        *ClaudeUsage
	firstTokenMs *int
	responseBody []byte
}

func (s *GeminiMessagesCompatService) handleNonStreamingResponse(c *gin.Context, resp *http.Response, originalModel string) (*ClaudeUsage, []byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to read upstream response")
	}

	unwrappedBody, err := unwrapGeminiResponse(body)
	if err != nil {
		return nil, nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}
	observeGeminiImageOutputs(c, unwrappedBody)

	var geminiResp map[string]any
	if err := json.Unmarshal(unwrappedBody, &geminiResp); err != nil {
		return nil, nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}

	claudeResp, usage := convertGeminiToClaudeMessage(geminiResp, originalModel, unwrappedBody, false)
	responseBody, err := json.Marshal(claudeResp)
	if err != nil {
		return nil, nil, err
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)

	return usage, cloneDataSharingRequestBody(responseBody), nil
}

func (s *GeminiMessagesCompatService) handleStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time, originalModel string) (*geminiStreamResult, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	messageID := generateAnthropicMsgID()
	responseAccumulator := &anthropicStreamResponseAccumulator{}
	var finalResponseBody []byte
	writeAnthropicStreamEvent := func(event string, data any) {
		writeSSE(c.Writer, event, data)
		if body := observeAnthropicMapEvent(responseAccumulator, event, data); len(body) > 0 {
			finalResponseBody = body
		}
	}
	messageStart := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         originalModel,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	}
	writeAnthropicStreamEvent("message_start", messageStart)
	flusher.Flush()

	var firstTokenMs *int
	var usage ClaudeUsage
	finishReason := ""
	sawToolUse := false

	nextBlockIndex := 0
	openBlockIndex := -1
	openBlockType := ""
	seenText := ""
	openToolIndex := -1
	openToolID := ""
	openToolName := ""
	seenToolJSON := ""

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("stream read error: %w", err)
		}

		if !strings.HasPrefix(line, "data:") {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}

		unwrappedBytes, err := unwrapGeminiResponse([]byte(payload))
		if err != nil {
			continue
		}
		observeGeminiImageOutputs(c, unwrappedBytes)

		var geminiResp map[string]any
		if err := json.Unmarshal(unwrappedBytes, &geminiResp); err != nil {
			continue
		}

		if fr := extractGeminiFinishReason(geminiResp); fr != "" {
			finishReason = fr
		}

		parts := extractGeminiParts(geminiResp)
		for _, part := range parts {
			if text, ok := part["text"].(string); ok && text != "" {
				// 开始文本块前先关闭已打开的 tool_use 块，和 functionCall 分支
				// 先关闭文本块的处理保持对称；否则工具块和文本块会在
				// Anthropic SSE 中重叠，违反 content block 生命周期约束。
				if openToolIndex >= 0 {
					writeAnthropicStreamEvent("content_block_stop", map[string]any{
						"type":  "content_block_stop",
						"index": openToolIndex,
					})
					openToolIndex = -1
					openToolName = ""
					seenToolJSON = ""
				}

				delta, newSeen := computeGeminiTextDelta(seenText, text)
				seenText = newSeen
				if delta == "" {
					continue
				}

				if openBlockType != "text" {
					if openBlockIndex >= 0 {
						writeAnthropicStreamEvent("content_block_stop", map[string]any{
							"type":  "content_block_stop",
							"index": openBlockIndex,
						})
					}
					openBlockType = "text"
					openBlockIndex = nextBlockIndex
					nextBlockIndex++
					writeAnthropicStreamEvent("content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": openBlockIndex,
						"content_block": map[string]any{
							"type": "text",
							"text": "",
						},
					})
				}

				if firstTokenMs == nil {
					ms := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &ms
				}
				writeAnthropicStreamEvent("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": openBlockIndex,
					"delta": map[string]any{
						"type": "text_delta",
						"text": delta,
					},
				})
				flusher.Flush()
				continue
			}

			if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
				name, _ := fc["name"].(string)
				args := fc["args"]
				if strings.TrimSpace(name) == "" {
					name = "tool"
				}

				// 工具调用前先关闭当前文本块。
				if openBlockIndex >= 0 {
					writeAnthropicStreamEvent("content_block_stop", map[string]any{
						"type":  "content_block_stop",
						"index": openBlockIndex,
					})
					openBlockIndex = -1
					openBlockType = ""
				}

				// 分片收到工具参数时，保持同一个 tool_use 块并持续发出 delta。
				if openToolIndex >= 0 && openToolName != name {
					writeAnthropicStreamEvent("content_block_stop", map[string]any{
						"type":  "content_block_stop",
						"index": openToolIndex,
					})
					openToolIndex = -1
					openToolName = ""
					seenToolJSON = ""
				}

				if openToolIndex < 0 {
					openToolID = "toolu_" + randomHex(8)
					openToolIndex = nextBlockIndex
					openToolName = name
					nextBlockIndex++
					sawToolUse = true

					writeAnthropicStreamEvent("content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": openToolIndex,
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    openToolID,
							"name":  name,
							"input": map[string]any{},
						},
					})
				}

				argsJSONText := "{}"
				switch v := args.(type) {
				case nil:
					// 保持默认的 "{}"。
				case string:
					if strings.TrimSpace(v) != "" {
						argsJSONText = v
					}
				default:
					if b, err := json.Marshal(args); err == nil && len(b) > 0 {
						argsJSONText = string(b)
					}
				}

				delta, newSeen := computeGeminiTextDelta(seenToolJSON, argsJSONText)
				seenToolJSON = newSeen
				if delta != "" {
					writeAnthropicStreamEvent("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": openToolIndex,
						"delta": map[string]any{
							"type":         "input_json_delta",
							"partial_json": delta,
						},
					})
				}
				flusher.Flush()
			}
		}

		if u := extractGeminiUsage(unwrappedBytes); u != nil {
			usage = *u
		}

		// EOF 前最后一行即使没有换行符也要处理。
		if errors.Is(err, io.EOF) {
			break
		}
	}

	if openBlockIndex >= 0 {
		writeAnthropicStreamEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": openBlockIndex,
		})
	}
	if openToolIndex >= 0 {
		writeAnthropicStreamEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": openToolIndex,
		})
	}

	stopReason := mapGeminiFinishReasonToClaudeStopReason(finishReason)
	if sawToolUse {
		stopReason = "tool_use"
	}

	usageObj := map[string]any{
		"output_tokens": usage.OutputTokens,
	}
	if usage.InputTokens > 0 {
		usageObj["input_tokens"] = usage.InputTokens
	}
	writeAnthropicStreamEvent("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": usageObj,
	})
	writeAnthropicStreamEvent("message_stop", map[string]any{
		"type": "message_stop",
	})
	flusher.Flush()

	return &geminiStreamResult{usage: &usage, firstTokenMs: firstTokenMs, responseBody: finalResponseBody}, nil
}

func writeSSE(w io.Writer, event string, data any) {
	if event != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// generateAnthropicMsgID 生成 Anthropic 官方格式的消息 ID：msg_01 + 22 位 Base62。
func generateAnthropicMsgID() string {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const idLen = 22
	randomBytes := make([]byte, idLen)
	if _, err := rand.Read(randomBytes); err != nil {
		// 极端情况下熵源不可用，仍保持客户端依赖的固定格式。
		return fmt.Sprintf("msg_01%022x", uint64(time.Now().UnixNano()))
	}
	b := make([]byte, idLen)
	for i := range b {
		b[i] = charset[int(randomBytes[i])%len(charset)]
	}
	return "msg_01" + string(b)
}

func (s *GeminiMessagesCompatService) writeClaudeError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return fmt.Errorf("%s", message)
}

func (s *GeminiMessagesCompatService) writeGoogleError(c *gin.Context, status int, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  googleapi.HTTPStatusToGoogleStatus(status),
		},
	})
	return fmt.Errorf("%s", message)
}

func unwrapIfNeeded(isOAuth bool, raw []byte) []byte {
	if !isOAuth {
		return raw
	}
	inner, err := unwrapGeminiResponse(raw)
	if err != nil {
		return raw
	}
	return inner
}

func collectGeminiSSE(body io.Reader, isOAuth bool) (map[string]any, *ClaudeUsage, error) {
	reader := bufio.NewReader(body)

	var last map[string]any
	var lastWithParts map[string]any
	var collectedParts []any
	usage := &ClaudeUsage{}

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				switch payload {
				case "", "[DONE]":
					if payload == "[DONE]" {
						return mergeCollectedGeminiParts(pickGeminiCollectResult(last, lastWithParts), collectedParts), usage, nil
					}
				default:
					var parsed map[string]any
					var rawBytes []byte
					if isOAuth {
						innerBytes, err := unwrapGeminiResponse([]byte(payload))
						if err == nil {
							rawBytes = innerBytes
							_ = json.Unmarshal(innerBytes, &parsed)
						}
					} else {
						rawBytes = []byte(payload)
						_ = json.Unmarshal(rawBytes, &parsed)
					}
					if parsed != nil {
						last = parsed
						if u := extractGeminiUsage(rawBytes); u != nil {
							usage = u
						}
						if parts := extractGeminiParts(parsed); len(parts) > 0 {
							lastWithParts = parsed
							collectedParts = appendCollectedGeminiParts(collectedParts, parts)
						}
					}
				}
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
	}

	return mergeCollectedGeminiParts(pickGeminiCollectResult(last, lastWithParts), collectedParts), usage, nil
}

func pickGeminiCollectResult(last map[string]any, lastWithParts map[string]any) map[string]any {
	if lastWithParts != nil {
		return lastWithParts
	}
	if last != nil {
		return last
	}
	return map[string]any{}
}

// appendCollectedGeminiParts 聚合 SSE 增量，同时保持 reasoning 与普通文本的边界。
func appendCollectedGeminiParts(collected []any, parts []map[string]any) []any {
	for _, part := range parts {
		partCopy := make(map[string]any, len(part))
		for key, value := range part {
			partCopy[key] = value
		}

		text, hasText := partCopy["text"].(string)
		if hasText && text != "" && len(collected) > 0 {
			if previous, ok := collected[len(collected)-1].(map[string]any); ok {
				previousText, previousHasText := previous["text"].(string)
				previousThought, _ := previous["thought"].(bool)
				currentThought, _ := partCopy["thought"].(bool)
				if previousHasText && previousThought == currentThought {
					previousCopy := make(map[string]any, len(previous))
					for key, value := range previous {
						previousCopy[key] = value
					}
					previousCopy["text"] = previousText + text
					collected[len(collected)-1] = previousCopy
					continue
				}
			}
		}

		collected = append(collected, partCopy)
	}
	return collected
}

// mergeCollectedGeminiParts 将完整 SSE part 序列写回最终 Gemini 响应。
func mergeCollectedGeminiParts(response map[string]any, collectedParts []any) map[string]any {
	if len(collectedParts) == 0 {
		return response
	}

	// 浅拷贝外层响应，避免直接改动调用方保留的 map。
	result := make(map[string]any)
	for k, v := range response {
		result[k] = v
	}

	// 取出或创建 candidates。
	candidates, ok := result["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		candidates = []any{map[string]any{}}
	} else {
		candidates = append([]any(nil), candidates...)
	}

	// 取出第一个 candidate。
	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		candidate = make(map[string]any)
		candidates[0] = candidate
	} else {
		candidateCopy := make(map[string]any, len(candidate))
		for key, value := range candidate {
			candidateCopy[key] = value
		}
		candidate = candidateCopy
		candidates[0] = candidate
	}

	// 取出或创建 content。
	content, ok := candidate["content"].(map[string]any)
	if !ok {
		content = map[string]any{"role": "model"}
		candidate["content"] = content
	} else {
		contentCopy := make(map[string]any, len(content))
		for key, value := range content {
			contentCopy[key] = value
		}
		content = contentCopy
		candidate["content"] = content
	}

	content["parts"] = append([]any(nil), collectedParts...)
	result["candidates"] = candidates

	return result
}

type geminiNativeStreamResult struct {
	usage        *ClaudeUsage
	firstTokenMs *int
	responseBody []byte
}

func isGeminiInsufficientScope(headers http.Header, body []byte) bool {
	if strings.Contains(strings.ToLower(headers.Get("Www-Authenticate")), "insufficient_scope") {
		return true
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "insufficient authentication scopes") || strings.Contains(lower, "access_token_scope_insufficient")
}

func estimateGeminiCountTokens(reqBody []byte) int {
	total := 0

	// systemInstruction.parts[].text
	gjson.GetBytes(reqBody, "systemInstruction.parts").ForEach(func(_, part gjson.Result) bool {
		if t := strings.TrimSpace(part.Get("text").String()); t != "" {
			total += estimateTokensForText(t)
		}
		return true
	})

	// contents[].parts[].text
	gjson.GetBytes(reqBody, "contents").ForEach(func(_, content gjson.Result) bool {
		content.Get("parts").ForEach(func(_, part gjson.Result) bool {
			if t := strings.TrimSpace(part.Get("text").String()); t != "" {
				total += estimateTokensForText(t)
			}
			return true
		})
		return true
	})

	if total < 0 {
		return 0
	}
	return total
}

func estimateTokensForText(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	runes := []rune(s)
	if len(runes) == 0 {
		return 0
	}
	ascii := 0
	for _, r := range runes {
		if r <= 0x7f {
			ascii++
		}
	}
	asciiRatio := float64(ascii) / float64(len(runes))
	if asciiRatio >= 0.8 {
		// Roughly 4 chars per token for English-like text.
		return (len(runes) + 3) / 4
	}
	// For CJK-heavy text, approximate 1 rune per token.
	return len(runes)
}

type UpstreamHTTPResult struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func (s *GeminiMessagesCompatService) handleNativeNonStreamingResponse(c *gin.Context, resp *http.Response, isOAuth bool) (*ClaudeUsage, []byte, error) {
	if s.cfg != nil && s.cfg.Gateway.GeminiDebugResponseHeaders {
		logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] ========== Response Headers ==========")
		for key, values := range resp.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-ratelimit") {
				logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] %s: %v", key, values)
			}
		}
		logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] ========================================")
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, nil, err
	}

	if isOAuth {
		unwrappedBody, uwErr := unwrapGeminiResponse(respBody)
		if uwErr == nil {
			respBody = unwrappedBody
		}
	}
	observeGeminiImageOutputs(c, respBody)

	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, respBody)

	if u := extractGeminiUsage(respBody); u != nil {
		return u, cloneDataSharingRequestBody(respBody), nil
	}
	return &ClaudeUsage{}, cloneDataSharingRequestBody(respBody), nil
}

func (s *GeminiMessagesCompatService) handleNativeStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time, isOAuth bool) (*geminiNativeStreamResult, error) {
	if s.cfg != nil && s.cfg.Gateway.GeminiDebugResponseHeaders {
		logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] ========== Streaming Response Headers ==========")
		for key, values := range resp.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-ratelimit") {
				logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] %s: %v", key, values)
			}
		}
		logger.LegacyPrintf("service.gemini_messages_compat", "[GeminiAPI] ====================================================")
	}

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}

	c.Status(resp.StatusCode)
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/event-stream; charset=utf-8"
	}
	c.Header("Content-Type", contentType)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	reader := bufio.NewReader(resp.Body)
	usage := &ClaudeUsage{}
	var firstTokenMs *int
	var last map[string]any
	var lastWithParts map[string]any
	var collectedParts []any

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				// keepalive 和结束标记直接透传。
				if payload == "" || payload == "[DONE]" {
					_, _ = io.WriteString(c.Writer, line)
					flusher.Flush()
				} else {
					var rawToWrite string
					rawToWrite = payload

					var rawBytes []byte
					if isOAuth {
						innerBytes, err := unwrapGeminiResponse([]byte(payload))
						if err == nil {
							rawToWrite = string(innerBytes)
							rawBytes = innerBytes
						}
					} else {
						rawBytes = []byte(payload)
					}

					if u := extractGeminiUsage(rawBytes); u != nil {
						usage = u
					}
					var parsed map[string]any
					if err := json.Unmarshal(rawBytes, &parsed); err == nil && parsed != nil {
						last = parsed
						if parts := extractGeminiParts(parsed); len(parts) > 0 {
							lastWithParts = parsed
							collectedParts = appendCollectedGeminiParts(collectedParts, parts)
						}
					}
					observeGeminiImageOutputs(c, rawBytes)

					if firstTokenMs == nil {
						ms := int(time.Since(startTime).Milliseconds())
						firstTokenMs = &ms
					}

					if isOAuth {
						// SSE 格式需要双换行分隔事件。
						_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", rawToWrite)
					} else {
						// AI Studio 响应直接透传。
						_, _ = io.WriteString(c.Writer, line)
					}
					flusher.Flush()
				}
			} else {
				_, _ = io.WriteString(c.Writer, line)
				flusher.Flush()
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	var responseBody []byte
	if last != nil || lastWithParts != nil {
		collected := mergeCollectedGeminiParts(pickGeminiCollectResult(last, lastWithParts), collectedParts)
		responseBody, _ = json.Marshal(collected)
	}
	return &geminiNativeStreamResult{usage: usage, firstTokenMs: firstTokenMs, responseBody: cloneDataSharingRequestBody(responseBody)}, nil
}

// ForwardAIStudioGET forwards a GET request to AI Studio (generativelanguage.googleapis.com) for
// endpoints like /v1beta/models and /v1beta/models/{model}.
//
// This is used to support Gemini SDKs that call models listing endpoints before generation.
func (s *GeminiMessagesCompatService) ForwardAIStudioGET(ctx context.Context, account *Account, path string) (*UpstreamHTTPResult, error) {
	if account == nil {
		return nil, errors.New("account is nil")
	}
	// path 会被直接拼到上游 base URL 后面，因此按路径护栏逐片段校验，
	// 见 upstream_path_guard.go。
	sanitizedPath, ok := sanitizedUpstreamPathSuffix(path)
	if !ok || sanitizedPath == "" {
		return nil, errors.New("invalid path")
	}
	path = sanitizedPath

	baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	fullURL := strings.TrimRight(normalizedBaseURL, "/") + path

	var proxyURL string
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}

	switch account.Type {
	case AccountTypeAPIKey:
		apiKey := strings.TrimSpace(account.GetCredential("api_key"))
		if apiKey == "" {
			return nil, errors.New("gemini api_key not configured")
		}
		req.Header.Set("x-goog-api-key", apiKey)
	case AccountTypeOAuth:
		if s.tokenProvider == nil {
			return nil, errors.New("gemini token provider not configured")
		}
		accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
	default:
		return nil, fmt.Errorf("unsupported account type: %s", account.Type)
	}

	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	wwwAuthenticate := resp.Header.Get("Www-Authenticate")
	filteredHeaders := responseheaders.FilterHeaders(resp.Header, s.responseHeaderFilter)
	if wwwAuthenticate != "" {
		filteredHeaders.Set("Www-Authenticate", wwwAuthenticate)
	}
	return &UpstreamHTTPResult{
		StatusCode: resp.StatusCode,
		Headers:    filteredHeaders,
		Body:       body,
	}, nil
}

// unwrapGeminiResponse 解包 Gemini OAuth 响应中的 response 字段
// 使用 gjson 零拷贝提取，避免完整 Unmarshal+Marshal
func unwrapGeminiResponse(raw []byte) ([]byte, error) {
	result := gjson.GetBytes(raw, "response")
	if result.Exists() && result.Type == gjson.JSON {
		return []byte(result.Raw), nil
	}
	return raw, nil
}

func convertGeminiToClaudeMessage(geminiResp map[string]any, originalModel string, rawData []byte, includeInlineData bool) (map[string]any, *ClaudeUsage) {
	usage := extractGeminiUsage(rawData)
	if usage == nil {
		usage = &ClaudeUsage{}
	}

	contentBlocks := make([]any, 0)
	sawToolUse := false
	if candidates, ok := geminiResp["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if content, ok := cand["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok {
					for _, part := range parts {
						pm, ok := part.(map[string]any)
						if !ok {
							continue
						}
						if text, ok := pm["text"].(string); ok && text != "" {
							if thought, _ := pm["thought"].(bool); thought {
								contentBlocks = append(contentBlocks, map[string]any{
									"type":     "thinking",
									"thinking": text,
								})
							} else {
								contentBlocks = append(contentBlocks, map[string]any{
									"type": "text",
									"text": text,
								})
							}
						}
						if inlineData, ok := pm["inlineData"].(map[string]any); includeInlineData && ok {
							mimeType, _ := inlineData["mimeType"].(string)
							data, _ := inlineData["data"].(string)
							if isGeminiInlineImageMIMEType(mimeType) && isValidBase64(data) {
								contentBlocks = append(contentBlocks, map[string]any{
									"type": "text",
									"text": fmt.Sprintf("![image](data:%s;base64,%s)", mimeType, data),
								})
							}
						}
						if fc, ok := pm["functionCall"].(map[string]any); ok {
							name, _ := fc["name"].(string)
							if strings.TrimSpace(name) == "" {
								name = "tool"
							}
							args := fc["args"]
							sawToolUse = true
							contentBlocks = append(contentBlocks, map[string]any{
								"type":  "tool_use",
								"id":    "toolu_" + randomHex(8),
								"name":  name,
								"input": args,
							})
						}
					}
				}
			}
		}
	}

	stopReason := mapGeminiFinishReasonToClaudeStopReason(extractGeminiFinishReason(geminiResp))
	if sawToolUse {
		stopReason = "tool_use"
	}

	resp := map[string]any{
		"id":            generateAnthropicMsgID(),
		"type":          "message",
		"role":          "assistant",
		"model":         originalModel,
		"content":       contentBlocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
		},
	}

	return resp, usage
}

func isGeminiInlineImageMIMEType(mimeType string) bool {
	switch mimeType {
	case "image/gif", "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

func isValidBase64(data string) bool {
	if data == "" {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(data)
	return err == nil
}

func extractGeminiUsage(data []byte) *ClaudeUsage {
	usage := gjson.GetBytes(data, "usageMetadata")
	if !usage.Exists() {
		return nil
	}
	prompt := int(usage.Get("promptTokenCount").Int())
	cand := int(usage.Get("candidatesTokenCount").Int())
	cached := int(usage.Get("cachedContentTokenCount").Int())
	thoughts := int(usage.Get("thoughtsTokenCount").Int())

	// 从 candidatesTokensDetails 提取 IMAGE 模态 token 数
	imageTokens := 0
	candidateDetails := usage.Get("candidatesTokensDetails")
	if candidateDetails.Exists() {
		candidateDetails.ForEach(func(_, detail gjson.Result) bool {
			if detail.Get("modality").String() == "IMAGE" {
				imageTokens = int(detail.Get("tokenCount").Int())
				return false
			}
			return true
		})
	}

	// 注意：Gemini 的 promptTokenCount 包含 cachedContentTokenCount，
	// 但 Claude 的 input_tokens 不包含 cache_read_input_tokens，需要减去
	return &ClaudeUsage{
		InputTokens:          prompt - cached,
		OutputTokens:         cand + thoughts,
		CacheReadInputTokens: cached,
		ImageOutputTokens:    imageTokens,
	}
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func (s *GeminiMessagesCompatService) handleGeminiUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte) {
	// 遵守自定义错误码策略：未命中则跳过所有限流处理
	if !account.ShouldHandleErrorCode(statusCode) {
		return
	}
	if s.rateLimitService != nil && (statusCode == 401 || statusCode == 403 || statusCode == 529) {
		s.rateLimitService.HandleUpstreamError(ctx, account, statusCode, headers, body)
		return
	}
	if statusCode != 429 {
		return
	}
	// 池模式账号保留在上游账号池中，由请求级重试或切号消化 429；
	// 管理员显式配置的自定义错误策略优先，命中时仍允许写入账号状态。
	if account.IsPoolMode() && !account.IsCustomErrorCodesEnabled() {
		return
	}

	oauthType := account.GeminiOAuthType()
	tierID := account.GeminiTierID()
	projectID := strings.TrimSpace(account.GetCredential("project_id"))
	isCodeAssist := account.IsGeminiCodeAssist()

	resetAt := ParseGeminiRateLimitResetTime(body)
	if resetAt == nil {
		// 根据账号类型使用不同的默认重置时间
		var ra time.Time
		if isCodeAssist || oauthType == "google_one" {
			// Gemini CLI / Google One：按层级回退冷却时间
			cooldown := geminiCooldownForTier(tierID)
			if s.rateLimitService != nil {
				cooldown = s.rateLimitService.GeminiCooldown(ctx, account)
			}
			ra = time.Now().Add(cooldown)
			if isCodeAssist {
				logger.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (Code Assist, tier=%s, project=%s) rate limited, cooldown=%v", account.ID, tierID, projectID, time.Until(ra).Truncate(time.Second))
			} else {
				logger.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (Google One OAuth, tier=%s, project=%s) rate limited, cooldown=%v", account.ID, tierID, projectID, time.Until(ra).Truncate(time.Second))
			}
		} else {
			// API Key / AI Studio OAuth: PST 午夜
			if ts := nextGeminiDailyResetUnix(); ts != nil {
				ra = time.Unix(*ts, 0)
				logger.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d (API Key/AI Studio, type=%s) rate limited, reset at PST midnight (%v)", account.ID, account.Type, ra)
			} else {
				// 兜底：5 分钟
				ra = time.Now().Add(5 * time.Minute)
				logger.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d rate limited, fallback to 5min", account.ID)
			}
		}
		_ = s.accountRepo.SetRateLimited(ctx, account.ID, ra)
		return
	}

	// 使用解析到的重置时间
	resetTime := time.Unix(*resetAt, 0)
	_ = s.accountRepo.SetRateLimited(ctx, account.ID, resetTime)
	logger.LegacyPrintf("service.gemini_messages_compat", "[Gemini 429] Account %d rate limited until %v (oauth_type=%s, tier=%s)",
		account.ID, resetTime, oauthType, tierID)
}

// applyGeminiUpstreamErrorPolicy 统一 Gemini 三种协议入口的显式策略和默认状态处理。
// 池模式绕过时绝不能继续调用 handleGeminiUpstreamError，否则 429 会写入本地限流。
func (s *GeminiMessagesCompatService) applyGeminiUpstreamErrorPolicy(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	body []byte,
	mappedModel string,
) UpstreamErrorDecision {
	decision := upstreamErrorDecisionWithoutPersistence(account, statusCode)
	if s == nil || account == nil {
		return decision
	}
	if headers != nil && headers.Get(geminiAppliedTempPolicyHeader) == "1" {
		headers.Del(geminiAppliedTempPolicyHeader)
		decision.Policy = ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		return decision
	}
	if s.rateLimitService != nil {
		decision.Policy = s.rateLimitService.ApplyExplicitErrorPolicy(ctx, account, statusCode, body, mappedModel)
		decision.StopScheduling = decision.Policy == ErrorPolicyCustomMatched || decision.Policy == ErrorPolicyTempUnscheduled
	}
	switch decision.Policy {
	case ErrorPolicyCustomMatched, ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case ErrorPolicyCustomSkipped, ErrorPolicyPoolBypassed:
		return decision
	}
	s.handleGeminiUpstreamError(ctx, account, statusCode, headers, body)
	return decision
}

// ParseGeminiRateLimitResetTime 解析 Gemini 格式的 429 响应，返回重置时间的 Unix 时间戳
func ParseGeminiRateLimitResetTime(body []byte) *int64 {
	// 第一阶段：gjson 结构化提取
	errMsg := gjson.GetBytes(body, "error.message").String()
	if looksLikeGeminiDailyQuota(errMsg) {
		if ts := nextGeminiDailyResetUnix(); ts != nil {
			return ts
		}
	}

	// 遍历 error.details 查找 quotaResetDelay
	var found *int64
	gjson.GetBytes(body, "error.details").ForEach(func(_, detail gjson.Result) bool {
		v := detail.Get("metadata.quotaResetDelay").String()
		if v == "" {
			return true
		}
		if dur, err := time.ParseDuration(v); err == nil {
			// Use ceil to avoid undercounting fractional seconds (e.g. 10.1s should not become 10s),
			// which can affect scheduling decisions around thresholds (like 10s).
			ts := time.Now().Unix() + int64(math.Ceil(dur.Seconds()))
			found = &ts
			return false
		}
		return true
	})
	if found != nil {
		return found
	}

	// 第二阶段：regex 回退匹配 "Please retry in Xs"
	matches := retryInRegex.FindStringSubmatch(string(body))
	if len(matches) == 2 {
		if dur, err := time.ParseDuration(matches[1] + "s"); err == nil {
			ts := time.Now().Unix() + int64(math.Ceil(dur.Seconds()))
			return &ts
		}
	}

	return nil
}

func looksLikeGeminiDailyQuota(message string) bool {
	m := strings.ToLower(message)
	if strings.Contains(m, "per day") || strings.Contains(m, "requests per day") || strings.Contains(m, "quota") && strings.Contains(m, "per day") {
		return true
	}
	return false
}

func nextGeminiDailyResetUnix() *int64 {
	reset := geminiDailyResetTime(time.Now())
	ts := reset.Unix()
	return &ts
}

func ensureGeminiFunctionCallThoughtSignatures(body []byte) []byte {
	// Fast path: only run when functionCall is present.
	if !bytes.Contains(body, []byte(`"functionCall"`)) {
		return body
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	contentsAny, ok := payload["contents"].([]any)
	if !ok || len(contentsAny) == 0 {
		return body
	}

	modified := false
	for _, c := range contentsAny {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		partsAny, ok := cm["parts"].([]any)
		if !ok || len(partsAny) == 0 {
			continue
		}
		for _, p := range partsAny {
			pm, ok := p.(map[string]any)
			if !ok || pm == nil {
				continue
			}
			if fc, ok := pm["functionCall"].(map[string]any); !ok || fc == nil {
				continue
			}
			ts, _ := pm["thoughtSignature"].(string)
			if strings.TrimSpace(ts) == "" {
				pm["thoughtSignature"] = geminiDummyThoughtSignature
				modified = true
			}
		}
	}

	if !modified {
		return body
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return b
}

func extractGeminiFinishReason(geminiResp map[string]any) string {
	if candidates, ok := geminiResp["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if fr, ok := cand["finishReason"].(string); ok {
				return fr
			}
		}
	}
	return ""
}

func extractGeminiParts(geminiResp map[string]any) []map[string]any {
	if candidates, ok := geminiResp["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if content, ok := cand["content"].(map[string]any); ok {
				if partsAny, ok := content["parts"].([]any); ok && len(partsAny) > 0 {
					out := make([]map[string]any, 0, len(partsAny))
					for _, p := range partsAny {
						pm, ok := p.(map[string]any)
						if !ok {
							continue
						}
						out = append(out, pm)
					}
					return out
				}
			}
		}
	}
	return nil
}

func computeGeminiTextDelta(seen, incoming string) (delta, newSeen string) {
	incoming = strings.TrimSuffix(incoming, "\u0000")
	if incoming == "" {
		return "", seen
	}

	// Cumulative mode: incoming contains full text so far.
	if strings.HasPrefix(incoming, seen) {
		return strings.TrimPrefix(incoming, seen), incoming
	}
	// Duplicate/rewind: ignore.
	if strings.HasPrefix(seen, incoming) {
		return "", seen
	}
	// Delta mode: treat incoming as incremental chunk.
	return incoming, seen + incoming
}

func mapGeminiFinishReasonToClaudeStopReason(finishReason string) string {
	switch strings.ToUpper(strings.TrimSpace(finishReason)) {
	case "MAX_TOKENS":
		return "max_tokens"
	case "STOP":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func convertClaudeMessagesToGeminiGenerateContent(body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	toolUseIDToName := make(map[string]string)

	systemText := extractClaudeSystemText(req["system"])
	contents, err := convertClaudeMessagesToGeminiContents(req["messages"], toolUseIDToName)
	if err != nil {
		return nil, err
	}

	out := make(map[string]any)
	if systemText != "" {
		out["systemInstruction"] = map[string]any{
			"parts": []any{map[string]any{"text": systemText}},
		}
	}
	out["contents"] = contents

	if tools := convertClaudeToolsToGeminiTools(req["tools"]); tools != nil {
		out["tools"] = tools
	}

	generationConfig := convertClaudeGenerationConfig(req)
	if generationConfig != nil {
		out["generationConfig"] = generationConfig
	}

	stripGeminiFunctionIDs(out)
	return json.Marshal(out)
}

func stripGeminiFunctionIDs(req map[string]any) {
	// Defensive cleanup: some upstreams reject unexpected `id` fields in functionCall/functionResponse.
	contents, ok := req["contents"].([]any)
	if !ok {
		return
	}
	for _, c := range contents {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		contentParts, ok := cm["parts"].([]any)
		if !ok {
			continue
		}
		for _, p := range contentParts {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if fc, ok := pm["functionCall"].(map[string]any); ok && fc != nil {
				delete(fc, "id")
			}
			if fr, ok := pm["functionResponse"].(map[string]any); ok && fr != nil {
				delete(fr, "id")
			}
		}
	}
}

func extractClaudeSystemText(system any) string {
	switch v := system.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, p := range v {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := pm["type"].(string); t != "text" {
				continue
			}
			if text, ok := pm["text"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return ""
	}
}

func convertClaudeMessagesToGeminiContents(messages any, toolUseIDToName map[string]string) ([]any, error) {
	arr, ok := messages.([]any)
	if !ok {
		return nil, errors.New("messages must be an array")
	}

	out := make([]any, 0, len(arr))
	for _, m := range arr {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))
		gRole := "user"
		if role == "assistant" {
			gRole = "model"
		}

		parts := make([]any, 0)
		switch content := mm["content"].(type) {
		case string:
			// 字符串形式的 content，保留所有内容（包括空白）
			parts = append(parts, map[string]any{"text": content})
		case []any:
			// 如果只有一个 block，不过滤空白（让上游 API 报错）
			singleBlock := len(content) == 1

			for _, block := range content {
				bm, ok := block.(map[string]any)
				if !ok {
					continue
				}
				bt, _ := bm["type"].(string)
				switch bt {
				case "text":
					if text, ok := bm["text"].(string); ok {
						// 单个 block 时保留所有内容（包括空白）
						// 多个 blocks 时过滤掉空白
						if singleBlock || strings.TrimSpace(text) != "" {
							parts = append(parts, map[string]any{"text": text})
						}
					}
				case "tool_use":
					id, _ := bm["id"].(string)
					name, _ := bm["name"].(string)
					if strings.TrimSpace(id) != "" && strings.TrimSpace(name) != "" {
						toolUseIDToName[id] = name
					}
					signature, _ := bm["signature"].(string)
					signature = strings.TrimSpace(signature)
					if signature == "" {
						signature = geminiDummyThoughtSignature
					}
					parts = append(parts, map[string]any{
						"thoughtSignature": signature,
						"functionCall": map[string]any{
							"name": name,
							"args": bm["input"],
						},
					})
				case "tool_result":
					toolUseID, _ := bm["tool_use_id"].(string)
					name := toolUseIDToName[toolUseID]
					if name == "" {
						name = "tool"
					}
					parts = append(parts, map[string]any{
						"functionResponse": map[string]any{
							"name": name,
							"response": map[string]any{
								"content": extractClaudeContentText(bm["content"]),
							},
						},
					})
				case "image":
					if src, ok := bm["source"].(map[string]any); ok {
						if srcType, _ := src["type"].(string); srcType == "base64" {
							mediaType, _ := src["media_type"].(string)
							data, _ := src["data"].(string)
							if mediaType != "" && data != "" {
								parts = append(parts, map[string]any{
									"inlineData": map[string]any{
										"mimeType": mediaType,
										"data":     data,
									},
								})
							}
						}
					}
				default:
					// best-effort: preserve unknown blocks as text
					if b, err := json.Marshal(bm); err == nil {
						parts = append(parts, map[string]any{"text": string(b)})
					}
				}
			}
		default:
			// ignore
		}

		out = append(out, map[string]any{
			"role":  gRole,
			"parts": parts,
		})
	}
	return out, nil
}

func extractClaudeContentText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var sb strings.Builder
		for _, part := range t {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if pm["type"] == "text" {
				if text, ok := pm["text"].(string); ok {
					_, _ = sb.WriteString(text)
				}
			}
		}
		return sb.String()
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func convertClaudeToolsToGeminiTools(tools any) []any {
	arr, ok := tools.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}

	hasWebSearch := false
	funcDecls := make([]any, 0, len(arr))
	for _, t := range arr {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if isClaudeWebSearchToolMap(tm) {
			hasWebSearch = true
			continue
		}

		var name, desc string
		var params any

		// 检查是否为 custom 类型工具 (MCP)
		toolType, _ := tm["type"].(string)
		if toolType == "custom" {
			// Custom 格式: 从 custom 字段获取 description 和 input_schema
			custom, ok := tm["custom"].(map[string]any)
			if !ok {
				continue
			}
			name, _ = tm["name"].(string)
			desc, _ = custom["description"].(string)
			params = custom["input_schema"]
		} else {
			// 标准格式: 从顶层字段获取
			name, _ = tm["name"].(string)
			desc, _ = tm["description"].(string)
			params = tm["input_schema"]
		}

		if name == "" {
			continue
		}

		// 为 nil params 提供默认值
		if params == nil {
			params = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		// 清理 JSON Schema
		cleanedParams := cleanToolSchema(params)

		funcDecls = append(funcDecls, map[string]any{
			"name":        name,
			"description": desc,
			"parameters":  cleanedParams,
		})
	}

	out := make([]any, 0, 2)
	if len(funcDecls) > 0 {
		out = append(out, map[string]any{
			"functionDeclarations": funcDecls,
		})
	}
	if hasWebSearch {
		out = append(out, map[string]any{
			"googleSearch": map[string]any{},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeGeminiRequestForAIStudio(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) == 0 {
		return body
	}

	modified := false
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		googleSearch, ok := tool["googleSearch"]
		if !ok {
			continue
		}
		if _, exists := tool["google_search"]; exists {
			continue
		}
		tool["google_search"] = googleSearch
		delete(tool, "googleSearch")
		modified = true
	}

	if !modified {
		return body
	}

	normalized, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return normalized
}

func isClaudeWebSearchToolMap(tool map[string]any) bool {
	toolType, _ := tool["type"].(string)
	// 名为 web_search 的普通 function 仍属于客户端工具；Hermes 等 Chat Completions
	// 客户端会用普通 function 表示其内置运行时工具。只有显式声明为服务端搜索类型的
	// 工具才能提升为 Gemini 的 googleSearch 内置工具。
	return strings.HasPrefix(toolType, "web_search") || toolType == "google_search"
}

// cleanToolSchema 清理工具的 JSON Schema，移除 Gemini 不支持的字段
func cleanToolSchema(schema any) any {
	if schema == nil {
		return nil
	}

	switch v := schema.(type) {
	case map[string]any:
		cleaned := make(map[string]any)
		for key, value := range v {
			// 跳过不支持的字段
			if key == "$schema" || key == "$id" || key == "$ref" ||
				key == "$defs" || key == "definitions" ||
				key == "additionalProperties" || key == "patternProperties" || key == "minLength" ||
				key == "maxLength" || key == "minItems" || key == "maxItems" || key == "exclusiveMinimum" {
				continue
			}
			// 递归清理嵌套对象
			cleaned[key] = cleanToolSchema(value)
		}
		// 规范化 type 字段为大写
		if typeVal, ok := cleaned["type"].(string); ok {
			cleaned["type"] = strings.ToUpper(typeVal)
		} else if typeValues, ok := cleaned["type"].([]any); ok {
			for _, typeValue := range typeValues {
				typeName, ok := typeValue.(string)
				if ok && !strings.EqualFold(typeName, "null") {
					cleaned["type"] = strings.ToUpper(typeName)
					break
				}
			}
			if _, ok := cleaned["type"].([]any); ok {
				delete(cleaned, "type")
			}
		}
		if cleaned["type"] == "INTEGER" {
			if minimum, ok := incrementIntegralSchemaBound(v["exclusiveMinimum"]); ok {
				if existing, exists := cleaned["minimum"]; !exists || schemaNumberLess(existing, minimum) {
					cleaned["minimum"] = minimum
				}
			}
		}
		return cleaned
	case []any:
		cleaned := make([]any, len(v))
		for i, item := range v {
			cleaned[i] = cleanToolSchema(item)
		}
		return cleaned
	default:
		return v
	}
}

// incrementIntegralSchemaBound 将整数独占下界安全地转换为包含下界。
func incrementIntegralSchemaBound(value any) (any, bool) {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v+1 <= v {
			return nil, false
		}
		return v + 1, true
	case int:
		if v == math.MaxInt {
			return nil, false
		}
		return v + 1, true
	case int64:
		if v == math.MaxInt64 {
			return nil, false
		}
		return v + 1, true
	case json.Number:
		i, err := v.Int64()
		if err != nil || i == math.MaxInt64 {
			return nil, false
		}
		return json.Number(fmt.Sprintf("%d", i+1)), true
	default:
		return nil, false
	}
}

// schemaNumberLess 比较 cleaner 支持的 JSON 数值表示。
func schemaNumberLess(left, right any) bool {
	leftNumber, leftOK := schemaNumberFloat64(left)
	rightNumber, rightOK := schemaNumberFloat64(right)
	return leftOK && rightOK && leftNumber < rightNumber
}

// schemaNumberFloat64 将 JSON schema 数值转换为可比较的有限浮点数。
func schemaNumberFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	default:
		return 0, false
	}
}

func convertClaudeGenerationConfig(req map[string]any) map[string]any {
	out := make(map[string]any)
	if mt, ok := asInt(req["max_tokens"]); ok && mt > 0 {
		out["maxOutputTokens"] = mt
	}
	if temp, ok := req["temperature"].(float64); ok {
		out["temperature"] = temp
	}
	if topP, ok := req["top_p"].(float64); ok {
		out["topP"] = topP
	}
	if stopSeq, ok := req["stop_sequences"].([]any); ok && len(stopSeq) > 0 {
		out["stopSequences"] = stopSeq
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
