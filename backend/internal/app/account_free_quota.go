package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// selectionFreeQuotaGates 保留两条普通选择链与高级选择器各自的缓存作用域。
type selectionFreeQuotaGates struct {
	Generic    *account.FreeQuotaGate
	Compatible *account.FreeQuotaGate
	Advanced   func() *account.FreeQuotaGate
}

// provideSelectionFreeQuota 只投影原配置和用量来源，所有缓存及后台任务仍归原生拥有者。
func provideSelectionFreeQuota(cfg *config.Config, reader usage.UsageLogRepository, tasks *lifecycle.Tasks) *selectionFreeQuotaGates {
	metrics := &account.FreeQuotaMetrics{}
	options := func() account.FreeQuotaOptions {
		if cfg == nil {
			return account.FreeQuotaOptions{}
		}
		v := cfg.Gateway.Grok
		return account.FreeQuotaOptions{Enabled: v.FreeQuotaSoftGateEnabled, TokenLimit: v.FreeQuotaTokenLimit,
			Percent: v.FreeQuotaSoftGatePercent, WindowHours: v.FreeQuotaWindowHours, CacheSeconds: v.FreeQuotaStatsCacheSeconds}
	}
	var load func(context.Context, []int64, time.Time) (map[int64]int64, error)
	if reader != nil {
		load = func(ctx context.Context, ids []int64, start time.Time) (map[int64]int64, error) {
			return usage.ReadAccountTokenWindow(ctx, reader, ids, start)
		}
	}
	factory := func() *account.FreeQuotaGate {
		return account.NewFreeQuotaGate(options, load, tasks.Go, time.Now, func(failed bool, message string, fields ...any) {
			if failed {
				slog.Warn(message, fields...)
			} else {
				slog.Info(message, fields...)
			}
		}, metrics)
	}
	// 两条普通选择链不合并缓存；高级调度器仍逐实例取得独立缓存。
	return &selectionFreeQuotaGates{Generic: factory(), Compatible: factory(), Advanced: factory}
}
