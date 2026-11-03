package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/bstats"
	"github.com/TokenFlux/TokenRouter/internal/bstats/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// runtime_bstats.go assembles optional usage reporting and its lifecycle hooks.
// The reporter shares persisted administrator settings and the application build
// version. Reporting initialization can fail without preventing gateway startup,
// and shutdown cancels reporting before the settings and logging resources close.

// bstatsRuntimeReady marks registration of the metrics startup and shutdown hooks.
type bstatsRuntimeReady struct{}

// provideBStats constructs the reporter without starting timers or network work.
func provideBStats(store *settings.Store, info BuildInfo) *bstats.Service {
	log := func(format string, args ...any) {
		logging.LegacyPrintf("service.bstats", format, args...)
	}
	return bstats.New(store, provider.NewClient(info.Version, log), log)
}

// provideBStatsRuntime registers reporting alongside other periodic producers.
// Initialization errors leave reporting paused while the application continues;
// a later committed settings update can retry initialization. Shutdown waits for
// cancellation before dependencies are released by later lifecycle stages.
// @project-doc docs/architecture/system_architecture.md#startup_and_shutdown
func provideBStatsRuntime(manager *lifecycle.Manager, metrics *bstats.Service) *bstatsRuntimeReady {
	manager.Register(lifecycle.Hook{
		Name:       "BStats",
		StartOrder: 980,
		StopOrder:  shutdownStageProducers,
		Start: func(ctx context.Context) error {
			// Metrics are optional: a storage failure must pause outbound reporting
			// without making the API gateway unavailable or substituting a temporary identity.
			if err := metrics.Start(ctx); err != nil {
				logging.LegacyPrintf("service.bstats", "bStats reporting paused: %v", err)
			}
			return nil
		},
		Stop: metrics.Stop,
	})
	return &bstatsRuntimeReady{}
}
