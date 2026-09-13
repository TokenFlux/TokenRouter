package service

import (
	"context"
	"errors"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
)

// 两个未迁平台失败适配仍共用此时长，S09 清零。
const tokenRefreshTempUnschedDuration = acctcore.TokenRefreshTempUnschedDuration

type tokenRefreshRegistration struct {
	platform  string
	refresher TokenRefresher
	executor  OAuthRefreshExecutor
}

type GrokOAuthRefreshMutationRepository = acctcore.GrokRefreshMutationWriter

// TokenRefreshService OAuth token自动刷新服务
// 定期检查并刷新即将过期的token
type TokenRefreshService struct {
	accountPrivacy   *acctcore.PrivacyService
	accountRepo      AccountRepository
	candidatePager   OAuthRefreshCandidatePager
	registrations    []tokenRefreshRegistration
	refreshPolicy    BackgroundRefreshPolicy
	cfg              *config.TokenRefreshConfig
	cacheInvalidator TokenCacheInvalidator
	schedulerCache   SchedulerCache   // 用于同步更新调度器缓存，解决 token 刷新后缓存不一致问题
	tempUnschedCache TempUnschedCache // 用于清除 Redis 中的临时不可调度缓存
	refreshAPI       *OAuthRefreshAPI // 统一刷新 API
	runtimeBlocker   AccountRuntimeBlocker

	// OpenAI privacy: 刷新成功后检查并设置 training opt-out
	privacyClientFactory PrivacyClientFactory
	proxyRepo            ProxyRepository

	coreOnce sync.Once
	core     *acctcore.BackgroundRefreshService

	// 测试专用超时注入点，生产环境使用 TokenRefreshConfig 的秒数配置。
	attemptTimeoutOverride time.Duration
}

// NewTokenRefreshService 创建token刷新服务
func NewTokenRefreshService(
	accountRepo AccountRepository,
	oauthService *OAuthService,
	openaiOAuthService *OpenAIOAuthService,
	geminiOAuthService *GeminiOAuthService,
	antigravityOAuthService *AntigravityOAuthService,
	cacheInvalidator TokenCacheInvalidator,
	schedulerCache SchedulerCache,
	cfg *config.Config,
	tempUnschedCache TempUnschedCache,
	qoderOAuthServices []*QoderOAuthService,
	grokOAuthServices []*GrokOAuthService,
) *TokenRefreshService {
	var qoderOAuthService *QoderOAuthService
	if len(qoderOAuthServices) > 0 {
		qoderOAuthService = qoderOAuthServices[0]
	}
	var grokOAuthService *GrokOAuthService
	if len(grokOAuthServices) > 0 {
		grokOAuthService = grokOAuthServices[0]
	}
	return newTokenRefreshService(accountRepo, oauthService, openaiOAuthService, geminiOAuthService, antigravityOAuthService, qoderOAuthService, grokOAuthService, cacheInvalidator, schedulerCache, cfg, tempUnschedCache, nil, nil)
}

func NewTokenRefreshServiceWithHTTPUpstream(
	accountRepo AccountRepository,
	oauthService *OAuthService,
	openaiOAuthService *OpenAIOAuthService,
	geminiOAuthService *GeminiOAuthService,
	antigravityOAuthService *AntigravityOAuthService,
	cacheInvalidator TokenCacheInvalidator,
	schedulerCache SchedulerCache,
	cfg *config.Config,
	tempUnschedCache TempUnschedCache,
	qoderOAuthService *QoderOAuthService,
	grokOAuthServices []*GrokOAuthService,
	httpUpstream HTTPUpstream,
	tlsFPProfileService *TLSFingerprintProfileService,
) *TokenRefreshService {
	var grokOAuthService *GrokOAuthService
	if len(grokOAuthServices) > 0 {
		grokOAuthService = grokOAuthServices[0]
	}
	return newTokenRefreshService(accountRepo, oauthService, openaiOAuthService, geminiOAuthService, antigravityOAuthService, qoderOAuthService, grokOAuthService, cacheInvalidator, schedulerCache, cfg, tempUnschedCache, httpUpstream, tlsFPProfileService)
}

func newTokenRefreshService(
	accountRepo AccountRepository,
	oauthService *OAuthService,
	openaiOAuthService *OpenAIOAuthService,
	geminiOAuthService *GeminiOAuthService,
	antigravityOAuthService *AntigravityOAuthService,
	qoderOAuthService *QoderOAuthService,
	grokOAuthService *GrokOAuthService,
	cacheInvalidator TokenCacheInvalidator,
	schedulerCache SchedulerCache,
	cfg *config.Config,
	tempUnschedCache TempUnschedCache,
	httpUpstream HTTPUpstream,
	tlsFPProfileService *TLSFingerprintProfileService,
) *TokenRefreshService {
	refreshCfg := &config.TokenRefreshConfig{}
	if cfg != nil {
		refreshCfg = &cfg.TokenRefresh
	}
	s := &TokenRefreshService{
		accountRepo:      accountRepo,
		refreshPolicy:    DefaultBackgroundRefreshPolicy(),
		cfg:              refreshCfg,
		cacheInvalidator: cacheInvalidator,
		schedulerCache:   schedulerCache,
		tempUnschedCache: tempUnschedCache,
	}
	if pager, ok := accountRepo.(OAuthRefreshCandidatePager); ok {
		s.candidatePager = pager
	}

	openAIRefresher := NewOpenAITokenRefresher(openaiOAuthService, accountRepo)

	claudeRefresher := NewClaudeTokenRefresher(oauthService)
	geminiRefresher := NewGeminiTokenRefresher(geminiOAuthService)
	agRefresher := NewAntigravityTokenRefresher(antigravityOAuthService)
	qoderRefresher := NewQoderTokenRefresherWithHTTPUpstream(qoderOAuthService, httpUpstream, tlsFPProfileService)
	grokRefresher := NewGrokTokenRefresher(grokOAuthService)

	// 每个平台只注册一次，同一注册表同时决定执行器和仓储候选资格，避免两者漂移。
	s.registrations = []tokenRefreshRegistration{
		{platform: PlatformAnthropic, refresher: claudeRefresher, executor: claudeRefresher},
		{platform: PlatformOpenAI, refresher: openAIRefresher, executor: openAIRefresher},
		{platform: PlatformGemini, refresher: geminiRefresher, executor: geminiRefresher},
		{platform: PlatformAntigravity, refresher: agRefresher, executor: agRefresher},
		{platform: PlatformQoder, refresher: qoderRefresher, executor: qoderRefresher},
		{platform: PlatformGrok, refresher: grokRefresher, executor: grokRefresher},
	}

	return s
}

// SetPrivacyDeps 注入 OpenAI privacy opt-out 所需依赖
func (s *TokenRefreshService) SetPrivacyDeps(factory PrivacyClientFactory, proxyRepo ProxyRepository) {
	s.privacyClientFactory = factory
	s.proxyRepo = proxyRepo
}

// SetRefreshAPI 注入统一的 OAuth 刷新 API
func (s *TokenRefreshService) SetRefreshAPI(api *OAuthRefreshAPI) {
	s.refreshAPI = api
}

// SetRefreshPolicy 注入后台刷新调用侧策略（用于显式化平台/场景差异行为）。
func (s *TokenRefreshService) SetRefreshPolicy(policy BackgroundRefreshPolicy) {
	s.refreshPolicy = policy
}

func (s *TokenRefreshService) SetAccountRuntimeBlocker(blocker AccountRuntimeBlocker) {
	s.runtimeBlocker = blocker
}

// prepareRefreshFailure 在条件写入之前取得发布资格，防止提交后覆盖显式清理或新凭据。
func (s *TokenRefreshService) prepareRefreshFailure(value *Account) func(time.Time, string) {
	if s == nil || s.runtimeBlocker == nil || value == nil {
		return func(time.Time, string) {}
	}
	observer, ok := s.runtimeBlocker.(acctcore.RefreshFailureObserver)
	if !ok {
		return func(time.Time, string) {}
	}
	notice := acctcore.FailureNotice(AccountRecordView(value))
	publish := observer.PrepareRefreshFailure(value.ID)
	return func(until time.Time, reason string) { notice.Until = until; notice.Reason = reason; publish(notice) }
}

func (s *TokenRefreshService) notifyAccountSchedulingBlockCleared(accountID int64) {
	if s == nil || s.runtimeBlocker == nil || accountID <= 0 {
		return
	}
	s.runtimeBlocker.ClearAccountSchedulingBlock(accountID)
}

// Start 启动后台刷新服务，旧入口委托显式 context 入口。
func (s *TokenRefreshService) Start() { _ = s.StartContext(context.Background()) }
func (s *TokenRefreshService) StartContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.Core().StartContext(ctx)
}

// Stop 保留旧同步入口，app 使用剩余生命周期预算调用 StopContext。
func (s *TokenRefreshService) Stop() { _ = s.StopContext(context.Background()) }
func (s *TokenRefreshService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.Core().StopContext(ctx)
}

func (s *TokenRefreshService) processRefreshContext(ctx context.Context) { s.Core().ScanCycle(ctx) }

// legacyRefreshCandidatePager 仅转换持久化投影与原 SQL 游标元数据。
type legacyRefreshCandidatePager struct{ source OAuthRefreshCandidatePager }

func (p legacyRefreshCandidatePager) ListOAuthRefreshCandidatePage(ctx context.Context, options acctcore.OAuthRefreshPageOptions) (*acctcore.OAuthRefreshCandidatePage, error) {
	page, err := p.source.ListOAuthRefreshCandidatePage(ctx, options)
	if page == nil {
		return nil, err
	}
	return &acctcore.OAuthRefreshCandidatePage{Accounts: AccountRecordsView(page.Accounts), NextAfterID: page.NextAfterID, HasMore: page.HasMore}, err
}

func (s *TokenRefreshService) attemptTimeout() time.Duration {
	lease, configured := s.refreshAPI.LockLease()
	return s.refreshTuning().AttemptTimeout(s.attemptTimeoutOverride, lease, configured)
}

// refreshAttempts 只绑定旧平台及尚未完成拆分的后置端口；app 后续直接提供同一组端口。
func (s *TokenRefreshService) refreshAttempts(original *Account, fallback bool) acctcore.RefreshAttempts {
	// 仅旧直接交换路径沿用原对象赋值时机；统一 API 的回读对象不能回写初始快照。
	legacy := func(v *acctcore.Record) *Account {
		if fallback && original != nil {
			return original
		}
		return AccountFromRecord(v)
	}

	options := acctcore.RefreshAttempts{Tuning: s.refreshTuning(), Policy: s.refreshPolicy, AttemptTimeout: s.attemptTimeout(), Now: time.Now, Info: slog.Info, Warn: slog.Warn, Error: slog.Error, NonRetryable: isNonRetryableRefreshError, SharedProviderError: isSharedProviderRefreshError,
		AmbiguousEntitlement: func(v *acctcore.Record, err error) bool {
			return isAmbiguousGrokEntitlementRefreshError(AccountFromRecord(v), err)
		},
		Persist: func(ctx context.Context, v *acctcore.Record, credentials map[string]any) error {
			old := legacy(v)
			err := persistAccountCredentials(ctx, s.accountRepo, old, credentials)
			if old != nil {
				*v = *AccountRecordView(old)
			}
			return err
		},
		PrepareFailure: func(v *acctcore.Record) func(time.Time, string) { return s.prepareRefreshFailure(AccountFromRecord(v)) },
		ClearRefreshRequest: func(ctx context.Context, v *acctcore.Record, outcome string) {
			old := legacy(v)
			s.clearAntigravityForceTokenRefresh(ctx, old, outcome)
			if old != nil {
				*v = *AccountRecordView(old)
			}
		},
		PostActions: func(ctx context.Context, v *acctcore.Record) {
			old := legacy(v)
			s.postRefreshActions(ctx, old)
			if old != nil {
				*v = *AccountRecordView(old)
			}
		},
		SyncCleanup: func(ctx context.Context, v *acctcore.Record) {
			s.postRefreshStateSyncWithCleanup(ctx, AccountFromRecord(v))
		},
	}
	if s.refreshAPI != nil {
		options.API = s.refreshAPI.inner
	}
	options.FailureWriter, _ = s.accountRepo.(acctcore.RefreshFailureWriter)
	options.GrokMutation, _ = s.accountRepo.(acctcore.GrokRefreshMutationWriter)
	if s.cacheInvalidator != nil {
		options.Invalidate = func(ctx context.Context, v *acctcore.Record) error {
			return s.cacheInvalidator.InvalidateToken(ctx, AccountFromRecord(v))
		}
	}
	return options
}

type legacyBackgroundRefreshOperation struct {
	source  TokenRefresher
	initial *Account
}

func (p legacyBackgroundRefreshOperation) Refresh(ctx context.Context, v *acctcore.Record) (map[string]any, error) {
	old := p.initial
	if old == nil {
		old = AccountFromRecord(v)
	}
	result, err := p.source.Refresh(ctx, old)
	if old != nil {
		*v = *AccountRecordView(old)
	}
	return result, err
}

func (s *TokenRefreshService) postRefreshActions(ctx context.Context, value *Account) {
	record := AccountRecordView(value)
	s.refreshPostActions().Run(ctx, record)
	if value != nil && record != nil {
		value.Extra = record.Extra
	}
}

func (s *TokenRefreshService) postRefreshStateSyncWithCleanup(parent context.Context, value *Account) {
	record := AccountRecordView(value)
	s.refreshPostActions().SyncWithCleanup(parent, record)
	if value != nil && record != nil {
		value.Extra = record.Extra
	}
}

func (s *TokenRefreshService) clearAntigravityForceTokenRefresh(ctx context.Context, value *Account, outcome string) {
	record := AccountRecordView(value)
	s.refreshPostActions().ClearRefreshRequest(ctx, record, outcome)
	if value != nil && record != nil {
		value.Extra = record.Extra
	}
}

var errRefreshSkipped = acctcore.ErrRefreshSkipped

type providerConfigurationRefreshError = acctcore.ProviderConfigurationRefreshError

type providerCycleContainmentRefreshError = acctcore.ProviderCycleContainmentRefreshError

type refreshAttemptTimeoutError = acctcore.RefreshAttemptTimeoutError

func isAmbiguousGrokEntitlementRefreshError(account *Account, err error) bool {
	if account == nil || !account.IsGrokOAuth() || err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	coarseEntitlementLabel := strings.EqualFold(infraerrors.Reason(err), "GROK_OAUTH_ENTITLEMENT_DENIED") ||
		strings.Contains(msg, "grok_oauth_entitlement_denied")
	if !coarseEntitlementLabel {
		return false
	}

	for _, evidence := range []string{
		"subscription required",
		"no active grok subscription",
		"no active subscription",
		"grok subscription required",
		"account is not entitled",
		"not entitled",
		"entitlement required",
		"subscription inactive",
		"subscription expired",
		"upgrade your plan",
	} {
		if strings.Contains(msg, evidence) {
			return false
		}
	}
	if bodyIndex := strings.Index(msg, "body:"); bodyIndex >= 0 {
		body := msg[bodyIndex+len("body:"):]
		for _, evidence := range []string{
			"entitlement_denied",
			"entitlement denied",
			"subscription_required",
			"no_active_subscription",
		} {
			if strings.Contains(body, evidence) {
				return false
			}
		}
	}
	return true
}

func isSharedProviderRefreshError(err error) bool {
	if err == nil {
		return false
	}
	var qoderOpenAPIErr *qoder.OpenAPIError
	if errors.As(err, &qoderOpenAPIErr) && qoderOpenAPIErr.InvalidCredentials() {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"invalid_client",
		"unauthorized_client",
		"invalid_scope",
		"unknown scope",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// isNonRetryableRefreshError 判断是否为不可重试的刷新错误
// 这些错误通常表示凭证已失效或配置确实缺失，需要用户重新授权
// 注意：missing_project_id 错误只在真正缺失（从未获取过）时返回，临时获取失败不会返回此错误
func isNonRetryableRefreshError(err error) bool {
	if err == nil {
		return false
	}
	var qoderOpenAPIErr *qoder.OpenAPIError
	if errors.As(err, &qoderOpenAPIErr) && qoderOpenAPIErr.InvalidCredentials() {
		return true
	}
	msg := strings.ToLower(err.Error())
	nonRetryable := []string{
		"invalid_grant",                       // refresh_token 已失效
		"invalid_refresh_token",               // refresh_token 无效，team 账号工作区被删除时会出现
		"token_expired",                       // OpenAI refresh_token 已过期，需要重新授权
		"app_session_terminated",              // OpenAI app session 被终止，需要重新授权
		"invalid_client",                      // 客户端配置错误
		"unauthorized_client",                 // 客户端未授权
		"access_denied",                       // 访问被拒绝
		"refresh_token_reused",                // OpenAI refresh_token 已被消费，需要重新授权
		"refresh_token_invalidated",           // OpenAI session 结束导致 refresh_token 被废止
		"refresh token has already been used", // 兼容错误体未透出 code 的情况
		"missing_project_id",                  // 缺少 project_id
		"no refresh token available",
		"grok_oauth_entitlement_denied",
		"entitlement_denied",
		"invalid_scope",
		"unknown scope",
		"subscription required",
		"no active grok subscription",
	}
	for _, needle := range nonRetryable {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// refreshTuning 为旧独立构造保留配置投影，不复制预算和退避算法。
func (s *TokenRefreshService) refreshTuning() *acctcore.RefreshTuning {
	if s == nil || s.cfg == nil {
		return nil
	}
	v := s.cfg
	return &acctcore.RefreshTuning{Enabled: v.Enabled, CheckIntervalMinutes: v.CheckIntervalMinutes, RefreshBeforeExpiryHours: v.RefreshBeforeExpiryHours, MaxRetries: v.MaxRetries, RetryBackoffSeconds: v.RetryBackoffSeconds, CandidatePageSize: v.CandidatePageSize, ProviderConcurrency: v.ProviderConcurrency, ProviderQPS: v.ProviderQPS, ProviderFailureThreshold: v.ProviderFailureThreshold, AttemptTimeoutSeconds: v.AttemptTimeoutSeconds, CycleTimeoutSeconds: v.CycleTimeoutSeconds}
}

// refreshPostActions 只投影未迁供应商及调度能力；规则和调用顺序均由账号核心拥有。
func (s *TokenRefreshService) refreshPostActions() *acctcore.RefreshPostActions {
	options := &acctcore.RefreshPostActions{Now: time.Now, Info: slog.Info, Warn: slog.Warn, Debug: slog.Debug, Privacy: s.privacyService(),
		ClearBlock:  s.notifyAccountSchedulingBlockCleared,
		NeedsReauth: func(v *acctcore.Record) bool { return accountGrokNeedsReauth(AccountFromRecord(v)) },
		ClearReauth: func(ctx context.Context, v *acctcore.Record) { clearGrokNeedsReauthExtra(ctx, s.accountRepo, v.ID) },
		ClearError: func(ctx context.Context, v *acctcore.Record) (bool, error) {
			writer, ok := s.accountRepo.(acctcore.UsageRecoveryWriter)
			if !ok {
				return false, errors.New("refresh error conditional writer is not configured")
			}
			return writer.ClearUsageErrorIfUnchanged(ctx, acctcore.UsageRecoveryVersion{CredentialVersion: acctcore.FailureVersion(v).CredentialVersion, ErrorMessage: v.ErrorMessage})
		},
		ClearCooldown: func(ctx context.Context, v *acctcore.Record) (bool, error) {
			writer, ok := s.accountRepo.(acctcore.RefreshCooldownWriter)
			if !ok {
				return false, errors.New("refresh cooldown conditional writer is not configured")
			}
			return writer.ClearRefreshCooldownIfUnchanged(ctx, acctcore.ObserveRefreshCooldown(v))
		},
	}
	options.RequestClearer, _ = s.accountRepo.(acctcore.RefreshRequestClearer)
	if s.cacheInvalidator != nil {
		options.Invalidate = func(ctx context.Context, v *acctcore.Record) error {
			return s.cacheInvalidator.InvalidateToken(ctx, AccountFromRecord(v))
		}
	}
	if s.schedulerCache != nil {
		options.SyncAccount = func(ctx context.Context, v *acctcore.Record) error {
			return s.schedulerCache.SetAccount(ctx, AccountFromRecord(v))
		}
	}
	if s.tempUnschedCache != nil {
		options.DeleteCooldown = s.tempUnschedCache.DeleteTempUnsched
	}
	return options
}

// Core 仅供独立旧构造兼容；生产由 app 在启动前绑定唯一运行实例。
func (s *TokenRefreshService) Core() *acctcore.BackgroundRefreshService {
	s.coreOnce.Do(func() { s.core = acctcore.NewBackgroundRefreshService(s.BackgroundRefreshOptions()) })
	return s.core
}
func (s *TokenRefreshService) BindBackgroundCore(core *acctcore.BackgroundRefreshService) {
	s.coreOnce.Do(func() { s.core = core })
}

// BackgroundRefreshOptions 只投影旧供应商和未迁缓存端口，不创建 worker、锁或缓存。
func (s *TokenRefreshService) BackgroundRefreshOptions() acctcore.BackgroundRefreshOptions {
	options := acctcore.BackgroundRefreshOptions{Tuning: s.refreshTuning(), Attempts: s.refreshAttempts(nil, false), Debug: slog.Debug, Info: slog.Info, Warn: slog.Warn, Error: slog.Error}
	pager := s.candidatePager
	if pager == nil {
		pager, _ = s.accountRepo.(OAuthRefreshCandidatePager)
	}
	if pager != nil {
		options.Pager = legacyRefreshCandidatePager{pager}
	}
	for _, registration := range s.registrations {
		entry := acctcore.RefreshRegistration{Platform: registration.platform}
		if registration.refresher != nil {
			entry.Refresher = legacyBackgroundRefreshOperation{source: registration.refresher}
		}
		if registration.executor != nil {
			entry.Executor = legacyRefreshExecutor{source: registration.executor}
		}
		options.Registrations = append(options.Registrations, entry)
	}
	options.Reconciliation = acctcore.GrokReconciliationOptions{Reader: legacyRefreshReader{s.accountRepo}, Now: time.Now, Skew: grokTokenRefreshSkew, PrepareFailure: options.Attempts.PrepareFailure, Invalidate: options.Attempts.Invalidate}
	options.Reconciliation.ConditionalError, _ = s.accountRepo.(acctcore.GrokOAuthConditionalErrorWriter)
	return options
}
func (p legacyBackgroundRefreshOperation) CanRefresh(v *acctcore.Record) bool {
	return p.source.CanRefresh(AccountFromRecord(v))
}
func (p legacyBackgroundRefreshOperation) NeedsRefresh(v *acctcore.Record, window time.Duration) bool {
	return p.source.NeedsRefresh(AccountFromRecord(v), window)
}
