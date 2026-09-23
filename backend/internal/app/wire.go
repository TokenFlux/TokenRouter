//go:build wireinject

package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/google/wire"
)

// initializeApplication 只构造和登记资源；运行由 Application.Run 统一启动。
func initializeApplication(ctx context.Context, cfg *config.Config, info BuildInfo, manager *lifecycle.Manager, restarter *lifecycle.Restarter, tasks *lifecycle.Tasks) (*Application, error) {
	wire.Build(
		egressAssemblyProviders,
		accountAssemblyProviders,
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
		moderationAssemblyProviders,
		notificationAssemblyProviders,
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
