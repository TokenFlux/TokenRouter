package admin

import (
	"context"
	"log/slog"
	"sync"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

const (
	grokImportProbeConcurrency = 3
	grokImportProbeTimeout     = 25 * time.Second
	grokImportProbeQueueLimit  = 64
)

type grokImportProber interface {
	QueryQuota(ctx context.Context, accountID int64) (*service.GrokQuotaProbeResult, error)
}

type grokImportProbeTask struct {
	prober    grokImportProber
	accountID int64
}

type grokImportProbeScheduler struct {
	mu          sync.Mutex
	queue       []grokImportProbeTask
	pending     map[int64]struct{}
	inFlight    map[int64]struct{}
	concurrency int
	workers     int
	maxWorkers  int
	timeout     time.Duration
}

var defaultGrokImportProbeScheduler = newGrokImportProbeScheduler(
	grokImportProbeConcurrency,
	grokImportProbeTimeout,
)

func newGrokImportProbeScheduler(concurrency int, timeout time.Duration) *grokImportProbeScheduler {
	if concurrency <= 0 {
		concurrency = 1
	}
	if timeout <= 0 {
		timeout = grokImportProbeTimeout
	}
	return &grokImportProbeScheduler{
		concurrency: concurrency,
		timeout:     timeout,
		pending:     make(map[int64]struct{}),
		inFlight:    make(map[int64]struct{}),
	}
}

func (s *grokImportProbeScheduler) schedule(prober grokImportProber, account *service.Account) {
	if s == nil || prober == nil || account == nil || account.ID <= 0 {
		return
	}
	if account.Platform != service.PlatformGrok || account.Type != service.AccountTypeOAuth {
		return
	}

	s.mu.Lock()
	if _, exists := s.pending[account.ID]; exists {
		s.mu.Unlock()
		return
	}
	if _, exists := s.inFlight[account.ID]; exists {
		s.mu.Unlock()
		return
	}
	if len(s.queue) >= grokImportProbeQueueLimit {
		s.mu.Unlock()
		slog.Debug("grok_import_active_probe_dropped", "account_id", account.ID, "reason", "queue_full")
		return
	}
	s.queue = append(s.queue, grokImportProbeTask{prober: prober, accountID: account.ID})
	s.pending[account.ID] = struct{}{}
	if s.workers < s.concurrency {
		s.workers++
		if s.workers > s.maxWorkers {
			s.maxWorkers = s.workers
		}
		go s.worker()
	}
	s.mu.Unlock()
}

func (s *grokImportProbeScheduler) worker() {
	for {
		task, ok := s.nextTask()
		if !ok {
			return
		}
		s.run(task.prober, task.accountID)
		s.finish(task.accountID)
	}
}

func (s *grokImportProbeScheduler) nextTask() (grokImportProbeTask, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		s.workers--
		return grokImportProbeTask{}, false
	}
	task := s.queue[0]
	s.queue[0] = grokImportProbeTask{}
	s.queue = s.queue[1:]
	if len(s.queue) == 0 {
		s.queue = nil
	}
	delete(s.pending, task.accountID)
	s.inFlight[task.accountID] = struct{}{}
	return task, true
}

func (s *grokImportProbeScheduler) finish(accountID int64) {
	s.mu.Lock()
	delete(s.inFlight, accountID)
	s.mu.Unlock()
}

func (s *grokImportProbeScheduler) run(prober grokImportProber, accountID int64) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error(
				"grok_import_active_probe_panic",
				"account_id", accountID,
				"recovery_type", panicType(recovered),
			)
		}
	}()

	// 排队时间不计入超时，确保每个导入账号都会执行探测；该超时只限制实际的上游请求。
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	result, err := prober.QueryQuota(ctx, accountID)
	if err != nil {
		slog.Warn(
			"grok_import_active_probe_failed",
			"account_id", accountID,
			"status", infraerrors.Code(err),
			"reason", infraerrors.Reason(err),
		)
		return
	}
	if result == nil {
		slog.Warn(
			"grok_import_active_probe_failed",
			"account_id", accountID,
			"reason", "empty_result",
		)
		return
	}

	slog.Info(
		"grok_import_active_probe_completed",
		"account_id", accountID,
		"model", result.Model,
		"status", result.StatusCode,
		"headers_observed", result.HeadersObserved,
	)
}

func panicType(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case error:
		return "error"
	default:
		return "unknown"
	}
}

func (h *AccountHandler) scheduleGrokImportProbe(account *service.Account) {
	if h == nil {
		return
	}
	defaultGrokImportProbeScheduler.schedule(h.grokImportProber, account)
}

func (h *GrokOAuthHandler) scheduleGrokImportProbe(account *service.Account) {
	if h == nil {
		return
	}
	defaultGrokImportProbeScheduler.schedule(h.importProber, account)
}

// ProvideAccountHandler 为生产环境注入 Grok 主动探测器，同时保留便于单元测试的构造函数。
func ProvideAccountHandler(
	adminService service.AdminService,
	settingService *service.SettingService,
	oauthService *service.OAuthService,
	openaiOAuthService *service.OpenAIOAuthService,
	geminiOAuthService *service.GeminiOAuthService,
	antigravityOAuthService *service.AntigravityOAuthService,
	grokOAuthService service.GrokOAuthTokenService,
	rateLimitService *service.RateLimitService,
	accountUsageService *service.AccountUsageService,
	accountTestService *service.AccountTestService,
	concurrencyService *service.ConcurrencyService,
	gatewayService *service.GatewayService,
	openAIGatewayService *service.OpenAIGatewayService,
	crsSyncService *service.CRSSyncService,
	sessionLimitCache service.SessionLimitCache,
	rpmCache service.RPMCache,
	tokenCacheInvalidator service.TokenCacheInvalidator,
	grokQuotaService *service.GrokQuotaService,
) *AccountHandler {
	handler := NewAccountHandler(
		adminService,
		settingService,
		oauthService,
		openaiOAuthService,
		geminiOAuthService,
		antigravityOAuthService,
		rateLimitService,
		accountUsageService,
		accountTestService,
		concurrencyService,
		crsSyncService,
		sessionLimitCache,
		rpmCache,
		tokenCacheInvalidator,
		grokOAuthService,
	)
	handler.grokImportProber = grokQuotaService
	diagnostics := service.NewAdvancedSchedulerScoreDiagnosticService(adminService, concurrencyService, rateLimitService)
	diagnostics.SetSchedulingServices(gatewayService, openAIGatewayService)
	handler.SetAdvancedSchedulerScoreDiagnosticService(diagnostics)
	return handler
}
