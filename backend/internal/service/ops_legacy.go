// 旧构造入口只投影，不保留 Ops 规则、缓存或 worker。
package service

import (
	"context"
	"database/sql"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"go.uber.org/zap"
)

func LegacyOpsOptions(cfg *config.Config) *ops.Options {
	if cfg == nil {
		return nil
	}
	o := &ops.Options{RunMode: cfg.RunMode, Timezone: cfg.Timezone, IsNotFound: func(e error) bool { return errors.Is(e, sql.ErrNoRows) || errors.Is(e, ops.ErrRowNotFound) }, Logf: func(f string, a ...any) { logger.LegacyPrintf("service.ops", f, a...) }}
	o.CleanupCompleted = func(counts string) {
		logger.L().Info("[OpsCleanup] cleanup complete", zap.String("component", "service.ops_cleanup"), zap.String("deleted_counts", counts))
	}
	o.Ops.Enabled = cfg.Ops.Enabled
	o.Ops.Aggregation.Enabled = cfg.Ops.Aggregation.Enabled
	o.Ops.Cleanup = ops.CleanupOptions(cfg.Ops.Cleanup)
	o.Ops.MetricsCollectorCache = ops.MetricsCollectorCacheOptions(cfg.Ops.MetricsCollectorCache)
	o.Database.MaxOpenConns = cfg.Database.MaxOpenConns
	o.Redis.PoolSize = cfg.Redis.PoolSize
	o.Log = ops.LogOptions{Level: cfg.Log.Level, Caller: cfg.Log.Caller, StacktraceLevel: cfg.Log.StacktraceLevel, Sampling: ops.SamplingOptions(cfg.Log.Sampling)}
	return o
}
func NewOpsService(repo OpsRepository, settings SettingRepository, cfg *config.Config, accounts AccountRepository, users UserRepository, concurrency *ConcurrencyService, gateway *GatewayService, openai *OpenAIGatewayService, gemini *GeminiMessagesCompatService, antigravity *AntigravityGatewayService, sink *OpsSystemLogSink) *OpsService {
	var a ops.AccountReader
	if accounts != nil {
		base := legacyOpsAccounts{accounts}
		a = base
		if stats, ok := accounts.(interface {
			ListOpsAccountsForStats(context.Context, string, *int64) ([]Account, error)
		}); ok {
			a = legacyOpsAccountStats{base, stats}
		}
	}
	var u ops.UserReader
	if users != nil {
		u = legacyOpsUsers{users}
	}
	var c ops.ConcurrencyReader
	if concurrency != nil {
		c = concurrency
	}
	return ops.NewOpsService(repo, settings, LegacyOpsOptions(cfg), a, u, c, sink, provider.LogControl{})
}

type legacyOpsAccounts struct{ AccountRepository }

func (r legacyOpsAccounts) ListPage(ctx context.Context, p pagination.PaginationParams, platform string, group int64) ([]ops.AccountObservation, *pagination.PaginationResult, error) {
	a, pg, e := r.ListWithFilters(ctx, p, platform, "", "", "", group, "")
	return legacyOpsAccountViews(a), pg, e
}

type legacyOpsAccountStats struct {
	legacyOpsAccounts
	stats interface {
		ListOpsAccountsForStats(context.Context, string, *int64) ([]Account, error)
	}
}

func (r legacyOpsAccountStats) ListOpsAccountsForStats(ctx context.Context, p string, g *int64) ([]ops.AccountObservation, error) {
	a, e := r.stats.ListOpsAccountsForStats(ctx, p, g)
	return legacyOpsAccountViews(a), e
}
func legacyOpsAccountViews(a []Account) []ops.AccountObservation {
	out := make([]ops.AccountObservation, len(a))
	for i, v := range a {
		out[i] = ops.AccountObservation{ID: v.ID, Name: v.Name, Platform: v.Platform, Status: v.Status, ErrorMessage: v.ErrorMessage, Schedulable: v.Schedulable, Concurrency: v.Concurrency, LoadFactor: v.EffectiveLoadFactor(), TempUnschedulableUntil: v.TempUnschedulableUntil, RateLimitResetAt: v.RateLimitResetAt, OverloadUntil: v.OverloadUntil}
		if v.Groups != nil {
			out[i].Groups = make([]*ops.GroupObservation, len(v.Groups))
			for j, g := range v.Groups {
				if g != nil {
					out[i].Groups[j] = &ops.GroupObservation{ID: g.ID, Name: g.Name, Platform: g.Platform}
				}
			}
		}
	}
	return querycache.Clone(out)
}

type legacyOpsUsers struct{ UserRepository }

func (r legacyOpsUsers) ListActivePage(ctx context.Context, p pagination.PaginationParams) ([]ops.UserObservation, *pagination.PaginationResult, error) {
	users, pg, e := r.ListWithFilters(ctx, p, UserListFilters{Status: StatusActive})
	out := make([]ops.UserObservation, len(users))
	for i, u := range users {
		out[i] = ops.UserObservation{ID: u.ID, Email: u.Email, Username: u.Username, Concurrency: u.Concurrency}
	}
	return out, pg, e
}
