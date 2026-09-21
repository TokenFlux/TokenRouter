package provider

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// GrokQuotaStore 只包含原探测所需的读取、观测字段和限流写入。
type GrokQuotaStore interface {
	GetByID(context.Context, int64) (*account.Record, error)
	UpdateExtra(context.Context, int64, map[string]any) error
	account.GrokRateLimitWriter
}

// NewGrokQuota 组合唯一探测运行时和平台端口，不另建目录缓存或后台轮询。
func NewGrokQuota(store GrokQuotaStore, token *account.GrokTokenSource, requests *GrokQuotaTransport, stats account.LocalUsageStats) *account.GrokQuotaService {
	// 未配置传输时仍保留重置等本地错误入口，不执行网络请求。
	if requests == nil {
		requests = &GrokQuotaTransport{}
	}
	runtime := &account.ProbeRuntime{}
	models := account.GrokModelsOptions{Runtime: runtime, Available: func() bool { return store != nil }, Fetch: requests.FetchModels, Debug: slog.Debug}
	if store != nil {
		models.UpdateExtra = store.UpdateExtra
	}
	if token != nil {
		models.Token = token.GetAccessToken
	}
	options := account.GrokQuotaOptions{
		Timeout: 20 * time.Second, Available: func() bool { return token != nil && requests != nil && requests.Do != nil },
		Token: token.GetAccessToken, ResolveProxy: requests.ResolveProxy, Active: requests.ActiveQuota, Billing: requests.FetchBilling,
		ProbeModel: func() string { return grok.DefaultResponsesModel }, ScheduleModels: func(value *account.Record) { account.ScheduleGrokObservedModels(models, value) },
		MergeBilling: grok.MergeBillingProbeResult, StampBilling: grok.StampBillingSummary,
		StampQuota: func(value *account.Record, snapshot *grok.QuotaSnapshot, model string) {
			account.StampGrokQuotaPlan(value, snapshot, model, grok.ResolveGrokTextResponsesModelID, grok.ApplyGrok45ResponsesPlanSignal)
		},
		ResetAt: account.GrokRateLimitResetAtForAccount, NormalizeResets: account.NormalizeGrokExhaustedWindowResets,
		PersistLimit: func(ctx context.Context, value *account.Record, reset time.Time) {
			account.PersistGrokRateLimit(ctx, store, value, reset, slog.Warn)
		},
		CanRecover: account.IsSuccessfulGrokRateLimitRecovery, ClearLimit: func(ctx context.Context, value *account.Record) {
			account.ClearGrokRateLimitAfterRecovery(ctx, store, value, slog.Warn)
		},
		MediaEligibility: func(value *account.Record) (bool, string) {
			return account.GrokMediaGenerationEligibility(value, GrokTierRules())
		},
		LocalStats: func(ctx context.Context, id int64, billing *grok.BillingSummary, now time.Time) (*account.WindowStats, *account.WindowStats, *account.WindowStats) {
			return account.GrokLocalUsageForQuota(ctx, stats, id, billing, now, slog.Warn)
		},
		MapStatus: requests.MapStatus, Warn: slog.Warn,
	}
	if store != nil {
		options.GetAccount = store.GetByID
		options.UpdateExtra = store.UpdateExtra
	}
	return account.NewGrokQuotaService(options, runtime)
}
