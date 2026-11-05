package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// runtime_plugin.go binds the plugin process collection to application lifecycle.
// Construction registers ownership without launching children. Startup reconciles
// durable installations and development records asynchronously. Shutdown stops
// loading and drains processes before shared host services are released. KV
// maintenance starts before process reconciliation and stops in the same shutdown
// stage, so both workers finish before their shared database dependency closes.

// pluginRuntimeReady marks registration of the application's plugin lifecycle owner.
type pluginRuntimeReady struct{}

// providePluginKV shares durable storage and maintenance across all process generations.
func providePluginKV(store *postgres.Store) *plugin.KV {
	return plugin.NewKV(store, slog.Default())
}

// providePluginProcesses constructs the shared native process collection for this host.
func providePluginProcesses(cfg *config.Config, info BuildInfo, host *provider.HostServices) *provider.Processes {
	return provider.NewProcesses(plugin.RuntimeOptions{HostVersion: info.Version, InstanceID: uuid.NewString(), Timezone: cfg.Timezone, HTTPProxy: cfg.Plugins.PluginHTTPProxy}, slog.Default(), host.Bind)
}

// providePluginPackages constructs verified package storage before accepting administrative uploads.
func providePluginPackages(cfg *config.Config) (*provider.PackageFiles, error) {
	return provider.NewPackageFiles(cfg.Plugins.Dir, provider.DownloadOptions{URLPrefix: cfg.Plugins.DownloadURLPrefix, ProxyURL: cfg.Plugins.DownloadProxy})
}

// providePluginConfiguration reuses the shared identity cipher and persistent-key requirement.
func providePluginConfiguration(cfg *config.Config, cipher identity.SecretEncryptor) *plugin.Configuration {
	return plugin.NewConfiguration(cipher, cfg.Security.SecretEncryptionKeyConfigured)
}

// providePluginSource overlays development records on the shared durable installation store.
// Capability policy reads and the manager use this same source instance. Loading
// remains deferred until lifecycle startup establishes development identity ownership.
func providePluginSource(cfg *config.Config, store *postgres.Store, configuration *plugin.Configuration) *plugin.DevSource {
	return plugin.NewDevSource(plugin.DevOptions{Enabled: cfg.Plugins.Enabled, DevMode: cfg.Plugins.DevMode, Directories: cfg.Plugins.DevDirs}, provider.DirectoryReader{}, store, configuration, slog.Default())
}

// providePluginManager assembles verified storage, installation policy, and process ownership.
// The source is shared with host capabilities to avoid a manager/process dependency
// cycle. Construction leaves reconciliation to the registered application lifecycle hook.
func providePluginManager(cfg *config.Config, info BuildInfo, source *plugin.DevSource, configuration *plugin.Configuration, packages *provider.PackageFiles, processes *provider.Processes) *plugin.Manager {
	return plugin.NewManager(plugin.ManagerOptions{
		Enabled: cfg.Plugins.Enabled, MaxRunning: cfg.Plugins.MaxRunning, HostVersion: info.Version, StateRoot: filepath.Join(cfg.Plugins.Dir, "state"),
	}, source, packages, processes, configuration, slog.Default())
}

// providePluginService binds administration to the same runtime manager and staged uploads.
func providePluginService(manager *plugin.Manager, packages *provider.PackageFiles, catalog *plugin.CatalogService, processes *provider.Processes) *plugin.Service {
	return plugin.NewService(manager, packages, catalog, processes)
}

// providePluginCatalog binds signed transfers and durable sources to host compatibility.
// Construction performs no network access. The lifecycle hook ensures the builtin
// registration before HTTP startup and owns refresh cancellation before storage closes.
func providePluginCatalog(cfg *config.Config, info BuildInfo, store *postgres.Store, source *plugin.DevSource) (*plugin.CatalogService, error) {
	fetcher, err := provider.NewIndexClient(cfg.Plugins.DownloadProxy, plugin.OfficialIndexKeys)
	if err != nil {
		return nil, err
	}
	return plugin.NewCatalogService(plugin.CatalogOptions{
		Enabled: cfg.Plugins.Enabled, OfficialURL: cfg.Plugins.OfficialIndexURL,
		Host: manifest.HostInfo{Version: info.Version, APIVersions: []int{1}, Platform: runtime.GOOS + "/" + runtime.GOARCH, Permissions: manifest.Permissions()},
	}, store, source, fetcher, slog.Default()), nil
}

// providePluginRuntime registers asynchronous reconciliation and bounded plugin shutdown.
// Startup restores enabled installations through the shared manager. The stop hook
// has an eight-second budget within the application's broader shutdown context.
// Enabled installations also register the KV worker in the same shutdown stage.
func providePluginRuntime(cfg *config.Config, plugins *plugin.Manager, kv *plugin.KV, catalog *plugin.CatalogService, manager *lifecycle.Manager) *pluginRuntimeReady {
	manager.Register(lifecycle.Hook{Name: "PluginCatalog", StartOrder: 974, StopOrder: shutdownStagePlugins, Start: catalog.Start, Stop: catalog.Stop})
	if cfg.Plugins.Enabled {
		manager.Register(lifecycle.Hook{Name: "PluginKV", StartOrder: 975, StopOrder: shutdownStagePlugins, Start: kv.Start, Stop: kv.Stop})
	}
	manager.Register(lifecycle.Hook{Name: "PluginRuntime", StartOrder: 976, StopOrder: shutdownStagePlugins, Start: plugins.Start, Stop: func(ctx context.Context) error {
		budget, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return plugins.Stop(budget)
	}})
	return &pluginRuntimeReady{}
}
