package sdk

import (
	"context"
	"fmt"
	"os"
	"time"
)

// parent.go terminates orphaned plugin processes independently of author callbacks.
// The platform monitor binds observation to the startup parent, while a periodic
// loop checks that relationship without using the plugin's RPC connection or
// relying on graceful shutdown by a host that may already have been killed.
//
// Unix kernels expose reparenting through the current parent PID. Windows retains
// the original parent identifier after exit, so its monitor holds a process handle
// and checks the signaled exit state instead. Keeping that handle also prevents
// PID reuse from being mistaken for the original host's continued lifetime. Normal
// SDK shutdown cancels observation and releases any platform resource it acquired.

// parentMonitor owns platform-specific observation of one captured parent process lifetime.
type parentMonitor interface {
	// alive reports whether the original parent still owns this plugin process.
	alive() bool
	// close releases resources after observation ends without terminating the parent.
	close()
}

// watchParent exits after the plugin's startup parent disappears or changes.
// Cancellation stops observation during normal shutdown. Platform setup failure
// also exits because the plugin can no longer establish its host's lifetime.
//
// The monitor is acquired once before polling, binding Windows checks to a held
// process object rather than a potentially reused numeric PID. Each observation
// uses a two-second interval and calls no author code. The ordinary cancellation
// path releases the handle; process exit lets the operating system release all
// remaining descriptors even when a plugin callback refuses to finish.
func watchParent(ctx context.Context, parent int) {
	monitor, err := newParentMonitor(parent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Plugin parent observation failed: %v\n", err)
		os.Exit(1)
	}
	defer monitor.close()
	// Observation has its own cancellation path, so blocked plugin callbacks cannot
	// prevent host-exit detection or retain a Windows process handle after normal shutdown.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !monitor.alive() {
				os.Exit(1)
			}
		}
	}
}
