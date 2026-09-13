// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	settings "github.com/TokenFlux/TokenRouter/internal/settings"
	log "log"
	"log/slog"
	time "time"
)

// provideGeminiQuotaPolicy 分开静态配置投影与动态 settings 读取，不构造第二份策略缓存。
func provideGeminiQuotaPolicy(cfg *config.Config, store *settings.Store) *account.GeminiQuotaService {
	tiers := make(map[string]account.GeminiTierQuotaOverride, len(cfg.Gemini.Quota.Tiers))
	for id, v := range cfg.Gemini.Quota.Tiers {
		tiers[id] = account.GeminiTierQuotaOverride{ProRPD: v.ProRPD, FlashRPD: v.FlashRPD, CooldownMinutes: v.CooldownMinutes}
	}
	return account.NewGeminiQuotaService(account.GeminiQuotaOptions{StaticTiers: tiers, StaticPolicy: cfg.Gemini.Quota.Policy, Now: time.Now, Log: log.Printf, NotFound: settings.ErrSettingNotFound, LoadPolicy: func(ctx context.Context) (string, error) {
		return store.GetValue(ctx, account.GeminiQuotaPolicySettingKey)
	}})
}
func provideLegacyGeminiQuota(core *account.GeminiQuotaService) *service.GeminiQuotaService {
	return service.WrapGeminiQuotaService(core)
}

// provideGeminiPrecheck 保留洛杉矶日界与独立日统计缓存，不持有全量配置。
func provideGeminiPrecheck(policy *account.GeminiQuotaService, usage service.UsageLogRepository) *account.GeminiPrecheck {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		location = time.FixedZone("PST", -8*3600)
	}
	return account.NewGeminiPrecheck(policy, legacybridge.GeminiQuotaUsageReader(usage), account.GeminiPrecheckOptions{Now: time.Now, Location: location, Info: slog.Info})
}
