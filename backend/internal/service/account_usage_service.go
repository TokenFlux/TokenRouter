package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"log"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	httppool "github.com/TokenFlux/TokenRouter/internal/pkg/httpclient"
	openaipkg "github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
)

type UsageLogRepository interface {
	// Create creates a usage log and returns whether it was actually inserted.
	// inserted is false when the insert was skipped due to conflict (idempotent retries).
	Create(ctx context.Context, log *UsageLog) (inserted bool, err error)
	GetByID(ctx context.Context, id int64) (*UsageLog, error)
	Delete(ctx context.Context, id int64) error

	ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error)
	ListByAPIKey(ctx context.Context, apiKeyID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error)
	ListByAccount(ctx context.Context, accountID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error)

	ListByUserAndTimeRange(ctx context.Context, userID int64, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error)
	ListByAPIKeyAndTimeRange(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error)
	ListByAccountAndTimeRange(ctx context.Context, accountID int64, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error)
	ListByModelAndTimeRange(ctx context.Context, modelName string, startTime, endTime time.Time) ([]UsageLog, *pagination.PaginationResult, error)

	GetAccountWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usagestats.AccountStats, error)
	GetAccountTodayStats(ctx context.Context, accountID int64) (*usagestats.AccountStats, error)

	// Admin dashboard stats
	GetDashboardStats(ctx context.Context) (*usagestats.DashboardStats, error)
	GetUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) ([]usagestats.TrendDataPoint, error)
	GetModelStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) ([]usagestats.ModelStat, error)
	GetEndpointStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) ([]usagestats.EndpointStat, error)
	GetUpstreamEndpointStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) ([]usagestats.EndpointStat, error)
	GetGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) ([]usagestats.GroupStat, error)
	GetUserBreakdownStats(ctx context.Context, startTime, endTime time.Time, dim usagestats.UserBreakdownDimension, limit int) ([]usagestats.UserBreakdownItem, error)
	GetAllGroupUsageSummary(ctx context.Context, todayStart time.Time) ([]usagestats.GroupUsageSummary, error)
	GetAPIKeyUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagestats.APIKeyUsageTrendPoint, error)
	GetUserUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagestats.UserUsageTrendPoint, error)
	GetUserSpendingRanking(ctx context.Context, startTime, endTime time.Time, limit int) (*usagestats.UserSpendingRankingResponse, error)
	GetUsageRanking(ctx context.Context, startTime, endTime time.Time, limit int, sortBy UsageRankingSortBy) (*usagestats.UsageRankingResponse, error)
	GetBatchUserUsageStats(ctx context.Context, userIDs []int64, startTime, endTime time.Time) (map[int64]*usagestats.BatchUserUsageStats, error)
	GetBatchAPIKeyUsageStats(ctx context.Context, apiKeyIDs []int64, startTime, endTime time.Time) (map[int64]*usagestats.BatchAPIKeyUsageStats, error)

	// User dashboard stats
	GetUserDashboardStats(ctx context.Context, userID int64) (*usagestats.UserDashboardStats, error)
	GetAPIKeyDashboardStats(ctx context.Context, apiKeyID int64) (*usagestats.UserDashboardStats, error)
	GetUserUsageTrendByUserID(ctx context.Context, userID int64, startTime, endTime time.Time, granularity string) ([]usagestats.TrendDataPoint, error)
	GetUserModelStats(ctx context.Context, userID int64, startTime, endTime time.Time) ([]usagestats.ModelStat, error)

	// Admin usage listing/stats
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error)
	GetGlobalStats(ctx context.Context, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetStatsWithFilters(ctx context.Context, filters usagestats.UsageLogFilters) (*usagestats.UsageStats, error)

	// Account stats
	GetAccountUsageStats(ctx context.Context, accountID int64, startTime, endTime time.Time) (*usagestats.AccountUsageStatsResponse, error)

	// Aggregated stats (optimized)
	GetUserStatsAggregated(ctx context.Context, userID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetAPIKeyStatsAggregated(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetAccountStatsAggregated(ctx context.Context, accountID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetModelStatsAggregated(ctx context.Context, modelName string, startTime, endTime time.Time) (*usagestats.UsageStats, error)
	GetDailyStatsAggregated(ctx context.Context, userID int64, startTime, endTime time.Time) ([]map[string]any, error)
}

type accountWindowStatsBatchReader interface {
	GetAccountWindowStatsBatch(ctx context.Context, accountIDs []int64, startTime time.Time) (map[int64]*usagestats.AccountStats, error)
}

type antigravityUsageCache = accountcore.OAuthAntigravityUsageCache

type qoderUsageCache = accountcore.OAuthQoderUsageCache

const (
	apiCacheTTL         = accountcore.OAuthUsageAPICacheTTL
	apiErrorCacheTTL    = accountcore.OAuthUsageAPIErrorCacheTTL
	antigravityErrorTTL = accountcore.OAuthUsageAntigravityErrorTTL
	apiQueryMaxJitter   = accountcore.OAuthUsageAPIQueryMaxJitter
	windowStatsCacheTTL = accountcore.OAuthUsageWindowStatsCacheTTL
	openAIProbeCacheTTL = accountcore.OAuthUsageOpenAIProbeCacheTTL
	grokProbeRetryTTL   = accountcore.OAuthUsageGrokProbeRetryTTL
	grokFreeQuotaWindow = accountcore.OAuthUsageGrokFreeQuotaWindow
)

type UsageCache = accountcore.OAuthUsageCache

// NewUsageCache 复用唯一账号缓存实现。
func NewUsageCache() *UsageCache { return accountcore.NewOAuthUsageCache() }

// WindowStats 兼容旧消费入口，展示模型由账号模块唯一拥有。
type WindowStats = accountcore.WindowStats

// UsageProgress 兼容旧消费入口，展示模型由账号模块唯一拥有。
type UsageProgress = accountcore.UsageProgress

// AntigravityModelQuota 兼容旧消费入口，展示模型由账号模块唯一拥有。
type AntigravityModelQuota = accountcore.AntigravityModelQuota

// AntigravityModelDetail 兼容旧消费入口，展示模型由账号模块唯一拥有。
type AntigravityModelDetail = accountcore.AntigravityModelDetail

// AICredit 兼容旧消费入口，展示模型由账号模块唯一拥有。
type AICredit = accountcore.AICredit

// QoderQuotaProgress 兼容旧消费入口，展示模型由账号模块唯一拥有。
type QoderQuotaProgress = accountcore.QoderQuotaProgress

// QoderQuotaInfo 兼容旧消费入口，展示模型由账号模块唯一拥有。
type QoderQuotaInfo = accountcore.QoderQuotaInfo

// UsageInfo 兼容旧消费入口，展示模型由账号模块唯一拥有。
type UsageInfo = accountcore.UsageInfo

// ClaudeUsageWindow 兼容旧消费入口，展示模型由账号模块唯一拥有。
type ClaudeUsageWindow = accountcore.ClaudeUsageWindow

// ClaudeUsageResponse 兼容旧消费入口，展示模型由账号模块唯一拥有。
type ClaudeUsageResponse = accountcore.ClaudeUsageResponse

// ClaudeUsageFetchOptions 包含获取 Claude 用量数据所需的所有选项
type ClaudeUsageFetchOptions struct {
	AccessToken string                  // OAuth access token
	ProxyURL    string                  // 代理 URL（可选）
	AccountID   int64                   // 账号 ID（用于连接池隔离）
	TLSProfile  *tlsfingerprint.Profile // TLS 指纹 Profile（nil 表示不启用）
	Fingerprint *Fingerprint            // 缓存的指纹信息（User-Agent 等）
}

// ClaudeUsageFetcher fetches usage data from Anthropic OAuth API
type ClaudeUsageFetcher interface {
	FetchUsage(ctx context.Context, accessToken, proxyURL string) (*ClaudeUsageResponse, error)
	// FetchUsageWithOptions 使用完整选项获取用量数据，支持 TLS 指纹和自定义 User-Agent
	FetchUsageWithOptions(ctx context.Context, opts *ClaudeUsageFetchOptions) (*ClaudeUsageResponse, error)
}

// AccountUsageService 账号使用量查询服务
type AccountUsageService struct {
	statistics              *accountcore.LocalUsageStatistics
	coreOnce                sync.Once
	core                    *accountcore.OAuthUsageService
	accountRepo             AccountRepository
	usageLogRepo            UsageLogRepository
	usageFetcher            ClaudeUsageFetcher
	geminiQuotaService      *GeminiQuotaService
	antigravityQuotaFetcher *AntigravityQuotaFetcher
	grokQuotaFetcher        *GrokQuotaFetcher
	grokQuotaService        *GrokQuotaService
	openAIQuotaService      *OpenAIQuotaService
	cache                   *UsageCache
	identityCache           IdentityCache
	tlsFPProfileService     *TLSFingerprintProfileService
	httpUpstream            HTTPUpstream
	quotaAutoPauseSettings  OpenAIQuotaAutoPauseSettingsReader
	agentIdentityTaskMu     sync.Mutex
	agentIdentityWS         agentIdentityWSConnectionInvalidator
	qoderSessionProvider    *QoderTokenProvider
}

// NewAccountUsageService 创建AccountUsageService实例
func NewAccountUsageService(
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
) *AccountUsageService {
	qoderSessionProvider := NewQoderTokenProvider()
	qoderSessionProvider.SetHTTPUpstream(httpUpstream, tlsFPProfileService)
	return &AccountUsageService{
		accountRepo:             accountRepo,
		usageLogRepo:            usageLogRepo,
		usageFetcher:            usageFetcher,
		geminiQuotaService:      geminiQuotaService,
		antigravityQuotaFetcher: antigravityQuotaFetcher,
		grokQuotaFetcher:        grokQuotaFetcher,
		grokQuotaService:        grokQuotaService,
		openAIQuotaService:      openAIQuotaService,
		cache:                   cache,
		identityCache:           identityCache,
		tlsFPProfileService:     tlsFPProfileService,
		httpUpstream:            httpUpstream,
		quotaAutoPauseSettings:  quotaAutoPauseSettings,
		qoderSessionProvider:    qoderSessionProvider,
	}
}

func (s *AccountUsageService) GetUsage(ctx context.Context, accountID int64, force ...bool) (*UsageInfo, error) {
	return s.Core().GetUsage(ctx, accountID, force...)
}

func (s *AccountUsageService) GetUsageBatch(ctx context.Context, accountIDs []int64, force bool) (map[int64]*UsageInfo, map[int64]string, error) {
	return s.Core().GetUsageBatch(ctx, accountIDs, force)
}

func (s *AccountUsageService) GetPassiveUsage(ctx context.Context, accountID int64) (*UsageInfo, error) {
	return s.Core().GetPassiveUsage(ctx, accountID)
}

func (s *AccountUsageService) applyOpenAIQuotaAutoPauseState(ctx context.Context, account *Account, usage *UsageInfo) {
	if account == nil || usage == nil || !account.IsOpenAI() {
		return
	}
	// 用量接口可能刚刷新了 account.Extra，这里必须基于刷新后的内存快照实时计算。
	if s != nil && s.quotaAutoPauseSettings != nil {
		ctx = WithOpenAIQuotaAutoPauseSettings(ctx, s.quotaAutoPauseSettings.GetOpenAIQuotaAutoPauseSettings(ctx))
	}
	usage.QuotaAutoPaused = EvaluateOpenAIQuotaAutoPause(ctx, account)
}

// buildPassiveUsageWindow 委托账号所属的纯展示/资格规则。
func buildPassiveUsageWindow(extra map[string]any, utilKey, resetKey string) *UsageProgress {
	return accountcore.BuildPassiveUsageWindow(extra, utilKey, resetKey, time.Now)
}

func (s *AccountUsageService) fetchOpenAICodexSnapshot(ctx context.Context, account *Account) (map[string]any, error) {
	if account == nil || !account.IsOAuth() {
		return nil, nil
	}
	accessToken := ""
	if !account.IsOpenAIAgentIdentity() {
		accessToken = account.GetOpenAIAccessToken()
	}
	if accessToken == "" && !account.IsOpenAIAgentIdentity() {
		return nil, fmt.Errorf("no access token available")
	}
	modelID := openaipkg.CodexUsageProbeModel
	payload := createOpenAITestPayload(modelID, "", true)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal openai probe payload: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, chatgptCodexURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("create openai probe request: %w", err)
	}
	req.Host = "chatgpt.com"
	req.Header.Set("Content-Type", "application/json")
	if account.IsOpenAIAgentIdentity() {
		authHeaders, authErr := buildAgentIdentityAuthenticationHeaders(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, account)
		if authErr != nil {
			return nil, fmt.Errorf("build Agent Identity authentication: %w", authErr)
		}
		for key, values := range authHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	canonical := resolveCodexOutboundIdentity("")
	req.Header.Set("Originator", canonical.originator)
	req.Header.Set("Version", canonical.version)
	req.Header.Set("User-Agent", canonical.userAgent)
	if s.identityCache != nil {
		if fp, fpErr := s.identityCache.GetFingerprint(reqCtx, account.ID); fpErr == nil && fp != nil && strings.TrimSpace(fp.UserAgent) != "" {
			req.Header.Set("User-Agent", strings.TrimSpace(fp.UserAgent))
		}
	}
	// 与真实转发一致：originator 与最终 User-Agent（可能来自指纹缓存，如 codex-tui）首段配套，
	// 否则探针被上游 404（issue #3901）。
	enforceCodexIdentityHeaders(req.Header)
	setOpenAIChatGPTAccountHeaders(req.Header, account)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.doOpenAICodexProbeRequest(req, account, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("openai codex probe request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	updates, err := extractOpenAICodexProbeUpdates(resp)
	if err != nil {
		return nil, err
	}
	if len(updates) > 0 {
		return updates, nil
	}
	return nil, nil
}

func (s *AccountUsageService) doOpenAICodexProbeRequest(req *http.Request, account *Account, proxyURL string) (*http.Response, error) {
	if s != nil && s.httpUpstream != nil {
		// Codex 后台快照探测也要复用网关上游链路，确保代理、OpenAI HTTP/2 策略和 TLS 指纹与用户请求一致。
		req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
		return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveTLSProfile(account))
	}
	client, err := httppool.GetClient(httppool.Options{
		ProxyURL:              proxyURL,
		Timeout:               15 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("build openai probe client: %w", err)
	}
	return client.Do(req)
}

func (s *AccountUsageService) resolveTLSProfile(account *Account) *tlsfingerprint.Profile {
	if s == nil || s.tlsFPProfileService == nil {
		return nil
	}
	return s.tlsFPProfileService.ResolveTLSProfile(account)
}

func extractOpenAICodexProbeUpdates(resp *http.Response) (map[string]any, error) {
	if resp == nil {
		return nil, nil
	}
	if snapshot := ParseCodexRateLimitHeaders(resp.Header); snapshot != nil {
		return buildCodexUsageExtraUpdates(snapshot, time.Now()), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai codex probe returned status %d", resp.StatusCode)
	}
	return nil, nil
}

// mergeAccountExtra 将旧平台临时记录投影到唯一纯合并实现。
func mergeAccountExtra(value *Account, updates map[string]any) {
	if value != nil {
		value.Extra = accountcore.MergeUsageExtra(value.Extra, updates)
	}
}

const (
	qoderQuotaUsagePath         = "/api/v2/quota/usage"
	qoderQuotaSnapshotExtraKey  = accountcore.QoderUsageQuotaSnapshotExtraKey
	qoderQuotaUpdatedAtExtraKey = accountcore.QoderUsageQuotaUpdatedAtExtraKey
)

type qoderQuotaUsageResponse struct {
	UserID               string                 `json:"userId"`
	UserType             string                 `json:"userType"`
	UsageType            string                 `json:"usageType"`
	TotalUsagePercentage float64                `json:"totalUsagePercentage"`
	IsQuotaExceeded      bool                   `json:"isQuotaExceeded"`
	ExpiresAt            qoder.FlexibleInt64    `json:"expiresAt"`
	UpgradeURL           string                 `json:"upgradeUrl"`
	AddCreditsURL        string                 `json:"addCreditsUrl"`
	UserQuota            *qoderQuotaProgressRaw `json:"userQuota"`
	AddOnQuota           *qoderQuotaProgressRaw `json:"addOnQuota"`
	AddOnQuotaSnake      *qoderQuotaProgressRaw `json:"add_on_quota"`
	OrgResourcePackage   *qoderQuotaProgressRaw `json:"orgResourcePackage"`
	OrgResourcePkgSnake  *qoderQuotaProgressRaw `json:"org_resource_package"`
	SharedQuota          *qoderQuotaProgressRaw `json:"sharedQuota"`
	SharedQuotaSnake     *qoderQuotaProgressRaw `json:"shared_quota"`
	IsPlanQuotaProrated  bool                   `json:"isPlanQuotaProrated"`
}

type qoderQuotaProgressRaw struct {
	Total          float64 `json:"total"`
	Cap            float64 `json:"cap"`
	Used           float64 `json:"used"`
	Remaining      float64 `json:"remaining"`
	Percentage     float64 `json:"percentage"`
	Unit           string  `json:"unit"`
	DetailURL      string  `json:"detailUrl"`
	DetailURLSnake string  `json:"detail_url"`
	Available      bool    `json:"available"`
	OrganizationID string  `json:"organizationId"`

	totalSet      bool
	capSet        bool
	usedSet       bool
	remainingSet  bool
	percentageSet bool
	availableSet  bool
}

func (r *qoderQuotaProgressRaw) UnmarshalJSON(data []byte) error {
	type alias qoderQuotaProgressRaw
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = qoderQuotaProgressRaw(decoded)

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil
	}
	r.totalSet = qoderJSONHasAnyField(fields, "total")
	r.capSet = qoderJSONHasAnyField(fields, "cap")
	r.usedSet = qoderJSONHasAnyField(fields, "used")
	r.remainingSet = qoderJSONHasAnyField(fields, "remaining")
	r.percentageSet = qoderJSONHasAnyField(fields, "percentage")
	r.availableSet = qoderJSONHasAnyField(fields, "available")
	return nil
}

func qoderJSONHasAnyField(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		return true
	}
	return false
}

func (s *AccountUsageService) fetchQoderQuotaUsage(ctx context.Context, account *Account) (*qoderQuotaUsageResponse, error) {
	if account == nil {
		return nil, fmt.Errorf("qoder: account is nil")
	}
	provider := s.qoderSessionProvider
	if provider == nil {
		provider = NewQoderTokenProvider()
		provider.SetHTTPUpstream(s.httpUpstream, s.tlsFPProfileService)
	}
	usage, err := s.fetchQoderQuotaUsageWithProvider(ctx, account, provider)
	if err == nil || strings.TrimSpace(account.GetCredential("pat")) == "" || !isQoderAuthenticationError(err) {
		return usage, err
	}

	// PAT 可随时重新交换；认证失败时丢弃旧 session 并仅重试一次，避免额度页永久停留在需重新授权状态。
	provider.Invalidate(account.ID)
	return s.fetchQoderQuotaUsageWithProvider(ctx, account, provider)
}

func (s *AccountUsageService) fetchQoderQuotaUsageWithProvider(ctx context.Context, account *Account, provider *QoderTokenProvider) (*qoderQuotaUsageResponse, error) {
	session, err := provider.GetSession(ctx, account)
	if err != nil {
		return nil, err
	}
	profile, err := qoderProfileForAccount(account)
	if err != nil {
		return nil, err
	}
	logicalPath := qoder.QuotaUsagePath
	if profile.Site == qoder.SiteCN {
		query := url.Values{}
		if organizationID := strings.TrimSpace(session.Identity.OrganizationID); organizationID != "" {
			query.Set("orgId", organizationID)
		}
		if encoded := query.Encode(); encoded != "" {
			logicalPath += "?" + encoded
		}
	}
	doer := newQoderRequestDoer(account, s.httpUpstream, s.tlsFPProfileService)
	var usage qoderQuotaUsageResponse
	client := qoder.NewClientForProfile(profile)
	request := client.BearerJSONRequestContextWithDoer
	if qoderQuotaUsesSignedAuth(account, profile.Site) {
		request = client.JSONRequestContextWithDoer
	}
	if err := request(ctx, http.MethodGet, session, logicalPath, nil, nil, doer, &usage); err != nil {
		return nil, fmt.Errorf("qoder: quota usage request: %w", err)
	}
	return &usage, nil
}

// qoderQuotaUsesSignedAuth 对齐 1.24.2 客户端：国际站和 QoderCN20 使用 COSY 签名，国内旧会话使用普通 Bearer。
func qoderQuotaUsesSignedAuth(account *Account, site qoder.Site) bool {
	if site != qoder.SiteCN {
		return true
	}
	if strings.TrimSpace(account.GetCredential("pat")) != "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(account.GetCredential("refresh_mode")), qoder.RefreshModeQoderCN20)
}

// isQoderAuthenticationError 只把明确的 401/403 视为可通过 PAT 重建 session 的认证失败。
func isQoderAuthenticationError(err error) bool {
	var apiErr *qoder.APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

func buildQoderUsageInfoAt(resp *qoderQuotaUsageResponse, now time.Time) *UsageInfo {
	return &UsageInfo{
		Source:     "active",
		UpdatedAt:  &now,
		QoderQuota: qoderQuotaInfoFromResponse(resp, now, false),
	}
}

func qoderQuotaInfoFromResponse(resp *qoderQuotaUsageResponse, updatedAt time.Time, fromSnapshot bool) *QoderQuotaInfo {
	if resp == nil {
		return nil
	}
	var expiresAt *time.Time
	if resp.ExpiresAt > 0 {
		rawExpiresAt := int64(resp.ExpiresAt)
		var t time.Time
		if rawExpiresAt >= 1_000_000_000_000 {
			t = time.UnixMilli(rawExpiresAt)
		} else {
			t = time.Unix(rawExpiresAt, 0)
		}
		expiresAt = &t
	}
	quota := &QoderQuotaInfo{
		UserID:               strings.TrimSpace(resp.UserID),
		UserType:             strings.TrimSpace(resp.UserType),
		UsageType:            strings.TrimSpace(resp.UsageType),
		TotalUsagePercentage: normalizeQoderQuotaPercentage(resp.TotalUsagePercentage),
		IsQuotaExceeded:      resp.IsQuotaExceeded,
		ExpiresAt:            expiresAt,
		UpgradeURL:           strings.TrimSpace(resp.UpgradeURL),
		AddCreditsURL:        strings.TrimSpace(resp.AddCreditsURL),
		IsPlanQuotaProrated:  resp.IsPlanQuotaProrated,
		LastUpdatedAt:        &updatedAt,
		SnapshotFromAccount:  fromSnapshot,
	}
	quota.UserQuota = qoderQuotaProgressFromRaw(resp.UserQuota, true)
	quota.AddOnQuota = qoderQuotaProgressFromRaw(firstNonNilQoderQuotaProgress(resp.AddOnQuota, resp.AddOnQuotaSnake), true)
	quota.OrgResourcePackage = qoderQuotaProgressFromRaw(firstNonNilQoderQuotaProgress(
		resp.OrgResourcePackage,
		resp.OrgResourcePkgSnake,
		resp.SharedQuota,
		resp.SharedQuotaSnake,
	), true)
	return quota
}

func firstNonNilQoderQuotaProgress(values ...*qoderQuotaProgressRaw) *qoderQuotaProgressRaw {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func qoderQuotaProgressFromRaw(raw *qoderQuotaProgressRaw, useCapAsTotal bool) *QoderQuotaProgress {
	if raw == nil {
		return nil
	}
	total := 0.0
	if qoderQuotaRawFieldSet(raw.totalSet, raw.Total) {
		total = raw.Total
	}
	if useCapAsTotal && total <= 0 {
		if qoderQuotaRawFieldSet(raw.capSet, raw.Cap) {
			total = raw.Cap
		}
		if total <= 0 && (qoderQuotaRawFieldSet(raw.usedSet, raw.Used) || qoderQuotaRawFieldSet(raw.remainingSet, raw.Remaining)) {
			total = raw.Used + raw.Remaining
		}
	}
	used := 0.0
	if qoderQuotaRawFieldSet(raw.usedSet, raw.Used) {
		used = raw.Used
	}
	remaining := 0.0
	if qoderQuotaRawFieldSet(raw.remainingSet, raw.Remaining) {
		remaining = raw.Remaining
	} else if total > used {
		remaining = total - used
	}
	if remaining < 0 {
		remaining = 0
	}
	percentage := 0.0
	if qoderQuotaRawFieldSet(raw.percentageSet, raw.Percentage) {
		percentage = raw.Percentage
	} else if total > 0 && used > 0 {
		percentage = used / total
	}
	percentage = normalizeQoderQuotaPercentage(percentage)
	available := raw.Available
	if useCapAsTotal && !raw.availableSet {
		baseCapacity := 0.0
		if qoderQuotaRawFieldSet(raw.capSet, raw.Cap) {
			baseCapacity = raw.Cap
		} else if qoderQuotaRawFieldSet(raw.totalSet, raw.Total) {
			baseCapacity = raw.Total
		}
		available = baseCapacity > 0
	}
	return &QoderQuotaProgress{
		Total:          total,
		Used:           used,
		Remaining:      remaining,
		Percentage:     percentage,
		Unit:           strings.TrimSpace(raw.Unit),
		DetailURL:      strings.TrimSpace(firstNonEmptyQoder(raw.DetailURL, raw.DetailURLSnake)),
		Cap:            raw.Cap,
		Available:      available,
		OrganizationID: strings.TrimSpace(raw.OrganizationID),
	}
}

func qoderQuotaRawFieldSet(explicit bool, value float64) bool {
	return explicit || value != 0
}

func normalizeQoderQuotaPercentage(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value >= 0 && value <= 1 {
		value *= 100
	}
	return math.Round(value*100) / 100
}

// qoderQuotaRateLimitResetAt 委托账号所属的纯展示/资格规则。
func qoderQuotaRateLimitResetAt(quota *QoderQuotaInfo, now time.Time) (time.Time, bool) {
	return accountcore.QoderQuotaRateLimitResetAt(quota, now)
}

// qoderQuotaTotalRemaining 委托账号所属的纯展示/资格规则。
func qoderQuotaTotalRemaining(quota *QoderQuotaInfo) (float64, bool) {
	return accountcore.QoderQuotaTotalRemaining(quota)
}

// qoderQuotaTotalCapacity 委托账号所属的纯展示/资格规则。
func qoderQuotaTotalCapacity(quota *QoderQuotaInfo) float64 {
	return accountcore.QoderQuotaTotalCapacity(quota)
}

func buildQoderDegradedUsageAt(err error, account *Account, now time.Time) *UsageInfo {
	info := &UsageInfo{
		UpdatedAt: &now,
		Error:     fmt.Sprintf("usage API error: %v", err),
	}
	if err != nil {
		var apiErr *qoder.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				info.ErrorCode = errorCodeUnauthenticated
				info.NeedsReauth = true
			case http.StatusTooManyRequests:
				info.ErrorCode = errorCodeRateLimited
			default:
				info.ErrorCode = errorCodeNetworkError
			}
			if snapshot := qoderQuotaSnapshotFromExtra(account); snapshot != nil {
				snapshot.SnapshotFromAccount = true
				info.QoderQuota = snapshot
			}
			return info
		}
		errStr := err.Error()
		switch {
		case strings.Contains(errStr, "status 401") || strings.Contains(errStr, "status 403"):
			info.ErrorCode = errorCodeUnauthenticated
			info.NeedsReauth = true
		case strings.Contains(errStr, "status 429"):
			info.ErrorCode = errorCodeRateLimited
		case strings.Contains(errStr, "request:"):
			info.ErrorCode = errorCodeNetworkError
		default:
			info.ErrorCode = errorCodeNetworkError
		}
	}
	if snapshot := qoderQuotaSnapshotFromExtra(account); snapshot != nil {
		snapshot.SnapshotFromAccount = true
		info.QoderQuota = snapshot
	}
	return info
}

func qoderQuotaSnapshotFromExtra(account *Account) *QoderQuotaInfo {
	if account == nil || account.Extra == nil {
		return nil
	}
	raw, ok := account.Extra[qoderQuotaSnapshotExtraKey]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var quota QoderQuotaInfo
	if err := json.Unmarshal(data, &quota); err != nil {
		return nil
	}
	if quota.LastUpdatedAt == nil {
		if updatedRaw, ok := account.Extra[qoderQuotaUpdatedAtExtraKey].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, updatedRaw); err == nil {
				quota.LastUpdatedAt = &parsed
			}
		}
	}
	return &quota
}

func grokLocalUsageForQuota(
	ctx context.Context,
	repo UsageLogRepository,
	accountID int64,
	billing *xai.BillingSummary,
	now time.Time,
) (*WindowStats, *WindowStats, *WindowStats) {
	return accountcore.GrokLocalUsageForQuota(ctx, legacyGrokUsageReader(repo), accountID, billing, now, slog.Warn)
}

// buildAntigravityDegradedUsage 从 FetchQuota 错误构建降级 UsageInfo
func buildAntigravityDegradedUsageAt(err error, now time.Time) *UsageInfo {
	errMsg := fmt.Sprintf("usage API error: %v", err)
	slog.Warn("antigravity usage fetch failed, returning degraded response", "error", err)

	info := &UsageInfo{
		UpdatedAt: &now,
		Error:     errMsg,
	}

	// 从错误信息推断 error_code 和状态标记
	// 错误格式来自 antigravity/client.go: "fetchAvailableModels 失败 (HTTP %d): ..."
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "HTTP 401") ||
		strings.Contains(errStr, "UNAUTHENTICATED") ||
		strings.Contains(errStr, "invalid_grant"):
		info.ErrorCode = errorCodeUnauthenticated
		info.NeedsReauth = true
	case strings.Contains(errStr, "HTTP 429"):
		info.ErrorCode = errorCodeRateLimited
	default:
		info.ErrorCode = errorCodeNetworkError
	}

	return info
}

// enrichUsageWithAccountError 结合账号错误状态修正 UsageInfo
// 场景 1（成功路径）：FetchAvailableModels 正常返回，但账号已因 403 被标记为 error，
//
//	需要在正常 usage 数据上附加 forbidden/validation 信息。
//
// 场景 2（降级路径）：被封号的账号 OAuth token 失效，FetchAvailableModels 返回 401，
//
//	降级逻辑设置了 needs_reauth，但账号实际是 403 封号/需验证，需覆盖为正确状态。
func enrichUsageWithAccountError(info *UsageInfo, account *Account) {
	if info == nil || account == nil || account.Status != StatusError {
		return
	}
	msg := strings.ToLower(account.ErrorMessage)
	if !strings.Contains(msg, "403") && !strings.Contains(msg, "forbidden") &&
		!strings.Contains(msg, "violation") && !strings.Contains(msg, "validation") {
		return
	}
	fbType := classifyForbiddenType(account.ErrorMessage)
	info.IsForbidden = true
	info.ForbiddenType = fbType
	info.ForbiddenReason = account.ErrorMessage
	info.NeedsVerify = fbType == forbiddenTypeValidation
	info.IsBanned = fbType == forbiddenTypeViolation
	info.ValidationURL = extractValidationURL(account.ErrorMessage)
	info.ErrorCode = errorCodeForbidden
	info.NeedsReauth = false
}

func (s *AccountUsageService) GetTodayStats(ctx context.Context, accountID int64) (*WindowStats, error) {
	return s.localUsageStatistics().GetTodayStats(ctx, accountID)
}

func (s *AccountUsageService) GetTodayStatsBatch(ctx context.Context, accountIDs []int64) (map[int64]*WindowStats, error) {
	return s.localUsageStatistics().GetTodayStatsBatch(ctx, accountIDs)
}

func windowStatsFromAccountStats(stats *usagestats.AccountStats) *WindowStats {
	if stats == nil {
		return &WindowStats{}
	}
	return &WindowStats{
		Requests:     stats.Requests,
		Tokens:       stats.Tokens,
		Cost:         stats.Cost,
		StandardCost: stats.StandardCost,
		UserCost:     stats.UserCost,
	}
}

// buildCodexUsageProgressFromExtra 委托账号所属的纯展示/资格规则。
func buildCodexUsageProgressFromExtra(extra map[string]any, window string, now time.Time) *UsageProgress {
	return accountcore.BuildCodexUsageProgressFromExtra(extra, window, now, time.Now)
}

// codexWindowStatsStart 委托账号所属的纯展示/资格规则。
func codexWindowStatsStart(progress *UsageProgress, fallbackWindow time.Duration, now time.Time) time.Time {
	return accountcore.CodexWindowStatsStart(progress, fallbackWindow, now)
}

func (s *AccountUsageService) GetAccountUsageStats(ctx context.Context, accountID int64, startTime, endTime time.Time) (*usagestats.AccountUsageStatsResponse, error) {
	stats, err := s.usageLogRepo.GetAccountUsageStats(ctx, accountID, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("get account usage stats failed: %w", err)
	}
	return stats, nil
}

// fetchOAuthUsageRaw 从 Anthropic API 获取原始响应（不构建 UsageInfo）
// 如果账号开启了 TLS 指纹，则使用 TLS 指纹伪装
// 如果有缓存的 Fingerprint，则使用缓存的 User-Agent 等信息
func (s *AccountUsageService) fetchOAuthUsageRaw(ctx context.Context, account *Account) (*ClaudeUsageResponse, error) {
	accessToken := account.GetCredential("access_token")
	if accessToken == "" {
		return nil, fmt.Errorf("no access token available")
	}

	var proxyURL string
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 构建完整的选项
	opts := &ClaudeUsageFetchOptions{
		AccessToken: accessToken,
		ProxyURL:    proxyURL,
		AccountID:   account.ID,
		TLSProfile:  s.resolveTLSProfile(account),
	}

	// 尝试获取缓存的 Fingerprint（包含 User-Agent 等信息）
	if s.identityCache != nil {
		if fp, err := s.identityCache.GetFingerprint(ctx, account.ID); err == nil && fp != nil {
			opts.Fingerprint = fp
		}
	}

	return s.usageFetcher.FetchUsageWithOptions(ctx, opts)
}

// parseTime 委托账号所属的纯展示/资格规则。
func parseTime(s string) (time.Time, error) { return accountcore.ParseUsageTime(s) }

// tryClearRecoverableAccountError 仅适配本轮观察值与新条件恢复能力。
func (s *AccountUsageService) tryClearRecoverableAccountError(ctx context.Context, value *Account) {
	if value == nil {
		return
	}
	writer, _ := s.accountRepo.(accountcore.UsageRecoveryWriter)
	applied, err := accountcore.RecoverUsageAccountError(ctx, AccountRecordView(value), writer)
	if err != nil {
		log.Printf("[usage] failed to clear recoverable account error for account %d: %v", value.ID, err)
		return
	}
	if applied {
		value.Status = StatusActive
		value.ErrorMessage = ""
	}
}

// buildUsageInfo 委托账号所属的纯展示/资格规则。
func (s *AccountUsageService) buildUsageInfo(resp *ClaudeUsageResponse, updatedAt *time.Time) *UsageInfo {
	return accountcore.BuildUsageInfo(resp, updatedAt, time.Now, log.Printf)
}

// estimateSetupTokenUsage 委托账号所属的纯展示/资格规则。
func (s *AccountUsageService) estimateSetupTokenUsage(account *Account) *UsageInfo {
	return accountcore.EstimateSetupTokenUsage(AccountRecordView(account), time.Now)
}

// GetAccountWindowStats 获取账号在指定时间窗口内的使用统计
// 用于账号列表页面显示当前窗口费用
func (s *AccountUsageService) GetAccountWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usagestats.AccountStats, error) {
	return s.usageLogRepo.GetAccountWindowStats(ctx, accountID, startTime)
}
