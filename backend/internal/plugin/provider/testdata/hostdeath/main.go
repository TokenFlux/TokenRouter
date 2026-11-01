package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
)

// main.go provides an isolated host for native parent-death regression tests.
// It starts exactly one supplied fixture binary through the real process
// runtime, prints the child PID after initialization, and reports startup phases
// to stderr. Successful startup waits for abrupt host termination; failed startup
// cancels owned children before reporting its exit status to the parent test.
//
// Readiness covers both independently timed production phases, handshake and
// Init, rather than imposing a shorter combined fixture deadline. State changes
// and elapsed time distinguish slow executable startup from transport or author
// initialization failures. Only a running child is published on stdout, so the
// parent test starts its separate death-detection deadline after readiness and
// never counts a startup failure as successful orphan cleanup.

// main starts a child fixture and waits for deliberate host termination.
// Arguments contain only test-owned executable and state-directory paths. This
// program never loads application configuration or connects to a business database.
//
// The startup deadline includes the full handshake and Init budgets. A reported
// terminal startup failure ends the fixture after bounded child cleanup, while successful
// readiness leaves the host alive for an abrupt kill without graceful shutdown.
// Diagnostic stderr remains separate from the stdout PID consumed by the test.
func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	started := time.Now()
	options := plugin.RuntimeOptions{HostVersion: "0.0.0-dev", Timezone: "UTC"}.WithDefaults()
	logger.Info("Parent-death fixture entered main", "host_pid", os.Getpid(), "handshake_budget", options.StartTimeout, "init_budget", options.InitTimeout)
	runtime := provider.NewProcesses(options, logger, nil)
	if err := runtime.Replace(context.Background(), plugin.ProcessSpec{PluginID: "test.parentdeath", Version: "1.0.0", Executable: os.Args[1], StateDir: os.Args[2], MemoryMiB: 128, ConfigJSON: []byte(`{}`)}); err != nil {
		logger.Error("Parent-death fixture could not start its child", "error", err)
		os.Exit(3)
	}
	// Handshake and Init have independent production budgets. Allow both complete
	// phases before the fixture declares startup incomplete, plus one polling margin.
	deadline := started.Add(options.StartTimeout + options.InitTimeout + time.Second)
	var previous plugin.State
	for time.Now().Before(deadline) {
		state := runtime.Status("test.parentdeath")
		if state.State != previous {
			logger.Info("Parent-death fixture child state", "state", state.State, "child_pid", state.PID, "elapsed", time.Since(started), "error", state.LastError)
			previous = state.State
		}
		if state.State == plugin.StateRunning {
			fmt.Println(state.PID)
			select {}
		}
		if state.State == plugin.StateCrashLoop || state.State == plugin.StateFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	logger.Error("Parent-death fixture did not initialize", "elapsed", time.Since(started), "status", runtime.Status("test.parentdeath"))
	// Startup failure is not the abrupt-parent-death scenario under test. Finish
	// its owned child cleanup before exiting so failed readiness cannot leave orphans.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.StopAll(ctx); err != nil {
		logger.Error("Parent-death fixture cleanup failed", "error", err)
	}
	os.Exit(4)
}
