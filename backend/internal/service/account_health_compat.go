// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	slog "log/slog"
	time "time"
)

// legacyHealthStore 只转换旧账号形状，健康写入仍使用原存储的独立操作。
type legacyHealthStore struct{ AccountRepository }

func (s legacyHealthStore) GetByID(ctx context.Context, id int64) (*account.Record, error) {
	v, err := s.AccountRepository.GetByID(ctx, id)
	return AccountRecordView(v), err
}

// HealthOptions 在原调用点读取动态设置，不冻结启动时的业务设置。
func (s *RateLimitService) HealthOptions() account.HealthOptions {
	minutes := 0
	cnMinutes := 0
	if s.cfg != nil {
		minutes = s.cfg.RateLimit.OverloadCooldownMinutes
		cnMinutes = s.cfg.Gateway.CNProviders.IntervalMinutes
	}
	var fallback429 func(context.Context) (*account.RateLimit429CooldownSettings, error)
	if s.settingService != nil {
		fallback429 = func(ctx context.Context) (*account.RateLimit429CooldownSettings, error) {
			return s.settingService.GetRateLimit429CooldownSettings(ctx)
		}
	}
	return account.HealthOptions{RateLimit429Settings: fallback429, CNIntervalMinutes: cnMinutes, ForbiddenCounter: s.openAI403CounterCache, ForbiddenSettings: func(ctx context.Context) (*account.OpenAI403CooldownSettings, error) {
		if s.settingService == nil {
			return nil, nil
		}
		return s.settingService.GetOpenAI403CooldownSettings(ctx)
	}, OverloadMinutes: minutes, OverloadSettings: func(ctx context.Context) (*account.OverloadCooldownSettings, error) {
		if s.settingService == nil {
			return nil, nil
		}
		return s.settingService.GetOverloadCooldownSettings(ctx)
	}, HasThresholdSettings: func() bool { return s.settingService != nil }, Thresholds: func(ctx context.Context) map[string]int { return s.settingService.GetAccountSchedulingThresholds(ctx) }, Now: time.Now, Warn: slog.Warn, Info: slog.Info, TimeoutCounter: s.timeoutCounterCache, Block: func(v *account.Record, until time.Time, reason string) {
		s.notifyAccountSchedulingBlocked(AccountFromRecord(v), until, reason)
	}, StreamSettings: func(ctx context.Context) (*account.StreamTimeoutSettings, error, bool) {
		if s.settingService == nil {
			return nil, nil, false
		}
		v, err := s.settingService.GetStreamTimeoutSettings(ctx)
		return v, err, true
	}}
}
func (s *RateLimitService) BindHealth(core *account.HealthService) { s.health = core }
func (s *RateLimitService) HealthCore() *account.HealthService {
	if s.health != nil {
		return s.health
	}
	// 可选仓储缺省必须保持 nil，不能用含 nil 的包装伪装成已配置后端。
	var store account.HealthStore
	if s.accountRepo != nil {
		store = legacyHealthStore{s.accountRepo}
	}
	return account.NewHealthService(store, s.tempUnschedCache, s.HealthOptions())
}
