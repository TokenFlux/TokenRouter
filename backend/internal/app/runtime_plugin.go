package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
)

// runtime_plugin.go binds the plugin process collection to application lifecycle.
// Construction registers ownership without launching children. Startup reconciles
// durable installations and development records asynchronously. Shutdown stops
// loading and drains processes before shared host services are released.

// pluginRuntimeReady marks registration of the application's plugin lifecycle owner.
type pluginRuntimeReady struct{}

// providePluginProcesses constructs the shared native process collection for this host.
func providePluginProcesses(cfg *config.Config, info BuildInfo) *provider.Processes {
	return provider.NewProcesses(plugin.RuntimeOptions{HostVersion: info.Version, InstanceID: uuid.NewString(), Timezone: cfg.Timezone, HTTPProxy: cfg.Plugins.PluginHTTPProxy}, slog.Default(), nil)
}

// providePluginPackages constructs verified package storage before accepting administrative uploads.
func providePluginPackages(cfg *config.Config) (*provider.PackageFiles, error) {
	return provider.NewPackageFiles(cfg.Plugins.Dir, provider.DownloadOptions{URLPrefix: cfg.Plugins.DownloadURLPrefix, ProxyURL: cfg.Plugins.DownloadProxy})
}

// providePluginManager assembles durable records, development overlays, and encrypted configuration.
// The shared cipher requires its configured persistent key before storing secrets.
// Construction leaves process startup to the registered application lifecycle hook.
func providePluginManager(cfg *config.Config, info BuildInfo, client *dbent.Client, cipher identity.SecretEncryptor, packages *provider.PackageFiles, processes *provider.Processes) *plugin.Manager {
	configuration := plugin.NewConfiguration(cipher, cfg.Totp.EncryptionKeyConfigured)
	source := plugin.NewDevSource(plugin.DevOptions{Enabled: cfg.Plugins.Enabled, DevMode: cfg.Plugins.DevMode, Directories: cfg.Plugins.DevDirs}, provider.DirectoryReader{}, postgres.New(client), configuration, slog.Default())
	return plugin.NewManager(plugin.ManagerOptions{
		Enabled:     cfg.Plugins.Enabled,
		MaxRunning:  cfg.Plugins.MaxRunning,
		HostVersion: info.Version,
		StateRoot:   filepath.Join(cfg.Plugins.Dir, "state"),
	}, source, packages, processes, configuration, slog.Default())
}

// providePluginService binds administration to the same runtime manager and staged uploads.
func providePluginService(manager *plugin.Manager, packages *provider.PackageFiles) *plugin.Service {
	return plugin.NewService(manager, packages)
}

// providePluginRuntime registers asynchronous reconciliation and bounded plugin shutdown.
// Startup restores enabled installations through the shared manager. The stop hook
// has an eight-second budget within the application's broader shutdown context.
func providePluginRuntime(plugins *plugin.Manager, manager *lifecycle.Manager) *pluginRuntimeReady {
	manager.Register(lifecycle.Hook{Name: "PluginRuntime", StartOrder: 976, StopOrder: shutdownStagePlugins, Start: plugins.Start, Stop: func(ctx context.Context) error {
		budget, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return plugins.Stop(budget)
	}})
	return &pluginRuntimeReady{}
}
