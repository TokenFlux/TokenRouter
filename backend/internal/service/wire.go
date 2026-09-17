package service

import (
	"context"
	"database/sql"
	"log"
	"time"

	accountredis "github.com/TokenFlux/TokenRouter/internal/account/rediscache"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/google/uuid"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
)

func ProvideGrokOAuthService(proxyRepo ProxyRepository, oauthClient GrokOAuthClient, cfg *config.Config, redisClient *redis.Client) *GrokOAuthService {
	svc := NewGrokOAuthService(proxyRepo, oauthClient, cfg)
	// Wire 层允许直接依赖 Redis，在这里装配跨实例单次消费的会话存储。
	if redisClient != nil {
		svc = svc.WithSessionStore(accountredis.NewGrokSessionStore(redisClient))
	}
	return svc
}

// BuildInfo 保存构建信息。
type BuildInfo struct {
	Version   string
	BuildType string
}

// ProvideUpdateService creates UpdateService with BuildInfo
func ProvideUpdateService(cache UpdateCache, githubClient GitHubReleaseClient, buildInfo BuildInfo) *UpdateService {
	return NewUpdateService(cache, githubClient, buildInfo.Version, buildInfo.BuildType)
}

// ProvideEmailQueueService creates EmailQueueService with default worker count
func ProvideEmailQueueService(emailService *EmailService) *EmailQueueService {
	return NewEmailQueueService(emailService, 3)
}

// ProvideAuthService 注入验证码服务与运行时缓存失效依赖，同时保持测试使用的构造函数兼容。
func ProvideAuthService(
	entClient *dbent.Client,
	userRepo UserRepository,
	redeemRepo RedeemCodeRepository,
	refreshTokenCache RefreshTokenCache,
	cfg *config.Config,
	settingService *SettingService,
	emailService *EmailService,
	turnstileService *TurnstileService,
	tencentCaptchaService *TencentCaptchaService,
	aliyunCaptchaService *AliyunCaptchaService,
	emailQueueService *EmailQueueService,
	promoService *PromoService,
	defaultSubAssigner DefaultSubscriptionAssigner,
	affiliateService *AffiliateService,
	userPlatformQuotaRepo UserPlatformQuotaRepository,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	billingCache BillingCache,
) *AuthService {
	svc := NewAuthService(
		entClient,
		userRepo,
		redeemRepo,
		refreshTokenCache,
		cfg,
		settingService,
		emailService,
		turnstileService,
		emailQueueService,
		promoService,
		defaultSubAssigner,
		affiliateService,
		userPlatformQuotaRepo,
	)
	svc.SetTencentCaptchaService(tencentCaptchaService)
	svc.SetAliyunCaptchaService(aliyunCaptchaService)
	svc.SetRuntimeCaches(authCacheInvalidator, billingCache)
	svc.PinIdentityCore()
	return svc
}

// ProvideBatchImageModelPricingResolver 创建批量图片模型定价解析器。
func ProvideBatchImageModelPricingResolver(resolver *ModelPricingResolver, groupRepo GroupRepository) *BatchImageModelPricingResolver {
	return &BatchImageModelPricingResolver{Resolver: resolver, GroupRepo: groupRepo}
}

// 以下四个 provider 把现有宽接口仓储以创作台窄接口注入，保持服务可测试性。
func ProvideCreativeUserRepository(repo UserRepository) CreativeUserRepository { return repo }

func ProvideCreativeGroupRepository(repo GroupRepository) CreativeGroupRepository { return repo }

func ProvideCreativeAccountRepository(repo AccountRepository) CreativeAccountRepository { return repo }

func ProvideCreativeUserGroupRateRepository(repo UserGroupRateRepository) CreativeUserGroupRateRepository {
	return repo
}

// ProvideCreativeRunOutboxRepositories 为 Wire 的可变参数构造显式 slice。
func ProvideCreativeRunOutboxRepositories(repo CreativeRunOutboxRepository) []CreativeRunOutboxRepository {
	return []CreativeRunOutboxRepository{repo}
}

// ProvideBatchImageCleanupService 创建并启动批量图片清理服务。
func ProvideBatchImageCleanupService(repo BatchImageRepository, accountRepo AccountRepository, cfg *config.Config) *BatchImageCleanupService {
	svc := NewBatchImageCleanupService(repo, accountRepo, cfg)

	return svc
}

// ProvideTokenRefreshService creates and starts TokenRefreshService
func ProvideTokenRefreshService(
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
	privacyClientFactory PrivacyClientFactory,
	proxyRepo ProxyRepository,
	refreshAPI *OAuthRefreshAPI,
	runtimeBlocker AccountRuntimeBlocker,
	httpUpstream HTTPUpstream,
	tlsFPProfileService *TLSFingerprintProfileService,
) *TokenRefreshService {
	svc := NewTokenRefreshServiceWithHTTPUpstream(accountRepo, oauthService, openaiOAuthService, geminiOAuthService, antigravityOAuthService, cacheInvalidator, schedulerCache, cfg, tempUnschedCache, qoderOAuthService, []*GrokOAuthService{grokOAuthService}, httpUpstream, tlsFPProfileService)
	// 注入 OpenAI privacy opt-out 依赖
	svc.SetPrivacyDeps(privacyClientFactory, proxyRepo)
	// 注入统一 OAuth 刷新 API（消除 TokenRefreshService 与 TokenProvider 之间的竞争条件）
	svc.SetRefreshAPI(refreshAPI)
	// 调用侧显式注入后台刷新策略，避免策略漂移
	svc.SetRefreshPolicy(DefaultBackgroundRefreshPolicy())
	svc.SetAccountRuntimeBlocker(runtimeBlocker)

	return svc
}

// ProvideOAuthRefreshAPI creates OAuthRefreshAPI with default lock TTL.
func ProvideOAuthRefreshAPI(accountRepo AccountRepository, tokenCache GeminiTokenCache) *OAuthRefreshAPI {
	return NewOAuthRefreshAPI(accountRepo, tokenCache)
}

// ProvideOpenAIOAuthService 构造 OpenAI OAuth 服务，并注入账号补全与隐私设置依赖。
func ProvideOpenAIOAuthService(
	proxyRepo ProxyRepository,
	oauthClient OpenAIOAuthClient,
	privacyClientFactory PrivacyClientFactory,
	settingService *SettingService,
	tokenRouterReader OpenAIOAuthTokenRouterReader,
	tokenProfileResolver OpenAIOAuthTokenProfileResolver,
) *OpenAIOAuthService {
	svc := NewOpenAIOAuthService(proxyRepo, oauthClient)
	svc.SetPrivacyClientFactory(privacyClientFactory)
	svc.SetTokenTLSRouterDeps(settingService, tokenRouterReader, tokenProfileResolver)
	return svc
}

// ProvideOpenAIGatewayTLSFingerprintRouterServices 为 Wire 的可变参数构造显式 slice。
func ProvideOpenAIGatewayTLSFingerprintRouterServices(tlsFPRouterService *TLSFingerprintRouterService) []*TLSFingerprintRouterService {
	return []*TLSFingerprintRouterService{tlsFPRouterService}
}

// ProvideClaudeTokenProvider creates ClaudeTokenProvider with OAuthRefreshAPI injection
func ProvideClaudeTokenProvider(
	accountRepo AccountRepository,
	tokenCache GeminiTokenCache,
	oauthService *OAuthService,
	refreshAPI *OAuthRefreshAPI,
) *ClaudeTokenProvider {
	p := NewClaudeTokenProvider(accountRepo, tokenCache, oauthService)
	executor := NewClaudeTokenRefresher(oauthService)
	p.SetRefreshAPI(refreshAPI, executor)
	p.SetRefreshPolicy(ClaudeProviderRefreshPolicy())
	return p
}

// ProvideOpenAITokenProvider creates OpenAITokenProvider with OAuthRefreshAPI injection
func ProvideOpenAITokenProvider(
	accountRepo AccountRepository,
	tokenCache GeminiTokenCache,
	openaiOAuthService *OpenAIOAuthService,
	refreshAPI *OAuthRefreshAPI,
) *OpenAITokenProvider {
	p := NewOpenAITokenProvider(accountRepo, tokenCache, openaiOAuthService)
	executor := NewOpenAITokenRefresher(openaiOAuthService, accountRepo)
	p.SetRefreshAPI(refreshAPI, executor)
	p.SetRefreshPolicy(OpenAIProviderRefreshPolicy())
	return p
}

// ProvideOpenAIQuotaService 注入 Agent Identity 连接失效器，并保留 fork 的 TLS 指纹配额请求链路。
func ProvideOpenAIQuotaService(
	adminService AdminService,
	httpUpstream HTTPUpstream,
	tokenProvider *OpenAITokenProvider,
	tlsFPProfileService *TLSFingerprintProfileService,
	tlsFPRouterService *TLSFingerprintRouterService,
	openAIGatewayService *OpenAIGatewayService,
) *OpenAIQuotaService {
	service := NewOpenAIQuotaService(adminService, httpUpstream, tokenProvider, tlsFPProfileService, tlsFPRouterService)
	if openAIGatewayService != nil {
		service.accountRepo = openAIGatewayService.accountRepo
	}
	service.agentIdentityWS = openAIGatewayService
	return service
}

// ProvideAccountUsageService 为后台配额探针注入 Agent Identity task 恢复所需的连接失效器。
func ProvideAccountUsageService(
	accountRepo AccountRepository,
	usageLogRepo UsageLogRepository,
	usageFetcher ClaudeUsageFetcher,
	geminiQuotaService *GeminiQuotaService,
	antigravityQuotaFetcher *AntigravityQuotaFetcher,
	grokQuotaFetcher *GrokQuotaFetcher,
	grokQuotaService *GrokQuotaService,
	openAIQuotaService *OpenAIQuotaService,
	cache *UsageCache,
	identityCache IdentityCache,
	tlsFPProfileService *TLSFingerprintProfileService,
	httpUpstream HTTPUpstream,
	quotaAutoPauseSettings OpenAIQuotaAutoPauseSettingsReader,
	openAIGatewayService *OpenAIGatewayService,
) *AccountUsageService {
	service := NewAccountUsageService(
		accountRepo,
		usageLogRepo,
		usageFetcher,
		geminiQuotaService,
		antigravityQuotaFetcher,
		grokQuotaFetcher,
		grokQuotaService,
		openAIQuotaService,
		cache,
		identityCache,
		tlsFPProfileService,
		httpUpstream,
		quotaAutoPauseSettings,
	)
	service.agentIdentityWS = openAIGatewayService
	return service
}

// ProvideUpstreamUsageService 创建 API Key 上游用量查询服务。
// 该服务只返回管理员查询结果，不参与账号调度或运行态快照写入。
func ProvideUpstreamUsageService(
	accountRepo AccountRepository,
	httpUpstream HTTPUpstream,
	cfg *config.Config,
	tlsFPProfileService *TLSFingerprintProfileService,
) *UpstreamUsageService {
	return NewUpstreamUsageService(accountRepo, httpUpstream, cfg, tlsFPProfileService)
}

// ProvideAccountTestService 为管理端账号测试复用网关的 Agent Identity 连接失效能力。
func ProvideAccountTestService(
	accountRepo AccountRepository,
	geminiTokenProvider *GeminiTokenProvider,
	claudeTokenProvider *ClaudeTokenProvider,
	grokTokenProvider *GrokTokenProvider,
	antigravityGatewayService *AntigravityGatewayService,
	openAIGatewayService *OpenAIGatewayService,
	httpUpstream HTTPUpstream,
	cfg *config.Config,
	tlsFPProfileService *TLSFingerprintProfileService,
	settingService *SettingService,
) *AccountTestService {
	service := NewAccountTestService(
		accountRepo,
		geminiTokenProvider,
		claudeTokenProvider,
		grokTokenProvider,
		antigravityGatewayService,
		openAIGatewayService,
		httpUpstream,
		cfg,
		tlsFPProfileService,
	)
	service.agentIdentityWS = openAIGatewayService
	service.SetSettingService(settingService)
	return service
}

func ProvideGrokQuotaService(
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	tokenProvider *GrokTokenProvider,
	httpUpstream HTTPUpstream,
	cfg *config.Config,
	usageLogRepo UsageLogRepository,
	settingService *SettingService,
) *GrokQuotaService {
	service := NewGrokQuotaService(accountRepo, proxyRepo, tokenProvider, httpUpstream, cfg, usageLogRepo)
	service.SetSettingService(settingService)
	return service
}

// ProvideGeminiTokenProvider creates GeminiTokenProvider with OAuthRefreshAPI injection
func ProvideGeminiTokenProvider(
	accountRepo AccountRepository,
	tokenCache GeminiTokenCache,
	geminiOAuthService *GeminiOAuthService,
	refreshAPI *OAuthRefreshAPI,
) *GeminiTokenProvider {
	p := NewGeminiTokenProvider(accountRepo, tokenCache, geminiOAuthService)
	executor := NewGeminiTokenRefresher(geminiOAuthService)
	p.SetRefreshAPI(refreshAPI, executor)
	p.SetRefreshPolicy(GeminiProviderRefreshPolicy())
	return p
}

// ProvideAntigravityTokenProvider creates AntigravityTokenProvider with OAuthRefreshAPI injection
func ProvideAntigravityTokenProvider(
	accountRepo AccountRepository,
	tokenCache GeminiTokenCache,
	antigravityOAuthService *AntigravityOAuthService,
	refreshAPI *OAuthRefreshAPI,
	tempUnschedCache TempUnschedCache,
) *AntigravityTokenProvider {
	p := NewAntigravityTokenProvider(accountRepo, tokenCache, antigravityOAuthService)
	executor := NewAntigravityTokenRefresher(antigravityOAuthService)
	p.SetRefreshAPI(refreshAPI, executor)
	p.SetRefreshPolicy(AntigravityProviderRefreshPolicy())
	p.SetTempUnschedCache(tempUnschedCache)
	return p
}

// ProvideGrokTokenProvider 创建注入 OAuthRefreshAPI 的 GrokTokenProvider。
func ProvideGrokTokenProvider(
	accountRepo AccountRepository,
	tokenCache GeminiTokenCache,
	grokOAuthService *GrokOAuthService,
	refreshAPI *OAuthRefreshAPI,
	tempUnschedCache TempUnschedCache,
) *GrokTokenProvider {
	p := NewGrokTokenProvider(accountRepo, tokenCache)
	executor := NewGrokTokenRefresher(grokOAuthService)
	p.SetRefreshAPI(refreshAPI, executor)
	p.SetRefreshPolicy(GrokProviderRefreshPolicy())
	p.SetTempUnschedCache(tempUnschedCache)
	return p
}

// ProvideDashboardAggregationService 创建并启动仪表盘聚合服务。
func ProvideDashboardAggregationService(repo DashboardAggregationRepository, timingWheel *TimingWheelService, lockCache LeaderLockCache, db *sql.DB, cfg *config.Config, settings *PreAggregationSettingsService) *DashboardAggregationService {
	svc := NewDashboardAggregationService(repo, timingWheel, cfg)
	svc.SetPreAggregationSettings(settings)
	svc.SetLeaderLock(lockCache, db)

	return svc
}

// ProvideUsageCleanupService 创建并启动使用记录清理任务服务
func ProvideUsageCleanupService(repo UsageCleanupRepository, timingWheel *TimingWheelService, dashboardAgg *DashboardAggregationService, cfg *config.Config) *UsageCleanupService {
	svc := NewUsageCleanupService(repo, timingWheel, dashboardAgg, cfg)

	return svc
}

// ProvideAccountExpiryService creates and starts AccountExpiryService.
func ProvideAccountExpiryService(accountRepo AccountRepository) *AccountExpiryService {
	svc := NewAccountExpiryService(accountRepo, time.Minute)

	return svc
}

// ProvideProxyExpiryService 创建并启动代理过期扫描服务。
func ProvideProxyExpiryService(proxyRepo ProxyRepository) *ProxyExpiryService {
	svc := NewProxyExpiryService(proxyRepo, time.Minute)

	return svc
}

// ProvideSubscriptionExpiryService 保留旧装配入口；生产改由 app 构造。
func ProvideSubscriptionExpiryService(repo UserSubscriptionRepository, settings SettingRepository, notifications *NotificationEmailService, lock LeaderLockCache, db *sql.DB) *SubscriptionExpiryService {
	return billing.NewSubscriptionExpiryService(repo, billing.ExpiryOptions{Interval: time.Minute, Owner: uuid.NewString(), Observe: log.Printf, Settings: settings, Notifier: LegacyExpiryNotifier{Service: notifications}, Lease: func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return tryAcquireSingletonLeaderLock(ctx, lock, db, key, owner, ttl)
	}})
}

// ProvideTimingWheelService creates and starts TimingWheelService
func ProvideTimingWheelService() (*TimingWheelService, error) {
	svc, err := NewTimingWheelService()
	if err != nil {
		return nil, err
	}

	return svc, nil
}

// ProvideDeferredService creates and starts DeferredService
func ProvideDeferredService(accountRepo AccountRepository, timingWheel *TimingWheelService) *DeferredService {
	svc := NewDeferredService(accountRepo, timingWheel, 10*time.Second)

	return svc
}

// ProvideOpsMetricsCollector creates and starts OpsMetricsCollector.
func ProvideOpsMetricsCollector(
	opsRepo OpsRepository,
	settingRepo SettingRepository,
	accountRepo AccountRepository,
	concurrencyService *ConcurrencyService,
	db *sql.DB,
	redisClient *redis.Client,
	cfg *config.Config,
) *OpsMetricsCollector {
	collector := NewOpsMetricsCollector(opsRepo, settingRepo, accountRepo, concurrencyService, db, redisClient, cfg)

	return collector
}

// ProvideOpsAggregationService creates and starts OpsAggregationService (hourly/daily pre-aggregation).
func ProvideOpsAggregationService(
	opsRepo OpsRepository,
	settingRepo SettingRepository,
	db *sql.DB,
	redisClient *redis.Client,
	cfg *config.Config,
	preAggregationSettings *PreAggregationSettingsService,
) *OpsAggregationService {
	svc := NewOpsAggregationService(opsRepo, settingRepo, db, redisClient, cfg, preAggregationSettings)

	return svc
}

// ProvideOpsAlertEvaluatorService creates and starts OpsAlertEvaluatorService.
func ProvideOpsAlertEvaluatorService(
	opsService *OpsService,
	opsRepo OpsRepository,
	emailService *EmailService,
	redisClient *redis.Client,
	cfg *config.Config,
	proxyRepo ProxyRepository,
) *OpsAlertEvaluatorService {
	svc := NewOpsAlertEvaluatorService(opsService, opsRepo, emailService, redisClient, cfg, proxyRepo)

	return svc
}

// ProvideOpsCleanupService creates and starts OpsCleanupService (cron scheduled).
func ProvideOpsCleanupService(
	opsRepo OpsRepository,
	settingRepo SettingRepository,
	opsService *OpsService,
	db *sql.DB,
	redisClient *redis.Client,
	cfg *config.Config,
) *OpsCleanupService {
	svc := NewOpsCleanupService(opsRepo, db, redisClient, cfg, settingRepo)

	if opsService != nil {
		opsService.SetCleanupReloader(svc)
	}
	return svc
}

func ProvideOpsSystemLogSink(opsRepo OpsRepository) *OpsSystemLogSink {
	sink := NewOpsSystemLogSink(opsRepo)

	return sink
}

// ProvideAuditLogService 创建操作审计日志服务并启动异步写入与保留期清理协程。
// 停止逻辑挂在 cmd/server 的 provideCleanup。
func ProvideAuditLogService(repo AuditLogRepository, settingService *SettingService) *AuditLogService {
	svc := NewAuditLogService(repo, settingService)

	return svc
}

func buildIdempotencyConfig(cfg *config.Config) IdempotencyConfig {
	idempotencyCfg := DefaultIdempotencyConfig()
	if cfg != nil {
		if cfg.Idempotency.DefaultTTLSeconds > 0 {
			idempotencyCfg.DefaultTTL = time.Duration(cfg.Idempotency.DefaultTTLSeconds) * time.Second
		}
		if cfg.Idempotency.SystemOperationTTLSeconds > 0 {
			idempotencyCfg.SystemOperationTTL = time.Duration(cfg.Idempotency.SystemOperationTTLSeconds) * time.Second
		}
		if cfg.Idempotency.ProcessingTimeoutSeconds > 0 {
			idempotencyCfg.ProcessingTimeout = time.Duration(cfg.Idempotency.ProcessingTimeoutSeconds) * time.Second
		}
		if cfg.Idempotency.FailedRetryBackoffSeconds > 0 {
			idempotencyCfg.FailedRetryBackoff = time.Duration(cfg.Idempotency.FailedRetryBackoffSeconds) * time.Second
		}
		if cfg.Idempotency.MaxStoredResponseLen > 0 {
			idempotencyCfg.MaxStoredResponseLen = cfg.Idempotency.MaxStoredResponseLen
		}
		idempotencyCfg.ObserveOnly = cfg.Idempotency.ObserveOnly
	}
	return idempotencyCfg
}

func ProvideIdempotencyCoordinator(repo IdempotencyRepository, cfg *config.Config) *IdempotencyCoordinator {
	coordinator := NewIdempotencyCoordinator(repo, buildIdempotencyConfig(cfg))
	SetDefaultIdempotencyCoordinator(coordinator)
	return coordinator
}

func ProvideSystemOperationLockService(repo IdempotencyRepository, cfg *config.Config) *SystemOperationLockService {
	return NewSystemOperationLockService(repo, buildIdempotencyConfig(cfg))
}

func ProvideIdempotencyCleanupService(repo IdempotencyRepository, cfg *config.Config) *IdempotencyCleanupService {
	svc := NewIdempotencyCleanupService(repo, cfg)

	return svc
}

// ProvideOpsScheduledReportService creates and starts OpsScheduledReportService.
func ProvideOpsScheduledReportService(
	opsService *OpsService,
	userService *UserService,
	emailService *EmailService,
	redisClient *redis.Client,
	cfg *config.Config,
) *OpsScheduledReportService {
	svc := NewOpsScheduledReportService(opsService, userService, emailService, redisClient, cfg)

	return svc
}

// ProvideAPIKeyAuthCacheInvalidator 提供 API Key 认证缓存失效能力
func ProvideAPIKeyAuthCacheInvalidator(apiKeyService *APIKeyService) APIKeyAuthCacheInvalidator {
	// Start Pub/Sub subscriber for L1 cache invalidation across instances
	return apiKeyService
}

// ProvideBackupService creates and starts BackupService
func ProvideBackupService(
	settingRepo SettingRepository,
	cfg *config.Config,
	encryptor SecretEncryptor,
	storeFactory BackupObjectStoreFactory,
	dumper DBDumper,
	db *sql.DB,
) *BackupService {
	svc := NewBackupService(settingRepo, cfg, encryptor, storeFactory, dumper)
	svc.SetMaintenanceDB(db)

	return svc
}

// ProvideOpsService 构造 OpsService，并接入由 SettingService 支撑的配额自动暂停缓存写入回调。
// 这与 SetCleanupReloader 模式一致：OpsService 不持有 *SettingService 引用，
// 而是由 wire 注入一个小回调，让 ops_advanced_settings 写入后能立刻传播到调度热路径缓存。
func ProvideOpsService(
	opsRepo OpsRepository,
	settingRepo SettingRepository,
	cfg *config.Config,
	accountRepo AccountRepository,
	userRepo UserRepository,
	concurrencyService *ConcurrencyService,
	gatewayService *GatewayService,
	openAIGatewayService *OpenAIGatewayService,
	geminiCompatService *GeminiMessagesCompatService,
	antigravityGatewayService *AntigravityGatewayService,
	systemLogSink *OpsSystemLogSink,
	settingService *SettingService,
	authCacheInvalidationWorker *AuthCacheInvalidationWorker,
	apiKeyService *APIKeyService,
	preAggregationSettings *PreAggregationSettingsService,
) *OpsService {
	svc := NewOpsService(
		opsRepo,
		settingRepo,
		cfg,
		accountRepo,
		userRepo,
		concurrencyService,
		gatewayService,
		openAIGatewayService,
		geminiCompatService,
		antigravityGatewayService,
		systemLogSink,
	)
	svc.SetPreAggregationSettings(preAggregationSettings)
	if settingService != nil {
		svc.SetOpenAIQuotaAutoPauseSettingsSink(settingService.SetOpenAIQuotaAutoPauseSettings)
		// 可选预热：让进程启动后的首个调度请求看到已填充缓存，而不是零默认值。
		// 该操作尽力而为，并由同步超时边界保护。
		settingService.WarmOpenAIQuotaAutoPauseSettings(context.Background())
	}
	var health ops.AuthHealthReader
	if authCacheInvalidationWorker != nil {
		health = authCacheInvalidationWorker
	}
	var keyHealth ops.KeyHealthReader
	if apiKeyService != nil {
		keyHealth = apiKeyService
	}
	svc.SetAuthObservers(health, keyHealth)

	return svc
}

// ProvideOpsIngressRejectAggregator 启动有界安全聚合运行时，并将其挂载到作为中间件记录器的 OpsService。
func ProvideOpsIngressRejectAggregator(opsRepo OpsRepository, opsService *OpsService) *OpsIngressRejectAggregator {
	repo, ok := opsRepo.(OpsIngressRejectRepository)
	if !ok {
		return nil
	}
	aggregator := NewOpsIngressRejectAggregator(repo)

	opsService.SetIngressRejectAggregator(aggregator)
	return aggregator
}

// ProvideSettingService wires SettingService with group reader and proxy repo.
func ProvideSettingService(settingRepo SettingRepository, paymentConfigService *PaymentConfigService, proxyRepo ProxyRepository, cfg *config.Config) *SettingService {
	svc := NewSettingService(settingRepo, cfg)
	svc.SetDefaultSubscriptionPlanReader(paymentConfigService)
	svc.SetProxyRepository(proxyRepo)
	antigravity.SetUserAgentVersionResolver(svc.GetAntigravityUserAgentVersion)
	// 无账号句柄的 OAuth/PAT/用量探针路径也必须复用后台配置的 Codex 身份。
	SetCodexCanonicalUserAgentResolver(func() string {
		return svc.GetOpenAICodexUserAgent(context.Background())
	})
	return svc
}

// ProvideBillingCacheService wires BillingCacheService with its RPM dependencies.
func ProvideBillingCacheService(
	cache BillingCache,
	userRepo UserRepository,
	subRepo UserSubscriptionRepository,
	apiKeyRepo APIKeyRepository,
	rpmCache UserRPMCache,
	rateRepo UserGroupRateRepository,
	cfg *config.Config,
	userPlatformQuotaRepo UserPlatformQuotaRepository,
) *BillingCacheService {
	return NewBillingCacheService(cache, userRepo, subRepo, apiKeyRepo, rpmCache, rateRepo, cfg, userPlatformQuotaRepo)
}

// ProvideAPIKeyService wires APIKeyService and connects rate-limit cache invalidation.
func ProvideAPIKeyService(
	apiKeyRepo APIKeyRepository,
	userRepo UserRepository,
	groupRepo GroupRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache APIKeyCache,
	cfg *config.Config,
	billingCacheService *BillingCacheService,
	concurrencyService *ConcurrencyService,
	teamRepo TeamRepository,
) *APIKeyService {
	svc := NewAPIKeyService(apiKeyRepo, userRepo, groupRepo, userSubRepo, userGroupRateRepo, cache, cfg)
	svc.SetRateLimitCacheInvalidator(billingCacheService)
	svc.SetConcurrencyService(concurrencyService)
	svc.SetTeamRepository(teamRepo)
	return svc
}

// ProviderSet is the Wire provider set for all services
var ProviderSet = wire.NewSet(
	// 核心服务
	NewProxyService,
	NewQoderTokenProvider,
	NewQoderGatewayService,
	ProvideOpenAIGatewayTLSFingerprintRouterServices,
	NewOpenAIGatewayService,
	NewCodexInviteResetService,
	ProvideOpenAIQuotaService,
	ProvideBatchImageModelPricingResolver,
	NewBatchImagePublicService,
	NewBatchImageDownloadService,
	ProvideBatchImageCleanupService,
	ProvideBatchImageWorkerRuntime,
	NewCreativePublicService,
	wire.Bind(new(CreativeSettingReader), new(*SettingService)),
	ProvideCreativeUserRepository,
	ProvideCreativeGroupRepository,
	ProvideCreativeAccountRepository,
	ProvideCreativeUserGroupRateRepository,
	ProvideCreativeRunOutboxRepositories,
	NewCreativeExecutor,
	wire.Bind(new(CreativeRunExecutor), new(*CreativeExecutor)),
	ProvideCreativeWorkerRuntime,
	wire.Bind(new(AccountRuntimeBlocker), new(*OpenAIGatewayService)),
	NewOAuthService,
	ProvideOpenAIOAuthService,
	wire.Bind(new(OpenAIQuotaAutoPauseSettingsReader), new(*SettingService)),
	ProvideGrokOAuthService,
	wire.Bind(new(GrokOAuthTokenService), new(*GrokOAuthService)),
	NewGeminiOAuthService,
	NewCompositeTokenCacheInvalidator,
	wire.Bind(new(TokenCacheInvalidator), new(*CompositeTokenCacheInvalidator)),
	NewAntigravityOAuthService,
	NewQoderOAuthService,
	ProvideGeminiTokenProvider,
	NewGeminiMessagesCompatService,
	ProvideAntigravityTokenProvider,
	ProvideGrokTokenProvider,
	ProvideOpenAITokenProvider,
	ProvideGrokQuotaService,
	ProvideClaudeTokenProvider,
	NewAntigravityGatewayService,
	ProvideAccountUsageService,
	ProvideAccountTestService,
	ProvideSettingService,
	NewDataManagementService,
	ProvideBackupService,

	NewRequestFingerprintService,

	ProvideTokenRefreshService,
	wire.Bind(new(GrokOAuthReconciler), new(*TokenRefreshService)),
	ProvideProxyExpiryService,
	ProvideTimingWheelService,
	NewAntigravityQuotaFetcher,
	NewGrokQuotaFetcher,
	NewUsageCache,
	NewDigestSessionStore,
	ProvideIdempotencyCoordinator,
	ProvideSystemOperationLockService,
	ProvideIdempotencyCleanupService,
)

// ProvideContentModerationService 创建内容审计服务并注入代理仓储。
func ProvideContentModerationService(
	settingRepo SettingRepository,
	repo ContentModerationRepository,
	hashCache ContentModerationHashCache,
	groupRepo GroupRepository,
	userRepo UserRepository,
	proxyRepo ProxyRepository,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	emailService *EmailService,
) *ContentModerationService {
	svc := NewContentModerationService(settingRepo, repo, hashCache, groupRepo, userRepo, authCacheInvalidator, emailService)
	svc.SetProxyRepository(proxyRepo)
	return svc
}

// ProvideUserPlatformQuotaUsageFlusher 创建并启动 UserPlatformQuotaUsageFlusher。
func ProvideUserPlatformQuotaUsageFlusher(cfg *config.Config, cache BillingCache, quotaRepo UserPlatformQuotaRepository, tw *TimingWheelService) *UserPlatformQuotaUsageFlusher {
	svc := NewUserPlatformQuotaUsageFlusher(cfg, cache, quotaRepo, tw)

	return svc
}

// ProvidePaymentConfigService wraps NewPaymentConfigService to accept the named
// payment.EncryptionKey type instead of raw []byte, avoiding Wire ambiguity.
func ProvidePaymentConfigService(entClient *dbent.Client, settingRepo SettingRepository, key payment.EncryptionKey, plans *billing.Plans) *PaymentConfigService {
	return NewPaymentConfigServiceWithPlans(entClient, settingRepo, []byte(key), plans)
}

// ProvideBalanceNotifyService creates BalanceNotifyService
func ProvideBalanceNotifyService(emailService *EmailService, settingRepo SettingRepository, accountRepo AccountRepository, notificationEmailService *NotificationEmailService) *BalanceNotifyService {
	svc := NewBalanceNotifyService(emailService, settingRepo, accountRepo)
	svc.SetNotificationEmailService(notificationEmailService)
	return svc
}

// ProvidePaymentService 创建支付服务并注入通知邮件服务。
func ProvidePaymentService(entClient *dbent.Client, registry *payment.Registry, loadBalancer payment.LoadBalancer, redeemService *RedeemService, subscriptionSvc *SubscriptionService, configService *PaymentConfigService, userRepo UserRepository, groupRepo GroupRepository, affiliateService *AffiliateService, notificationEmailService *NotificationEmailService) *PaymentService {
	svc := NewPaymentService(entClient, registry, loadBalancer, redeemService, subscriptionSvc, configService, userRepo, groupRepo, affiliateService)
	svc.SetNotificationEmailService(notificationEmailService)
	return svc
}

// ProvidePaymentOrderExpiryService 创建并启动支付订单过期服务。
func ProvidePaymentOrderExpiryService(paymentSvc *PaymentService, lockCache LeaderLockCache, db *sql.DB) *PaymentOrderExpiryService {
	svc := NewPaymentOrderExpiryService(paymentSvc, 60*time.Second)
	svc.SetLeaderLock(lockCache, db)

	return svc
}
