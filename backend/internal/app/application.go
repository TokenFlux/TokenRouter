package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

// application.go constructs the application and coordinates startup, HTTP
// serving, and lifecycle cleanup.
// Application construction can acquire resources before a later dependency
// fails. Initialize supplies one lifecycle manager and background-task tracker
// to the generated graph, registering the log backend before construction
// begins. Its named error result lets deferred rollback join cleanup failures
// with the original construction error under a fresh thirty-second budget.
//
// Run chooses normal stop after it receives successful lifecycle startup;
// failure or cancellation while awaiting startup selects rollback. Startup uses a
// buffered result channel so cancellation can initiate cleanup while the start
// callback is still returning. HTTP serving has its separate shutdown phase,
// followed by application cleanup. First-attempt lifecycle results govern
// later Cleanup calls and can report unfinished callbacks instead of complete
// resource release.

// BuildInfo carries build metadata from the process entry point to application modules.
type BuildInfo struct {
	Version   string // displayed build version
	BuildType string // release distribution type
}

// Application owns the HTTP server and the lifecycle of its dependencies.
type Application struct {
	// Server and lifecycle are assembled before Run. Request tracking registers
	// Server's shutdown work with lifecycle before startup seals further hook
	// registration, so both share the constructed application's resource ordering.
	Server    *http.Server       // application HTTP server
	lifecycle *lifecycle.Manager // ordered resource lifecycle
}

// Initialize constructs the application graph using cfg, build metadata, and
// the supplied restarter. Workers start later through Run. A construction
// error invokes rollback with a separate thirty-second background context and
// joins its failure with the original error.
//
// The manager owns acquired-resource registrations, including the log backend
// registered before generated construction. Cleanup therefore includes
// resources acquired before a later constructor fails. The background tracker
// is shared by graph consumers so their asynchronous effects have an
// application owner. A rollback timeout remains an incomplete-cleanup error;
// it does not guarantee every registered callback has finished.
// @project-doc docs/architecture/system_architecture.md#dependency_layers
func Initialize(ctx context.Context, cfg *config.Config, buildInfo BuildInfo, restarter *lifecycle.Restarter) (_ *Application, err error) {
	configureUpstreamIdentities(cfg)
	manager := lifecycle.New(func(name, event string, err error) {
		// LogBackend has closed the files used by this reporter. Skip its
		// successful completion message so reporting does not write after file release.
		if name == "LogBackend" && event == "stopped" {
			return
		}
		// Operation errors determine severity. Component names remain log values,
		// allowing ordinary hook completion and failed cleanup to share this reporter.
		if err != nil {
			logging.S().Errorf("[Lifecycle] %s %s err=%v", event, name, err)
		} else {
			logging.S().Infof("[Lifecycle] %s %s err=<nil>", event, name)
		}
	})
	// Flush buffered logs before closing owned files at the last cleanup level.
	// The reporter suppresses this hook's successful completion after file closure.
	manager.Register(lifecycle.Hook{Name: "LogBackend", StartOrder: -2000, StopOrder: 1000, Stop: func(context.Context) error { logging.Sync(); return logging.CloseFiles() }})
	tasks := installBackgroundTasks(manager)
	// Generated construction may fail after acquiring resources. Roll back the
	// manager registrations with a fresh budget and retain both failure causes.
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err = errors.Join(err, manager.Rollback(cleanupCtx))
		}
	}()
	return initializeApplication(ctx, cfg, buildInfo, manager, restarter, tasks)
}

// provideApplication attaches HTTP request tracking and registers Ops
// WebSocket shutdown using the supplied manager. Its runtimeReady dependency
// makes Wire resolve prior registrations first. Call before manager startup or
// shutdown and before HTTP serving.
func provideApplication(
	server *http.Server,
	manager *lifecycle.Manager,
	_ *runtimeReady,
	opsService *ops.OpsService,
	_ *ops.ErrorLogQueue,
) *Application {
	lifecycle.TrackRequests(server, manager)
	manager.Register(lifecycle.Hook{
		Name:       "OpsWSRuntime",
		StartOrder: 983,
		StopOrder:  17,
		Stop: func(context.Context) error {
			opsService.Realtime().Stop()
			return nil
		},
	})
	return &Application{Server: server, lifecycle: manager}
}

// installBackgroundTasks constructs the tracker shared by application effects
// and registers final Stop at its chosen lifecycle level. Call before manager
// startup or shutdown; earlier reusable barriers leave task admission
// available for later producers.
func installBackgroundTasks(manager *lifecycle.Manager) *lifecycle.Tasks {
	tasks := lifecycle.NewTasks()
	manager.Register(lifecycle.Hook{
		Name:       "ApplicationBackgroundTasks",
		StartOrder: 932,
		StopOrder:  68,
		Stop:       tasks.Stop,
	})
	return tasks
}

// Run starts registered lifecycle hooks, then serves the application HTTP
// server after startup succeeds. A startup failure or cancellation before
// success returns its cause and selects rollback; normal serving completion
// selects ordered Stop.
//
// Startup publishes one result through a buffered channel, allowing
// cancellation to leave that receive path while cleanup waits on the lifecycle
// startup signal. The deferred cleanup uses a fresh thirty-second context
// independent of ctx and joins its error into the named result. HTTP shutdown
// is handled separately by lifecycle.Serve. The started flag reflects the
// selected startup result, so cancellation racing successful startup can still
// choose rollback.
// @project-doc docs/architecture/system_architecture.md#startup_and_shutdown
func (a *Application) Run(ctx context.Context) (err error) {
	started := false
	// Cleanup runs even when startup or serving fails. Use a fresh context so
	// application cancellation does not skip the selected drain or rollback phase.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if started {
			err = errors.Join(err, a.lifecycle.Stop(cleanupCtx))
		} else {
			err = errors.Join(err, a.lifecycle.Rollback(cleanupCtx))
		}
	}()
	// Cancellation can leave the startup receive path before Start returns.
	// Buffer its one result so that goroutine can finish publishing without this receiver.
	result := make(chan error, 1)
	go func() { result <- a.lifecycle.Start(ctx) }()
	select {
	case err = <-result:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	started = true
	return lifecycle.Serve(ctx, a.Server)
}

// Cleanup requests the lifecycle manager's normal stop for constructed
// applications and external fixtures. It reuses any prior Stop or Rollback
// attempt, including that attempt's context and result; its own ctx controls
// only a first cleanup attempt.
func (a *Application) Cleanup(ctx context.Context) error { return a.lifecycle.Stop(ctx) }
