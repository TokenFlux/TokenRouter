package app

import "github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"

// runtime.go requires subsystem lifecycle registration and HTTP idempotency
// binding before application construction. The readiness arguments make Wire
// resolve those constructors before provideApplication receives its runtime
// marker; creating the marker itself starts no subsystem.

// runtimeReady marks resolution of application runtime registration dependencies for Wire.
type runtimeReady struct{}

// provideRuntime returns the application readiness marker after Wire resolves
// its registration dependencies. The marker orders construction of the graph;
// actual loading and background startup occur when the lifecycle manager runs
// the registered hooks.
func provideRuntime(
	_ *bstatsRuntimeReady,
	_ *modelCatalogRuntimeReady,
	_ *settingsRuntimeReady,
	_ *authRuntimeReady,
	_ *maintenanceRuntimeReady,
	_ *opsRuntimeReady,
	_ *queuesRuntimeReady,
	_ *jobsRuntimeReady,
	_ *coreRuntimeReady,
	_ *pluginRuntimeReady,
	_ *idempotencyHTTPReady,
	_ *promptpolicy.Service,
) *runtimeReady {
	return &runtimeReady{}
}
