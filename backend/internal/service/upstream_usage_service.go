package service

import (
	context "context"
	http "net/http"
	strings "strings"
	time "time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	tlsfingerprint "github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	urlvalidator "github.com/TokenFlux/TokenRouter/internal/util/urlvalidator"
)

const (
	// UpstreamUsageQueryExtraKey 是 API Key 账号用量查询的持久化配置键。
	UpstreamUsageQueryExtraKey = acctcore.UpstreamUsageQueryExtraKey

	UpstreamUsageAdapterSub2API         = acctcore.UpstreamUsageAdapterSub2API
	UpstreamUsageAdapterNewAPI          = acctcore.UpstreamUsageAdapterNewAPI
	UpstreamUsageAdapterZivv            = acctcore.UpstreamUsageAdapterZivv
	UpstreamUsageAdapterKimiCoding      = acctcore.UpstreamUsageAdapterKimiCoding
	UpstreamUsageAdapterZhipuCoding     = acctcore.UpstreamUsageAdapterZhipuCoding
	UpstreamUsageAdapterKimiBalance     = acctcore.UpstreamUsageAdapterKimiBalance
	UpstreamUsageAdapterDeepseekBalance = acctcore.UpstreamUsageAdapterDeepseekBalance

	// New API 钱包接口在官方部署中需要用户级访问令牌；它与转发 API Key
	// 分开保存，避免把一个 token 的额度误当成用户钱包余额。
	NewAPIUserAccessTokenCredentialKey = acctcore.NewAPIUserAccessTokenCredentialKey
	NewAPIUserIDCredentialKey          = acctcore.NewAPIUserIDCredentialKey

	upstreamUsageDefaultAdapter = UpstreamUsageAdapterSub2API
	upstreamUsageMaxBodyBytes   = 512 * 1024
	upstreamUsageTimeout        = 60 * time.Second
	upstreamUsageStatusTimeout  = 2 * time.Second
	upstreamUsageBatchLimit     = 100
	upstreamUsageConcurrency    = 4
)

var (
	ErrUpstreamUsageUnavailable       = acctcore.ErrUpstreamUsageUnavailable
	ErrUpstreamUsageAccountInvalid    = acctcore.ErrUpstreamUsageAccountInvalid
	ErrUpstreamUsageAccountDisabled   = acctcore.ErrUpstreamUsageAccountDisabled
	ErrUpstreamUsageDisabled          = acctcore.ErrUpstreamUsageDisabled
	ErrUpstreamUsageUnsupported       = acctcore.ErrUpstreamUsageUnsupported
	ErrUpstreamUsageAuthFailed        = acctcore.ErrUpstreamUsageAuthFailed
	ErrUpstreamUsageWalletUnavailable = acctcore.ErrUpstreamUsageWalletUnavailable
	ErrUpstreamUsageWalletAuthFailed  = acctcore.ErrUpstreamUsageWalletAuthFailed
	ErrUpstreamUsageRateLimited       = acctcore.ErrUpstreamUsageRateLimited
	ErrUpstreamUsageTimeout           = acctcore.ErrUpstreamUsageTimeout
	ErrUpstreamUsageInvalidResponse   = acctcore.ErrUpstreamUsageInvalidResponse
	ErrUpstreamUsageRequestFailed     = acctcore.ErrUpstreamUsageRequestFailed
	ErrUpstreamUsageIdentityChanged   = acctcore.ErrUpstreamUsageIdentityChanged
	ErrUpstreamUsageConfigInvalid     = acctcore.ErrUpstreamUsageConfigInvalid
	ErrUpstreamUsageBatchInvalid      = acctcore.ErrUpstreamUsageBatchInvalid
	ErrUpstreamUsageBatchTooLarge     = acctcore.ErrUpstreamUsageBatchTooLarge
)

// UpstreamUsageQueryConfig 兼容旧消费者，值类型归账号模块。
type UpstreamUsageQueryConfig = acctcore.UpstreamUsageQueryConfig

// UpstreamUsageAmount 兼容旧消费者，值类型归账号模块。
type UpstreamUsageAmount = acctcore.UpstreamUsageAmount

// UpstreamUsageBalanceEntry 兼容旧消费者，值类型归账号模块。
type UpstreamUsageBalanceEntry = acctcore.UpstreamUsageBalanceEntry

// UpstreamUsageLimit 兼容旧消费者，值类型归账号模块。
type UpstreamUsageLimit = acctcore.UpstreamUsageLimit

// UpstreamUsageSubscription 兼容旧消费者，值类型归账号模块。
type UpstreamUsageSubscription = acctcore.UpstreamUsageSubscription

// UpstreamUsageInfo 兼容旧消费者，值类型归账号模块。
type UpstreamUsageInfo = acctcore.UpstreamUsageInfo

// UpstreamUsageQueryResult 兼容旧消费者，值类型归账号模块。
type UpstreamUsageQueryResult = acctcore.UpstreamUsageQueryResult

// UpstreamUsageMetrics 兼容旧消费者，值类型归账号模块。
type UpstreamUsageMetrics = acctcore.UpstreamUsageMetrics

type UpstreamUsageAdapter = usagecontract.Adapter

// UpstreamUsageAdapterOption 兼容旧消费者，值类型归账号模块。
type UpstreamUsageAdapterOption = acctcore.UpstreamUsageAdapterOption

func UpstreamUsageAdapterOptions() []UpstreamUsageAdapterOption {
	return acctcore.UpstreamUsageAdapterOptions()
}

// UpstreamUsageService 负责 API Key 上游用量查询；结果只在当前请求中存在。
type UpstreamUsageService struct {
	core                *acctcore.UpstreamUsageService
	accountRepo         AccountRepository
	httpUpstream        HTTPUpstream
	cfg                 *config.Config
	tlsFPProfileService *TLSFingerprintProfileService
	execution           *accountprovider.UpstreamUsageExecution
}

// NewUpstreamUsageService 创建上游用量查询服务。
func NewUpstreamUsageService(repo AccountRepository, upstream HTTPUpstream, cfg *config.Config, tls *TLSFingerprintProfileService) *UpstreamUsageService {
	s := NewUpstreamUsageExecution(repo, upstream, cfg, tls)
	var reader acctcore.UpstreamUsageReader
	if repo != nil {
		reader = legacyUsageReader{repo}
	}
	s.core = acctcore.NewUpstreamUsageService(reader, s.execution, acctcore.UpstreamUsageOptions{Now: time.Now})
	return s
}

// NewUpstreamUsageExecution 创建上游用量查询服务。
func NewUpstreamUsageExecution(
	accountRepo AccountRepository,
	httpUpstream HTTPUpstream,
	cfg *config.Config,
	tlsFPProfileService *TLSFingerprintProfileService,
) *UpstreamUsageService {
	service := &UpstreamUsageService{
		accountRepo:         accountRepo,
		httpUpstream:        httpUpstream,
		cfg:                 cfg,
		tlsFPProfileService: tlsFPProfileService,
	}
	service.execution = accountprovider.NewUpstreamUsageExecution(accountprovider.UpstreamUsageExecutionOptions{
		Available: func() bool { return service.httpUpstream != nil && service.accountRepo != nil }, BaseURL: func(value *acctcore.Record) string { return upstreamUsageAccountBaseURL(AccountFromRecord(value)) }, Request: func(value *acctcore.Record, config acctcore.UpstreamUsageQueryConfig) (*usagecontract.Request, error) {
			return service.newHTTPClient(AccountFromRecord(value), config)
		},
	})
	return service
}

func (s *UpstreamUsageService) SnapshotMetrics() UpstreamUsageMetrics {
	return s.Core().SnapshotMetrics()
}

func EffectiveUpstreamUsageConfig(account *Account) (UpstreamUsageQueryConfig, error) {
	return acctcore.EffectiveUpstreamUsageConfig(protocolRecord(account))
}

func NormalizeUpstreamUsageExtra(extra map[string]any) error {
	return acctcore.NormalizeUpstreamUsageExtra(extra)
}

func normalizedUpstreamUsageConfigValue(value any) (any, bool) {
	return acctcore.NormalizedUpstreamUsageConfigValue(value)
}

func validateUsageBaseURLFormat(raw string) error { return egress.ValidateUsageBaseURLFormat(raw) }

func (s *UpstreamUsageService) QueryAccount(ctx context.Context, accountID int64) (*UpstreamUsageQueryResult, error) {
	return s.Core().QueryAccount(ctx, accountID)
}

func cnUpstreamUsageAdapterName(account *Account) string {
	return acctcore.CNUpstreamUsageAdapterName(protocolRecord(account))
}

func (s *UpstreamUsageService) QueryBatch(ctx context.Context, accountIDs []int64) (map[int64]*UpstreamUsageQueryResult, map[int64]error, error) {
	return s.Core().QueryBatch(ctx, accountIDs)
}

type upstreamUsageHTTPClient = usagecontract.Request

func (s *UpstreamUsageService) newHTTPClient(account *Account, queryConfig UpstreamUsageQueryConfig) (*upstreamUsageHTTPClient, error) {
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return nil, ErrUpstreamUsageAccountInvalid
	}
	baseURL := strings.TrimSpace(queryConfig.BaseURL)
	if baseURL == "" {
		baseURL = upstreamUsageAccountBaseURL(account)
	}
	validated, err := s.validateBaseURL(baseURL)
	if err != nil {
		return nil, ErrUpstreamUsageConfigInvalid.WithCause(err)
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			return nil, ErrUpstreamUsageRequestFailed
		}
		if account.Proxy.ID != *account.ProxyID {
			return nil, ErrUpstreamUsageIdentityChanged
		}
		proxyURL = account.Proxy.URL()
	}
	var profile *tlsfingerprint.Profile
	if s.tlsFPProfileService != nil {
		profile = s.tlsFPProfileService.ResolveTLSProfile(account)
	}
	return &upstreamUsageHTTPClient{BaseURL: validated, APIKey: apiKey, WalletToken: account.GetCredential(NewAPIUserAccessTokenCredentialKey), WalletUserID: account.GetCredential(NewAPIUserIDCredentialKey), ZhipuOrganization: account.GetCredential("zhipu_organization"), ZhipuProject: account.GetCredential("zhipu_project"), ApplyHeaders: account.ApplyHeaderOverrides, Endpoint: buildOpenAIEndpointURL,
		Context: func(ctx context.Context) context.Context {
			return WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI))
		},
		Do: func(req *http.Request) (*http.Response, error) {
			return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, profile)
		},
	}, nil
}

func (s *UpstreamUsageService) validateBaseURL(raw string) (string, error) {
	if err := validateUsageBaseURLFormat(raw); err != nil {
		return "", err
	}
	if s.cfg == nil {
		// 缺少运行时配置时采用最严格的默认值，不能因为测试或降级构造
		// 绕过 HTTPS 和私网地址护栏。
		return urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{AllowPrivate: false})
	}
	if !s.cfg.Security.URLAllowlist.Enabled {
		return urlvalidator.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
	}
	return urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
}

func upstreamUsageAccountBaseURL(account *Account) string {
	if account == nil {
		return ""
	}
	switch account.Platform {
	case PlatformOpenAI:
		return account.GetOpenAIBaseURL()
	case PlatformAnthropic:
		return account.GetBaseURL()
	case PlatformGrok:
		return account.GetGrokBaseURL()
	case PlatformGemini:
		return account.GetGeminiBaseURL("https://generativelanguage.googleapis.com")
	case PlatformAntigravity:
		return account.GetGeminiBaseURL("https://generativelanguage.googleapis.com")
	case PlatformKimi, PlatformZhipu, PlatformDeepseek:
		// 用量端点只替换路径并保留账号主机；缺少自定义地址时使用平台默认值。
		return account.GetOpenAIBaseURL()
	default:
		// 未知平台没有平台专用的 URL 归一化规则，仍允许复用凭据中的根地址。
		return strings.TrimSpace(account.GetCredential("base_url"))
	}
}

func validateNormalizedUsage(usage *UpstreamUsageInfo) error {
	return acctcore.ValidateNormalizedUsage(usage)
}

// --- Sub2API 适配器 ---

// --- Zivv 适配器 ---

// --- New API 适配器 ---

func upstreamUsageContextFingerprint(account *Account, config UpstreamUsageQueryConfig) string {
	return acctcore.UpstreamUsageContextFingerprint(AccountRecordView(account), config, upstreamUsageAccountBaseURL(account))
}

// legacyUsageReader 仅供兼容构造，生产组合根直接绑定唯一账号存储。
type legacyUsageReader struct{ source AccountRepository }

func (r legacyUsageReader) GetByID(ctx context.Context, id int64) (*acctcore.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return AccountRecordView(v), err
}
func (s *UpstreamUsageService) Available() bool           { return s != nil && s.execution.Available() }
func (s *UpstreamUsageService) Supports(name string) bool { return s.execution.Supports(name) }
func (s *UpstreamUsageService) BaseURL(value *acctcore.Record) string {
	return s.execution.BaseURL(value)
}
func (s *UpstreamUsageService) Query(ctx context.Context, value *acctcore.Record, config acctcore.UpstreamUsageQueryConfig) (*acctcore.UpstreamUsageInfo, error) {
	return s.execution.Query(ctx, value, config)
}
func (s *UpstreamUsageService) Core() *acctcore.UpstreamUsageService {
	if s == nil {
		return nil
	}
	return s.core
}
func (s *UpstreamUsageService) BindCore(core *acctcore.UpstreamUsageService) { s.core = core }

// Execution 返回同一供应商 Adapter，生产组合根直接绑定到账号核心。
func (s *UpstreamUsageService) Execution() *accountprovider.UpstreamUsageExecution {
	return s.execution
}
