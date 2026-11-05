package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// provider_archive.go constructs provider import, export, Codex invite reset,
// and CRS synchronization services. Provider import and export share
// administration, proxy transfer, privacy, and deferred quota probing. Archive
// callbacks use application task tracking, while Codex imports validate agent
// private keys and CRS synchronization reuses the platform authorization and
// refresh coordinator.

// grokImportQuotaProbe narrows Grok quota results for deferred archive import
// checks.
type grokImportQuotaProbe struct {
	source *provider.GrokQuotaService // import quota probing
}

// QueryQuota delegates Grok probing and returns model, status, and header
// observations. A nil result remains nil, and available summaries are returned
// with the original quota-query error.
func (p grokImportQuotaProbe) QueryQuota(ctx context.Context, id int64) (*provider.GrokImportProbeResult, error) {
	value, err := p.source.QueryQuota(ctx, id)
	if value == nil {
		return nil, err
	}
	return &provider.GrokImportProbeResult{Model: value.Model, StatusCode: value.StatusCode, HeadersObserved: value.HeadersObserved}, err
}

// provideProviderArchive constructs import/export with privacy and tracked
// background work. tasks, privacy, and probe services supply required
// callbacks; each archive probe schedules a provider snapshot through the
// shared Grok quota adapter.
func provideProviderArchive(admin *provider.Admin, proxies *egress.ProxyTransfer, privacy *provider.PrivacyService, probes *provider.GrokImportProbeScheduler, grok *provider.GrokQuotaService, tasks *lifecycle.Tasks) *provider.Archive {
	options := provider.ArchiveOptions{
		Now: time.Now, Info: slog.Info, Error: slog.Error, Debug: slog.Debug, DecodeIDToken: provideradapter.DecodeArchiveIDToken, Background: tasks.Go, ForcePrivacy: privacy.ForceAntigravityPrivacy,
		Probe: func(snapshot provider.ProviderSnapshot) {
			probes.Schedule(grokImportQuotaProbe{source: grok}, &snapshot)
		},
	}
	return provider.NewArchive(admin, proxies, options)
}

// provideCodexImporter binds provider management, archive import, and agent
// private-key validation. Token invalidation is added when available; imported
// credentials use the platform OAuth client ID and the system clock.
func provideCodexImporter(admin *provider.Admin, archive *provider.Archive, invalidator provider.TokenCacheInvalidator) *provider.CodexImporter {
	options := provider.CodexImportOptions{OAuthClientID: openai.ClientID, ValidatePrivateKey: func(value string) error { _, err := openai.ParseAgentIdentityPrivateKey(value); return err }}
	if invalidator != nil {
		options.Invalidate = func(ctx context.Context, value *provider.Record) error {
			return invalidator.InvalidateToken(ctx, value)
		}
	}
	options.Now = time.Now
	return provider.NewCodexImporter(admin, archive, options)
}

// provideCodexInvites binds invite reset to request-side tokens, transport,
// proxies, and TLS readers. admin and proxy provide required callbacks; reset
// clients are constructed by CodexInviteFactory when the service invokes them.
func provideCodexInvites(admin *provider.Admin, proxy egress.ProxyRepository, token *provider.OpenAITokenSource, transport httpclient.UpstreamTransport, profiles *egressprovider.TLSProfiles, routers provideradapter.OpenAITokenRouterReader) *provider.CodexInviteResetService {
	factory := provideradapter.CodexInviteFactory{Token: token, Proxy: proxy.GetByID, Transport: transport, Profiles: profiles, Routers: routers}
	return &provider.CodexInviteResetService{Options: provider.CodexInviteResetOptions{
		Read: admin.GetProvider, Client: factory.Client, NewID: uuid.NewString, Warn: slog.Warn,
	}}
}

// provideCRSSync constructs CRS synchronization with operator URL policy and
// shared platform refresh. cfg is required, and the CRS client receives its
// host list while coordinated exchange uses the supplied OAuthRefreshAPI.
func provideCRSSync(providers *providerpostgres.ProviderStore, proxies *egresspostgres.ProxyStore, oauth *provider.ClaudeAuthorization, openai *provider.OpenAIAuthorization, gemini *provider.GeminiAuthorization, cfg *config.Config, coordinator *provider.OAuthRefreshAPI) *provider.CRSSync {
	exchange := &provider.CRSAuthorization{Claude: oauth, OpenAI: openai, Gemini: gemini}
	v := cfg.Security.URLAllowlist
	client := provideradapter.NewCRSClient(provideradapter.CRSClientOptions{Configured: true, AllowlistEnabled: v.Enabled, AllowInsecureHTTP: v.AllowInsecureHTTP, AllowPrivateHosts: v.AllowPrivateHosts, Hosts: v.CrsSyncHosts})
	return provider.NewCRSSync(providers, proxies, client, provider.CRSOptions{Now: time.Now, Warn: slog.Warn, Refresh: exchange.Coordinated(coordinator, provideradapter.GeminiTokenCacheKey)})
}

// provideCRSHTTP exposes the assembled CRS synchronization service through
// administration HTTP.
func provideCRSHTTP(source *provider.CRSSync) *providerhttp.CRSHandler {
	return providerhttp.NewCRSHandler(source)
}
