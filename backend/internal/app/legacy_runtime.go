package app

import "github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"

// runtimeReady 让 Wire 在返回应用前登记所有运行资源。
type runtimeReady struct{}

func provideRuntime(_ *bootRuntimeReady, _ *authRuntimeReady, _ *maintenanceRuntimeReady, _ *opsRuntimeReady, _ *queuesRuntimeReady, _ *jobsRuntimeReady, _ *coreRuntimeReady, _ *gatewayCompletionReady, _ *idempotencyHTTPReady, _ *promptpolicy.Service) *runtimeReady {
	return &runtimeReady{}
}
