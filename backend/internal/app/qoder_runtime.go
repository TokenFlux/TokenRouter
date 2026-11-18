package app

import (
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// qoder_runtime.go assembles Qoder execution, session refresh, and shutdown
// barrier bindings for unified gateway text execution.
// Qoder attempts share token refresh, outbound transport, TLS profiles, and
// health persistence while registering an operation barrier that drains active
// attempts before completion queues and credential sessions shut down.

// qoderRequestActivity tracks accepted Qoder requests and attempts for
// application shutdown.
type qoderRequestActivity struct {
	*lifecycle.Operations // accepted Qoder operations
}

// provideQoderRequestRefresh binds session recovery to the execution token
// provider and credential coordinator.
func provideQoderRequestRefresh(store *providerpostgres.ProviderStore, tokens *provideradapter.QoderTokenProvider, coordinator *providercore.OAuthRefreshAPI, transport provideradapter.QoderTransport, profiles *egressprovider.TLSProfiles) *provideradapter.QoderRequestRefresh {
	return &provideradapter.QoderRequestRefresh{Store: store, Tokens: tokens, Coordinator: coordinator, Transport: transport, Profiles: profiles}
}

// provideQoderRuntime constructs Qoder execution using shared tokens,
// transport, health, and attributes.
func provideQoderRuntime(tokens *provideradapter.QoderTokenProvider, transport provideradapter.QoderTransport, profiles *egressprovider.TLSProfiles, store *providerpostgres.ProviderStore, attributes *routing.ModelAttributeService) *gatewayprovider.QoderRuntime {
	return gatewayprovider.NewQoderRuntime(gatewayprovider.QoderRuntimeOptions{Attributes: attributes, Tokens: tokens, Transport: transport, Profiles: profiles, Health: store})
}

// provideQoderRequestActivity registers a shared request/attempt barrier before
// lifecycle startup. It binds the same Enter callback to Qoder runtime attempts
// and stops at the request stage before completion queues and shared
// dependencies drain.
func provideQoderRequestActivity(manager *lifecycle.Manager, runtime *gatewayprovider.QoderRuntime) *qoderRequestActivity {
	activity := lifecycle.NewOperations("QoderRequestsAndAttempts")
	// Stop request and attempt admission together, then wait for accepted work
	// before completion recording and the shared platform dependencies enter later cleanup phases.
	manager.Register(lifecycle.Hook{Name: "QoderRequestsAndAttempts", StopOrder: shutdownStageRequests, Stop: activity.StopContext})
	runtime.BindAttemptActivity(activity.Enter)
	return &qoderRequestActivity{Operations: activity}
}
