//go:build wireinject

package app

import (
	"context"

	"github.com/google/wire"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

// wire.go declares the application dependency graph used to generate runtime
// initialization.
// Initialize passes its lifecycle manager, restarter, and task tracker to
// application construction. Wire uses those objects with the supplied context
// and configuration while resolving subsystem constructors, then returns an
// Application after providers have registered their resource hooks.

// initializeApplication specifies the complete application graph for Wire
// generation. The wireinject build contains a placeholder result; generated
// runtime initialization resolves the providers using cfg, ctx, build
// metadata, and the supplied lifecycle objects.
func initializeApplication(ctx context.Context, cfg *config.Config, info BuildInfo, manager *lifecycle.Manager, restarter *lifecycle.Restarter, tasks *lifecycle.Tasks) (*Application, error) {
	wire.Build(
		egressAssemblyProviders,
		providerAssemblyProviders,
		gatewayAssemblyProviders,
		identityAssemblyProviders,
		apikeyAssemblyProviders,
		siteAssemblyProviders,
		routingAssemblyProviders,
		foundationAssemblyProviders,
		teamAssemblyProviders,
		backupAssemblyProviders,
		opsAssemblyProviders,
		tasksAssemblyProviders,
		settingsAssemblyProviders,
		paymentAssemblyProviders,
		promotionAssemblyProviders,
		searchAssemblyProviders,
		pluginAssemblyProviders,
		moderationAssemblyProviders,
		notificationAssemblyProviders,
		modelCatalogAssemblyProviders,
		billingAssemblyProviders,
		usageAssemblyProviders,
		auditAssemblyProviders,
		schedulerAssemblyProviders,
		upstreamAssemblyProviders,
		httpAssemblyProviders,
		runtimeAssemblyProviders,
	)
	return nil, nil
}
