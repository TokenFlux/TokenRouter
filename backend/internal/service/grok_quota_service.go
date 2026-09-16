package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/config"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const (
	grokQuotaUpstreamTimeout = 20 * time.Second
	grokQuotaProbeInput      = "hi"
	grokQuotaDefaultModel    = grokDefaultResponsesModel
	grokBillingExtraKey      = accountcore.GrokUsageBillingExtraKey
	grokBillingMaxAttempts   = 2
	grokBillingRetryDelay    = 100 * time.Millisecond
)

type GrokQuotaProbeResult = accountcore.GrokQuotaProbeResult

type GrokQuotaResetResult = accountcore.GrokQuotaResetResult

type GrokQuotaService struct {
	coreOnce       sync.Once
	core           *accountcore.GrokQuotaService
	accountRepo    AccountRepository
	proxyRepo      ProxyRepository
	tokenProvider  *GrokTokenProvider
	httpUpstream   HTTPUpstream
	usageLogRepo   UsageLogRepository
	settingService *SettingService
	cfg            *config.Config
	probeRuntime   accountcore.ProbeRuntime
}

func NewGrokQuotaService(
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	tokenProvider *GrokTokenProvider,
	httpUpstream HTTPUpstream,
	cfg *config.Config,
	usageLogRepos ...UsageLogRepository,
) *GrokQuotaService {
	var usageLogRepo UsageLogRepository
	if len(usageLogRepos) > 0 {
		usageLogRepo = usageLogRepos[0]
	}
	return &GrokQuotaService{
		accountRepo:   accountRepo,
		proxyRepo:     proxyRepo,
		tokenProvider: tokenProvider,
		httpUpstream:  httpUpstream,
		usageLogRepo:  usageLogRepo,
		cfg:           cfg,
	}
}

// SetSettingService 注入 Grok 系统级默认上游策略。
func (s *GrokQuotaService) SetSettingService(settingService *SettingService) {
	if s != nil {
		s.settingService = settingService
	}
}

func (s *GrokQuotaService) QueryQuota(ctx context.Context, accountID int64) (*GrokQuotaProbeResult, error) {
	return s.Core().QueryQuota(ctx, accountID)
}

func grokBillingHasAuthoritativeQuota(billing *xai.BillingSummary) bool {
	return accountcore.GrokBillingHasAuthoritativeQuota(billing)
}

func (s *GrokQuotaService) ProbeUsage(ctx context.Context, accountID int64) (*GrokQuotaProbeResult, error) {
	return s.Core().ProbeUsage(ctx, accountID)
}

func (s *GrokQuotaService) ProbeBilling(ctx context.Context, accountID int64) (*GrokQuotaProbeResult, error) {
	return s.Core().ProbeBilling(ctx, accountID)
}

func (s *GrokQuotaService) ProbeMediaEligibility(ctx context.Context, accountID int64) (bool, string, error) {
	return s.Core().ProbeMediaEligibility(ctx, accountID)
}

func (s *GrokQuotaService) runProbeFlight(
	ctx context.Context,
	key string,
	probe func(context.Context) (*GrokQuotaProbeResult, error),
) (*GrokQuotaProbeResult, error) {
	return s.Core().RunProbeFlight(ctx, key, probe)
}

func (s *GrokQuotaService) StopContext(ctx context.Context) error { return s.Core().StopContext(ctx) }

func (s *GrokQuotaService) fetchBilling(
	ctx context.Context,
	account *Account,
	token string,
	proxyURL string,
	weekly bool,
) (*xai.BillingSummary, int, error) {
	billingURL, err := buildGrokBillingURL(account, s.cfg, weekly)
	if err != nil {
		return nil, 0, infraerrors.Newf(http.StatusBadRequest, "GROK_QUOTA_BASE_URL_INVALID", "invalid Grok base_url: %v", err)
	}
	return xai.FetchBilling(ctx, xai.BillingFetchOptions{URL: billingURL, Token: token, AccountID: account.ID, Weekly: weekly, MaxAttempts: grokBillingMaxAttempts, RetryDelay: grokBillingRetryDelay, Do: func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.Do(req, proxyURL, account.ID, maxInt(account.Concurrency, 2))
	}, ApplyHeaders: account.ApplyHeaderOverrides, Truncate: truncate, MapStatus: mapUpstreamStatusCode, Warn: slog.Warn})
}

func (s *GrokQuotaService) ResetQuota(ctx context.Context, accountID int64) (*GrokQuotaResetResult, error) {
	return s.Core().ResetQuota(ctx, accountID)
}

func (s *GrokQuotaService) resolveProxyURL(ctx context.Context, account *Account) string {
	if account == nil || account.ProxyID == nil {
		return ""
	}
	switch {
	case account.Proxy != nil:
		return account.Proxy.URL()
	case s != nil && s.proxyRepo != nil:
		if proxy, err := s.proxyRepo.GetByID(ctx, *account.ProxyID); err == nil && proxy != nil {
			account.Proxy = proxy
			return proxy.URL()
		}
	}
	return ""
}

func grokQuotaProbeModel() string {
	return grokQuotaDefaultModel
}

func buildGrokQuotaProbeBody(model string) ([]byte, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = grokQuotaDefaultModel
	}
	return json.Marshal(map[string]any{
		"model":  model,
		"input":  grokQuotaProbeInput,
		"stream": true,
	})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *GrokQuotaService) activeQuota(ctx context.Context, account *Account, token, proxyURL, probeModel string, observe func(*xai.QuotaSnapshot, int)) error {
	body, err := buildGrokQuotaProbeBody(probeModel)
	if err != nil {
		return infraerrors.Newf(http.StatusBadRequest, "GROK_QUOTA_PROBE_BODY_ERROR", "failed to build probe body: %v", err)
	}
	targetURL, err := buildGrokResponsesURL(account, s.cfg, s.settingService)
	if err != nil {
		return infraerrors.Newf(http.StatusBadRequest, "GROK_QUOTA_BASE_URL_INVALID", "invalid Grok base_url: %v", err)
	}

	err = xai.FetchActiveQuota(ctx, xai.ActiveQuotaOptions{URL: targetURL, Body: body, Token: token, AccountID: account.ID, Model: probeModel, Timeout: grokQuotaUpstreamTimeout, ApplyHeaders: func(headers http.Header) {
		if account.IsGrokOAuth() {
			applyGrokCLIHeaders(headers)
		}
		account.ApplyHeaderOverrides(headers)
	}, Do: func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.Do(req, proxyURL, account.ID, maxInt(account.Concurrency, 1))
	}, Observe: observe, MapStatus: mapUpstreamStatusCode, Warn: slog.Warn})
	return err
}

// Core 为旧零值测试和旧构造入口建立同一个账号用例，不复制 ProbeRuntime。
func (s *GrokQuotaService) Core() *accountcore.GrokQuotaService {
	if s == nil {
		return nil
	}
	s.coreOnce.Do(func() {
		options := accountcore.GrokQuotaOptions{Timeout: grokQuotaUpstreamTimeout, Available: func() bool { return s.tokenProvider != nil && s.httpUpstream != nil }, Token: func(ctx context.Context, v *accountcore.Record) (string, error) {
			return s.tokenProvider.GetAccessToken(ctx, AccountFromRecord(v))
		}, ResolveProxy: func(ctx context.Context, v *accountcore.Record) string {
			legacy := AccountFromRecord(v)
			proxy := s.resolveProxyURL(ctx, legacy)
			if legacy != nil && v != nil {
				v.Proxy = AccountRecordView(legacy).Proxy
			}
			return proxy
		}, Active: func(ctx context.Context, v *accountcore.Record, token, proxy, model string, observe func(*xai.QuotaSnapshot, int)) error {
			return s.activeQuota(ctx, AccountFromRecord(v), token, proxy, model, observe)
		}, Billing: func(ctx context.Context, v *accountcore.Record, token, proxy string, weekly bool) (*xai.BillingSummary, int, error) {
			return s.fetchBilling(ctx, AccountFromRecord(v), token, proxy, weekly)
		}, ProbeModel: grokQuotaProbeModel, ScheduleModels: func(v *accountcore.Record) { s.scheduleGrokObservedModelsSync(AccountFromRecord(v)) }, MergeBilling: xai.MergeBillingProbeResult, StampBilling: xai.StampBillingSummary, StampQuota: func(v *accountcore.Record, snapshot *xai.QuotaSnapshot, model string) {
			stampGrokQuotaSnapshotForPlan(AccountFromRecord(v), snapshot, model)
		}, ResetAt: accountcore.GrokRateLimitResetAtForAccount, NormalizeResets: accountcore.NormalizeGrokExhaustedWindowResets, PersistLimit: func(ctx context.Context, v *accountcore.Record, until time.Time) {
			persistGrokRateLimit(ctx, s.accountRepo, AccountFromRecord(v), until)
		}, CanRecover: accountcore.IsSuccessfulGrokRateLimitRecovery, ClearLimit: func(ctx context.Context, v *accountcore.Record) {
			clearGrokRateLimitAfterRecovery(ctx, s.accountRepo, AccountFromRecord(v))
		}, MediaEligibility: func(v *accountcore.Record) (bool, string) {
			return AccountFromRecord(v).GrokMediaGenerationEligibility()
		}, LocalStats: func(ctx context.Context, id int64, billing *xai.BillingSummary, now time.Time) (*WindowStats, *WindowStats, *WindowStats) {
			return grokLocalUsageForQuota(ctx, s.usageLogRepo, id, billing, now)
		}, MapStatus: mapUpstreamStatusCode, Warn: slog.Warn}
		if s.accountRepo != nil {
			options.GetAccount = func(ctx context.Context, id int64) (*accountcore.Record, error) {
				v, err := s.accountRepo.GetByID(ctx, id)
				return AccountRecordView(v), err
			}
			options.UpdateExtra = s.accountRepo.UpdateExtra
		}
		s.core = accountcore.NewGrokQuotaService(options, &s.probeRuntime)
	})
	return s.core
}
