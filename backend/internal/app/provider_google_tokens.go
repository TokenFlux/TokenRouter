package app

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	usageerrors "github.com/TokenFlux/TokenRouter/internal/usage"
)

// provider_google_tokens.go assembles Gemini authorization and quota prechecks
// alongside cached Gemini and Antigravity credentials. Gemini and Antigravity
// token reads share provider storage, access-token cache, and refresh
// coordination. Gemini also binds project discovery and Vertex exchange, while
// quota prechecks use Los Angeles dates or a fixed PST fallback when the
// location database cannot be loaded.

// provideGeminiAuthorization binds configured Google OAuth and stored proxy
// resolution to platform clients. cfg and proxies supply request-time
// callbacks; unreadable or absent proxy results are reported through the
// resolver's false availability result.
func provideGeminiAuthorization(proxies egress.ProxyRepository, client provider.GeminiOAuthClient, discovery provider.GeminiCodeAssistClient, drive codeassist.DriveClient, cfg *config.Config) *provider.GeminiAuthorization {
	options := provideradapter.GeminiAuthorizationOptions(func() google.OAuthConfig {
		return google.OAuthConfig{ClientID: cfg.Provider.Gemini.OAuth.ClientID, ClientSecret: cfg.Provider.Gemini.OAuth.ClientSecret, Scopes: cfg.Provider.Gemini.OAuth.Scopes}
	}, func(ctx context.Context, id int64) (string, bool) {
		proxy, err := proxies.GetByID(ctx, id)
		// A failed or absent proxy read cannot supply authorization routing.
		// Return the resolver's unavailable result so the Google flow handles that selected dependency.
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	})
	return provider.NewGeminiAuthorization(client, discovery, drive, options)
}

// provideGeminiQuotaPolicy binds startup tier overrides and policy JSON reads
// to quota resolution. cfg is required; configured tiers are collected before
// construction, and LoadPolicy reads the shared store when the service requests
// runtime policy.
func provideGeminiQuotaPolicy(cfg *config.Config, store *settings.Store) *provider.GeminiQuotaService {
	tiers := make(map[string]provider.GeminiTierQuotaOverride, len(cfg.Provider.Gemini.Quota.Tiers))
	for id, v := range cfg.Provider.Gemini.Quota.Tiers {
		tiers[id] = provider.GeminiTierQuotaOverride{ProRPD: v.ProRPD, FlashRPD: v.FlashRPD, CooldownMinutes: v.CooldownMinutes}
	}
	return provider.NewGeminiQuotaService(provider.GeminiQuotaOptions{StaticTiers: tiers, StaticPolicy: cfg.Provider.Gemini.Quota.Policy, Now: time.Now, Log: log.Printf, NotFound: settings.ErrSettingNotFound, LoadPolicy: func(ctx context.Context) (string, error) {
		return store.GetValue(ctx, provider.GeminiQuotaPolicySettingKey)
	}})
}

// provideGeminiPrecheck constructs quota admission using local Gemini usage and
// its own cache. It uses America/Los_Angeles when loaded, otherwise a fixed
// UTC-minus-eight PST zone for the daily window calculations.
func provideGeminiPrecheck(policy *provider.GeminiQuotaService, usage usageerrors.UsageLogRepository) *provider.GeminiPrecheck {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		// Location loading failure selects fixed PST so quota dates remain defined.
		// This fallback uses UTC minus eight throughout the year and therefore has no daylight-saving transition.
		location = time.FixedZone("PST", -8*3600)
	}
	return provider.NewGeminiPrecheck(policy, newProviderGeminiUsageReader(usage), provider.GeminiPrecheckOptions{Now: time.Now, Location: location, Info: slog.Info})
}

// provideGeminiTokens binds cached tokens, project discovery, Vertex exchange,
// and credential persistence. Refresh delegates to the shared coordinator with
// the platform cache key, while persist returns the provider mutation error for
// token handling.
func provideGeminiTokens(store *postgres.ProviderStore, cache provider.AccessTokenCache, authorization *provider.GeminiAuthorization, refresh *provider.OAuthRefreshAPI) *provider.GeminiTokenSource {
	executor := &provider.GeminiTokenRefresher{Authorization: authorization, Key: provideradapter.GeminiTokenCacheKey}
	return &provider.GeminiTokenSource{Options: provider.GeminiTokenOptions{
		Cache: cache, Repository: store, Policy: provider.GeminiProviderRefreshPolicy(),
		Debug: slog.Debug, Warn: slog.Warn, Logf: log.Printf,
		Project: authorization.FetchProject, ResolveProxy: authorization.Options.ResolveProxy,
		Vertex: func(ctx context.Context, value *provider.Record) (string, error) {
			return provideradapter.VertexServiceAccountAccessToken(ctx, cache, value)
		},
		Persist: func(ctx context.Context, value *provider.Record, credentials map[string]any) error {
			_, err := provider.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
		Refresh: func(ctx context.Context, value *provider.Record, window time.Duration) (*provider.OAuthRefreshResult, error) {
			return refresh.RefreshIfNeeded(ctx, value, executor, window)
		},
	}}
}

// provideAntigravityTokens combines cached tokens, project backfill, temporary
// pause state, and platform refresh. It delegates conditional persistence and
// refresh to provider helpers, retaining their errors while diagnostics use the
// configured print and log callbacks.
func provideAntigravityTokens(store *postgres.ProviderStore, cache provider.AccessTokenCache, authorization *provider.AntigravityAuthorization, refresh *provider.OAuthRefreshAPI, cooldown provider.TempUnschedCache) *provider.AntigravityTokenSource {
	executor := &provider.AntigravityRefreshRules{
		RefreshProviderToken:     authorization.RefreshProviderToken,
		BuildProviderCredentials: authorization.BuildProviderCredentials,
		// Platform print diagnostics have no returned result. Output errors are
		// discarded by this callback while the refresh operation retains its own credential or persistence error.
		Printf: func(format string, args ...any) { _, _ = fmt.Printf(format, args...) },
		Logf:   log.Printf,
	}
	return &provider.AntigravityTokenSource{Options: provider.AntigravityTokenOptions{
		Cache: cache, Repository: store, Policy: provider.AntigravityProviderRefreshPolicy(),
		Debug: slog.Debug, Warn: slog.Warn, TempUnschedCache: cooldown,
		SetTempUnschedulable: store.SetTempUnschedulable,
		FillProject:          authorization.FillProjectID,
		Persist: func(ctx context.Context, value *provider.Record, credentials map[string]any) error {
			_, err := provider.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
		Refresh: func(ctx context.Context, value *provider.Record, window time.Duration) (*provider.OAuthRefreshResult, error) {
			return refresh.RefreshIfNeeded(ctx, value, executor, window)
		},
	}}
}
