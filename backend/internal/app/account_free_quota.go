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

// bindAccountFreeQuota 只投影配置与统计端口，缓存和裁决由账号模块唯一实现。
func bindAccountFreeQuota(cfg *config.Config, reader usage.UsageLogRepository, tasks *lifecycle.Tasks, gateway interface {
	BindFreeQuotaGate(*account.FreeQuotaGate)
}, openai interface {
	BindFreeQuotaGates(*account.FreeQuotaGate, func() *account.FreeQuotaGate)
}) {
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
	gateway.BindFreeQuotaGate(factory())
	openai.BindFreeQuotaGates(factory(), factory)
}
