package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"golang.org/x/sync/singleflight"
)

const (
	GrokDefaultBaseURLModeAPI     = "api"
	GrokDefaultBaseURLModeUSEast1 = "us-east-1"
	GrokDefaultBaseURLModeUSWest2 = "us-west-2"
	GrokDefaultBaseURLModeEUWest1 = "eu-west-1"
	GrokDefaultBaseURLModeCLI     = "cli"
)

func normalizeGrokDefaultBaseURLMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case GrokDefaultBaseURLModeAPI:
		return GrokDefaultBaseURLModeAPI
	case GrokDefaultBaseURLModeUSEast1:
		return GrokDefaultBaseURLModeUSEast1
	case GrokDefaultBaseURLModeUSWest2:
		return GrokDefaultBaseURLModeUSWest2
	case GrokDefaultBaseURLModeEUWest1:
		return GrokDefaultBaseURLModeEUWest1
	case GrokDefaultBaseURLModeCLI:
		return GrokDefaultBaseURLModeCLI
	default:
		return GrokDefaultBaseURLModeCLI
	}
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
	if s == nil || s.settingRepo == nil {
		return GrokDefaultBaseURLModeCLI
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyGrokDefaultBaseURLMode)
	if err != nil {
		return GrokDefaultBaseURLModeCLI
	}
	return normalizeGrokDefaultBaseURLMode(raw)
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
	ErrRegistrationDisabled  = infraerrors.Forbidden("REGISTRATION_DISABLED", "registration is currently disabled")
	ErrSettingNotFound       = infraerrors.NotFound("SETTING_NOT_FOUND", "setting not found")
	ErrDefaultSubPlanInvalid = infraerrors.BadRequest(
		"DEFAULT_SUBSCRIPTION_PLAN_INVALID",
		"default subscription plan must exist",
	)
	ErrDefaultSubPlanDuplicate = infraerrors.BadRequest(
		"DEFAULT_SUBSCRIPTION_PLAN_DUPLICATE",
		"default subscription plan cannot be duplicated",
	)
)

type SettingRepository interface {
	Get(ctx context.Context, key string) (*Setting, error)
	GetValue(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	GetMultiple(ctx context.Context, keys []string) (map[string]string, error)
	SetMultiple(ctx context.Context, settings map[string]string) error
	GetAll(ctx context.Context) (map[string]string, error)
	Delete(ctx context.Context, key string) error
}

// WebSearchManagerBuilder creates a websearch.Manager from config (injected by infra layer).
// proxyURLs maps proxy ID to resolved URL for provider-level proxy support.
type WebSearchManagerBuilder func(cfg *WebSearchEmulationConfig, proxyURLs map[int64]string)

// SettingService 系统设置服务
type SettingService struct {
	settingRepo                 SettingRepository
	defaultSubPlanReader        DefaultSubscriptionPlanReader
	proxyRepo                   ProxyRepository // for resolving websearch provider proxy URLs
	cfg                         *config.Config
	onUpdate                    func() // Callback when settings are updated (for cache invalidation)
	version                     string // Application version
	webSearchManagerBuilder     WebSearchManagerBuilder
	antigravityUAVersionCache   atomic.Value // *cachedAntigravityUserAgentVersion
	antigravityUAVersionSF      singleflight.Group
	openAICodexUACache          atomic.Value // *cachedOpenAICodexUserAgent
	openAICodexUASF             singleflight.Group
	openAIAllowCodexPluginCache atomic.Value // *cachedOpenAIAllowCodexPlugin
	openAIAllowCodexPluginSF    singleflight.Group
	cyberSessionBlockCache      atomic.Value // *cachedCyberSessionBlockRuntime
	cyberSessionBlockSF         singleflight.Group

	// panelRateLimitCache 面板 API 限流配置进程内缓存（*cachedPanelRateLimitSettings）。
	// 面板每个认证请求都会读取，禁止在热路径上直接访问 DB。
	panelRateLimitCache atomic.Value
	panelRateLimitSF    singleflight.Group

	// openAIQuotaAutoPauseSettingsCache 保存最近一次观测到的配额自动暂停设置。
	// GetOpenAIQuotaAutoPauseSettings 在请求热路径读取这个 atomic.Value，绝不阻塞等待 DB；
	// 当缓存项过期时，后台 goroutine 通过 openAIQuotaAutoPauseSettingsSF 刷新它
	// （stale-while-revalidate）。该字段归属单个服务实例，也让测试天然隔离：
	// 每个 SettingService 实例拥有自己的缓存，不共享包级状态。
	openAIQuotaAutoPauseSettingsCache atomic.Value // *cachedOpenAIQuotaAutoPauseSettings
	openAIQuotaAutoPauseSettingsSF    singleflight.Group
}

// DefaultPlatformQuotaSetting 单 platform 三档限额（nil = 沿用上层；0 = 显式禁用；>0 = 上限）
type DefaultPlatformQuotaSetting struct {
	DailyLimitUSD   *float64 `json:"daily"`
	WeeklyLimitUSD  *float64 `json:"weekly"`
	MonthlyLimitUSD *float64 `json:"monthly"`
}

type ProviderDefaultGrantSettings struct {
	Balance          float64
	Concurrency      int
	Subscriptions    []DefaultSubscriptionSetting
	GrantOnSignup    bool
	GrantOnFirstBind bool
	PlatformQuotas   map[string]*DefaultPlatformQuotaSetting // key = platform name
}

type AuthSourceDefaultSettings struct {
	Email                        ProviderDefaultGrantSettings
	LinuxDo                      ProviderDefaultGrantSettings
	OIDC                         ProviderDefaultGrantSettings
	WeChat                       ProviderDefaultGrantSettings
	GitHub                       ProviderDefaultGrantSettings
	Google                       ProviderDefaultGrantSettings
	DingTalk                     ProviderDefaultGrantSettings
	ForceEmailOnThirdPartySignup bool
}

type authSourceDefaultKeySet struct {
	// source 是 auth source 标识（如 "email"、"github"），仅用于 parse 时
	// slog.Warn 诊断输出，不再参与 key 拼接（platformQuotas 字段已存完整 key）。
	source           string
	balance          string
	concurrency      string
	subscriptions    string
	grantOnSignup    string
	grantOnFirstBind string
	platformQuotas   string // SettingKeyAuthSourcePlatformQuotas(source)
}

var (
	emailAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "email",
		balance:          SettingKeyAuthSourceDefaultEmailBalance,
		concurrency:      SettingKeyAuthSourceDefaultEmailConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultEmailSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultEmailGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultEmailGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("email"),
	}
	linuxDoAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "linuxdo",
		balance:          SettingKeyAuthSourceDefaultLinuxDoBalance,
		concurrency:      SettingKeyAuthSourceDefaultLinuxDoConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultLinuxDoSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultLinuxDoGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("linuxdo"),
	}
	oidcAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "oidc",
		balance:          SettingKeyAuthSourceDefaultOIDCBalance,
		concurrency:      SettingKeyAuthSourceDefaultOIDCConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultOIDCSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultOIDCGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("oidc"),
	}
	weChatAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "wechat",
		balance:          SettingKeyAuthSourceDefaultWeChatBalance,
		concurrency:      SettingKeyAuthSourceDefaultWeChatConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultWeChatSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultWeChatGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultWeChatGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("wechat"),
	}
	gitHubAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "github",
		balance:          SettingKeyAuthSourceDefaultGitHubBalance,
		concurrency:      SettingKeyAuthSourceDefaultGitHubConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultGitHubSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultGitHubGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultGitHubGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("github"),
	}
	googleAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "google",
		balance:          SettingKeyAuthSourceDefaultGoogleBalance,
		concurrency:      SettingKeyAuthSourceDefaultGoogleConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultGoogleSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultGoogleGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultGoogleGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("google"),
	}
	dingTalkAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "dingtalk",
		balance:          SettingKeyAuthSourceDefaultDingTalkBalance,
		concurrency:      SettingKeyAuthSourceDefaultDingTalkConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultDingTalkSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultDingTalkGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultDingTalkGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("dingtalk"),
	}
)

const (
	defaultAuthSourceBalance     = 0
	defaultAuthSourceConcurrency = 5
	defaultWeChatConnectMode     = "open"
	defaultWeChatConnectScopes   = "snsapi_login"
	defaultWeChatConnectFrontend = "/auth/wechat/callback"
	defaultGitHubOAuthAuthorize  = "https://github.com/login/oauth/authorize"
	defaultGitHubOAuthToken      = "https://github.com/login/oauth/access_token"
	defaultGitHubOAuthUserInfo   = "https://api.github.com/user"
	defaultGitHubOAuthEmails     = "https://api.github.com/user/emails"
	defaultGitHubOAuthScopes     = "read:user user:email"
	defaultGitHubOAuthFrontend   = "/auth/oauth/callback"
	defaultGoogleOAuthAuthorize  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleOAuthToken      = "https://oauth2.googleapis.com/token"
	defaultGoogleOAuthUserInfo   = "https://openidconnect.googleapis.com/v1/userinfo"
	defaultGoogleOAuthScopes     = "openid email profile"
	defaultGoogleOAuthFrontend   = "/auth/oauth/callback"
	defaultLoginAgreementMode    = "modal"
	defaultLoginAgreementDate    = "2026-03-31"
)

// NewSettingService 创建系统设置服务实例
func NewSettingService(settingRepo SettingRepository, cfg *config.Config) *SettingService {
	return &SettingService{
		settingRepo: settingRepo,
		cfg:         cfg,
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

	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyAPIKeyACLTrustForwardedIP,
		SettingKeyForwardedClientIPHeaders,
		settingKeyForwardedClientIPModeV2,
	})
	if err != nil {
		s.cfg.SetForwardedClientIPSettings(false, nil)
		return fmt.Errorf("get forwarded client ip settings: %w", err)
	}

	enabled := s.cfg.Security.TrustForwardedIPForAPIKeyACL
	headers := s.cfg.ForwardedClientIPSettings().Headers
	storedValue, hasStoredValue := values[SettingKeyAPIKeyACLTrustForwardedIP]
	if hasStoredValue {
		enabled = storedValue == "true"
	}

	var headersErr error
	if storedHeaders, ok := values[SettingKeyForwardedClientIPHeaders]; ok {
		headers, headersErr = parseForwardedClientIPHeadersSetting(storedHeaders)
		if headersErr != nil {
			enabled = false
			headers = []string{}
			headersErr = fmt.Errorf("load forwarded client ip headers: %w", headersErr)
		}
	}

	updates := make(map[string]string)
	if _, hasStoredHeaders := values[SettingKeyForwardedClientIPHeaders]; !hasStoredHeaders {
		headersJSON, marshalErr := json.Marshal(headers)
		if marshalErr != nil {
			headers = []string{}
			headersErr = errors.Join(headersErr, fmt.Errorf("marshal forwarded client ip headers: %w", marshalErr))
			headersJSON = []byte("[]")
		}
		updates[SettingKeyForwardedClientIPHeaders] = string(headersJSON)
	}
	if values[settingKeyForwardedClientIPModeV2] != "true" {
		updates[settingKeyForwardedClientIPModeV2] = "true"
		// 本迁移之前的新安装会默认持久化 false；仅在未配置可信代理策略时恢复兼容模式。
		if headersErr == nil && hasStoredValue && !enabled && !s.cfg.Server.TrustedProxiesConfigured {
			enabled = true
			updates[SettingKeyAPIKeyACLTrustForwardedIP] = "true"
		}
	}
	if len(updates) > 0 {
		if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
			s.cfg.SetForwardedClientIPSettings(enabled, headers)
			return errors.Join(headersErr, fmt.Errorf("migrate forwarded client ip setting: %w", err))
		}
	}

	s.cfg.SetForwardedClientIPSettings(enabled, headers)
	return headersErr
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
	s.onUpdate = callback
}

// SetVersion sets the application version for injection into public settings
func (s *SettingService) SetVersion(version string) {
	s.version = version
}

// getStringOrDefault 获取字符串值或默认值
func (s *SettingService) getStringOrDefault(settings map[string]string, key, defaultValue string) string {
	if value, ok := settings[key]; ok && value != "" {
		return value
	}
	return defaultValue
}

// GetBalanceUnitName 获取内部余额展示单位名称
func (s *SettingService) GetBalanceUnitName(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyBalanceUnitName)
	if err != nil {
		return "USD"
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "USD"
	}
	return value
}

// GetOpenAI403CooldownSettings 获取 OpenAI OAuth 403 冷却配置
func (s *SettingService) GetOpenAI403CooldownSettings(ctx context.Context) (*OpenAI403CooldownSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAI403CooldownSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultOpenAI403CooldownSettings(), nil
		}
		return nil, fmt.Errorf("get openai 403 cooldown settings: %w", err)
	}
	if value == "" {
		return DefaultOpenAI403CooldownSettings(), nil
	}

	settings := *DefaultOpenAI403CooldownSettings()
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultOpenAI403CooldownSettings(), nil
	}

	if settings.CooldownMinutes < 1 {
		settings.CooldownMinutes = openAI403CooldownMinutesDefault
	}
	if settings.CooldownMinutes > 120 {
		settings.CooldownMinutes = 120
	}
	if settings.ThresholdCount < 1 {
		settings.ThresholdCount = openAI403DisableThresholdDefault
	}
	if settings.ThresholdCount > 20 {
		settings.ThresholdCount = 20
	}
	if settings.ThresholdWindowMinutes < 1 {
		settings.ThresholdWindowMinutes = openAI403CounterWindowMinutesDefault
	}
	if settings.ThresholdWindowMinutes > 1440 {
		settings.ThresholdWindowMinutes = 1440
	}

	return &settings, nil
}

// GetOpenAIOAuthImportDefaults 获取 OpenAI OAuth 账号导入缺省模板。
func (s *SettingService) GetOpenAIOAuthImportDefaults(ctx context.Context) (*OpenAIOAuthImportDefaults, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIOAuthImportDefaults)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultOpenAIOAuthImportDefaults(), nil
		}
		return nil, fmt.Errorf("get openai oauth import defaults: %w", err)
	}
	if value == "" {
		return DefaultOpenAIOAuthImportDefaults(), nil
	}

	var settings OpenAIOAuthImportDefaults
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		slog.Warn("failed to unmarshal openai oauth import defaults, falling back to defaults",
			"error", err,
			"key", SettingKeyOpenAIOAuthImportDefaults)
		return DefaultOpenAIOAuthImportDefaults(), nil
	}

	return fillOpenAIOAuthImportDefaults(&settings), nil
}

// GetUsageRankingLimit 获取用户侧用量排行展示数量。
func (s *SettingService) GetUsageRankingLimit(ctx context.Context) int {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyUsageRankingLimit)
	if err != nil {
		return DefaultUsageRankingLimit
	}
	return normalizeUsageRankingLimitString(value)
}

// IsOpenAIAllowClaudeCodeCodexPluginEnabled 全局开关：是否额外放行 Claude Code 的 Codex 插件（默认关闭）。
// 仅在调用方已确认账号 codex_cli_only 开启时读取，避免对非受限账号产生无谓查询。
// 使用进程内 atomic.Value 缓存（60s TTL），避免在每个网关请求热路径上访问 DB。
func (s *SettingService) IsOpenAIAllowClaudeCodeCodexPluginEnabled(ctx context.Context) bool {
	if cached, ok := s.openAIAllowCodexPluginCache.Load().(*cachedOpenAIAllowCodexPlugin); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}
	result, _, _ := s.openAIAllowCodexPluginSF.Do("openai_allow_codex_plugin_enabled", func() (any, error) {
		if cached, ok := s.openAIAllowCodexPluginCache.Load().(*cachedOpenAIAllowCodexPlugin); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIAllowCodexPluginDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAIAllowClaudeCodeCodexPlugin)
		if err != nil {
			if errors.Is(err, ErrSettingNotFound) {
				// 设置不存在 → 默认关闭，正常 TTL 缓存
				s.openAIAllowCodexPluginCache.Store(&cachedOpenAIAllowCodexPlugin{
					value:     false,
					expiresAt: time.Now().Add(openAIAllowCodexPluginCacheTTL).UnixNano(),
				})
				return false, nil
			}
			slog.Warn("failed to get openai_allow_claude_code_codex_plugin setting", "error", err)
			// DB 错误 → 安全默认关闭，短 TTL 快速重试
			s.openAIAllowCodexPluginCache.Store(&cachedOpenAIAllowCodexPlugin{
				value:     false,
				expiresAt: time.Now().Add(openAIAllowCodexPluginErrorTTL).UnixNano(),
			})
			return false, nil
		}
		enabled := value == "true"
		s.openAIAllowCodexPluginCache.Store(&cachedOpenAIAllowCodexPlugin{
			value:     enabled,
			expiresAt: time.Now().Add(openAIAllowCodexPluginCacheTTL).UnixNano(),
		})
		return enabled, nil
	})
	if val, ok := result.(bool); ok {
		return val
	}
	return false
}

func (s *SettingService) IsRegistrationEmailNormalizationEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEmailNormalization)
	if err != nil {
		return false
	}
	return value == "true"
}

// SetDefaultSubscriptionPlanReader injects an optional plan reader for default subscription validation.
func (s *SettingService) SetDefaultSubscriptionPlanReader(reader DefaultSubscriptionPlanReader) {
	s.defaultSubPlanReader = reader
}

// SetOpenAI403CooldownSettings 设置 OpenAI OAuth 403 冷却配置
func (s *SettingService) SetOpenAI403CooldownSettings(ctx context.Context, settings *OpenAI403CooldownSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	// 禁用时修正为合法值即可，不拒绝请求
	if settings.CooldownMinutes < 1 || settings.CooldownMinutes > 120 {
		if settings.Enabled {
			return fmt.Errorf("cooldown_minutes must be between 1-120")
		}
		settings.CooldownMinutes = openAI403CooldownMinutesDefault
	}
	if settings.ThresholdCount < 1 || settings.ThresholdCount > 20 {
		if settings.Enabled && settings.ErrorOnThresholdEnabled {
			return fmt.Errorf("threshold_count must be between 1-20")
		}
		settings.ThresholdCount = openAI403DisableThresholdDefault
	}
	if settings.ThresholdWindowMinutes < 1 || settings.ThresholdWindowMinutes > 1440 {
		if settings.Enabled && settings.ErrorOnThresholdEnabled {
			return fmt.Errorf("threshold_window_minutes must be between 1-1440")
		}
		settings.ThresholdWindowMinutes = openAI403CounterWindowMinutesDefault
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal openai 403 cooldown settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyOpenAI403CooldownSettings, string(data))
}

// SetOpenAIOAuthImportDefaults 保存 OpenAI OAuth 账号导入缺省模板。
func (s *SettingService) SetOpenAIOAuthImportDefaults(ctx context.Context, settings *OpenAIOAuthImportDefaults) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if err := validateOpenAIOAuthImportDefaults(settings); err != nil {
		return err
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal openai oauth import defaults: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyOpenAIOAuthImportDefaults, string(data))
}

func (s *SettingService) baseEmailOAuthConfig(provider string) config.EmailOAuthProviderConfig {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		base := config.EmailOAuthProviderConfig{
			AuthorizeURL:        defaultGitHubOAuthAuthorize,
			TokenURL:            defaultGitHubOAuthToken,
			UserInfoURL:         defaultGitHubOAuthUserInfo,
			EmailsURL:           defaultGitHubOAuthEmails,
			Scopes:              defaultGitHubOAuthScopes,
			FrontendRedirectURL: defaultGitHubOAuthFrontend,
		}
		if s != nil && s.cfg != nil {
			return mergeEmailOAuthBaseConfig(base, s.cfg.GitHubOAuth)
		}
		return base
	case "google":
		base := config.EmailOAuthProviderConfig{
			AuthorizeURL:        defaultGoogleOAuthAuthorize,
			TokenURL:            defaultGoogleOAuthToken,
			UserInfoURL:         defaultGoogleOAuthUserInfo,
			Scopes:              defaultGoogleOAuthScopes,
			FrontendRedirectURL: defaultGoogleOAuthFrontend,
		}
		if s != nil && s.cfg != nil {
			return mergeEmailOAuthBaseConfig(base, s.cfg.GoogleOAuth)
		}
		return base
	default:
		return config.EmailOAuthProviderConfig{}
	}
}

func (s *SettingService) buildOpsAdvancedSettingsWithQuotaAutoPause(ctx context.Context, quota OpsOpenAIAccountQuotaAutoPauseSettings) (*OpsAdvancedSettings, error) {
	cfg := defaultOpsAdvancedSettings()
	if s != nil && s.settingRepo != nil {
		rawSettings, err := s.settingRepo.GetAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("get settings for ops advanced merge: %w", err)
		}
		raw := rawSettings[SettingKeyOpsAdvancedSettings]
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), cfg); err != nil {
				return nil, fmt.Errorf("unmarshal ops advanced settings: %w", err)
			}
		}
	}
	// 系统设置只接管 OpenAI 配额自动暂停子配置，其他运维高级设置必须原样保留。
	cfg.OpenAIAccountQuotaAutoPause = quota
	normalizeOpsAdvancedSettings(cfg)
	return cfg, nil
}

func (s *SettingService) validateDefaultSubscriptionPlans(ctx context.Context, items []DefaultSubscriptionSetting) error {
	if len(items) == 0 {
		return nil
	}

	checked := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if item.PlanID <= 0 {
			continue
		}
		if _, ok := checked[item.PlanID]; ok {
			return ErrDefaultSubPlanDuplicate.WithMetadata(map[string]string{
				"plan_id": strconv.FormatInt(item.PlanID, 10),
			})
		}
		checked[item.PlanID] = struct{}{}
		if s.defaultSubPlanReader == nil {
			continue
		}

		plan, err := s.defaultSubPlanReader.GetByID(ctx, item.PlanID)
		if err != nil {
			if infraerrors.IsNotFound(err) {
				return ErrDefaultSubPlanInvalid.WithMetadata(map[string]string{
					"plan_id": strconv.FormatInt(item.PlanID, 10),
				})
			}
			return fmt.Errorf("get default subscription plan %d: %w", item.PlanID, err)
		}
		if plan == nil || plan.ID <= 0 {
			return ErrDefaultSubPlanInvalid.WithMetadata(map[string]string{
				"plan_id": strconv.FormatInt(item.PlanID, 10),
			})
		}
	}

	return nil
}

func emailOAuthSettingKeys(provider string) (enabled, clientID, clientSecret, redirectURL, frontendRedirectURL string) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		return SettingKeyGitHubOAuthEnabled,
			SettingKeyGitHubOAuthClientID,
			SettingKeyGitHubOAuthClientSecret,
			SettingKeyGitHubOAuthRedirectURL,
			SettingKeyGitHubOAuthFrontendRedirectURL
	case "google":
		return SettingKeyGoogleOAuthEnabled,
			SettingKeyGoogleOAuthClientID,
			SettingKeyGoogleOAuthClientSecret,
			SettingKeyGoogleOAuthRedirectURL,
			SettingKeyGoogleOAuthFrontendRedirectURL
	default:
		return "", "", "", "", ""
	}
}

func fillOpenAIOAuthImportDefaults(settings *OpenAIOAuthImportDefaults) *OpenAIOAuthImportDefaults {
	if settings == nil {
		return DefaultOpenAIOAuthImportDefaults()
	}

	defaults := DefaultOpenAIOAuthImportDefaults()
	if len(defaults.Credentials) > 0 {
		if settings.Credentials == nil {
			settings.Credentials = map[string]any{}
		}
		for key, value := range defaults.Credentials {
			// 已显式保存的键保持原样；空数组可用于表达“不限制模型”。
			if _, exists := settings.Credentials[key]; !exists {
				settings.Credentials[key] = value
			}
		}
	}
	return settings
}

func findForbiddenImportField(fields map[string]any, forbidden map[string]struct{}) (string, bool) {
	for key := range fields {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := forbidden[normalized]; ok {
			return key, true
		}
	}
	return "", false
}

func normalizeUsageRankingLimit(value int) int {
	if value <= 0 {
		return DefaultUsageRankingLimit
	}
	if value > MaxUsageRankingLimit {
		return MaxUsageRankingLimit
	}
	return value
}

func normalizeUsageRankingLimitString(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return DefaultUsageRankingLimit
	}
	return normalizeUsageRankingLimit(value)
}

func parseOpenAIQuotaAutoPauseSettingsFromRaw(raw string) OpsOpenAIAccountQuotaAutoPauseSettings {
	cfg := defaultOpsAdvancedSettings()
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), cfg); err != nil {
			return OpsOpenAIAccountQuotaAutoPauseSettings{}
		}
	}
	normalizeOpsAdvancedSettings(cfg)
	return cfg.OpenAIAccountQuotaAutoPause
}

func validateOpenAIOAuthImportDefaults(settings *OpenAIOAuthImportDefaults) error {
	if settings.Account.Concurrency != nil && *settings.Account.Concurrency < 0 {
		return fmt.Errorf("account.concurrency must be >= 0")
	}
	if settings.Account.Priority != nil && *settings.Account.Priority < 0 {
		return fmt.Errorf("account.priority must be >= 0")
	}
	if settings.Account.RateMultiplier != nil && *settings.Account.RateMultiplier < 0 {
		return fmt.Errorf("account.rate_multiplier must be >= 0")
	}
	if settings.Account.ExpiresAt != nil && *settings.Account.ExpiresAt < 0 {
		return fmt.Errorf("account.expires_at must be >= 0")
	}

	forbiddenCredentials := map[string]struct{}{
		"access_token":            {},
		"refresh_token":           {},
		"id_token":                {},
		"expires_at":              {},
		"email":                   {},
		"client_id":               {},
		"chatgpt_account_id":      {},
		"chatgpt_user_id":         {},
		"organization_id":         {},
		"plan_type":               {},
		"subscription_expires_at": {},
	}
	if field, ok := findForbiddenImportField(settings.Credentials, forbiddenCredentials); ok {
		return fmt.Errorf("credentials.%s is not allowed in import defaults", field)
	}

	forbiddenExtra := map[string]struct{}{
		"email": {},
		"name":  {},
	}
	if field, ok := findForbiddenImportField(settings.Extra, forbiddenExtra); ok {
		return fmt.Errorf("extra.%s is not allowed in import defaults", field)
	}

	return nil
}

const (
	// DefaultUsageRankingLimit 是用量排行默认展示名次。
	DefaultUsageRankingLimit = 20
	// MaxUsageRankingLimit 是用量排行允许展示的最大名次。
	MaxUsageRankingLimit = 100
)

const openAIAllowCodexPluginCacheTTL = 60 * time.Second

const openAIAllowCodexPluginDBTimeout = 5 * time.Second

const openAIAllowCodexPluginErrorTTL = 5 * time.Second

// DefaultSubscriptionPlanReader validates plan references used by default subscriptions.
type DefaultSubscriptionPlanReader interface {
	GetByID(ctx context.Context, id int64) (*SubscriptionPlan, error)
}

// cachedOpenAIAllowCodexPlugin Codex 插件放行开关缓存（进程内缓存，60s TTL）。
// IsOpenAIAllowClaudeCodeCodexPluginEnabled 在每个 codex_cli_only 账号的网关请求热路径上被调用，避免每次访问 DB。
type cachedOpenAIAllowCodexPlugin struct {
	value     bool
	expiresAt int64 // unix nano
}
