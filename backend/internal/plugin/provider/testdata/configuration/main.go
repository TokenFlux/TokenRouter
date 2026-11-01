package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// main.go exposes configuration delivery and initialization barriers to process tests.
// The fixture reports author-owned applied values separately from the SDK's
// configuration handle, allowing tests to distinguish persisted intent from a
// completed notification. One executable can omit the optional watcher entirely
// or reject its first notification to exercise restart and identical retry paths.
//
// A requested startup barrier writes an arrival marker before waiting for
// cancellation. The host can therefore update configuration during an actual
// Init RPC without depending on a sleep or process scheduling speed. Atomic
// observations let HTTP requests inspect notification outcomes while the SDK
// executes callbacks on separate RPC goroutines.

// implementation provides observable initialization and HTTP behavior without a configuration watcher.
type implementation struct {
	amount        atomic.Int64 // author-applied configuration value
	notifications atomic.Int64 // received configuration callback count
	// stateDir is assigned during Init and remains immutable after the SDK publishes
	// readiness; configuration callbacks use it only for isolated test arrival markers.
	stateDir string
}

// watcher adds optional configuration notifications to the ordinary fixture implementation.
type watcher struct {
	*implementation // shared initialization and request behavior
}

// main selects the fixture's optional watcher from its host-bound plugin identity.
func main() {
	base := &implementation{}
	if os.Getenv("TR_PLUGIN_ID") == "test.nowatcher" {
		sdk.Serve(base)
		return
	}
	sdk.Serve(&watcher{implementation: base})
}

// Init records applied values or waits at the explicitly requested startup barrier.
// A marker in the isolated state directory confirms arrival. Cancellation ends
// the barrier so the host can replace this incomplete generation safely.
//
// The blocked generation never publishes author state or an initialized SDK
// environment. Its replacement receives a new configuration without the barrier,
// allowing tests to verify that readiness belongs to the updated Init payload
// instead of depending on a race between separate process startup delays.
func (p *implementation) Init(ctx context.Context, env *sdk.Env) error {
	p.stateDir = env.StateDir
	if env.Config.Bool("blockInit") {
		if err := os.WriteFile(filepath.Join(env.StateDir, "initializing"), []byte("ready"), 0o600); err != nil {
			return err
		}
		// The marker makes the blocked Init observable before a configuration update
		// cancels this generation, without allowing old values to become ready.
		<-ctx.Done()
		return ctx.Err()
	}
	p.amount.Store(env.Config.Int("amount"))
	return nil
}

// Handler reports author-applied configuration and completed notification attempts for tests.
func (p *implementation) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sdk.WriteJSON(w, http.StatusOK, map[string]int64{"amount": p.amount.Load(), "notifications": p.notifications.Load()})
	})
}

// ConfigChanged applies values or enters the explicitly requested callback failure mode.
// A rejected first notification preserves author state for an identical retry.
// A blocking notification publishes an arrival marker and waits for cancellation,
// allowing tests to distinguish request draining from the shorter Shutdown budget.
func (p *watcher) ConfigChanged(ctx context.Context, config sdk.Config) error {
	attempt := p.notifications.Add(1)
	if config.Bool("blockChange") {
		if err := os.WriteFile(filepath.Join(p.stateDir, "changing"), []byte("ready"), 0o600); err != nil {
			return err
		}
		// Hold the lifecycle callback after its arrival marker so the host must
		// retain its management lease until explicit request cancellation releases it.
		<-ctx.Done()
		return ctx.Err()
	}
	if config.Bool("failOnce") && attempt == 1 {
		return fmt.Errorf("fixture rejected its first configuration notification")
	}
	p.amount.Store(config.Int("amount"))
	return nil
}
