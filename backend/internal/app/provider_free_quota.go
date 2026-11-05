package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// provider_free_quota.go constructs separate free-quota gates for generic,
// compatible, and advanced selection. Free-quota decisions are cached per
// selection scope, so loading usage into one gate does not populate another
// gate's cache. Construction gives generic and compatible selection separate
// gates and advanced selection a factory, with usage loading, task tracking,
// and observations shared among them.

// selectionFreeQuotaGates supplies separately cached gates and an advanced-selection gate factory.
type selectionFreeQuotaGates struct {
	// Generic, Compatible, and gates returned by Advanced share the options
	// getter, usage loader, task tracker, and metrics. Each gate owns its cache
	// and in-flight refresh state; every Advanced call constructs a fresh gate.
	Generic    *provider.FreeQuotaGate        // generic selection quota
	Compatible *provider.FreeQuotaGate        // compatible selection quota
	Advanced   func() *provider.FreeQuotaGate // advanced gate factory
}

// provideSelectionFreeQuota constructs independent quota gates using cfg and
// the usage reader. The gates use tasks.Go for on-demand refresh work. Nil cfg
// supplies disabled settings, and a nil reader leaves loading unset; either
// case lets gate checks return no blocked providers. Supply the application
// task tracker to own requested refreshes and coordinate cfg writes with gate
// checks because the options getter retains cfg.
func provideSelectionFreeQuota(cfg *config.Config, reader usage.UsageLogRepository, tasks *lifecycle.Tasks) *selectionFreeQuotaGates {
	metrics := &provider.FreeQuotaMetrics{}
	options := func() provider.FreeQuotaOptions {
		if cfg == nil {
			return provider.FreeQuotaOptions{}
		}
		v := cfg.Scheduling.GrokFreeQuota
		return provider.FreeQuotaOptions{
			Enabled: v.Enabled, TokenLimit: v.TokenLimit,
			Percent: v.ThresholdPercent, WindowHours: v.WindowHours, CacheSeconds: v.StatsCacheSeconds,
		}
	}
	var load func(context.Context, []int64, time.Time) (map[int64]int64, error)
	if reader != nil {
		load = func(ctx context.Context, ids []int64, start time.Time) (map[int64]int64, error) {
			return usage.ReadProviderTokenWindow(ctx, reader, ids, start)
		}
	}
	factory := func() *provider.FreeQuotaGate {
		return provider.NewFreeQuotaGate(options, load, tasks.Go, time.Now, func(failed bool, message string, fields ...any) {
			if failed {
				slog.Warn(message, fields...)
			} else {
				slog.Info(message, fields...)
			}
		}, metrics)
	}
	return &selectionFreeQuotaGates{Generic: factory(), Compatible: factory(), Advanced: factory}
}
