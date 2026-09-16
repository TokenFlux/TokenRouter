// Grok 额度与账单查询用例持有观测合并、状态写入及原共享运行时，供应商 I/O 从端口注入。
package account

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

const GrokQuotaSnapshotExtraKey = "grok_usage_snapshot"

type GrokQuotaOptions struct {
	Timeout          time.Duration
	Available        func() bool
	GetAccount       func(context.Context, int64) (*Record, error)
	UpdateExtra      func(context.Context, int64, map[string]any) error
	Token            func(context.Context, *Record) (string, error)
	ResolveProxy     func(context.Context, *Record) string
	Active           func(context.Context, *Record, string, string, string, func(*usageview.QuotaSnapshot, int)) error
	Billing          func(context.Context, *Record, string, string, bool) (*usageview.BillingSummary, int, error)
	ProbeModel       func() string
	ScheduleModels   func(*Record)
	MergeBilling     func(*usageview.BillingSummary, *usageview.BillingSummary, *usageview.BillingSummary, bool, bool) *usageview.BillingSummary
	StampBilling     func(*usageview.BillingSummary, int, string) *usageview.BillingSummary
	StampQuota       func(*Record, *usageview.QuotaSnapshot, string)
	ResetAt          func(*Record, *usageview.QuotaSnapshot, time.Time) (time.Time, bool)
	NormalizeResets  func(*usageview.QuotaSnapshot, time.Time, time.Time)
	PersistLimit     func(context.Context, *Record, time.Time)
	CanRecover       func(*Record, *usageview.QuotaSnapshot) bool
	ClearLimit       func(context.Context, *Record)
	MediaEligibility func(*Record) (bool, string)
	LocalStats       func(context.Context, int64, *usageview.BillingSummary, time.Time) (*WindowStats, *WindowStats, *WindowStats)
	MapStatus        func(int) int
	Warn             func(string, ...any)
}
type GrokQuotaService struct {
	Options GrokQuotaOptions
	Runtime *ProbeRuntime
}

func NewGrokQuotaService(options GrokQuotaOptions, runtime *ProbeRuntime) *GrokQuotaService {
	return &GrokQuotaService{Options: options, Runtime: runtime}
}

type GrokQuotaProbeResult struct {
	Source            string                    `json:"source"`
	Model             string                    `json:"model,omitempty"`
	Billing           *usageview.BillingSummary `json:"billing,omitempty"`
	Snapshot          *usageview.QuotaSnapshot  `json:"snapshot,omitempty"`
	LocalUsage24h     *WindowStats              `json:"local_usage_24h,omitempty"`
	LocalUsage7d      *WindowStats              `json:"local_usage_7d,omitempty"`
	LocalUsageMonthly *WindowStats              `json:"local_usage_monthly,omitempty"`
	StatusCode        int                       `json:"status_code,omitempty"`
	HeadersObserved   bool                      `json:"headers_observed"`
	ResetSupported    bool                      `json:"reset_supported"`
	FetchedAt         int64                     `json:"fetched_at"`
	Persisted         bool                      `json:"persisted"`
	ProbeError        string                    `json:"probe_error,omitempty"`
}
type GrokQuotaResetResult struct {
	Supported bool   `json:"supported"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

// QueryQuota 合并 xAI billing 数据与主动额度响应头探测；Free 账号的 billing 响应不含 usage_percent。
func (s *GrokQuotaService) QueryQuota(ctx context.Context, accountID int64) (*GrokQuotaProbeResult, error) {
	billingResult, billingErr := s.ProbeBilling(ctx, accountID)
	if billingErr == nil && billingResult != nil && GrokBillingHasAuthoritativeQuota(billingResult.Billing) {
		if acc, err := s.Options.GetAccount(ctx, accountID); err == nil {
			s.Options.ScheduleModels(acc)
		}
		return billingResult, nil
	}

	probeResult, probeErr := s.ProbeUsage(ctx, accountID)
	if probeErr != nil {
		if billingResult != nil && billingResult.Billing != nil {
			billingResult.ProbeError = probeErr.Error()
			return billingResult, nil
		}
		return nil, probeErr
	}
	if probeResult == nil {
		if billingErr != nil {
			return nil, billingErr
		}
		return nil, infraerrors.New(infraerrors.Category(502), "GROK_QUOTA_PROBE_EMPTY", "Grok quota probe returned no result")
	}
	if billingResult != nil {
		probeResult.Source = "hybrid_probe"
		probeResult.Billing = billingResult.Billing
		probeResult.LocalUsage24h = billingResult.LocalUsage24h
		probeResult.LocalUsage7d = billingResult.LocalUsage7d
		probeResult.LocalUsageMonthly = billingResult.LocalUsageMonthly
		probeResult.Persisted = probeResult.Persisted || billingResult.Persisted
	}
	if acc, err := s.Options.GetAccount(ctx, accountID); err == nil {
		s.Options.ScheduleModels(acc)
	}
	return probeResult, nil
}
func (s *GrokQuotaService) ProbeUsage(ctx context.Context, accountID int64) (*GrokQuotaProbeResult, error) {
	return s.RunProbeFlight(ctx, "active:"+strconv.FormatInt(accountID, 10), func(sharedCtx context.Context) (*GrokQuotaProbeResult, error) {
		return s.ProbeUsageDirect(sharedCtx, accountID)
	})
}
func (s *GrokQuotaService) ProbeUsageDirect(ctx context.Context, accountID int64) (*GrokQuotaProbeResult, error) {
	account, token, proxyURL, err := s.PrepareProbe(ctx, accountID)
	if err != nil {
		return nil, err
	}
	probeModel := s.Options.ProbeModel()
	var result *GrokQuotaProbeResult
	err = s.Options.Active(ctx, account, token, proxyURL, probeModel, func(snapshot *usageview.QuotaSnapshot, statusCode int) {

		s.Options.StampQuota(account, snapshot, probeModel)
		resetAt, limited := s.Options.ResetAt(account, snapshot, time.Now())
		if limited {
			s.Options.NormalizeResets(snapshot, resetAt, time.Now())
		}
		// 探测失败不能覆盖此前观测到的快照。401/403 以及传输或服务端错误通常不带额度响应头，
		// 只有成功响应或带有效限流响应头的 429 才适合持久化。成功但不带响应头的 200 仍会记录为
		// 明确的“无响应头”观测，使界面能够区分这种情况与从未探测。
		persistErr := error(nil)
		persisted := false
		shouldPersist := statusCode < 400 || statusCode == 429
		if shouldPersist && (snapshot.HeadersObserved || statusCode == 200) {
			persistErr = s.Options.UpdateExtra(ctx, account.ID, map[string]any{
				GrokQuotaSnapshotExtraKey: snapshot,
			})
			persisted = persistErr == nil
		}
		if limited {
			s.Options.PersistLimit(ctx, account, resetAt)
		} else if s.Options.CanRecover(account, snapshot) {
			s.Options.ClearLimit(ctx, account)
		}

		result = &GrokQuotaProbeResult{

			Source: "active_probe",

			Model: probeModel,

			Snapshot: snapshot,

			StatusCode: statusCode,

			HeadersObserved: snapshot.HeadersObserved,

			ResetSupported: false,

			FetchedAt: time.Now().Unix(),

			Persisted: persisted,
		}
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ProbeBilling 只调用 xAI billing 端点，账号用量刷新使用该方法，避免打开账号列表时消耗模型额度。
func (s *GrokQuotaService) ProbeBilling(ctx context.Context, accountID int64) (*GrokQuotaProbeResult, error) {
	return s.RunProbeFlight(ctx, "billing:"+strconv.FormatInt(accountID, 10), func(sharedCtx context.Context) (*GrokQuotaProbeResult, error) {
		return s.ProbeBillingDirect(sharedCtx, accountID)
	})
}

// ProbeMediaEligibility 刷新计费状态，并按媒体调度使用的持久化账号快照重新判断资格。
// 探测失败时保持拒绝；禁止访问或 Free 等确定状态作为普通的不合格结果返回。
func (s *GrokQuotaService) ProbeMediaEligibility(ctx context.Context, accountID int64) (bool, string, error) {
	_, probeErr := s.ProbeBilling(ctx, accountID)
	account, err := s.LoadGrokOAuthAccount(ctx, accountID)
	if err != nil {
		return false, "billing_probe_failed", err
	}
	eligible, reason := s.Options.MediaEligibility(account)
	if reason == "billing_unobserved" && probeErr != nil {
		return false, reason, probeErr
	}
	return eligible, reason, nil
}
func (s *GrokQuotaService) ProbeBillingDirect(ctx context.Context, accountID int64) (*GrokQuotaProbeResult, error) {
	account, token, proxyURL, err := s.PrepareProbe(ctx, accountID)
	if err != nil {
		return nil, err
	}

	probeCtx, cancel := context.WithTimeout(ctx, s.Options.Timeout)
	defer cancel()
	type billingResult struct {
		summary *usageview.BillingSummary
		status  int
		err     error
	}
	var weekly, monthly billingResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		weekly.summary, weekly.status, weekly.err = s.Options.Billing(probeCtx, account, token, proxyURL, true)
	}()
	go func() {
		defer wg.Done()
		monthly.summary, monthly.status, monthly.err = s.Options.Billing(probeCtx, account, token, proxyURL, false)
	}()
	wg.Wait()

	weeklyOK := weekly.summary != nil
	monthlyOK := monthly.summary != nil
	previous, _ := ParseGrokBillingSnapshot(account.Extra)
	if !weeklyOK && !monthlyOK {
		probeErr := s.MergeGrokBillingProbeErrors(weekly.status, monthly.status, weekly.err, monthly.err)
		billing := s.Options.MergeBilling(previous, nil, nil, false, false)
		if billing == nil {
			billing = &usageview.BillingSummary{Partial: true, FailedWindows: []string{"weekly", "monthly"}}
		}
		billing.WeeklyStatusCode = weekly.status
		billing.MonthlyStatusCode = monthly.status
		billing = s.Options.StampBilling(billing, s.PreferBillingObservationStatus(weekly.status, monthly.status), "billing_probe")
		if persistErr := s.Options.UpdateExtra(ctx, account.ID, map[string]any{GrokUsageBillingExtraKey: billing}); persistErr != nil {
			s.Options.Warn("grok_billing_failure_persist_failed", "account_id", account.ID, "error", persistErr)
		}
		return nil, probeErr
	}
	statusCode := s.PreferSuccessfulBillingStatus(weekly.status, monthly.status, weeklyOK, monthlyOK)
	billing := s.Options.MergeBilling(previous, weekly.summary, monthly.summary, weeklyOK, monthlyOK)
	billing.WeeklyStatusCode = weekly.status
	billing.MonthlyStatusCode = monthly.status
	billing = s.Options.StampBilling(billing, statusCode, "billing_probe")
	persistErr := s.Options.UpdateExtra(ctx, account.ID, map[string]any{
		GrokUsageBillingExtraKey: billing,
	})
	if persistErr != nil {
		s.Options.Warn("grok_billing_persist_failed", "account_id", account.ID, "error", persistErr)
	}
	now := time.Now().UTC()
	localUsage24h, localUsage7d, localUsageMonthly := s.Options.LocalStats(ctx, account.ID, billing, now)
	return &GrokQuotaProbeResult{

		Source: "billing_probe",

		Billing: billing,

		LocalUsage24h: localUsage24h,

		LocalUsage7d: localUsage7d,

		LocalUsageMonthly: localUsageMonthly,

		StatusCode: statusCode,

		FetchedAt: now.Unix(),

		Persisted: persistErr == nil,
	}, nil
}

// PreferBillingObservationStatus 汇总失败探测状态，优先保留可确定媒体资格的 403。
func (s *GrokQuotaService) PreferBillingObservationStatus(weeklyStatus, monthlyStatus int) int {
	if weeklyStatus == 403 || monthlyStatus == 403 {
		return 403
	}
	if weeklyStatus != 0 {
		return weeklyStatus
	}
	return monthlyStatus
}
func (s *GrokQuotaService) RunProbeFlight(
	ctx context.Context,
	key string,
	probe func(context.Context) (*GrokQuotaProbeResult, error),
) (*GrokQuotaProbeResult, error) {
	if s == nil {
		return nil, infraerrors.New(infraerrors.Category(500), "GROK_QUOTA_NOT_CONFIGURED", "grok quota service is not configured")
	}
	value, err := s.Runtime.Run(ctx, key, s.Options.Timeout+5*time.Second, func(shared context.Context) (any, error) {
		return probe(shared)
	})
	if err != nil {
		return nil, err
	}
	result, ok := value.(*GrokQuotaProbeResult)
	if !ok || result == nil {
		return nil, infraerrors.New(infraerrors.Category(500), "GROK_QUOTA_PROBE_RESULT_INVALID", "invalid Grok quota probe result")
	}
	return CloneGrokQuotaProbeResult(result), nil
}

// StopContext 等待共享探测与模型同步；供应商协议仍留此适配入口，S09 退出。
func (s *GrokQuotaService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.Runtime.StopContext(ctx)
}
func (s *GrokQuotaService) MergeGrokBillingProbeErrors(weeklyStatus, monthlyStatus int, weeklyErr, monthlyErr error) error {
	weeklyKey := s.GrokBillingProbeErrorKey(weeklyStatus, weeklyErr)
	monthlyKey := s.GrokBillingProbeErrorKey(monthlyStatus, monthlyErr)
	if weeklyKey == monthlyKey {
		switch {
		case weeklyErr != nil:
			return weeklyErr
		case monthlyErr != nil:
			return monthlyErr
		case weeklyStatus == 429:
			return infraerrors.New(infraerrors.Category(429), "GROK_QUOTA_PROBE_UPSTREAM_ERROR", "billing rate limited")
		case weeklyStatus != 0 && weeklyStatus != 200:
			return infraerrors.New(infraerrors.Category(s.Options.MapStatus(weeklyStatus)), "GROK_QUOTA_PROBE_UPSTREAM_ERROR", "xAI billing endpoints returned the same upstream error")
		default:
			return infraerrors.New(infraerrors.Category(502), "GROK_QUOTA_BILLING_EMPTY", "xAI billing endpoints returned no quota data")
		}
	}
	s.Options.Warn("grok_quota_probe_parts_failed", "weekly_status", weeklyStatus, "weekly_error", weeklyErr, "monthly_status", monthlyStatus, "monthly_error", monthlyErr)
	return infraerrors.New(infraerrors.Category(502), "GROK_QUOTA_PROBE_PARTS_FAILED", "weekly and monthly billing probes failed differently").WithMetadata(map[string]string{
		"weekly_status": strconv.Itoa(weeklyStatus), "monthly_status": strconv.Itoa(monthlyStatus),
	})
}
func (s *GrokQuotaService) GrokBillingProbeErrorKey(status int, err error) string {
	if err != nil {
		return strconv.Itoa(status) + ":" + strconv.Itoa(int(infraerrors.CategoryOf(err))) + ":" + infraerrors.Reason(err)
	}
	return strconv.Itoa(status) + ":empty"
}
func (s *GrokQuotaService) PreferSuccessfulBillingStatus(weeklyStatus, monthlyStatus int, weeklyOK, monthlyOK bool) int {
	if weeklyOK && weeklyStatus >= 200 && weeklyStatus < 300 {
		return weeklyStatus
	}
	if monthlyOK && monthlyStatus >= 200 && monthlyStatus < 300 {
		return monthlyStatus
	}
	if weeklyStatus != 0 {
		return weeklyStatus
	}
	return monthlyStatus
}
func (s *GrokQuotaService) ResetQuota(ctx context.Context, accountID int64) (*GrokQuotaResetResult, error) {
	if _, err := s.LoadGrokOAuthAccount(ctx, accountID); err != nil {
		return nil, err
	}
	return nil, infraerrors.New(infraerrors.Category(501), "GROK_QUOTA_RESET_UNSUPPORTED", "xAI does not expose a Grok subscription quota reset endpoint for OAuth accounts")
}
func (s *GrokQuotaService) PrepareProbe(ctx context.Context, accountID int64) (*Record, string, string, error) {
	if s == nil || s.Options.Available == nil || !s.Options.Available() {
		return nil, "", "", infraerrors.New(infraerrors.Category(500), "GROK_QUOTA_NOT_CONFIGURED", "grok quota service is not configured")
	}
	account, err := s.LoadGrokOAuthAccount(ctx, accountID)
	if err != nil {
		return nil, "", "", err
	}
	proxyURL := s.Options.ResolveProxy(ctx, account)

	token, err := s.Options.Token(ctx, account)
	if err != nil {
		return nil, "", "", infraerrors.Newf(infraerrors.Category(502), "GROK_QUOTA_TOKEN_UNAVAILABLE", "failed to acquire access token: %v", err)
	}
	if strings.TrimSpace(token) == "" {
		return nil, "", "", infraerrors.New(infraerrors.Category(502), "GROK_QUOTA_TOKEN_UNAVAILABLE", "access token is empty")
	}

	return account, token, proxyURL, nil
}
func (s *GrokQuotaService) LoadGrokOAuthAccount(ctx context.Context, accountID int64) (*Record, error) {
	if s == nil || s.Options.GetAccount == nil {
		return nil, infraerrors.New(infraerrors.Category(500), "GROK_QUOTA_NOT_CONFIGURED", "grok quota service is not configured")
	}
	account, err := s.Options.GetAccount(ctx, accountID)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(404), "GROK_QUOTA_ACCOUNT_NOT_FOUND", "account not found: %v", err)
	}
	if account == nil {
		return nil, infraerrors.New(infraerrors.Category(404), "GROK_QUOTA_ACCOUNT_NOT_FOUND", "account not found")
	}
	if account.Platform != PlatformGrok {
		return nil, infraerrors.New(infraerrors.Category(400), "GROK_QUOTA_INVALID_PLATFORM", "account is not a Grok account")
	}
	if account.Type != AccountTypeOAuth {
		return nil, infraerrors.New(infraerrors.Category(400), "GROK_QUOTA_INVALID_TYPE", "account is not an OAuth account")
	}
	return account, nil
}
