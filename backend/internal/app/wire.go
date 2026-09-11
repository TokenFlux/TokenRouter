//go:build wireinject

package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/repository"
	"github.com/TokenFlux/TokenRouter/internal/server"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/site"
	sitepostgres "github.com/TokenFlux/TokenRouter/internal/site/postgres"

	"github.com/google/wire"
)

// initializeApplication 只构造和登记资源；运行由 Application.Run 统一启动。
func initializeApplication(ctx context.Context, cfg *config.Config, info BuildInfo, manager *lifecycle.Manager, restarter *lifecycle.Restarter, tasks *lifecycle.Tasks) (*Application, error) {
	wire.Build(repository.ProviderSet, service.ProviderSet, payment.ProviderSet, middleware.ProviderSet, handler.ProviderSet, server.ProviderSet,
		provideEnt, provideRedis, providePrivacyClientFactory, provideServiceBuildInfo, provideHandlerBuildInfo, provideSecretEncryptor, provideSettingsStore, provideRouterRuntime, providePricingService,
		site.NewAnnouncementService, sitepostgres.NewAnnouncementRepository, sitepostgres.NewAnnouncementReadRepository, provideAnnouncementUsers, provideAnnouncementSubscriptions, provideAnnouncementExpiry,
		provideRestartRequester, provideBootRuntime, provideAuthRuntime, provideMaintenanceRuntime, provideOpsRuntime, provideQueuesRuntime, provideJobsRuntime, provideCoreRuntime, provideRuntime, provideApplication)
	return nil, nil
}
