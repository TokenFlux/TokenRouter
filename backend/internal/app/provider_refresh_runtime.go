package app

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
)

// provider_refresh_runtime.go assembles manual and background credential
// refresh around one shared coordinator. Both paths use platform-specific
// exchange implementations. Background registration provides both eligibility
// and execution for six platforms, while application hooks stop accepted
// coordinator work before the later provider clients and storage are released.

// providerRefreshRegistrations contains platform eligibility and exchange
// entries for background maintenance.
type providerRefreshRegistrations []provider.RefreshRegistration

// provideManagedRefresh binds manual exchanges to shared credential
// coordination and privacy operations. Optional token invalidation is attached
// when supplied, and each platform result becomes a credential observation with
// its project-missing flag and exchange error.
func provideManagedRefresh(admin *provider.Admin, privacy *provider.PrivacyService, coordinator *provider.OAuthRefreshAPI, transport httpclient.UpstreamTransport, profiles *egressprovider.TLSProfiles, claude *provider.ClaudeAuthorization, openai *provider.OpenAIAuthorization, gemini *provider.GeminiAuthorization, ag *provider.AntigravityAuthorization, grok provider.GrokRefreshTokenService, invalidator provider.TokenCacheInvalidator) *provider.ManagedRefreshService {
	qoder := provideradapter.NewQoderTokenRefresher(provideradapter.QoderRefreshOptions{Transport: transport, Profiles: profiles})
	source := &provider.ManualCredentialExchange{Claude: claude, OpenAI: openai, Gemini: gemini, Antigravity: ag, Grok: grok, Qoder: qoder.Refresh}
	exchange := func(ctx context.Context, value *provider.Record) (provider.ManagedRefreshObservation, error) {
		credentials, missing, err := source.Refresh(ctx, value)
		return provider.ManagedRefreshObservation{Credentials: credentials, ProjectIDMissing: missing}, err
	}
	var invalidate func(context.Context, *provider.Record) error
	if invalidator != nil {
		invalidate = func(ctx context.Context, value *provider.Record) error {
			return invalidator.InvalidateToken(ctx, value)
		}
	}
	return provider.NewManagedRefreshService(provider.ManagedRefreshOptions{Store: admin, Privacy: privacy, Coordinate: coordinator.WithManagedRefresh, CacheKey: provideradapter.ManagedRefreshCacheKey, Exchange: exchange, Invalidate: invalidate, Log: log.Printf, Warn: slog.Warn, Error: slog.Error})
}

// provideProviderRefresh constructs the credential coordinator with provider
// writes and token cache. Call before lifecycle startup; its registered stop
// waits at level twenty-five for accepted work using those dependencies.
func provideProviderRefresh(store *providerpostgres.ProviderStore, cache provider.AccessTokenCache, manager *lifecycle.Manager) *provider.OAuthRefreshAPI {
	coordinator := provider.NewOAuthRefreshAPI(store, cache, provider.RefreshOptions{
		Now: time.Now, Warn: slog.Warn, Info: slog.Info, Error: slog.Error,
		Platform: provider.ProviderRefreshPlatformPolicy(),
	})
	manager.Register(lifecycle.Hook{Name: "ProviderRefreshCoordinator", StopOrder: shutdownStageProbesAndRefresh, Stop: coordinator.StopContext})
	return coordinator
}

// provideRefreshPlatforms returns six platform registrations with eligibility
// and exchange implementations. Qoder uses shared transport and its
// authorization credential builder; Antigravity uses its exchange/credential
// callbacks, and the remaining platforms receive their shared authorization
// services.
func provideRefreshPlatforms(claude *provider.ClaudeAuthorization, openai *provider.OpenAIAuthorization, gemini *provider.GeminiAuthorization, antigravity *provider.AntigravityAuthorization, qoder *provideradapter.QoderAuthorization, grok *provider.GrokAuthorization, transport httpclient.UpstreamTransport, profiles *egressprovider.TLSProfiles) providerRefreshRegistrations {
	claudeRefresh := &provider.ClaudeTokenRefresher{Authorization: claude}
	openaiRefresh := &provider.OpenAITokenRefresher{Authorization: openai}
	geminiRefresh := &provider.GeminiTokenRefresher{Authorization: gemini, Key: provideradapter.GeminiTokenCacheKey}
	agRefresh := &provider.AntigravityRefreshRules{
		RefreshProviderToken:     antigravity.RefreshProviderToken,
		BuildProviderCredentials: antigravity.BuildProviderCredentials,
		// Print diagnostics have no error return in the refresh rules. Discard
		// output failure here while credential exchange and persistence keep their own failure results.
		Printf: func(format string, args ...any) { _, _ = fmt.Printf(format, args...) },
		Logf:   log.Printf,
	}
	qoderRefresh := provideradapter.NewQoderTokenRefresher(provideradapter.QoderRefreshOptions{
		Transport: transport, Profiles: profiles, BuildCredentials: qoder.Core.BuildProviderCredentials,
	})
	grokRefresh := provider.NewGrokTokenRefresher(grok)
	return providerRefreshRegistrations{
		{Platform: provider.PlatformAnthropic, Refresher: claudeRefresh, Executor: claudeRefresh},
		{Platform: provider.PlatformOpenAI, Refresher: openaiRefresh, Executor: openaiRefresh},
		{Platform: provider.PlatformGemini, Refresher: geminiRefresh, Executor: geminiRefresh},
		{Platform: provider.PlatformAntigravity, Refresher: agRefresh, Executor: agRefresh},
		{Platform: provider.PlatformQoder, Refresher: qoderRefresh, Executor: qoderRefresh},
		{Platform: provider.PlatformGrok, Refresher: grokRefresh, Executor: grokRefresh},
	}
}

// provideBackgroundRefresh constructs periodic and Grok reconciliation work
// from required cfg. Tuning values are captured, attempt timeout incorporates
// the coordinator lease, and shared post actions handle invalidation, cleanup,
// and publication after provider-owned refresh attempts.
func provideBackgroundRefresh(store *providerpostgres.ProviderStore, refresh *provider.OAuthRefreshAPI, cfg *config.Config, registrations providerRefreshRegistrations, post *provider.RefreshPostActions, observer provider.RefreshFailureObserver) *provider.BackgroundRefreshService {
	v := cfg.Provider.TokenRefresh
	tuning := &provider.RefreshTuning{
		Enabled: v.Enabled, CheckIntervalMinutes: v.CheckIntervalMinutes,
		RefreshBeforeExpiryHours: v.RefreshBeforeExpiryHours, MaxRetries: v.MaxAttempts,
		RetryBackoffSeconds: v.RetryBackoffSeconds, CandidatePageSize: v.CandidatePageSize,
		ProviderConcurrency: v.PlatformConcurrency, ProviderQPS: v.PlatformQPS,
		ProviderFailureThreshold: v.PlatformFailureThreshold,
		AttemptTimeoutSeconds:    v.AttemptTimeoutSeconds, CycleTimeoutSeconds: v.CycleTimeoutSeconds,
	}
	lease, configured := refresh.LockLease()
	prepare := func(value *provider.Record) func(time.Time, string) {
		return provider.PrepareRefreshFailureNotice(observer, value)
	}
	attempts := provider.RefreshAttempts{
		API: refresh, Tuning: tuning, Policy: provider.DefaultBackgroundRefreshPolicy(),
		AttemptTimeout: tuning.AttemptTimeout(0, lease, configured),
		Now:            time.Now, Info: slog.Info, Warn: slog.Warn, Error: slog.Error,
		NonRetryable:         provideradapter.IsNonRetryableRefreshError,
		SharedProviderError:  provideradapter.IsSharedProviderRefreshError,
		AmbiguousEntitlement: provideradapter.IsAmbiguousGrokEntitlementRefreshError,
		FailureWriter:        store, GrokMutation: store, Invalidate: post.Invalidate,
		PrepareFailure: prepare, ClearRefreshRequest: post.ClearRefreshRequest,
		PostActions: post.Run, SyncCleanup: post.SyncWithCleanup,
		Persist: func(ctx context.Context, value *provider.Record, credentials map[string]any) error {
			_, err := provider.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
	}
	return provider.NewBackgroundRefreshService(provider.BackgroundRefreshOptions{
		Tuning: tuning, Pager: store, Registrations: registrations, Attempts: attempts,
		Debug: slog.Debug, Info: slog.Info, Warn: slog.Warn, Error: slog.Error,
		Reconciliation: provider.GrokReconciliationOptions{
			Reader: store, ConditionalError: store, Now: time.Now,
			Skew: provider.GrokTokenRefreshSkew, PrepareFailure: prepare, Invalidate: post.Invalidate,
		},
	})
}
