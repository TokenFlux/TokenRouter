package app

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/postgres"
)

// model_catalog.go constructs the remote model catalog and connects its
// snapshots to model attribute administration. Administration reads
// capabilities and attributes from this service and stores overrides through
// routing. Construction also passes catalog storage and URL-security options
// from configuration; the catalog lifecycle hooks perform initial loading and
// later synchronization.

// provideModelAttributes supplies model attributes for administration and display.
func provideModelAttributes(repo *postgres.ModelAttributeStore, catalog *provider.Service, invalidator apikey.APIKeyAuthCacheInvalidator) *routing.ModelAttributeService {
	return &routing.ModelAttributeService{
		Repo:        repo,
		Invalidator: invalidator,
		Catalog: routing.ModelAttributeCatalog{
			View: catalog.CapabilityView,
			Index: func() (modelcatalog.Index, routing.ModelAttributeSnapshot) {
				index, status := catalog.AttributeIndex()
				return index, routing.ModelAttributeSnapshot{Version: status.Version, LastUpdated: status.LastUpdated, LastError: status.LastError}
			},
			Lookup:     catalog.ModelAttributes,
			Update:     catalog.ForceUpdate,
			Candidates: modelidentity.CandidatesFactory,
			Snapshot: func() routing.ModelAttributeSnapshot {
				snapshot := catalog.AttributesSnapshot()
				return routing.ModelAttributeSnapshot{Items: snapshot.Items, Version: snapshot.Version, LastUpdated: snapshot.LastUpdated, LastError: snapshot.LastError}
			},
		},
	}
}

// provideModelCatalogService constructs a catalog from startup storage and
// security options, cloning the configured pricing-host slice. It returns an
// uninitialized service and a nil error; catalog lifecycle hooks load data and
// start synchronization.
func provideModelCatalogService(cfg *config.Config, remote provider.RemoteClient) (*provider.Service, error) {
	options := provider.Options{
		DataDir:               cfg.Storage.Dir,
		RemoteURL:             cfg.ModelCatalog.CatalogURL,
		FallbackFile:          cfg.ModelCatalog.SupplementFile,
		CheckIntervalMinutes:  cfg.ModelCatalog.CheckIntervalMinutes,
		URLAllowlistEnabled:   cfg.Security.URLAllowlist.Enabled,
		AllowInsecureHTTP:     cfg.Security.URLAllowlist.AllowInsecureHTTP,
		AllowPrivateHosts:     cfg.Security.URLAllowlist.AllowPrivateHosts,
		PricingHosts:          slices.Clone(cfg.Security.URLAllowlist.ModelCatalogHosts),
		ModelLookupCandidates: modelidentity.CandidatesFactory,
	}
	return provider.NewService(options, remote), nil
}

// provideModelCatalogRemoteClient delegates catalog HTTP construction with the
// configured update proxy and proxy-error policy. Proxy initialization errors
// produce a failing client unless AllowDirectOnError permits a standard HTTP
// client, whose environment proxy settings can still apply.
func provideModelCatalogRemoteClient(cfg *config.Config) provider.RemoteClient {
	return provider.NewRemoteClient(cfg.OutboundProxy.URL, cfg.OutboundProxy.AllowDirectOnError)
}
