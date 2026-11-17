package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	providerauth "github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// provider_qoder.go constructs Qoder authorization and execution session
// sources with shared credential invalidation. Qoder authorization resolves
// supplied proxy IDs from persistence and uses an empty proxy URL when no ID is
// supplied. Execution tokens use a separate session cache bound to transport
// and TLS profiles; the composite invalidator connects that cache with
// access-token cache invalidation.

// provideQoderAuthorizationHTTP constructs the HTTP adapter for Qoder authorization operations.
func provideQoderAuthorizationHTTP(source *provideradapter.QoderAuthorization) *providerhttp.QoderOAuthHandler {
	return providerhttp.NewQoderOAuthHandler(source)
}

// provideQoderAuthorization constructs authorization with a callback for
// persisted proxy lookup. A nil proxy ID resolves to an empty URL; supplied IDs
// require an available repository and an existing proxy record. The adapter
// selects its default upstream client factory from the nil factory argument.
// Callers arrange Core.Start for session maintenance and Core.StopContext for
// shutdown; provideAuthRuntime supplies those callbacks in the application graph.
func provideQoderAuthorization(proxies egress.ProxyRepository) *provideradapter.QoderAuthorization {
	return provideradapter.NewQoderAuthorization(func(ctx context.Context, id *int64) (string, error) {
		if id == nil {
			return "", nil
		}
		if proxies == nil {
			return "", errors.New("proxy repository is not configured")
		}
		proxy, err := proxies.GetByID(ctx, *id)
		if err != nil {
			return "", fmt.Errorf("get proxy: %w", err)
		}
		if proxy == nil {
			return "", errors.New("proxy not found")
		}
		return proxy.URL(), nil
	}, nil)
}

// provideQoderTokens creates the execution session cache and binds the
// supplied transport and TLS profiles for site-specific exchanges. Session
// builds begin on demand; provideAuthRuntime registers StopContext to cancel
// and wait for them before shared connections close. Standalone callers own
// that shutdown for their returned instance.
func provideQoderTokens(transport provideradapter.QoderTransport, profiles *egressprovider.TLSProfiles) *provideradapter.QoderTokenProvider {
	source := provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{})
	source.SetHTTPUpstream(transport, profiles)
	return source
}

// provideTokenCacheInvalidator constructs the provider's composite credential
// invalidator using access-token and Qoder session caches. Platform key and
// session selection stay in that implementation; access-token deletion failures
// are reported through slog.Warn.
func provideTokenCacheInvalidator(cache providerauth.AccessTokenCache, sessions *provideradapter.QoderTokenProvider) providerauth.TokenCacheInvalidator {
	return providerauth.NewCompositeTokenCacheInvalidator(cache, sessions, slog.Warn)
}

// provideQoderCatalog binds one account catalog owner to credentials and storage.
// The admin sync endpoint starts queries. The shutdown hook cancels any manual
// queries still running before their storage and transport dependencies close.
func provideQoderCatalog(store *postgres.ProviderStore, tokens *provideradapter.QoderTokenProvider, transport provideradapter.QoderTransport, profiles *egressprovider.TLSProfiles, manager *lifecycle.Manager) *providerauth.QoderCatalogService {
	service := providerauth.NewQoderCatalogService(providerauth.QoderCatalogOptions{
		Read:   store.GetByID,
		Writer: store,
		Fetch:  provideradapter.QoderCatalogFetcher(tokens, transport, profiles),
	})
	manager.Register(lifecycle.Hook{Name: "QoderModelCatalog", StopOrder: shutdownStageProducers, Stop: service.StopContext})
	return service
}
