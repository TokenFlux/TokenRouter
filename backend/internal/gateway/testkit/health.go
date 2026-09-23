package testkit

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// HealthInput 只组合原生健康测试依赖，不能执行业务规则或复制缓存。
type HealthInput struct {
	Store   provider.ExecutionAccountStore
	Cache   account.TempUnschedCache
	Options account.HealthOptions
	Readers *provider.RuntimeReaders
}

type healthStore struct{ provider.ExecutionAccountStore }

func (s healthStore) GetByID(ctx context.Context, id int64) (*account.Record, error) {
	value, err := s.ExecutionAccountStore.GetByID(ctx, id)
	return provider.ExecutionRecord(value), err
}
func (s healthStore) ListByPlatform(ctx context.Context, platform string) ([]account.Record, error) {
	values, err := s.ExecutionAccountStore.ListByPlatform(ctx, platform)
	if values == nil {
		return nil, err
	}
	out := make([]account.Record, len(values))
	for i := range values {
		out[i] = *provider.ExecutionRecord(&values[i])
	}
	return out, err
}

// NewHealthObserver 构造独立测试图；全部裁决调用生产原生实现。
func NewHealthObserver(input HealthInput) *accountprovider.UpstreamHealth {
	options := input.Options
	options.Now = time.Now
	options.Warn, options.Info = slog.Warn, slog.Info
	options.APIKeyHealthWarn = accountprovider.LogAPIKeyHealthWarning
	options.SessionWindows = input.Store
	if input.Readers != nil {
		source := input.Readers.Account
		options.APIKeyHealthSettings = source.GetOpenAIAPIKeyHealthBreakerSettings
		options.RateLimit429Settings = source.GetRateLimit429CooldownSettings
		options.ForbiddenSettings = source.GetOpenAI403CooldownSettings
		options.OverloadSettings = source.GetOverloadCooldownSettings
		options.HasThresholdSettings = func() bool { return true }
		options.Thresholds = source.GetAccountSchedulingThresholds
		options.StreamSettings = func(ctx context.Context) (*account.StreamTimeoutSettings, error, bool) {
			v, err := source.GetStreamTimeoutSettings(ctx)
			return v, err, true
		}
	}
	var store account.HealthStore
	var teamStore account.TeamLinkedStore
	if input.Store != nil {
		store = healthStore{input.Store}
		teamStore = healthStore{input.Store}
	}
	var recovery *account.RecoveryService
	options.ClearWindowRateLimit = func(ctx context.Context, id int64) error { return recovery.ClearRateLimit(ctx, id) }
	health := account.NewHealthService(store, input.Cache, options)
	recovery = account.NewRecoveryService(healthStore{input.Store}, input.Cache, account.RecoveryOptions{Now: time.Now, Warn: slog.Warn, ResetCounter: health.ResetForbiddenCounter, InvalidateToken: options.InvalidateUnauthorizedToken})
	return &accountprovider.UpstreamHealth{
		Core: health,
		Team: account.NewTeamLinkedHealth(teamStore, account.TeamLinkedOptions{Now: time.Now, Warn: slog.Warn, Block: options.Block}),
		Limits: &accountprovider.RateLimitObserver{Health: health, Plans: input.Store, NextGeminiDaily: func() *int64 {
			location, err := time.LoadLocation("America/Los_Angeles")
			if err != nil {
				location = time.FixedZone("PST", -8*3600)
			}
			reset := account.GeminiDailyResetTime(time.Now(), location).Unix()
			return &reset
		}},
		Models: &accountprovider.ModelHealth{Health: health, CodexRules: provider.CodexModelRules(), IsImageModel: media.IsGPTImageGenerationModel},
	}
}
