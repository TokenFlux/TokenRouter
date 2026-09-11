package app

// runtimeReady 让 Wire 在返回应用前登记所有运行资源。
type runtimeReady struct{}

func provideRuntime(_ *bootRuntimeReady, _ *authRuntimeReady, _ *maintenanceRuntimeReady, _ *opsRuntimeReady, _ *queuesRuntimeReady, _ *jobsRuntimeReady, _ *coreRuntimeReady) *runtimeReady {
	return &runtimeReady{}
}
