package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
)

// runtime_plugin.go binds the plugin process collection to application lifecycle.
// Construction registers ownership without launching children. Startup loads
// explicitly enabled development directories asynchronously, and shutdown stops
// loading and drains processes before shared host services are released.

// pluginRuntimeReady marks registration of the application's plugin lifecycle owner.
type pluginRuntimeReady struct{}

// providePluginProcesses constructs the shared native process collection for this host.
func providePluginProcesses(cfg *config.Config, info BuildInfo) *provider.Processes {
	return provider.NewProcesses(plugin.RuntimeOptions{HostVersion: info.Version, InstanceID: uuid.NewString(), Timezone: cfg.Timezone, HTTPProxy: cfg.Plugins.PluginHTTPProxy}, slog.Default(), nil)
}

// providePluginRuntime registers nonblocking startup and bounded plugin shutdown.
// Development loading requires both configuration switches. The stop hook has
// an eight-second budget within the application's broader shutdown context.
func providePluginRuntime(cfg *config.Config, processes *provider.Processes, manager *lifecycle.Manager, info BuildInfo) *pluginRuntimeReady {
	source := plugin.NewDevSource(plugin.DevOptions{Enabled: cfg.Plugins.Enabled, DevMode: cfg.Plugins.DevMode, Directories: cfg.Plugins.DevDirs, StateRoot: filepath.Join(cfg.Plugins.Dir, "state"), MaxRunning: cfg.Plugins.MaxRunning, HostVersion: info.Version}, provider.DirectoryReader{}, processes, slog.Default())
	manager.Register(lifecycle.Hook{Name: "PluginRuntime", StartOrder: 976, StopOrder: shutdownStagePlugins, Start: source.Start, Stop: func(ctx context.Context) error {
		budget, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return source.Stop(budget)
	}})
	return &pluginRuntimeReady{}
}
