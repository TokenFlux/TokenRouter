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

	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/app"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/setup"
	"github.com/TokenFlux/TokenRouter/internal/web"

	"github.com/gin-gonic/gin"
)

//go:embed VERSION
var embeddedVersion string

// Build-time variables (can be set by ldflags)
var (
	Version   = ""
	Commit    = "unknown"
	Date      = "unknown"
	BuildType = "source" // "source" for manual builds, "release" for CI builds (set by ldflags)
)

func init() {
	// 如果 Version 已通过 ldflags 注入（例如 -X main.Version=...），则不要覆盖。
	if strings.TrimSpace(Version) != "" {
		return
	}

	// 默认从 embedded VERSION 文件读取版本号（编译期打包进二进制）。
	Version = strings.TrimSpace(embeddedVersion)
	if Version == "" {
		Version = "0.0.0-dev"
	}
}

// initLogger configures the default slog handler based on gin.Mode().
// In non-release mode, Debug level logs are enabled.
func main() {
	logger.InitBootstrap()
	err := run()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Server failed: %v\n", err)
		os.Exit(1)
	}
}

// run 将退出决定留在主协程，清理完成后才返回给 main。
func run() (err error) {
	syncOnReturn := true
	defer func() {
		if syncOnReturn {
			err = errors.Join(err, syncBootstrapLogs())
		}
	}()
	setupMode := flag.Bool("setup", false, "Run setup wizard in CLI mode")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()
	if *showVersion {
		log.Printf("Sub2API %s (commit: %s, built: %s)\n", Version, Commit, Date)
		return nil
	}
	if *setupMode {
		return setup.RunCLI()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	restarter := lifecycle.NewRestarter(runtime.GOOS, stop)
	defer restarter.Close()
	if setup.NeedsSetup() {
		if setup.AutoSetupEnabled() {
			log.Println("Auto setup mode enabled...")
			if err := setup.AutoSetupFromEnv(); err != nil {
				return fmt.Errorf("auto setup failed: %w", err)
			}
		} else {
			log.Println("First run detected, starting setup wizard...")
			syncOnReturn = false
			return runSetupServer(ctx, restarter)
		}
	}
	syncOnReturn = false
	return runMainServer(ctx, restarter)
}

func runSetupServer(ctx context.Context, restarter *lifecycle.Restarter) (err error) {
	manager := lifecycle.New()
	manager.Register(lifecycle.Hook{Name: "SetupLogs", StopOrder: 1000, Stop: func(context.Context) error { logger.Sync(); return nil }})
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = errors.Join(err, manager.Stop(cleanupCtx))
	}()
	r := gin.New()
	r.Use(middleware.Recovery())
	r.Use(middleware.CORS(config.CORSConfig{}))
	r.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true, Policy: config.DefaultCSPPolicy}, nil))

	// Register setup routes
	setup.RegisterRoutes(r, restarter)

	// Serve embedded frontend if available
	if web.HasEmbeddedFrontend() {
		r.Use(web.ServeEmbeddedFrontend())
	}

	// Get server address from config.yaml or environment variables (SERVER_HOST, SERVER_PORT)
	// This allows users to run setup on a different address if needed
	addr := config.GetServerAddress()
	log.Printf("Setup wizard available at http://%s", addr)
	log.Println("Complete the setup wizard to configure Sub2API")

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	server := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
		Protocols:         protocols,
	}

	lifecycle.TrackRequests(server, manager)
	return lifecycle.Serve(ctx, server)
}

// runMainServer 装配正式服务并协调进程启动与关闭。
// @project-doc docs/architecture/system_architecture.md#startup_and_shutdown
func runMainServer(ctx context.Context, restarter *lifecycle.Restarter) (err error) {
	syncOnReturn := true
	defer func() {
		if syncOnReturn {
			err = errors.Join(err, syncBootstrapLogs())
		}
	}()
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := logger.Init(app.OptionsFromConfig(cfg.Log)); err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}
	if cfg.RunMode == config.RunModeSimple {
		log.Println("⚠️  WARNING: Running in SIMPLE mode - billing and quota checks are DISABLED")
	}
	syncOnReturn = false
	application, err := app.Initialize(ctx, cfg, app.BuildInfo{Version: Version, BuildType: BuildType}, restarter)
	if err != nil {
		return fmt.Errorf("initialize application: %w", err)
	}
	return application.Run(ctx)
}

// syncBootstrapLogs 用于没有完整应用图的入口，日志同步也必须有界。
func syncBootstrapLogs() error {
	manager := lifecycle.New()
	manager.Register(lifecycle.Hook{Name: "BootstrapLogs", Stop: func(context.Context) error { logger.Sync(); return nil }})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return manager.Stop(ctx)
}
