package provider

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// host_calls.go tracks host work independently of leases on the plugin process.
// A plugin can initiate callbacks from its own background goroutines, so waiting
// for host-to-plugin requests alone cannot prove that database writes have ended.
// Each generation therefore closes callback admission and waits for its admitted
// host handlers before the process owner publishes cleanup completion or starts
// a replacement generation that could touch the same plugin data.
//
// Cancellation signals handlers to stop but does not count as their completion.
// The final callback return closes the drain channel, preserving the boundary
// even when a handler needs bounded post-commit cleanup after cancellation.
// A timed-out management caller can return an error while this tracked cleanup
// continues, leaving installation records available for a later safe retry.

// hostCalls owns one generation's callback admission and cancellation handles.
type hostCalls struct {
	logger *slog.Logger // generation-bound host diagnostic logger
	// mu protects closing, active, next, and drained; application handlers and cancellation functions execute outside the lock to avoid reentrant cleanup.
	mu      sync.Mutex
	closing bool                          // new callback admission closed
	active  map[uint64]context.CancelFunc // admitted callback cancellation handles
	next    uint64                        // next callback admission identity
	drained chan struct{}                 // zero active callbacks signal
}

// newHostCalls creates an open tracker with an initially completed drain signal.
func newHostCalls(logger *slog.Logger) *hostCalls {
	drained := make(chan struct{})
	close(drained)
	return &hostCalls{logger: logger, active: make(map[uint64]context.CancelFunc), drained: drained}
}

// intercept retains host work until its actual handler return, including panic paths.
// Closing admission rejects later calls with HOST_UNAVAILABLE. Each admitted
// handler receives a cancellable child context owned by this generation.
//
// Active registration and drain-channel replacement share the admission lock,
// preventing shutdown from observing zero work between those operations.
// Completion removes only its captured identity and closes the channel after
// the final handler exits. Unexpected host panics become generic RPC failures
// while diagnostics remain in the host log, preserving process availability.
func (c *hostCalls) intercept(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, resultErr error) {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return nil, hostStatus(codes.Unavailable, "HOST_UNAVAILABLE", "Plugin callback generation is stopping")
	}
	if len(c.active) == 0 {
		c.drained = make(chan struct{})
	}
	c.next++
	id := c.next
	callCtx, cancel := context.WithCancel(ctx)
	c.active[id] = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		if recovered := recover(); recovered != nil {
			c.logger.Error("Plugin host callback panicked", "method", info.FullMethod, "panic", fmt.Sprint(recovered))
			response = nil
			resultErr = hostStatus(codes.Internal, "HOST_INTERNAL", "Plugin host callback failed")
		}
		// Only actual handler completion releases the callback barrier; cancellation alone cannot establish that persistent side effects have finished.
		c.mu.Lock()
		delete(c.active, id)
		if len(c.active) == 0 {
			close(c.drained)
		}
		c.mu.Unlock()
	}()
	return handler(callCtx, request)
}

// closeAdmission rejects future callbacks and returns the current completion signal.
// The signal remains valid because closed admission prevents a new zero-to-one
// transition. Existing calls retain their contexts until cancellation or completion.
func (c *hostCalls) closeAdmission() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closing = true
	return c.drained
}

// cancelActive signals admitted handlers without treating cancellation as completion.
// Cancellation functions are copied under lock and invoked afterward, allowing
// handlers to finish and release their tracked identities without lock inversion.
func (c *hostCalls) cancelActive() {
	c.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(c.active))
	for _, cancel := range c.active {
		cancels = append(cancels, cancel)
	}
	c.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}
