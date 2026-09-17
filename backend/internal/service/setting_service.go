package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/moderation"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/creative"

	"github.com/TokenFlux/TokenRouter/internal/gateway"

	"github.com/TokenFlux/TokenRouter/internal/audit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/promotion"

	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"

	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"

	"github.com/TokenFlux/TokenRouter/internal/search"

	"github.com/TokenFlux/TokenRouter/internal/site"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/config"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const (
	GrokDefaultBaseURLModeAPI     = "api"
	GrokDefaultBaseURLModeUSEast1 = "us-east-1"
	GrokDefaultBaseURLModeUSWest2 = "us-west-2"
	GrokDefaultBaseURLModeEUWest1 = "eu-west-1"
	GrokDefaultBaseURLModeCLI     = "cli"
)

type UsageRankingSortBy = usage.UsageRankingSortBy

const UsageRankingSortByTotalTokens = usage.UsageRankingSortByTotalTokens
const UsageRankingSortByRequests = usage.UsageRankingSortByRequests
const UsageRankingSortByActualCost = usage.UsageRankingSortByActualCost

type UsageRankingSettings = usage.UsageRankingSettings

func IsValidUsageRankingSortBy(value string) bool { return usage.IsValidUsageRankingSortBy(value) }

func NormalizeUsageRankingSortBy(value string) UsageRankingSortBy {
	return usage.NormalizeUsageRankingSortBy(value)
}

func NormalizeUsageRankingSettings(settings UsageRankingSettings) UsageRankingSettings {
	return usage.NormalizeUsageRankingSettings(settings)
}

func normalizeGrokDefaultBaseURLMode(mode string) string {
	return gateway.NormalizeGrokDefaultBaseURLMode(mode)
}

func GrokBaseURLForMode(mode string) string {
	switch normalizeGrokDefaultBaseURLMode(mode) {
	case GrokDefaultBaseURLModeAPI:
		return xai.DefaultBaseURL
	case GrokDefaultBaseURLModeUSEast1:
		return xai.DefaultUSEast1BaseURL
	case GrokDefaultBaseURLModeUSWest2:
		return xai.DefaultUSWest2BaseURL
	case GrokDefaultBaseURLModeEUWest1:
		return xai.DefaultEUWest1BaseURL
	default:
		return xai.DefaultCLIBaseURL
	}
}

func (s *SettingService) GetGrokDefaultBaseURLMode(ctx context.Context) string {
	return s.GatewaySettings().GetGrokDefaultBaseURLMode(ctx)
}

func (s *SettingService) GetGrokDefaultBaseURL(ctx context.Context) string {
	return GrokBaseURLForMode(s.GetGrokDefaultBaseURLMode(ctx))
}

func (s *SettingService) ResolveGrokBaseURL(ctx context.Context, account *Account) string {
	def := xai.DefaultCLIBaseURL
	if s != nil {
		def = s.GetGrokDefaultBaseURL(ctx)
	}
	if account == nil {
		return def
	}
	return account.GetGrokBaseURLOr(def)
}

var (
	ErrRegistrationDisabled    = identity.ErrRegDisabled
	ErrSettingNotFound         = settings.ErrSettingNotFound
	ErrDefaultSubPlanInvalid   = identity.ErrDefaultSubPlanInvalid
	ErrDefaultSubPlanDuplicate = identity.ErrDefaultSubPlanDuplicate
)

// WebSearchManagerBuilder creates a websearch.Manager from config (injected by infra layer).
// proxyURLs maps proxy ID to resolved URL for provider-level proxy support.

// SettingService 系统设置服务
type SettingService struct {
	moderationSettings           *moderation.RuntimeSettings
	moderationSettingsOnce       sync.Once
	gatewayAdminRules            *gateway.AdminSettingsRules
	routingSettings              *routing.RuntimeSettings
	routingSettingsOnce          sync.Once
	schedulerAdminDefaults       *scheduler.AdminDefaults
	forwardedSettings            *runtimeconfig.ForwardedSettings
	forwardedSettingsOnce        sync.Once
	creativeSettings             *creative.RuntimeSettings
	creativeSettingsOnce         sync.Once
	grantSettings                *identity.GrantSettings
	grantSettingsOnce            sync.Once
	oauthSettingsOnce            sync.Once
	gatewaySettingsOnce          sync.Once
	auditSettingsOnce            sync.Once
	usageSettingsOnce            sync.Once
	promotionSettingsOnce        sync.Once
	identitySettingsOnce         sync.Once
	accountSettingsOnce          sync.Once
	oauthSettings                *identity.OAuthSettings
	gatewaySettings              *gateway.RuntimeSettings
	identitySettings             *identity.RuntimeSettings
	usageSettings                *usage.RuntimeSettings
	auditSettings                *audit.RetentionSettings
	accountSettings              *accountcore.RuntimeSettings
	promotionSettings            *promotion.RuntimeSettings
	backendMode                  *admission.BackendMode
	userPromptPolicy             *promptpolicy.Service
	searchConfig                 *search.ConfigService
	publicSite                   *site.PublicService
	runtimeSettingsMu            sync.Mutex
	settingRepo                  SettingRepository
	defaultSubPlanReader         DefaultSubscriptionPlanReader
	proxyRepo                    ProxyRepository // for resolving websearch provider proxy URLs
	cfg                          *config.Config
	runtimeSettings              *settings.Store
	creativeWorkerCountCallback  func(int)
	creativeWorkerStatusCallback func() CreativeWorkerStatus

	panelSettings *runtimeconfig.PanelSettings

	quotaSettings     *accountcore.QuotaSettingsCache
	quotaSettingsOnce sync.Once
}

type DefaultPlatformQuotaSetting = identity.DefaultPlatformQuotaSetting

type ProviderDefaultGrantSettings = identity.ProviderDefaultGrantSettings

type AuthSourceDefaultSettings = identity.AuthSourceDefaultSettings

const (
	defaultAuthSourceBalance     = 0
	defaultAuthSourceConcurrency = 5
	defaultWeChatConnectMode     = "open"
	defaultWeChatConnectScopes   = identity.OAuthDefaultWeChatConnectScopes
	defaultWeChatConnectFrontend = identity.OAuthDefaultWeChatConnectFrontend
	defaultGitHubOAuthAuthorize  = identity.OAuthDefaultGitHubOAuthAuthorize
	defaultGitHubOAuthToken      = identity.OAuthDefaultGitHubOAuthToken
	defaultGitHubOAuthUserInfo   = identity.OAuthDefaultGitHubOAuthUserInfo
	defaultGitHubOAuthEmails     = identity.OAuthDefaultGitHubOAuthEmails
	defaultGitHubOAuthScopes     = identity.OAuthDefaultGitHubOAuthScopes
	defaultGitHubOAuthFrontend   = identity.OAuthDefaultGitHubOAuthFrontend
	defaultGoogleOAuthAuthorize  = identity.OAuthDefaultGoogleOAuthAuthorize
	defaultGoogleOAuthToken      = identity.OAuthDefaultGoogleOAuthToken
	defaultGoogleOAuthUserInfo   = identity.OAuthDefaultGoogleOAuthUserInfo
	defaultGoogleOAuthScopes     = identity.OAuthDefaultGoogleOAuthScopes
	defaultGoogleOAuthFrontend   = identity.OAuthDefaultGoogleOAuthFrontend
	defaultLoginAgreementMode    = "modal"
	defaultLoginAgreementDate    = "2026-03-31"
)

// NewSettingService 创建系统设置服务实例
func NewSettingService(settingRepo SettingRepository, cfg *config.Config) *SettingService {
	store := settings.New(settingRepo)
	if settingRepo != nil {
		settingRepo = store
	}
	return &SettingService{
		backendMode:     admission.NewBackendMode(settingRepo, slog.Warn),
		panelSettings:   runtimeconfig.NewPanelSettings(settingRepo),
		runtimeSettings: store,
		settingRepo:     settingRepo,
		cfg:             cfg,
	}
}

// SetProxyRepository injects a proxy repo for resolving websearch provider proxy URLs.
func (s *SettingService) SetProxyRepository(repo ProxyRepository) {
	s.proxyRepo = repo
}

func (s *SettingService) LoadForwardedClientIPSettings(ctx context.Context) error {
	if s == nil || s.cfg == nil || s.settingRepo == nil {
		return nil
	}
	return s.ForwardedSettings().LoadForwardedClientIPSettings(ctx)
}

// GetAllSettings 获取所有系统设置
func (s *SettingService) GetAllSettings(ctx context.Context) (*SystemSettings, error) {
	settings, err := s.settingRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all settings: %w", err)
	}

	return s.parseSettings(settings), nil
}

// SetOnUpdateCallback sets a callback function to be called when settings are updated
// This is used for cache invalidation (e.g., HTML cache in frontend server)
func (s *SettingService) SetOnUpdateCallback(callback func()) {
	s.settingRuntime().SetOnUpdateCallback(callback)
}

// SetCreativeWorkerCountCallback 设置创作台 worker 数量热更新回调。
func (s *SettingService) SetCreativeWorkerCountCallback(callback func(int)) {
	if s == nil {
		return
	}
	s.creativeWorkerCountCallback = callback
}

// SetCreativeWorkerStatusCallback 设置创作台 worker 池状态读取回调。
func (s *SettingService) SetCreativeWorkerStatusCallback(callback func() CreativeWorkerStatus) {
	if s == nil {
		return
	}
	s.creativeWorkerStatusCallback = callback
}

// CreativeWorkerStatus 返回创作台 worker 池状态快照；回调未注入时返回未运行的零值快照。
func (s *SettingService) CreativeWorkerStatus() CreativeWorkerStatus {
	if s == nil || s.creativeWorkerStatusCallback == nil {
		return CreativeWorkerStatus{}
	}
	return s.creativeWorkerStatusCallback()
}

// SetVersion sets the application version for injection into public settings
func (s *SettingService) SetVersion(version string) {
	s.settingRuntime().SetVersion(version)
}

// GetBalanceUnitName 获取内部余额展示单位名称
func (s *SettingService) GetBalanceUnitName(ctx context.Context) string {
	return billing.ReadBalanceUnitName(ctx, s.settingRepo)
}

// GetOpenAI403CooldownSettings 获取 OpenAI OAuth 403 冷却配置
func (s *SettingService) GetOpenAI403CooldownSettings(ctx context.Context) (*OpenAI403CooldownSettings, error) {
	return s.AccountSettings().GetOpenAI403CooldownSettings(ctx)
}

// GetOpenAIOAuthImportDefaults 获取 OpenAI OAuth 账号导入缺省模板。
func (s *SettingService) GetOpenAIOAuthImportDefaults(ctx context.Context) (*OpenAIOAuthImportDefaults, error) {
	return s.AccountSettings().GetOpenAIOAuthImportDefaults(ctx)
}

// GetUsageRankingSettings 获取用户侧用量排行的完整运行时配置。
func (s *SettingService) GetUsageRankingSettings(ctx context.Context) (UsageRankingSettings, error) {
	return s.UsageSettings().GetUsageRankingSettings(ctx)
}

// GetUsageRankingLimit 获取用户侧用量排行展示数量。
func (s *SettingService) GetUsageRankingLimit(ctx context.Context) int {
	return s.UsageSettings().GetUsageRankingLimit(ctx)
}

// IsOpenAIAllowClaudeCodeCodexPluginEnabled 全局开关：是否额外放行 Claude Code 的 Codex 插件（默认关闭）。
// 仅在调用方已确认账号 codex_cli_only 开启时读取，避免对非受限账号产生无谓查询。
// 使用进程内 atomic.Value 缓存（60s TTL），避免在每个网关请求热路径上访问 DB。
func (s *SettingService) IsOpenAIAllowClaudeCodeCodexPluginEnabled(ctx context.Context) bool {
	return s.GatewaySettings().IsOpenAIAllowClaudeCodeCodexPluginEnabled(ctx)
}

func (s *SettingService) IsRegistrationEmailNormalizationEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsRegistrationEmailNormalizationEnabled(ctx)
}

// IsCreativeEnabled 读取创作台数据库运行时开关（键缺失或读取失败时默认开启，
// 与 team_enabled 保持同款"缺省 true"语义；进程级 creative.enabled 由调用方另行校验）。
func (s *SettingService) IsCreativeEnabled(ctx context.Context) bool {
	return s.CreativeSettings().IsCreativeEnabled(ctx)
}

// GetCreativeModelSettings 读取创作台模型白名单；缺失、损坏或读取失败均按空列表处理。
// 空列表是明确的 fail-closed 语义，不会因为数据库异常误放行生图模型。
func (s *SettingService) GetCreativeModelSettings(ctx context.Context) []CreativeModelSetting {
	return s.CreativeSettings().GetCreativeModelSettings(ctx)
}

// SetDefaultSubscriptionPlanReader injects an optional plan reader for default subscription validation.
func (s *SettingService) SetDefaultSubscriptionPlanReader(reader DefaultSubscriptionPlanReader) {
	s.defaultSubPlanReader = reader
}

// SetOpenAI403CooldownSettings 设置 OpenAI OAuth 403 冷却配置
func (s *SettingService) SetOpenAI403CooldownSettings(ctx context.Context, settings *OpenAI403CooldownSettings) error {
	return s.AccountSettings().SetOpenAI403CooldownSettings(ctx, settings)
}

// SetOpenAIOAuthImportDefaults 保存 OpenAI OAuth 账号导入缺省模板。
func (s *SettingService) SetOpenAIOAuthImportDefaults(ctx context.Context, settings *OpenAIOAuthImportDefaults) error {
	return s.AccountSettings().SetOpenAIOAuthImportDefaults(ctx, settings)
}

func (s *SettingService) validateDefaultSubscriptionPlans(ctx context.Context, items []DefaultSubscriptionSetting) error {
	if s.defaultSubPlanReader == nil {
		return identity.ValidateDefaultSubscriptionPlans(ctx, items, nil)
	}
	return identity.ValidateDefaultSubscriptionPlans(ctx, items, s.defaultSubPlanReader.GetByID)
}

const (
	// DefaultUsageRankingLimit 是用量排行默认展示名次。
	DefaultUsageRankingLimit = 20
	// MaxUsageRankingLimit 是用量排行允许展示的最大名次。
	MaxUsageRankingLimit = 100
)

// DefaultSubscriptionPlanReader validates plan references used by default subscriptions.
type DefaultSubscriptionPlanReader interface {
	GetByID(ctx context.Context, id int64) (*SubscriptionPlan, error)
}

// cachedOpenAIAllowCodexPlugin Codex 插件放行开关缓存（进程内缓存，60s TTL）。
// IsOpenAIAllowClaudeCodeCodexPluginEnabled 在每个 codex_cli_only 账号的网关请求热路径上被调用，避免每次访问 DB。

// settingRuntime 同时兼容旧测试和少数零值组装入口，运行态始终复用同一存取实例。
func (s *SettingService) settingRuntime() *settings.Store {
	s.runtimeSettingsMu.Lock()
	defer s.runtimeSettingsMu.Unlock()
	if s.runtimeSettings == nil {
		s.runtimeSettings = settings.New(s.settingRepo)
	}
	return s.runtimeSettings
}

// BackendModeSettings 只返回已存在的唯一发布缓存，供装配使用。
func (s *SettingService) BackendModeSettings() *admission.BackendMode { return s.backendMode }
