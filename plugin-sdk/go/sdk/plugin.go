package sdk

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// plugin.go declares the lifecycle and contribution interfaces implemented by
// plugin authors. Init is required, while HTTP, tasks, health, configuration,
// and shutdown behavior are selected by implemented interfaces. The process
// adapter owns transport setup and supplies host capabilities through Env.

// Plugin initializes one process generation before it can accept business calls.
type Plugin interface {
	// Init receives validated configuration and a connected host callback client.
	// It must honor cancellation and return only when contributions are ready;
	// a failure prevents this process generation from becoming healthy.
	Init(context.Context, *Env) error
}

// HTTPPlugin supplies ordinary Go handlers for declared plugin HTTP routes.
type HTTPPlugin interface {
	// Handler returns a concurrency-safe handler for requests authorized by the host.
	Handler() http.Handler
}

// TaskPlugin executes the named jobs declared by the plugin manifest.
type TaskPlugin interface {
	// RunTask executes one scheduled occurrence with its host-selected timestamp.
	// The host prevents overlapping runs of the same task. Implementations must
	// observe context cancellation when the configured task deadline expires.
	RunTask(context.Context, string, time.Time) error
}

// ConfigWatcher applies configuration changes without restarting the plugin process.
type ConfigWatcher interface {
	// ConfigChanged receives a snapshot after Env.Config publishes the new values.
	// It must honor cancellation; returning an error leaves the host's persisted
	// configuration intact and allows diagnosis without reverting settings silently.
	ConfigChanged(context.Context, Config) error
}

// HealthChecker augments the SDK's initialization and stalled-call health checks.
type HealthChecker interface {
	// Health returns nil while plugin-owned dependencies can serve business calls.
	// Implementations must honor the short host deadline. Repeated failures cause
	// the supervisor to stop and restart this process generation.
	Health(context.Context) error
}

// Shutdowner releases plugin-owned resources during the host's bounded drain period.
type Shutdowner interface {
	// Shutdown stops plugin-owned background work and releases external resources.
	// The host has stopped admitting requests before invoking it. Cancellation
	// signals that the remaining graceful shutdown budget has expired.
	Shutdown(context.Context) error
}

// Env holds process-generation identity, configuration, logging, and host capabilities.
type Env struct {
	PluginID        string   // publisher-qualified plugin identity
	Version         string   // running plugin release version
	PreviousVersion string   // previous running release version
	HostVersion     string   // host release build version
	InstanceID      string   // host process instance identity
	StateDir        string   // disposable local cache directory
	Permissions     []string // administrator-granted capability names
	// Config keeps a stable concurrent-read handle; the SDK publishes complete snapshots whenever the host applies new configuration.
	Config Config
	Host   *Host        // authenticated host capability client
	Logger *slog.Logger // structured plugin stderr logger
}
