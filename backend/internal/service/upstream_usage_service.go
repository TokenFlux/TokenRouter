package service

import (
	context "context"
	json "encoding/json"
	errors "errors"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	tlsfingerprint "github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	urlvalidator "github.com/TokenFlux/TokenRouter/internal/util/urlvalidator"
	io "io"
	math "math"
	http "net/http"
	url "net/url"
	strconv "strconv"
	strings "strings"
	sync "sync"
	time "time"
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

// UpstreamUsageAdapter 描述一个完整的上游请求与响应协议。
// 适配器拥有请求格式和解析规则，管理员配置只选择适配器和查询根地址。
type UpstreamUsageAdapter interface {
	Name() string
	Query(ctx context.Context, client *upstreamUsageHTTPClient) (*UpstreamUsageInfo, error)
}

// UpstreamUsageAdapterOption 兼容旧消费者，值类型归账号模块。
type UpstreamUsageAdapterOption = acctcore.UpstreamUsageAdapterOption

type upstreamUsageAdapterRegistration struct {
	Name      string
	Label     string
	Automatic bool
	Factory   func() UpstreamUsageAdapter
}

// 只绑定具体实现；目录、顺序与显示名称由 account 唯一提供。
var upstreamUsageAdapterRegistry = func() []upstreamUsageAdapterRegistration {
	factories := map[string]func() UpstreamUsageAdapter{
		UpstreamUsageAdapterSub2API:         func() UpstreamUsageAdapter { return &sub2APIUsageAdapter{} },
		UpstreamUsageAdapterNewAPI:          func() UpstreamUsageAdapter { return &newAPIUsageAdapter{} },
		UpstreamUsageAdapterZivv:            func() UpstreamUsageAdapter { return &zivvUsageAdapter{} },
		UpstreamUsageAdapterKimiCoding:      func() UpstreamUsageAdapter { return &kimiCodingUsageAdapter{} },
		UpstreamUsageAdapterZhipuCoding:     func() UpstreamUsageAdapter { return &zhipuCodingUsageAdapter{} },
		UpstreamUsageAdapterKimiBalance:     func() UpstreamUsageAdapter { return &kimiBalanceUsageAdapter{} },
		UpstreamUsageAdapterDeepseekBalance: func() UpstreamUsageAdapter { return &deepseekBalanceUsageAdapter{} },
	}
	specs := acctcore.UpstreamUsageAdapterCatalog()
	out := make([]upstreamUsageAdapterRegistration, 0, len(specs))
	for _, spec := range specs {
		out = append(out, upstreamUsageAdapterRegistration{Name: spec.Name, Label: spec.Label, Automatic: spec.Automatic, Factory: factories[spec.Name]})
	}
	return out
}()

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
	adapters            map[string]UpstreamUsageAdapter
	adapterMu           sync.RWMutex
}

// NewUpstreamUsageService 创建上游用量查询服务。
func NewUpstreamUsageService(repo AccountRepository, upstream HTTPUpstream, cfg *config.Config, tls *TLSFingerprintProfileService) *UpstreamUsageService {
	s := NewUpstreamUsageExecution(repo, upstream, cfg, tls)
	var reader acctcore.UpstreamUsageReader
	if repo != nil {
		reader = legacyUsageReader{repo}
	}
	s.core = acctcore.NewUpstreamUsageService(reader, s, acctcore.UpstreamUsageOptions{Now: time.Now})
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
		adapters:            make(map[string]UpstreamUsageAdapter),
	}
	for _, registration := range upstreamUsageAdapterRegistry {
		service.RegisterAdapter(registration.Factory())
	}
	return service
}

// RegisterAdapter 注册一个内置适配器，重复名称会覆盖旧实现以便测试替换。
func (s *UpstreamUsageService) RegisterAdapter(adapter UpstreamUsageAdapter) {
	if s == nil || adapter == nil || strings.TrimSpace(adapter.Name()) == "" {
		return
	}
	s.adapterMu.Lock()
	defer s.adapterMu.Unlock()
	if s.adapters == nil {
		s.adapters = make(map[string]UpstreamUsageAdapter)
	}
	s.adapters[strings.TrimSpace(adapter.Name())] = adapter
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

func (s *UpstreamUsageService) adapter(name string) UpstreamUsageAdapter {
	s.adapterMu.RLock()
	defer s.adapterMu.RUnlock()
	return s.adapters[name]
}

type upstreamUsageHTTPClient struct {
	account    *Account
	upstream   HTTPUpstream
	baseURL    string
	apiKey     string
	proxyURL   string
	tlsProfile *tlsfingerprint.Profile
}

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
	return &upstreamUsageHTTPClient{
		account: account, upstream: s.httpUpstream, baseURL: validated,
		apiKey: apiKey, proxyURL: proxyURL, tlsProfile: profile,
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

func (c *upstreamUsageHTTPClient) get(ctx context.Context, path string, authenticated bool) ([]byte, int, error) {
	endpoint, err := upstreamUsageEndpoint(c.baseURL, path)
	if err != nil {
		return nil, 0, ErrUpstreamUsageConfigInvalid.WithCause(err)
	}
	token := ""
	if authenticated {
		token = c.apiKey
	}
	return c.getURLWithBearer(ctx, endpoint, token, "")
}

func upstreamUsageEndpoint(base, path string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid base URL")
	}
	// 账号 Base URL 沿用 OpenAI 兼容端点约定：/v1、/v4、/v1beta
	// 等版本段作为根路径，不能再重复拼接一个 /v1。复用现有端点
	// 构造器，确保用量查询和转发对同一类 Base URL 的解释一致。
	endpoint := buildOpenAIEndpointURL(base, path)
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
		return "", errors.New("invalid endpoint URL")
	}
	// 调用方已经校验过 Base URL；这里再次清理非路径部分，避免未来新增
	// 调用路径时把历史查询串、片段或用户信息带到上游请求。
	parsedEndpoint.User = nil
	parsedEndpoint.RawQuery = ""
	parsedEndpoint.ForceQuery = false
	parsedEndpoint.Fragment = ""
	return strings.TrimRight(parsedEndpoint.String(), "/"), nil
}

func upstreamUsageStatusEndpoint(base string) (string, error) {
	return upstreamUsageRootEndpoint(base, "/api/status")
}

// upstreamUsageTokenEndpoint 构造 New API Token 专用端点并保留尾斜杠，
// 避免部分实例把无尾斜杠请求重定向后被客户端的禁止重定向策略拦截。
func upstreamUsageTokenEndpoint(base string) (string, error) {
	endpoint, err := upstreamUsageRootEndpoint(base, "/api/usage/token")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(endpoint, "/") + "/", nil
}

// upstreamUsageWalletEndpoint 构造兼容 New API 分支的钱包余额端点。
// 该端点固定为 /user/balance，不允许管理员从配置中注入路径。
func upstreamUsageWalletEndpoint(base string) (string, error) {
	return upstreamUsageRootEndpoint(base, "/user/balance")
}

// upstreamUsageUserSelfEndpoint 构造官方 New API 用户自查询端点。
// 该端点需要用户级 Access Token，而不是 relay API Key。
func upstreamUsageUserSelfEndpoint(base string) (string, error) {
	return upstreamUsageRootEndpoint(base, "/api/user/self")
}

// upstreamUsageRootEndpoint 从账号 Base URL 去掉末尾的 OpenAI 版本段，
// 用于 New API 这类挂在站点根路径下的管理接口。
func upstreamUsageRootEndpoint(base, path string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid base URL")
	}
	rootPath := strings.TrimRight(parsed.Path, "/")
	if openAIBaseURLHasVersionSuffix(rootPath) {
		if index := strings.LastIndex(rootPath, "/"); index >= 0 {
			rootPath = rootPath[:index]
		} else {
			rootPath = ""
		}
	}
	parsed.Path = strings.TrimRight(rootPath, "/") + "/" + strings.TrimLeft(path, "/")
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return strings.TrimRight(parsed.String(), "/"), nil
}

func upstreamUsageHTTPError(status int, unsupported bool) error {
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return nil
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUpstreamUsageAuthFailed
	case http.StatusTooManyRequests:
		return ErrUpstreamUsageRateLimited
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		if unsupported {
			return ErrUpstreamUsageUnsupported
		}
	}
	return ErrUpstreamUsageInvalidResponse
}

func upstreamUsageOperationError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrUpstreamUsageTimeout
	}
	return ErrUpstreamUsageRequestFailed
}

func validateNormalizedUsage(usage *UpstreamUsageInfo) error {
	return acctcore.ValidateNormalizedUsage(usage)
}

func validateUsageAmount(amount *UpstreamUsageAmount) error {
	return acctcore.ValidateUsageAmount(amount)
}

func validNonNegativeNumber(value float64) bool { return acctcore.ValidNonNegativeNumber(value) }

func validPositiveNumber(value float64) bool { return acctcore.ValidPositiveNumber(value) }

func validFiniteNumber(value float64) bool { return acctcore.ValidFiniteNumber(value) }

// --- Sub2API 适配器 ---

type sub2APIUsageAdapter struct{}

func (*sub2APIUsageAdapter) Name() string { return UpstreamUsageAdapterSub2API }

type sub2APIUsageResponse struct {
	Mode         string               `json:"mode"`
	IsValid      *bool                `json:"isValid"`
	Status       string               `json:"status"`
	PlanName     string               `json:"planName"`
	Unit         string               `json:"unit"`
	Remaining    *float64             `json:"remaining"`
	Balance      *float64             `json:"balance"`
	Quota        *sub2APIQuota        `json:"quota"`
	RateLimits   []sub2APIRateLimit   `json:"rate_limits"`
	Subscription *sub2APISubscription `json:"subscription"`
	ExpiresAt    *time.Time           `json:"expires_at"`
}

type sub2APIQuota struct {
	Limit     *float64 `json:"limit"`
	Used      *float64 `json:"used"`
	Remaining *float64 `json:"remaining"`
	Unit      string   `json:"unit"`
}

type sub2APIRateLimit struct {
	Window      string          `json:"window"`
	Limit       *float64        `json:"limit"`
	Used        *float64        `json:"used"`
	Remaining   *float64        `json:"remaining"`
	WindowStart json.RawMessage `json:"window_start"`
	ResetAt     *time.Time      `json:"reset_at"`
}

type sub2APISubscription struct {
	DailyUsageUSD      *float64   `json:"daily_usage_usd"`
	WeeklyUsageUSD     *float64   `json:"weekly_usage_usd"`
	MonthlyUsageUSD    *float64   `json:"monthly_usage_usd"`
	DailyLimitUSD      *float64   `json:"daily_limit_usd"`
	WeeklyLimitUSD     *float64   `json:"weekly_limit_usd"`
	MonthlyLimitUSD    *float64   `json:"monthly_limit_usd"`
	DailyResetAt       *time.Time `json:"daily_reset_at"`
	WeeklyResetAt      *time.Time `json:"weekly_reset_at"`
	MonthlyResetAt     *time.Time `json:"monthly_reset_at"`
	DailyWindowStart   *time.Time `json:"daily_window_start"`
	WeeklyWindowStart  *time.Time `json:"weekly_window_start"`
	MonthlyWindowStart *time.Time `json:"monthly_window_start"`
	Unlimited          *bool      `json:"unlimited"`
	ExpiresAt          *time.Time `json:"expires_at"`
}

func (a *sub2APIUsageAdapter) Query(ctx context.Context, client *upstreamUsageHTTPClient) (*UpstreamUsageInfo, error) {
	body, status, err := client.get(ctx, "/v1/usage", true)
	if err != nil {
		return nil, err
	}
	if httpErr := upstreamUsageHTTPError(status, true); httpErr != nil {
		return nil, httpErr
	}
	return parseSub2APIUsage(body)
}

func parseSub2APIUsage(body []byte) (*UpstreamUsageInfo, error) {
	var response sub2APIUsageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	if response.IsValid == nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	if !*response.IsValid {
		return nil, ErrUpstreamUsageAuthFailed
	}
	switch response.Mode {
	case "quota_limited":
		return normalizeSub2APIQuotaLimited(&response)
	case "unrestricted":
		return normalizeSub2APIUnrestricted(&response)
	default:
		return nil, ErrUpstreamUsageInvalidResponse
	}
}

func normalizeSub2APIQuotaLimited(response *sub2APIUsageResponse) (*UpstreamUsageInfo, error) {
	if response.Status != "active" && response.Status != "quota_exhausted" && response.Status != "expired" {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	limits, err := normalizeSub2APIRateLimits(response.RateLimits)
	if err != nil {
		return nil, err
	}
	subscription, err := normalizeSub2APISubscription(response.PlanName, response.Subscription)
	if err != nil {
		return nil, err
	}
	if response.Quota == nil {
		if len(limits) == 0 || response.Remaining != nil || strings.TrimSpace(response.Unit) != "" {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		expiresAt, err := normalizeTime(response.ExpiresAt)
		if err != nil {
			return nil, err
		}
		unit := ""
		if subscription != nil {
			unit = "USD"
		}
		return &UpstreamUsageInfo{Provider: UpstreamUsageAdapterSub2API, Mode: "limits", Unit: unit, Limits: limits, Subscription: subscription, ExpiresAt: expiresAt}, nil
	}
	quota := response.Quota
	if quota.Limit == nil || quota.Used == nil || quota.Remaining == nil || response.Remaining == nil ||
		quota.Unit != "USD" || response.Unit != quota.Unit || *quota.Limit <= 0 ||
		!validNonNegativeNumber(*quota.Limit) || !validNonNegativeNumber(*quota.Used) || !validNonNegativeNumber(*quota.Remaining) ||
		!validNonNegativeNumber(*response.Remaining) || !closeEnough(*quota.Remaining, math.Max(0, *quota.Limit-*quota.Used)) ||
		!closeEnough(*quota.Remaining, *response.Remaining) {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	expiresAt, err := normalizeTime(response.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &UpstreamUsageInfo{
		Provider: UpstreamUsageAdapterSub2API,
		Mode:     "quota",
		Unit:     quota.Unit,
		Balance:  &UpstreamUsageAmount{Used: quota.Used, Total: quota.Limit, Remaining: quota.Remaining},
		Limits:   limits, Subscription: subscription, ExpiresAt: expiresAt,
	}, nil
}

func normalizeSub2APIUnrestricted(response *sub2APIUsageResponse) (*UpstreamUsageInfo, error) {
	if response.Unit != "USD" || strings.TrimSpace(response.PlanName) == "" || response.Remaining == nil || !validFiniteNumber(*response.Remaining) {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	expiresAt, err := normalizeTime(response.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if (response.Subscription == nil) == (response.Balance == nil) {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	if response.Balance != nil {
		if !validFiniteNumber(*response.Balance) || !closeEnough(*response.Balance, *response.Remaining) {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		return &UpstreamUsageInfo{
			Provider:  UpstreamUsageAdapterSub2API,
			Mode:      "balance",
			Unit:      response.Unit,
			Balance:   &UpstreamUsageAmount{Remaining: response.Balance},
			ExpiresAt: expiresAt,
		}, nil
	}
	subscription, err := normalizeSub2APISubscription(response.PlanName, response.Subscription, response.Remaining)
	if err != nil || subscription == nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	if subscription.Unlimited {
		if *response.Remaining != -1 {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		return &UpstreamUsageInfo{Provider: UpstreamUsageAdapterSub2API, Mode: "subscription", Unit: response.Unit, Subscription: subscription, ExpiresAt: expiresAt}, nil
	}
	if !validNonNegativeNumber(*response.Remaining) || subscription.Remaining == nil || !closeEnough(*response.Remaining, *subscription.Remaining) {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	return &UpstreamUsageInfo{Provider: UpstreamUsageAdapterSub2API, Mode: "subscription", Unit: response.Unit, Subscription: subscription, ExpiresAt: expiresAt}, nil
}

func normalizeSub2APISubscription(planName string, raw *sub2APISubscription, legacyRemaining ...*float64) (*UpstreamUsageSubscription, error) {
	if raw == nil {
		return nil, nil
	}
	if strings.TrimSpace(planName) == "" || raw.DailyUsageUSD == nil || raw.WeeklyUsageUSD == nil || raw.MonthlyUsageUSD == nil ||
		!validNonNegativeNumber(*raw.DailyUsageUSD) || !validNonNegativeNumber(*raw.WeeklyUsageUSD) || !validNonNegativeNumber(*raw.MonthlyUsageUSD) {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	expiresAt, err := normalizeTime(raw.ExpiresAt)
	if err != nil || expiresAt == nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	var remainingSentinel *float64
	if len(legacyRemaining) > 0 {
		remainingSentinel = legacyRemaining[0]
	}
	unlimited := raw.Unlimited != nil && *raw.Unlimited
	if raw.Unlimited == nil && remainingSentinel != nil && *remainingSentinel == -1 {
		unlimited = true
	}
	limits, err := normalizeSub2APISubscriptionLimits(raw)
	if err != nil {
		return nil, err
	}
	if unlimited {
		if len(limits) != 0 {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		return &UpstreamUsageSubscription{PlanName: strings.TrimSpace(planName), Unlimited: true, ExpiresAt: expiresAt}, nil
	}
	if len(limits) == 0 {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	minimum := *limits[0].Remaining
	for _, limit := range limits[1:] {
		if limit.Remaining != nil && *limit.Remaining < minimum {
			minimum = *limit.Remaining
		}
	}
	return &UpstreamUsageSubscription{PlanName: strings.TrimSpace(planName), Remaining: &minimum, ExpiresAt: expiresAt, Limits: limits}, nil
}

func normalizeSub2APISubscriptionLimits(raw *sub2APISubscription) ([]UpstreamUsageLimit, error) {
	type subscriptionInput struct {
		name     string
		used     *float64
		limit    *float64
		resetAt  *time.Time
		start    *time.Time
		duration time.Duration
	}
	inputs := []subscriptionInput{
		{name: "daily", used: raw.DailyUsageUSD, limit: raw.DailyLimitUSD, resetAt: raw.DailyResetAt, start: raw.DailyWindowStart, duration: 24 * time.Hour},
		{name: "weekly", used: raw.WeeklyUsageUSD, limit: raw.WeeklyLimitUSD, resetAt: raw.WeeklyResetAt, start: raw.WeeklyWindowStart, duration: 7 * 24 * time.Hour},
		{name: "monthly", used: raw.MonthlyUsageUSD, limit: raw.MonthlyLimitUSD, resetAt: raw.MonthlyResetAt, start: raw.MonthlyWindowStart, duration: 30 * 24 * time.Hour},
	}
	limits := make([]UpstreamUsageLimit, 0, len(inputs))
	for _, input := range inputs {
		if input.limit == nil || *input.limit == 0 {
			continue
		}
		if input.used == nil || *input.limit < 0 || !validNonNegativeNumber(*input.limit) || !validNonNegativeNumber(*input.used) {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		remaining := math.Max(0, *input.limit-*input.used)
		resetAt, err := normalizeTime(input.resetAt)
		if err != nil {
			return nil, err
		}
		if resetAt == nil && input.start != nil {
			start, startErr := normalizeTime(input.start)
			if startErr != nil {
				return nil, startErr
			}
			if start != nil {
				value := start.Add(input.duration)
				resetAt = &value
			}
		}
		limits = append(limits, UpstreamUsageLimit{Name: input.name, Used: input.used, Limit: input.limit, Remaining: &remaining, ResetAt: resetAt})
	}
	return limits, nil
}

func normalizeSub2APIRateLimits(raw []sub2APIRateLimit) ([]UpstreamUsageLimit, error) {
	limits := make([]UpstreamUsageLimit, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		name := strings.TrimSpace(item.Window)
		if name == "" || (name != "5h" && name != "1d" && name != "7d") {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		if _, exists := seen[name]; exists {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		seen[name] = struct{}{}
		if item.Limit == nil || item.Used == nil || item.Remaining == nil || *item.Limit <= 0 || !validNonNegativeNumber(*item.Limit) ||
			!validNonNegativeNumber(*item.Used) || !validNonNegativeNumber(*item.Remaining) ||
			!closeEnough(*item.Remaining, math.Max(0, *item.Limit-*item.Used)) {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		if err := validateSub2APIWindowStart(item.WindowStart); err != nil {
			return nil, err
		}
		resetAt, err := normalizeTime(item.ResetAt)
		if err != nil {
			return nil, err
		}
		limits = append(limits, UpstreamUsageLimit{Name: name, Used: item.Used, Limit: item.Limit, Remaining: item.Remaining, ResetAt: resetAt})
	}
	return limits, nil
}

func validateSub2APIWindowStart(raw json.RawMessage) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		// 当前 /v1/usage 合约要求字段存在；只有明确的 JSON null 才表示
		// 该窗口没有可用的起始时间。
		return ErrUpstreamUsageInvalidResponse
	}
	if trimmed == "null" {
		return nil
	}
	var value time.Time
	if err := json.Unmarshal(raw, &value); err != nil || value.IsZero() {
		return ErrUpstreamUsageInvalidResponse
	}
	return nil
}

// --- Zivv 适配器 ---

// zivvUsageAdapter 对接 Zivv 自研网关公开给 API Key 的余额接口。
// Zivv 的 Anthropic Base URL 通常是站点根地址，因此显式请求带版本段的
// /v1/user/balance；已有的 URL 构造器会避免账号 Base URL 已带 /v1 时重复拼接。
type zivvUsageAdapter struct{}

func (*zivvUsageAdapter) Name() string { return UpstreamUsageAdapterZivv }

type zivvUsageResponse struct {
	Balance     *float64 `json:"balance"`
	Currency    string   `json:"currency"`
	IsAvailable *bool    `json:"is_available"`
	KeyLimit    *float64 `json:"key_limit"`
	KeyUsed     *float64 `json:"key_used"`
	PlanName    string   `json:"plan_name"`
	TotalUsed   *float64 `json:"total_used"`
}

func (a *zivvUsageAdapter) Query(ctx context.Context, client *upstreamUsageHTTPClient) (*UpstreamUsageInfo, error) {
	body, status, err := client.get(ctx, "/v1/user/balance", true)
	if err != nil {
		return nil, err
	}
	if httpErr := upstreamUsageHTTPError(status, true); httpErr != nil {
		return nil, httpErr
	}
	return parseZivvUsage(body)
}

func parseZivvUsage(body []byte) (*UpstreamUsageInfo, error) {
	var response zivvUsageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	if response.IsAvailable == nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	if !*response.IsAvailable {
		return nil, ErrUpstreamUsageAuthFailed
	}
	if response.Balance == nil || response.TotalUsed == nil || response.KeyLimit == nil || response.KeyUsed == nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	if !validFiniteNumber(*response.Balance) || !validNonNegativeNumber(*response.TotalUsed) ||
		!validNonNegativeNumber(*response.KeyLimit) || !validNonNegativeNumber(*response.KeyUsed) {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	unit := strings.ToUpper(strings.TrimSpace(response.Currency))
	if unit != "USD" && unit != "CNY" && unit != "TOKENS" {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	planName := strings.TrimSpace(response.PlanName)
	if planName == "" {
		planName = "Zivv"
	}
	total := *response.Balance + *response.TotalUsed
	if !validNonNegativeNumber(total) {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	usage := &UpstreamUsageInfo{
		Provider: UpstreamUsageAdapterZivv,
		Mode:     "balance",
		Unit:     unit,
		Balance:  &UpstreamUsageAmount{Used: response.TotalUsed, Total: &total, Remaining: response.Balance},
	}
	if *response.KeyLimit <= 0 {
		// Zivv 以 0 表示不设置 Key 累计限额；不要把它传成数值哨兵。
		usage.Subscription = &UpstreamUsageSubscription{PlanName: planName, Unlimited: true}
		return usage, nil
	}
	keyRemaining := math.Max(0, *response.KeyLimit-*response.KeyUsed)
	usage.Limits = []UpstreamUsageLimit{{
		Name: "key_quota", Used: response.KeyUsed, Limit: response.KeyLimit, Remaining: &keyRemaining,
	}}
	usage.Subscription = &UpstreamUsageSubscription{PlanName: planName, Remaining: &keyRemaining}
	return usage, nil
}

// --- New API 适配器 ---

type newAPIUsageAdapter struct{}

func (*newAPIUsageAdapter) Name() string { return UpstreamUsageAdapterNewAPI }

type newAPITokenUsageResponse struct {
	Code    *bool  `json:"code"`
	Success *bool  `json:"success"`
	Message string `json:"message"`
	Data    *struct {
		Object             string          `json:"object"`
		Name               string          `json:"name"`
		TotalGranted       *float64        `json:"total_granted"`
		TotalUsed          *float64        `json:"total_used"`
		TotalAvailable     *float64        `json:"total_available"`
		UnlimitedQuota     *bool           `json:"unlimited_quota"`
		ExpiresAt          *int64          `json:"expires_at"`
		UserBalance        json.RawMessage `json:"user_balance"`
		UserBalanceDisplay json.RawMessage `json:"user_balance_display"`
		Currency           string          `json:"currency"`
	} `json:"data"`
}

type newAPIWalletBalanceInfo struct {
	Currency     string          `json:"currency"`
	TotalBalance json.RawMessage `json:"total_balance"`
	Balance      json.RawMessage `json:"balance"`
	Remaining    json.RawMessage `json:"remaining"`
	Used         json.RawMessage `json:"used"`
	UsedBalance  json.RawMessage `json:"used_balance"`
	Total        json.RawMessage `json:"total"`
}

type newAPIWalletData struct {
	ID           json.RawMessage           `json:"id"`
	Quota        json.RawMessage           `json:"quota"`
	UsedQuota    json.RawMessage           `json:"used_quota"`
	Balance      json.RawMessage           `json:"balance"`
	Remaining    json.RawMessage           `json:"remaining"`
	TotalBalance json.RawMessage           `json:"total_balance"`
	Used         json.RawMessage           `json:"used"`
	Total        json.RawMessage           `json:"total"`
	Currency     string                    `json:"currency"`
	BalanceInfos []newAPIWalletBalanceInfo `json:"balance_infos"`
}

type newAPIWalletBalanceResponse struct {
	Code         *bool                     `json:"code"`
	Success      *bool                     `json:"success"`
	Message      string                    `json:"message"`
	BalanceInfos []newAPIWalletBalanceInfo `json:"balance_infos"`
	Currency     string                    `json:"currency"`
	Balance      json.RawMessage           `json:"balance"`
	Remaining    json.RawMessage           `json:"remaining"`
	TotalBalance json.RawMessage           `json:"total_balance"`
	Used         json.RawMessage           `json:"used"`
	UsedBalance  json.RawMessage           `json:"used_balance"`
	Total        json.RawMessage           `json:"total"`
	Data         *newAPIWalletData         `json:"data"`
}

type newAPIUsageDisplaySettings struct {
	Unit            string
	QuotaPerUnit    float64
	USDExchangeRate float64
}

const newAPIDefaultQuotaPerUnit = 500000.0

func (a *newAPIUsageAdapter) Query(ctx context.Context, client *upstreamUsageHTTPClient) (*UpstreamUsageInfo, error) {
	displayCtx, cancelDisplay := context.WithTimeout(ctx, upstreamUsageStatusTimeout)
	display := a.queryDisplaySettings(displayCtx, client)
	cancelDisplay()
	// 状态接口只是可选的单位探测；真正的 Token 额度请求使用整次查询的
	// 总截止时间，避免慢实例在短探测超时内被误判为失败。
	tokenUsage, tokenResponse, tokenErr := a.queryTokenUsage(ctx, client, display)
	if tokenErr != nil {
		return nil, tokenErr
	}
	wallet, walletErr := a.queryWallet(ctx, client, display, tokenResponse)
	if walletErr != nil {
		return nil, walletErr
	}
	if tokenUsage == nil || wallet == nil || wallet.Balance == nil {
		return nil, ErrUpstreamUsageWalletUnavailable
	}
	if tokenUsage.Unit != "" && wallet.Unit != "" && tokenUsage.Unit != wallet.Unit {
		// 一个结果只有一个 unit；不同货币没有可靠汇率时不能把 Key quota
		// 贴上钱包货币标签，否则会产生比缺失结果更危险的误导。
		return nil, ErrUpstreamUsageInvalidResponse
	}
	// New API 的 balance 字段只表示用户钱包；当前 API Key 的 quota
	// 保留在 limits/subscription 中，避免把 token 限额显示成钱包余额。
	tokenUsage.Mode = "balance"
	tokenUsage.Unit = wallet.Unit
	tokenUsage.Balance = wallet.Balance
	return tokenUsage, nil
}

func (a *newAPIUsageAdapter) queryDisplaySettings(ctx context.Context, client *upstreamUsageHTTPClient) newAPIUsageDisplaySettings {
	settings := newAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: newAPIDefaultQuotaPerUnit, USDExchangeRate: 1}
	endpoint, err := upstreamUsageStatusEndpoint(client.baseURL)
	if err != nil {
		return settings
	}
	body, status, err := client.getURL(ctx, endpoint, false)
	if err != nil || status < http.StatusOK || status >= http.StatusMultipleChoices {
		return settings
	}
	var response struct {
		Success *bool `json:"success"`
		Data    *struct {
			QuotaDisplayType *string  `json:"quota_display_type"`
			QuotaPerUnit     *float64 `json:"quota_per_unit"`
			USDExchangeRate  *float64 `json:"usd_exchange_rate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Success == nil || !*response.Success || response.Data == nil || response.Data.QuotaDisplayType == nil {
		return settings
	}
	switch strings.ToUpper(strings.TrimSpace(*response.Data.QuotaDisplayType)) {
	case "USD", "CNY", "TOKENS":
		settings.Unit = strings.ToUpper(strings.TrimSpace(*response.Data.QuotaDisplayType))
	}
	if response.Data.QuotaPerUnit != nil && validPositiveNumber(*response.Data.QuotaPerUnit) {
		settings.QuotaPerUnit = *response.Data.QuotaPerUnit
	}
	if response.Data.USDExchangeRate != nil && validPositiveNumber(*response.Data.USDExchangeRate) {
		settings.USDExchangeRate = *response.Data.USDExchangeRate
	}
	return settings
}

func (a *newAPIUsageAdapter) queryTokenUsage(ctx context.Context, client *upstreamUsageHTTPClient, settings newAPIUsageDisplaySettings) (*UpstreamUsageInfo, *newAPITokenUsageResponse, error) {
	endpoint, err := upstreamUsageTokenEndpoint(client.baseURL)
	if err != nil {
		return nil, nil, err
	}
	body, status, err := client.getURL(ctx, endpoint, true)
	if err != nil {
		return nil, nil, err
	}
	if httpErr := upstreamUsageHTTPError(status, true); httpErr != nil {
		return nil, nil, httpErr
	}
	response, err := parseNewAPITokenUsage(body)
	if err != nil {
		return nil, nil, err
	}
	usage, err := normalizeNewAPITokenUsage(response, settings)
	if err != nil {
		return nil, nil, err
	}
	return usage, response, nil
}

// queryWallet 查询用户钱包。不同 New API 分支的认证方式并不一致：
// 配置用户 PAT 时先请求官方 /api/user/self，否则尝试带 API Key 的 /user/balance。
// 两个路径都是适配器固定协议，不能由账号配置改写。
func (a *newAPIUsageAdapter) queryWallet(
	ctx context.Context,
	client *upstreamUsageHTTPClient,
	settings newAPIUsageDisplaySettings,
	tokenResponse *newAPITokenUsageResponse,
) (*UpstreamUsageInfo, error) {
	if embedded, present, err := normalizeNewAPITokenWallet(tokenResponse, settings); present {
		if err != nil {
			return nil, err
		}
		return embedded, nil
	}

	userToken := strings.TrimSpace(client.account.GetCredential(NewAPIUserAccessTokenCredentialKey))
	userID, err := configuredNewAPIUserID(client.account)
	if err != nil {
		return nil, err
	}
	var lastRequestErr error
	// 官方实例的用户自查询需要 PAT；如果已配置，优先使用它，避免
	// 先访问一个会返回前端 HTML 的兼容路径。
	if userToken != "" {
		endpoint, endpointErr := upstreamUsageUserSelfEndpoint(client.baseURL)
		if endpointErr != nil {
			return nil, endpointErr
		}
		body, status, requestErr := client.getURLWithBearer(ctx, endpoint, userToken, userID)
		if requestErr == nil {
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				return nil, ErrUpstreamUsageWalletAuthFailed
			}
			if status == http.StatusTooManyRequests {
				return nil, ErrUpstreamUsageRateLimited
			}
			if status >= http.StatusOK && status < http.StatusMultipleChoices {
				wallet, parseErr := parseNewAPIUserSelfWallet(body, settings, userID)
				if parseErr == nil {
					return wallet, nil
				}
				if errors.Is(parseErr, ErrUpstreamUsageAuthFailed) || errors.Is(parseErr, ErrUpstreamUsageWalletAuthFailed) {
					return nil, ErrUpstreamUsageWalletAuthFailed
				}
			}
		} else if errors.Is(requestErr, ErrUpstreamUsageTimeout) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, requestErr
		} else if requestErr != nil {
			lastRequestErr = requestErr
		}
	}

	// 部分 fork 允许 relay API Key 直接访问 /user/balance；官方 New API
	// 没有该路由时通常返回 404 或 SPA HTML，此时返回明确的钱包不可用错误。
	endpoint, err := upstreamUsageWalletEndpoint(client.baseURL)
	if err != nil {
		return nil, err
	}
	body, status, requestErr := client.getURLWithBearer(ctx, endpoint, client.apiKey, "")
	if requestErr != nil {
		if errors.Is(requestErr, ErrUpstreamUsageTimeout) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, requestErr
		}
		if lastRequestErr != nil {
			return nil, lastRequestErr
		}
		return nil, ErrUpstreamUsageWalletUnavailable
	}
	if status == http.StatusTooManyRequests {
		return nil, ErrUpstreamUsageRateLimited
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		wallet, parseErr := parseNewAPIWalletBalance(body, settings)
		if parseErr == nil {
			return wallet, nil
		}
	}
	return nil, ErrUpstreamUsageWalletUnavailable
}

// normalizeNewAPITokenWallet 兼容部分 fork 直接在 token 响应中附带钱包余额。
// display 字段已经是站点展示单位，原始 user_balance 仍按 /api/status 换算。
func normalizeNewAPITokenWallet(response *newAPITokenUsageResponse, settings newAPIUsageDisplaySettings) (*UpstreamUsageInfo, bool, error) {
	if response == nil || response.Data == nil {
		return nil, false, nil
	}
	data := response.Data
	if raw := nonEmptyJSON(data.UserBalanceDisplay); raw != nil {
		value, err := parseNewAPINumber(raw)
		if err != nil || value == nil {
			return nil, true, ErrUpstreamUsageInvalidResponse
		}
		unit, _ := normalizeNewAPIWalletUnit(data.Currency, settings.Unit)
		usage, usageErr := newAPIWalletAmountUsage(unit, nil, nil, value)
		return usage, true, usageErr
	}
	if raw := nonEmptyJSON(data.UserBalance); raw != nil {
		value, err := parseNewAPINumber(raw)
		if err != nil || value == nil || !validFiniteNumber(*value) {
			return nil, true, ErrUpstreamUsageInvalidResponse
		}
		remaining := normalizeNewAPIQuota(*value, settings)
		usage, usageErr := newAPIWalletAmountUsage(settings.Unit, nil, nil, &remaining)
		return usage, true, usageErr
	}
	return nil, false, nil
}

func parseNewAPIWalletBalance(body []byte, settings newAPIUsageDisplaySettings) (*UpstreamUsageInfo, error) {
	var response newAPIWalletBalanceResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	if response.Code != nil && !*response.Code || response.Success != nil && !*response.Success {
		return nil, ErrUpstreamUsageAuthFailed
	}
	infos := response.BalanceInfos
	if response.Data != nil && len(response.Data.BalanceInfos) > 0 {
		infos = response.Data.BalanceInfos
	}
	for pass := 0; pass < 2; pass++ {
		for _, info := range infos {
			unit, displayValue := normalizeNewAPIWalletUnit(info.Currency, settings.Unit)
			// balance_infos 的 total_balance 是钱包展示值；部分 fork 省略
			// currency，此时不能按内部 quota 再除一次 quota_per_unit。
			if strings.TrimSpace(info.Currency) == "" {
				displayValue = true
			}
			if len(infos) > 1 && pass == 0 && settings.Unit != "" && unit != settings.Unit {
				continue
			}
			remainingRaw := firstNewAPIRaw(info.TotalBalance, info.Balance, info.Remaining)
			remaining, err := parseNewAPINumber(remainingRaw)
			if err != nil || remaining == nil {
				return nil, ErrUpstreamUsageInvalidResponse
			}
			if !displayValue {
				value := normalizeNewAPIQuota(*remaining, settings)
				remaining = &value
			}
			used, err := parseNewAPINumber(firstNewAPIRaw(info.UsedBalance, info.Used))
			if err != nil {
				return nil, ErrUpstreamUsageInvalidResponse
			}
			total, err := parseNewAPINumber(info.Total)
			if err != nil {
				return nil, ErrUpstreamUsageInvalidResponse
			}
			if displayValue {
				if used != nil {
					usedValue := *used
					used = &usedValue
				}
				if total != nil {
					totalValue := *total
					total = &totalValue
				}
			} else {
				if used != nil {
					usedValue := normalizeNewAPIQuota(*used, settings)
					used = &usedValue
				}
				if total != nil {
					totalValue := normalizeNewAPIQuota(*total, settings)
					total = &totalValue
				}
			}
			return newAPIWalletAmountUsage(unit, used, total, remaining)
		}
	}
	if response.Data == nil {
		unit, displayValue := normalizeNewAPIWalletUnit(response.Currency, settings.Unit)
		remainingRaw := firstNewAPIRaw(response.TotalBalance, response.Balance, response.Remaining)
		if strings.TrimSpace(response.Currency) == "" {
			displayValue = true
		}
		remaining, err := parseNewAPINumber(remainingRaw)
		if err != nil || remaining == nil {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		used, err := parseNewAPINumber(firstNewAPIRaw(response.UsedBalance, response.Used))
		if err != nil {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		total, err := parseNewAPINumber(response.Total)
		if err != nil {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		if !displayValue {
			remainingValue := normalizeNewAPIQuota(*remaining, settings)
			remaining = &remainingValue
			if used != nil {
				usedValue := normalizeNewAPIQuota(*used, settings)
				used = &usedValue
			}
			if total != nil {
				totalValue := normalizeNewAPIQuota(*total, settings)
				total = &totalValue
			}
		}
		return newAPIWalletAmountUsage(unit, used, total, remaining)
	}
	data := response.Data
	unit, displayValue := normalizeNewAPIWalletUnit(data.Currency, settings.Unit)
	remainingRaw := firstNewAPIRaw(data.TotalBalance, data.Balance, data.Remaining, data.Quota)
	if strings.TrimSpace(data.Currency) == "" && nonEmptyJSON(data.Quota) == nil {
		displayValue = true
	}
	remaining, err := parseNewAPINumber(remainingRaw)
	if err != nil || remaining == nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	used, err := parseNewAPINumber(firstNewAPIRaw(data.Used, data.UsedQuota))
	if err != nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	total, err := parseNewAPINumber(data.Total)
	if err != nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	if !displayValue {
		remainingValue := normalizeNewAPIQuota(*remaining, settings)
		remaining = &remainingValue
		if used != nil {
			usedValue := normalizeNewAPIQuota(*used, settings)
			used = &usedValue
		}
		if total != nil {
			totalValue := normalizeNewAPIQuota(*total, settings)
			total = &totalValue
		}
	}
	return newAPIWalletAmountUsage(unit, used, total, remaining)
}

func parseNewAPIUserSelfWallet(body []byte, settings newAPIUsageDisplaySettings, expectedUserID string) (*UpstreamUsageInfo, error) {
	var response newAPIWalletBalanceResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	if response.Success != nil && !*response.Success || response.Code != nil && !*response.Code {
		return nil, ErrUpstreamUsageAuthFailed
	}
	if response.Data == nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	if expectedUserID != "" {
		if nonEmptyJSON(response.Data.ID) == nil {
			return nil, ErrUpstreamUsageWalletAuthFailed
		}
		actual, err := parseNewAPIInteger(response.Data.ID)
		if err != nil || actual != expectedUserID {
			return nil, ErrUpstreamUsageWalletAuthFailed
		}
	}
	remaining, err := parseNewAPINumber(response.Data.Quota)
	if err != nil || remaining == nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	unit, displayValue := normalizeNewAPIWalletUnit(response.Data.Currency, settings.Unit)
	if !displayValue {
		remainingValue := normalizeNewAPIQuota(*remaining, settings)
		remaining = &remainingValue
	}
	// used_quota 是账号生命周期累计用量，并非某个钱包周期内的已用金额；
	// 这里仅返回当前可用余额，避免虚构“钱包总额”。
	return newAPIWalletAmountUsage(unit, nil, nil, remaining)
}

func newAPIWalletAmountUsage(unit string, used, total, remaining *float64) (*UpstreamUsageInfo, error) {
	amount := &UpstreamUsageAmount{Used: used, Total: total, Remaining: remaining}
	if err := validateUsageAmount(amount); err != nil {
		return nil, ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	return &UpstreamUsageInfo{Provider: UpstreamUsageAdapterNewAPI, Mode: "balance", Unit: unit, Balance: amount}, nil
}

func configuredNewAPIUserID(account *Account) (string, error) {
	if account == nil {
		return "", nil
	}
	raw := strings.TrimSpace(account.GetCredential(NewAPIUserIDCredentialKey))
	if raw == "" {
		return "", nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return "", ErrUpstreamUsageConfigInvalid
	}
	return strconv.FormatInt(value, 10), nil
}

func parseNewAPINumber(raw json.RawMessage) (*float64, error) {
	raw = nonEmptyJSON(raw)
	if raw == nil {
		return nil, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		if !validFiniteNumber(value) {
			return nil, errors.New("invalid numeric value")
		}
		return &value, nil
	}
	var textValue string
	if err := json.Unmarshal(raw, &textValue); err != nil {
		return nil, err
	}
	textValue = strings.TrimSpace(textValue)
	textValue = strings.ReplaceAll(textValue, ",", "")
	textValue = strings.TrimPrefix(textValue, "$")
	textValue = strings.TrimPrefix(textValue, "¥")
	value, err := strconv.ParseFloat(textValue, 64)
	if err != nil || !validFiniteNumber(value) {
		return nil, errors.New("invalid numeric value")
	}
	return &value, nil
}

func parseNewAPIInteger(raw json.RawMessage) (string, error) {
	value, err := parseNewAPINumber(raw)
	if err != nil || value == nil || *value <= 0 || math.Trunc(*value) != *value {
		return "", errors.New("invalid user id")
	}
	return strconv.FormatInt(int64(*value), 10), nil
}

func nonEmptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	return raw
}

func firstNewAPIRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if raw := nonEmptyJSON(value); raw != nil {
			return raw
		}
	}
	return nil
}

func normalizeNewAPIWalletUnit(raw, fallback string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "USD", "$", "US$":
		return "USD", true
	case "CNY", "RMB", "¥", "￥":
		return "CNY", true
	case "TOKENS", "TOKEN":
		return "TOKENS", true
	}
	if fallback == "" {
		return "USD", false
	}
	return fallback, false
}

func parseNewAPITokenUsage(body []byte) (*newAPITokenUsageResponse, error) {
	var response newAPITokenUsageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, ErrUpstreamUsageInvalidResponse.WithCause(err)
	}
	// New API 的无效 token 在部分版本中会以 200 + success/code=false 返回，
	// 不能把这种明确的身份失败误报成响应格式错误。
	if response.Code != nil && !*response.Code || response.Success != nil && !*response.Success {
		return nil, ErrUpstreamUsageAuthFailed
	}
	if response.Code == nil || !*response.Code || response.Data == nil ||
		response.Data.Object != "token_usage" || response.Data.UnlimitedQuota == nil || response.Data.ExpiresAt == nil ||
		*response.Data.ExpiresAt < -1 {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	if *response.Data.UnlimitedQuota {
		// New API 的无限量 token 在部分版本使用 32 位整数计算，溢出后
		// total_granted/total_available 可能为负数；无限量结果不会使用这些
		// 数值，因此只校验它们若存在必须是有限数，避免把溢出值传到前端。
		for _, value := range []*float64{response.Data.TotalGranted, response.Data.TotalUsed, response.Data.TotalAvailable} {
			if value != nil && !validFiniteNumber(*value) {
				return nil, ErrUpstreamUsageInvalidResponse
			}
		}
		return &response, nil
	}
	if response.Data.TotalGranted == nil || response.Data.TotalUsed == nil || response.Data.TotalAvailable == nil ||
		!validNonNegativeNumber(*response.Data.TotalGranted) || !validNonNegativeNumber(*response.Data.TotalUsed) ||
		!validNonNegativeNumber(*response.Data.TotalAvailable) {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	return &response, nil
}

func normalizeNewAPITokenUsage(response *newAPITokenUsageResponse, settings newAPIUsageDisplaySettings) (*UpstreamUsageInfo, error) {
	if response == nil || response.Data == nil {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	expiresAt, err := normalizeNewAPITime(*response.Data.ExpiresAt)
	if err != nil {
		return nil, err
	}
	planName := strings.TrimSpace(response.Data.Name)
	if planName == "" {
		planName = "New API"
	}
	if *response.Data.UnlimitedQuota {
		return &UpstreamUsageInfo{
			Provider: UpstreamUsageAdapterNewAPI,
			Mode:     "subscription",
			Unit:     settings.Unit,
			Subscription: &UpstreamUsageSubscription{
				PlanName:  planName,
				Unlimited: true,
				ExpiresAt: expiresAt,
			},
			ExpiresAt: expiresAt,
		}, nil
	}
	if !closeEnough(*response.Data.TotalGranted, *response.Data.TotalUsed+*response.Data.TotalAvailable) {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	used := normalizeNewAPIQuota(*response.Data.TotalUsed, settings)
	total := normalizeNewAPIQuota(*response.Data.TotalGranted, settings)
	remaining := normalizeNewAPIQuota(*response.Data.TotalAvailable, settings)
	limit := UpstreamUsageLimit{Name: "token_quota", Used: &used, Limit: &total, Remaining: &remaining}
	return &UpstreamUsageInfo{
		Provider: UpstreamUsageAdapterNewAPI,
		Mode:     "limits",
		Unit:     settings.Unit,
		Limits:   []UpstreamUsageLimit{limit},
		Subscription: &UpstreamUsageSubscription{
			PlanName:  planName,
			Remaining: &remaining,
			ExpiresAt: expiresAt,
		},
		ExpiresAt: expiresAt,
	}, nil
}

func normalizeNewAPIQuota(value float64, settings newAPIUsageDisplaySettings) float64 {
	if settings.Unit == "TOKENS" {
		return value
	}
	divisor := settings.QuotaPerUnit
	if !validPositiveNumber(divisor) {
		divisor = newAPIDefaultQuotaPerUnit
	}
	converted := value / divisor
	if settings.Unit == "CNY" {
		rate := settings.USDExchangeRate
		if !validPositiveNumber(rate) {
			rate = 1
		}
		converted *= rate
	}
	return converted
}

func normalizeNewAPITime(value int64) (*time.Time, error) {
	if value <= 0 {
		return nil, nil
	}
	// 新版接口返回秒；兼容少数部署返回毫秒的实现。
	if value > 253402300799 {
		if value > 253402300799000 {
			return nil, ErrUpstreamUsageInvalidResponse
		}
		value /= 1000
	}
	result := time.Unix(value, 0).UTC()
	if result.IsZero() {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	return &result, nil
}

func (c *upstreamUsageHTTPClient) getURL(ctx context.Context, endpoint string, authenticated bool) ([]byte, int, error) {
	token := ""
	if authenticated {
		token = c.apiKey
	}
	return c.getURLWithBearer(ctx, endpoint, token, "")
}

// getURLWithHeaders 请求内置适配器声明的固定请求头。
// 账号级覆写先应用、再被固定头覆盖，避免探测请求改变认证或团队身份。
func (c *upstreamUsageHTTPClient) getURLWithHeaders(ctx context.Context, endpoint string, fixedHeaders map[string]string) ([]byte, int, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, 0, ErrUpstreamUsageConfigInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, ErrUpstreamUsageRequestFailed
	}
	reqCtx := WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI)
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(reqCtx))
	req.Header.Set("Accept", "application/json")
	c.account.ApplyHeaderOverrides(req.Header)
	// 账号级覆写不得改变内置适配器的认证身份。
	req.Header.Del("Authorization")
	req.Header.Del("api-key")
	for headerName, headerValue := range fixedHeaders {
		name := strings.TrimSpace(headerName)
		value := strings.TrimSpace(headerValue)
		if name == "" || value == "" {
			continue
		}
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return nil, 0, ErrUpstreamUsageConfigInvalid
		}
		// 固定头不允许被账号覆写；先删除所有大小写变体，再写入规范值。
		for existing := range req.Header {
			if strings.EqualFold(existing, name) {
				delete(req.Header, existing)
			}
		}
		req.Header.Set(name, value)
	}
	resp, err := c.upstream.DoWithTLS(req, c.proxyURL, c.account.ID, c.account.Concurrency, c.tlsProfile)
	if err != nil {
		return nil, 0, upstreamUsageOperationError(ctx, err)
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, ErrUpstreamUsageInvalidResponse
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, upstreamUsageMaxBodyBytes+1))
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, 0, upstreamUsageOperationError(ctx, readErr)
	}
	if int64(len(body)) > upstreamUsageMaxBodyBytes {
		return nil, 0, ErrUpstreamUsageInvalidResponse
	}
	return body, resp.StatusCode, nil
}

// getURLWithBearer 使用固定的 Bearer 令牌和可选用户 ID 请求管理端点。
// 用户 ID 只会写入适配器定义的 New-Api-User 头，不接受任意 Header 配置。
func (c *upstreamUsageHTTPClient) getURLWithBearer(ctx context.Context, endpoint, bearerToken, userID string) ([]byte, int, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, 0, ErrUpstreamUsageConfigInvalid
	}
	// 复用统一请求实现，但不再拼接路径。
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, ErrUpstreamUsageRequestFailed
	}
	reqCtx := WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI)
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(reqCtx))
	req.Header.Set("Accept", "application/json")
	c.account.ApplyHeaderOverrides(req.Header)
	// Header Override 不能改变管理查询实际使用的认证身份。
	req.Header.Del("Authorization")
	if strings.TrimSpace(bearerToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearerToken))
	}
	req.Header.Del("New-Api-User")
	if strings.TrimSpace(userID) != "" {
		req.Header.Set("New-Api-User", strings.TrimSpace(userID))
	}
	resp, err := c.upstream.DoWithTLS(req, c.proxyURL, c.account.ID, c.account.Concurrency, c.tlsProfile)
	if err != nil {
		return nil, 0, upstreamUsageOperationError(ctx, err)
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, ErrUpstreamUsageInvalidResponse
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, upstreamUsageMaxBodyBytes+1))
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, 0, upstreamUsageOperationError(ctx, readErr)
	}
	if int64(len(body)) > upstreamUsageMaxBodyBytes {
		return nil, 0, ErrUpstreamUsageInvalidResponse
	}
	return body, resp.StatusCode, nil
}

func normalizeTime(value *time.Time) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	if value.IsZero() {
		return nil, ErrUpstreamUsageInvalidResponse
	}
	normalized := value.UTC()
	return &normalized, nil
}

func closeEnough(left, right float64) bool {
	return math.Abs(left-right) <= math.Max(0.000001, math.Max(math.Abs(left), math.Abs(right))*0.00001)
}

func upstreamUsageContextFingerprint(account *Account, config UpstreamUsageQueryConfig) string {
	return acctcore.UpstreamUsageContextFingerprint(AccountRecordView(account), config, upstreamUsageAccountBaseURL(account))
}

// legacyUsageReader 仅供兼容构造，生产组合根直接绑定唯一账号存储。
type legacyUsageReader struct{ source AccountRepository }

func (r legacyUsageReader) GetByID(ctx context.Context, id int64) (*acctcore.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return AccountRecordView(v), err
}
func (s *UpstreamUsageService) Available() bool {
	return s != nil && s.httpUpstream != nil && s.accountRepo != nil
}
func (s *UpstreamUsageService) Supports(name string) bool { return s.adapter(name) != nil }
func (s *UpstreamUsageService) BaseURL(value *acctcore.Record) string {
	return upstreamUsageAccountBaseURL(AccountFromRecord(value))
}
func (s *UpstreamUsageService) Query(ctx context.Context, value *acctcore.Record, config acctcore.UpstreamUsageQueryConfig) (*acctcore.UpstreamUsageInfo, error) {
	adapter := s.adapter(config.Adapter)
	if adapter == nil {
		return nil, ErrUpstreamUsageUnsupported
	}
	client, err := s.newHTTPClient(AccountFromRecord(value), config)
	if err != nil {
		return nil, err
	}
	return adapter.Query(ctx, client)
}
func (s *UpstreamUsageService) Core() *acctcore.UpstreamUsageService {
	if s == nil {
		return nil
	}
	return s.core
}
func (s *UpstreamUsageService) BindCore(core *acctcore.UpstreamUsageService) { s.core = core }
