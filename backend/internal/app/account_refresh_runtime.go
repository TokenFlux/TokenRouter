package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideBackgroundRefresh 将唯一后台运行实例绑定旧入口，构造不启动任何维护任务。
func provideBackgroundRefresh(source *service.TokenRefreshService, store *postgres.AccountStore, privacy *account.PrivacyService, refresh *account.OAuthRefreshAPI, cfg *config.Config) *account.BackgroundRefreshService {
	source.SetAccountPrivacy(privacy)
	options := legacybridge.BackgroundRefreshOptions(source)
	v := cfg.TokenRefresh
	options.Tuning = &account.RefreshTuning{Enabled: v.Enabled, CheckIntervalMinutes: v.CheckIntervalMinutes, RefreshBeforeExpiryHours: v.RefreshBeforeExpiryHours, MaxRetries: v.MaxRetries, RetryBackoffSeconds: v.RetryBackoffSeconds, CandidatePageSize: v.CandidatePageSize, ProviderConcurrency: v.ProviderConcurrency, ProviderQPS: v.ProviderQPS, ProviderFailureThreshold: v.ProviderFailureThreshold, AttemptTimeoutSeconds: v.AttemptTimeoutSeconds, CycleTimeoutSeconds: v.CycleTimeoutSeconds}
	options.Pager = store
	options.Attempts.API = refresh
	options.Attempts.Tuning = options.Tuning
	options.Attempts.FailureWriter = store
	options.Attempts.GrokMutation = store
	options.Reconciliation.Reader = store
	options.Reconciliation.ConditionalError = store
	core := account.NewBackgroundRefreshService(options)
	source.BindBackgroundCore(core)
	return core
}
