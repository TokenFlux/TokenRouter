package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

// provideBackgroundRefresh 直接绑定唯一刷新运行实例，构造不启动维护任务。
func provideBackgroundRefresh(store *postgres.AccountStore, refresh *account.OAuthRefreshAPI, cfg *config.Config, registrations accountRefreshRegistrations, post *account.RefreshPostActions, observer account.RefreshFailureObserver) *account.BackgroundRefreshService {
	v := cfg.TokenRefresh
	tuning := &account.RefreshTuning{
		Enabled: v.Enabled, CheckIntervalMinutes: v.CheckIntervalMinutes,
		RefreshBeforeExpiryHours: v.RefreshBeforeExpiryHours, MaxRetries: v.MaxRetries,
		RetryBackoffSeconds: v.RetryBackoffSeconds, CandidatePageSize: v.CandidatePageSize,
		ProviderConcurrency: v.ProviderConcurrency, ProviderQPS: v.ProviderQPS,
		ProviderFailureThreshold: v.ProviderFailureThreshold,
		AttemptTimeoutSeconds:    v.AttemptTimeoutSeconds, CycleTimeoutSeconds: v.CycleTimeoutSeconds,
	}
	lease, configured := refresh.LockLease()
	prepare := func(value *account.Record) func(time.Time, string) {
		return account.PrepareRefreshFailureNotice(observer, value)
	}
	attempts := account.RefreshAttempts{
		API: refresh, Tuning: tuning, Policy: account.DefaultBackgroundRefreshPolicy(),
		AttemptTimeout: tuning.AttemptTimeout(0, lease, configured),
		Now:            time.Now, Info: slog.Info, Warn: slog.Warn, Error: slog.Error,
		NonRetryable:         provider.IsNonRetryableRefreshError,
		SharedProviderError:  provider.IsSharedProviderRefreshError,
		AmbiguousEntitlement: provider.IsAmbiguousGrokEntitlementRefreshError,
		FailureWriter:        store, GrokMutation: store, Invalidate: post.Invalidate,
		PrepareFailure: prepare, ClearRefreshRequest: post.ClearRefreshRequest,
		PostActions: post.Run, SyncCleanup: post.SyncWithCleanup,
		Persist: func(ctx context.Context, value *account.Record, credentials map[string]any) error {
			_, err := account.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
	}
	return account.NewBackgroundRefreshService(account.BackgroundRefreshOptions{
		Tuning: tuning, Pager: store, Registrations: registrations, Attempts: attempts,
		Debug: slog.Debug, Info: slog.Info, Warn: slog.Warn, Error: slog.Error,
		Reconciliation: account.GrokReconciliationOptions{
			Reader: store, ConditionalError: store, Now: time.Now,
			Skew: account.GrokTokenRefreshSkew, PrepareFailure: prepare, Invalidate: post.Invalidate,
		},
	})
}
