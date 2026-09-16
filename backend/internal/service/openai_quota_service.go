package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	quotaAccount "github.com/TokenFlux/TokenRouter/internal/account"

	quotaWire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/model"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
)

// ErrSparkShadowResetNotSupported 表示不允许直接通过 spark 影子账号消耗母账号重置次数。
var ErrSparkShadowResetNotSupported = quotaAccount.ErrSparkShadowResetNotSupported

const (
	chatGPTUsagePath            = "/wham/usage"
	chatGPTRateLimitCreditsPath = "/wham/rate-limit-reset-credits"
	chatGPTRateLimitResetPath   = "/wham/rate-limit-reset-credits/consume"
	openaiQuotaUpstreamTimeout  = 20 * time.Second
	openaiQuotaCodexBeta        = "codex-1"
	openaiQuotaCodexOriginator  = "Codex Desktop"
	openaiQuotaCodexLanguageTag = "zh-CN"
	openaiQuotaSecFetchSite     = "none"
	openaiQuotaSecFetchMode     = "no-cors"
	openaiQuotaSecFetchDest     = "empty"
	openaiQuotaResetCreditsKey  = "codex_reset_credit_snapshot"
)

type OpenAIRateLimitWindow = quotaWire.OpenAIRateLimitWindow

type OpenAIRateLimit = quotaWire.OpenAIRateLimit

type OpenAIAdditionalRateLimit = quotaWire.OpenAIAdditionalRateLimit

type OpenAIRateLimitResetCreditDetail = quotaWire.OpenAIRateLimitResetCreditDetail

type OpenAIRateLimitResetCredits = quotaWire.OpenAIRateLimitResetCredits

type OpenAIQuotaUsage = quotaWire.OpenAIQuotaUsage

type OpenAIQuotaResetCredit = quotaWire.OpenAIQuotaResetCredit

type OpenAIQuotaResetResult = quotaWire.OpenAIQuotaResetResult

// OpenAIQuotaService 查询和消耗 OpenAI OAuth 账号的 Codex 限流重置次数。
type OpenAIQuotaService struct {
	coreOnce            sync.Once
	core                *quotaAccount.OpenAIQuotaService
	adminService        AdminService
	httpUpstream        HTTPUpstream
	openAITokenProvider *OpenAITokenProvider
	tlsFPProfileService *TLSFingerprintProfileService
	tlsFPRouterReader   OpenAIOAuthTokenRouterReader
	accountRepo         AccountRepository
	agentIdentityTaskMu sync.Mutex
	agentIdentityWS     agentIdentityWSConnectionInvalidator
}

func NewOpenAIQuotaService(
	adminService AdminService,
	httpUpstream HTTPUpstream,
	openAITokenProvider *OpenAITokenProvider,
	tlsFPProfileService *TLSFingerprintProfileService,
	tlsFPRouterReader OpenAIOAuthTokenRouterReader,
) *OpenAIQuotaService {
	return &OpenAIQuotaService{

		adminService: adminService,

		httpUpstream: httpUpstream,

		openAITokenProvider: openAITokenProvider,

		tlsFPProfileService: tlsFPProfileService,

		tlsFPRouterReader: tlsFPRouterReader,
	}
}

func (s *OpenAIQuotaService) QueryUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error) {
	return s.Core().QueryUsage(ctx, accountID)
}

func (s *OpenAIQuotaService) CacheResetCreditsSnapshot(ctx context.Context, accountID int64, credits *OpenAIRateLimitResetCredits) error {
	return s.Core().CacheResetCreditsSnapshot(ctx, accountID, credits)
}

func (s *OpenAIQuotaService) CachePostResetSnapshot(ctx context.Context, accountID int64, usage *OpenAIQuotaUsage) error {
	return s.Core().CachePostResetSnapshot(ctx, accountID, usage)
}

func (s *OpenAIQuotaService) ResetCredit(ctx context.Context, accountID int64) (*OpenAIQuotaResetResult, error) {
	return s.Core().ResetCredit(ctx, accountID)
}

type openAIQuotaAccountContext struct {
	account    *Account
	token      string
	proxyURL   string
	userAgent  string
	tlsProfile *tlsfingerprint.Profile
}

func (s *OpenAIQuotaService) prepareAccount(ctx context.Context, accountID int64) (*openAIQuotaAccountContext, error) {
	prepared, err := s.Core().PrepareAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return &openAIQuotaAccountContext{account: AccountFromRecord(prepared.Account), token: prepared.Token}, nil
}

func (s *OpenAIQuotaService) resolveRuntimeRouter(account *Account) *model.TLSFingerprintRouter {
	if account == nil || s == nil || s.tlsFPRouterReader == nil {
		return nil
	}
	router := s.tlsFPRouterReader.GetRuntimeRouter(account.GetTLSFingerprintRouterID())
	if router == nil || !router.Enabled {
		return nil
	}
	return router
}

func (s *OpenAIQuotaService) resolveUserAgent(router *model.TLSFingerprintRouter) string {
	if router != nil {
		// 限流重置走 Codex Desktop 后台接口，复用邀请重置专用 UA 配置。
		if userAgent := strings.TrimSpace(router.CodexInviteResetUserAgent); userAgent != "" {
			return userAgent
		}
	}
	return codexInviteResetDefaultUserAgent
}

func (s *OpenAIQuotaService) resolveTLSProfile(account *Account, router *model.TLSFingerprintRouter) *tlsfingerprint.Profile {
	if s == nil || s.tlsFPProfileService == nil {
		return nil
	}
	if router != nil && router.CodexInviteResetTLSFingerprintProfileID != nil {
		if profile, ok := s.tlsFPProfileService.ResolveTokenTLSProfileByID(*router.CodexInviteResetTLSFingerprintProfileID); ok {
			return profile
		}
	}
	return s.tlsFPProfileService.ResolveTLSProfile(account)
}

func generateOpenAIQuotaRedeemRequestID() (string, error) {
	return nativeopenai.GenerateOpenAIQuotaRedeemRequestID()
}

func buildCodexSparkWindowExtraUpdates(usage *OpenAIQuotaUsage, now time.Time) map[string]any {
	return quotaAccount.BuildCodexSparkWindowExtraUpdates(usage, now)
}

// nativeQuotaClient 只把原账号和传输能力投影到无状态原生客户端，不创建缓存或新锁。
func (s *OpenAIQuotaService) nativeQuotaClient(value *openAIQuotaAccountContext) *nativeopenai.QuotaClient {
	options := nativeopenai.QuotaClientOptions{Available: value != nil && value.account != nil, URL: buildCodexInviteResetURL}
	if !options.Available {
		return &nativeopenai.QuotaClient{Options: options}
	}
	options.UserAgent = value.userAgent
	options.Authenticate = func(ctx context.Context) (string, string, error) {
		if value.account.IsOpenAIAgentIdentity() {
			headers, taskID, err := buildAgentIdentityAuthenticationHeadersWithTask(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, value.account)
			return headers.Get("Authorization"), taskID, err
		}
		return "Bearer " + value.token, "", nil
	}
	options.AccountHeaders = func(headers http.Header) { setOpenAIChatGPTAccountHeaders(headers, value.account) }
	options.Do = func(request *http.Request) (*http.Response, error) {
		return s.httpUpstream.DoWithTLS(request, value.proxyURL, value.account.ID, value.account.Concurrency, value.tlsProfile)
	}
	options.IsAgentIdentity = value.account.IsOpenAIAgentIdentity
	options.RecoverTask = func(ctx context.Context, taskID string) error {
		return ensureAgentIdentityTaskForAccount(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, value.account, taskID)
	}
	options.Redact = func(ctx context.Context, body []byte) []byte {
		return redactAgentIdentitySensitiveBodyForAccount(ctx, s.accountRepo, value.account, body)
	}
	options.Failure = func(status int, message string) {
		slog.Warn("openai_quota_upstream_failed", "account_id", value.account.ID, "status", status, "body", truncate(message, 240))
	}
	return &nativeopenai.QuotaClient{Options: options}
}

// Core 复用一个活动拥有者；读取端口在原时点取依赖，保留测试与运行时绑定时机。
func (s *OpenAIQuotaService) Core() *quotaAccount.OpenAIQuotaService {
	if s == nil {
		return quotaAccount.NewOpenAIQuotaService(quotaAccount.OpenAIQuotaOptions{})
	}
	s.coreOnce.Do(func() {
		options := quotaAccount.OpenAIQuotaOptions{

			Configured: func() bool { return s.adminService != nil && s.httpUpstream != nil },

			RedeemID: generateOpenAIQuotaRedeemRequestID,

			Warn: slog.Warn,
			Info: slog.Info,

			Client: func(ctx context.Context, record *quotaAccount.Record, token string) (quotaAccount.OpenAIQuotaClient, error) {
				account := AccountFromRecord(record)
				proxyURL := ""
				if account.ProxyID != nil {
					proxy, err := s.adminService.GetProxy(ctx, *account.ProxyID)
					if err != nil {
						return nil, err
					}
					if proxy != nil {
						proxyURL = proxy.URL()
					}
				}
				router := s.resolveRuntimeRouter(account)
				return s.nativeQuotaClient(&openAIQuotaAccountContext{
					account:    account,
					token:      token,
					proxyURL:   proxyURL,
					userAgent:  s.resolveUserAgent(router),
					tlsProfile: s.resolveTLSProfile(account, router),
				}), nil
			},
		}
		if s.adminService != nil {
			options.Read = func(ctx context.Context, id int64) (*quotaAccount.Record, error) {
				value, err := s.adminService.GetAccount(ctx, id)
				return AccountRecordView(value), err
			}
		}
		if s.openAITokenProvider != nil {
			options.Token = func(ctx context.Context, value *quotaAccount.Record) (string, error) {
				return s.openAITokenProvider.GetAccessToken(ctx, AccountFromRecord(value))
			}
		}
		if s.accountRepo != nil {
			options.SaveExtra = s.accountRepo.UpdateExtra
		}
		s.core = quotaAccount.NewOpenAIQuotaService(options)
	})
	return s.core
}
func (s *OpenAIQuotaService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.Core().StopContext(ctx)
}
