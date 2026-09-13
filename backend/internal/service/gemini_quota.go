// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	usagestats "github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	log "log"
	time "time"
)

type GeminiTierPolicy = accountcore.GeminiTierPolicy
type GeminiQuotaPolicy struct{ *accountcore.GeminiQuotaPolicy }
type GeminiQuotaService struct {
	core *accountcore.GeminiQuotaService
}

// NewGeminiQuotaService 只兼容旧独立构造；生产由 app 投影静态参数。
func NewGeminiQuotaService(cfg *config.Config, repo SettingRepository) *GeminiQuotaService {
	options := accountcore.GeminiQuotaOptions{Now: time.Now, Log: log.Printf, NotFound: ErrSettingNotFound}
	if cfg != nil {
		options.StaticTiers = legacyGeminiTierOverrides(cfg.Gemini.Quota.Tiers)
		options.StaticPolicy = cfg.Gemini.Quota.Policy
	}
	if repo != nil {
		options.LoadPolicy = func(ctx context.Context) (string, error) { return repo.GetValue(ctx, SettingKeyGeminiQuotaPolicy) }
	}
	return WrapGeminiQuotaService(accountcore.NewGeminiQuotaService(options))
}
func WrapGeminiQuotaService(core *accountcore.GeminiQuotaService) *GeminiQuotaService {
	return &GeminiQuotaService{core: core}
}
func (s *GeminiQuotaService) Core() *accountcore.GeminiQuotaService {
	if s == nil {
		return nil
	}
	return s.core
}
func (s *GeminiQuotaService) Policy(ctx context.Context) *GeminiQuotaPolicy {
	return &GeminiQuotaPolicy{s.Core().Policy(ctx)}
}
func (s *GeminiQuotaService) QuotaForAccount(ctx context.Context, v *Account) (GeminiQuota, bool) {
	return s.Core().QuotaForAccount(ctx, AccountRecordView(v))
}
func (s *GeminiQuotaService) CooldownForTier(ctx context.Context, tier string) time.Duration {
	return s.Core().CooldownForTier(ctx, tier)
}
func (s *GeminiQuotaService) CooldownForAccount(ctx context.Context, v *Account) time.Duration {
	return s.Core().CooldownForAccount(ctx, AccountRecordView(v))
}
func legacyGeminiTierOverrides(values map[string]config.GeminiTierQuotaConfig) map[string]accountcore.GeminiTierQuotaOverride {
	if values == nil {
		return nil
	}
	out := make(map[string]accountcore.GeminiTierQuotaOverride, len(values))
	for id, v := range values {
		out[id] = accountcore.GeminiTierQuotaOverride{ProRPD: v.ProRPD, FlashRPD: v.FlashRPD, CooldownMinutes: v.CooldownMinutes}
	}
	return out
}
func (p *GeminiQuotaPolicy) ApplyOverrides(values map[string]config.GeminiTierQuotaConfig) {
	if p == nil {
		return
	}
	p.GeminiQuotaPolicy.ApplyOverrides(legacyGeminiTierOverrides(values))
}

func geminiCooldownForTier(id string) time.Duration { return accountcore.GeminiCooldownForTier(id) }

// GeminiQuota 复用账号核心的纯值。
type GeminiQuota = accountcore.GeminiQuota

// GeminiUsageTotals 复用账号核心的纯值。
type GeminiUsageTotals = accountcore.GeminiUsageTotals

func geminiAggregateUsage(stats []usagestats.ModelStat) GeminiUsageTotals {
	values := make([]accountcore.GeminiModelUsage, len(stats))
	for i, v := range stats {
		values[i] = accountcore.GeminiModelUsage{Model: v.Model, Requests: v.Requests, TotalTokens: v.TotalTokens, AccountCost: v.AccountCost}
	}
	return accountcore.AggregateGeminiUsage(values)
}

func geminiQuotaLocation() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PST", -8*3600)
	}
	return loc
}

func geminiDailyResetTime(now time.Time) time.Time {
	return accountcore.GeminiDailyResetTime(now, geminiQuotaLocation())
}
