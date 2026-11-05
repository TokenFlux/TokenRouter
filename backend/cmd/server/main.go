package main

//go:generate go run github.com/google/wire/cmd/wire ../../internal/app

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/app"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/setup"
	"github.com/TokenFlux/TokenRouter/internal/web"
)

// main.go selects version output, terminal setup, web setup, or full
// application startup and hands serving to lifecycle. Bootstrap logging exists
// before configuration can be read. Entry paths that end before an application
// owns resources therefore still need a bounded attempt to flush their logs,
// and a flush timeout remains part of the error returned to main.
//
// The setup and full-server paths each assume log-cleanup ownership before
// their caller returns. Named errors let deferred cleanup join its failure
// with the startup or serving cause. Signal cancellation and the Linux
// restarter share the same serving context; closing the restarter retires
// pending work before the deferred signal-stop call runs. main decides the
// process exit status after these return paths finish their cleanup attempts.

var (
	// embeddedVersion stores the build-time VERSION contents used when the
	// linker supplies no version with nonblank content to the process entry.
	//go:embed VERSION
	embeddedVersion string

	// Version, Commit, Date, and BuildType are linker-overridable metadata read
	// by version output and application construction. init resolves blank Version
	// from embeddedVersion; the remaining fields keep their assigned defaults.
	Version   = ""        // displayed build version
	Commit    = "unknown" // source commit identifier
	Date      = "unknown" // recorded build timestamp
	BuildType = "source"  // "source" for manual builds, "release" for CI builds (set by ldflags)
)

// init resolves the process version before flags and application metadata use it.
// A linker-supplied Version with nonblank trimmed content remains verbatim;
// otherwise trimmed embedded contents are used, with a development label for an
// empty embedded file.
func init() {
	if strings.TrimSpace(Version) != "" {
		return
	}

	Version = strings.TrimSpace(embeddedVersion)
	// An empty embedded file still needs a display value for version output
	// and application build metadata, so source builds receive the development label.
	if Version == "" {
		Version = "0.0.0-dev"
	}
}

// main initializes bootstrap logging and reports run's final result as process status.
// run owns entry-path cleanup before returning; an error is written to stderr
// before exiting with status one, while successful completion returns normally.
func main() {
	logging.InitBootstrap()
	err := run()
	if err != nil {
		// Reporting is best effort after cleanup. A stderr write failure must
		// not change the nonzero exit status selected for the server's error.
		_, _ = fmt.Fprintf(os.Stderr, "Server failed: %v\n", err)
		os.Exit(1)
	}
}

// run selects the requested startup path and returns its final cleanup-aware result.
// Version output precedes offline configuration migration and terminal setup;
// those command paths finish before signal registration. Normal startup initializes
// from installation inputs, serves the
// web setup wizard, or constructs the configured application.
//
// The initial deferred log flush covers paths that end during this selection.
// Setting syncOnReturn false transfers that responsibility to the selected
// server entry, whose own manager supplies its cleanup budget. Signal cancellation
// and restarts stop the same serving context; restarter cleanup finishes before
// the deferred signal-stop call runs at this entry's return.
func run() (err error) {
	syncOnReturn := true
	// Keep early-return diagnostics flushable before a server manager exists.
	// Joining the flush result retains both the selected-path error and cleanup failure.
	defer func() {
		if syncOnReturn {
			err = errors.Join(err, syncBootstrapLogs())
		}
	}()
	setupMode := flag.Bool("setup", false, "Run setup wizard in CLI mode")
	showVersion := flag.Bool("version", false, "Show version information")
	migrateConfig := flag.Bool("migrate-config", false, "Write an upgraded configuration copy")
	configSource := flag.String("config", "", "Source configuration for -migrate-config")
	configOutput := flag.String("out", "", "Migration output path, or - for stdout")
	flag.Parse()
	if *showVersion {
		log.Printf("TokenRouter %s (commit: %s, built: %s)\n", Version, Commit, Date)
		return nil
	}
	if *migrateConfig {
		return config.MigrateConfig(*configSource, *configOutput, os.Stdout, os.Stderr)
	}
	if *setupMode {
		return setup.RunCLI()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	restarter := lifecycle.NewRestarter(runtime.GOOS, stop)
	// Close cancels or waits for restart callbacks before the deferred signal-stop
	// call runs, retiring timer work that also cancels the shared serving context.
	defer restarter.Close()
	if setup.NeedsSetup() {
		if setup.AutoSetupEnabled() {
			log.Println("Auto setup mode enabled...")
			if err := setup.AutoSetupFromEnv(); err != nil {
				return fmt.Errorf("auto setup failed: %w", err)
			}
		} else {
			log.Println("First run detected, starting setup wizard...")
			// The setup manager now owns the final log flush, including serving
			// errors. Disable this outer flush before returning through that manager.
			syncOnReturn = false
			return runSetupServer(ctx, restarter)
		}
	}
	// runMainServer flushes early initialization failures and hands later cleanup
	// to Application, keeping each return path under its selected log owner.
	syncOnReturn = false
	return runMainServer(ctx, restarter)
}

// runSetupServer serves installation routes and optional embedded frontend assets.
// Supply the serving context and setup restarter; request tracking and log sync
// belong to its lifecycle manager. Cleanup uses a fresh thirty-second budget and
// joins manager failure with the serving result after HTTP's separate shutdown phase.
func runSetupServer(ctx context.Context, restarter *lifecycle.Restarter) (err error) {
	manager := lifecycle.New()
	manager.Register(lifecycle.Hook{Name: "SetupLogs", StopOrder: 1000, Stop: func(context.Context) error { logging.Sync(); return nil }})
	// Serving cancellation must not consume the cleanup budget. Use a fresh
	// context so request tracking can finish before the final SetupLogs hook runs.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = errors.Join(err, manager.Stop(cleanupCtx))
	}()
	r := gin.New()
	r.Use(middleware.Recovery())
	r.Use(middleware.CORS(config.CORSConfig{}))
	r.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true, Policy: config.DefaultCSPPolicy}, nil))

	setup.RegisterRoutes(r, restarter)

	if web.HasEmbeddedFrontend() {
		r.Use(web.ServeEmbeddedFrontend())
	}

	// The installation listener uses SERVER_HOST and SERVER_PORT before a runtime
	// configuration exists; discovery of an installed file suppresses this path.
	addr := setup.ListenAddress()
	log.Printf("Setup wizard available at http://%s", addr)
	log.Println("Complete the setup wizard to configure TokenRouter")

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	server := &http.Server{
		Addr:    addr,
		Handler: r,
		// Setup has a fixed header-read budget to bound incomplete installation
		// requests before route handlers begin consuming their submitted configuration.
		ReadHeaderTimeout: 30 * time.Second,
		// Reuse browser connections within this two-minute idle window.
		IdleTimeout: 120 * time.Second,
		Protocols:   protocols,
	}

	lifecycle.TrackRequests(server, manager)
	return lifecycle.Serve(ctx, server)
}

// runMainServer loads bootstrap configuration and runs the constructed application.
// Configuration or logger initialization failures use the entry's bounded log
// flush. After logging initialization succeeds, app.Initialize owns rollback and
// logger cleanup, and Application.Run owns subsequent serving and shutdown.
// @project-doc docs/architecture/system_architecture.md#startup_and_shutdown
func runMainServer(ctx context.Context, restarter *lifecycle.Restarter) (err error) {
	syncOnReturn := true
	// Before Initialize registers LogBackend, retain a bounded bootstrap flush
	// for configuration and logger failures so their diagnostics survive early return.
	defer func() {
		if syncOnReturn {
			err = errors.Join(err, syncBootstrapLogs())
		}
	}()
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := logging.Init(app.OptionsFromConfig(cfg.Log, cfg.Environment)); err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}
	// Initialize registers LogBackend before graph construction, so its rollback
	// now owns file closure and log sync even if a later dependency fails.
	syncOnReturn = false
	application, err := app.Initialize(ctx, cfg, app.BuildInfo{Version: Version, BuildType: BuildType}, restarter)
	if err != nil {
		return fmt.Errorf("initialize application: %w", err)
	}
	return application.Run(ctx)
}

// syncBootstrapLogs attempts log synchronization through a thirty-second lifecycle budget.
// The manager reports timeout or unfinished cleanup, while logging.Sync discards
// its underlying writer error. This entry-point cleanup returns the
// manager result for joining with an earlier failure.
func syncBootstrapLogs() error {
	manager := lifecycle.New()
	manager.Register(lifecycle.Hook{Name: "BootstrapLogs", Stop: func(context.Context) error { logging.Sync(); return nil }})
	// Sync takes no context, so this budget bounds the manager's wait; a timed-out
	// callback may continue while its unfinished status returns to the entry path.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return manager.Stop(ctx)
}
