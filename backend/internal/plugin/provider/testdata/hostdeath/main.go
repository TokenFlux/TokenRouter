package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
)

// main.go provides an isolated host for Linux parent-death regression tests.
// It starts exactly one supplied fixture binary through the real process
// runtime, prints the child PID after initialization, and waits for the test
// to terminate this host without running graceful cleanup.

// main starts a child fixture and waits for deliberate host termination.
// Arguments contain only test-owned executable and state-directory paths. This
// program never loads application configuration or connects to a business database.
func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	runtime := provider.NewProcesses(plugin.RuntimeOptions{HostVersion: "0.0.0-dev", Timezone: "UTC"}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := runtime.Replace(context.Background(), plugin.ProcessSpec{PluginID: "test.parentdeath", Version: "1.0.0", Executable: os.Args[1], StateDir: os.Args[2], MemoryMiB: 128, ConfigJSON: []byte(`{}`)}); err != nil {
		os.Exit(3)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state := runtime.Status("test.parentdeath")
		if state.State == plugin.StateRunning {
			fmt.Println(state.PID)
			select {}
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(4)
}
