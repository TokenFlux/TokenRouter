package egress

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// proxy_expiry.go runs periodic proxy expiration sweeps and coordinates their
// startup and shutdown.
// Proxy expiration maintenance runs an immediate sweep and then repeats on its
// configured interval. Construction leaves the worker idle, and startup and
// terminal stopping share state so repeated Start calls cannot register
// duplicate workers or revive a service already stopped.
//
// The worker owns its ticker and completion signal. Each sweep receives a
// ten-second child context, while StopContext cancels the retained parent and
// waits outside the state lock. A caller timeout ends that wait but leaves the
// service stopped; a later wait can still observe eventual completion.
// Repository code needs to honor cancellation for shutdown to finish promptly.

// ProxyExpiryService owns the registered expiration worker and terminal startup
// seal.
//
// mu protects started, stopped, cancel, and done. Start registers one worker
// and completion signal; StopContext seals startup and cancels that worker
// before waiting outside the lock.
type ProxyExpiryService struct {
	proxyRepo ProxyRepository    // expiration sweep repository dependency
	interval  time.Duration      // recurring sweep tick duration
	now       func() time.Time   // expiration comparison clock callback
	mu        sync.Mutex         // worker lifecycle state lock
	started   bool               // worker startup registered flag
	stopped   bool               // terminal startup rejection flag
	cancel    context.CancelFunc // registered worker cancellation function
	done      chan struct{}      // registered worker completion signal
}

// NewProxyExpiryService constructs an idle worker with the repository,
// interval, and first optional clock. An absent or nil first clock selects
// time.Now, and Start requires a repository and positive interval.
func NewProxyExpiryService(repo ProxyRepository, interval time.Duration, clocks ...func() time.Time) *ProxyExpiryService {
	now := time.Now
	if len(clocks) > 0 && clocks[0] != nil {
		now = clocks[0]
	}
	return &ProxyExpiryService{proxyRepo: repo, interval: interval, now: now}
}

// Start registers one worker and launches its immediate expiration sweep when
// the repository and interval are usable. Nil receivers, missing repositories,
// and nonpositive intervals do nothing. The instance cannot start again after
// StopContext marks it stopped.
//
// The lifecycle lock covers startup flags, cancellation, and completion-channel
// creation before the goroutine launches. Concurrent starts therefore share one
// registration, and a later stop sees its cancellation and completion handles.
// The application owns the corresponding stop and repository shutdown ordering.
// @project-doc docs/operations/upstream_transport_security.md#upstream_proxy_lifecycle
func (s *ProxyExpiryService) Start() {
	if s == nil || s.proxyRepo == nil || s.interval <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return
	}
	// Register cancellation and completion while startup is serialized, so a
	// concurrent stop sees the entire worker lifetime before the goroutine begins.
	s.started = true
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(ctx, s.done)
}

// run performs the immediate sweep and then processes serial ticker events
// until cancellation. The worker owns ticker cleanup and closes done on exit,
// letting stop callers observe the same registered lifetime.
func (s *ProxyExpiryService) run(ctx context.Context, done chan struct{}) {
	// Publish completion after the worker returns; the deferred ticker stop
	// runs first so shutdown waiters observe a lifetime whose periodic ticker is stopped.
	defer close(done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			s.sweep(ctx)
		}
	}
}

// sweep invokes expiration maintenance with a ten-second child budget when
// parent remains live. It logs failures and allows the recurring worker to
// continue; the repository can report changes already committed before a later
// failure.
func (s *ProxyExpiryService) sweep(parent context.Context) {
	if parent.Err() != nil {
		return
	}
	// Each repository scan gets a child budget independent of the recurring tick
	// interval, while parent cancellation still reaches that in-progress scan.
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	changed, err := s.proxyRepo.SweepExpiredProxies(ctx, s.now())
	// A failed scan leaves later ticker events available for another attempt.
	// Log the repository result because sweep has no direct error-return caller.
	if err != nil {
		logging.LegacyPrintf("service.proxy_expiry", "[ProxyExpiry] sweep expired proxies failed: %v", err)
		return
	}
	if changed > 0 {
		logging.LegacyPrintf("service.proxy_expiry", "[ProxyExpiry] re-routed %d providers off expired proxies", changed)
	}
}

// StopContext seals future startup, cancels the registered worker, and waits
// for its completion or ctx. A nil receiver or unstarted worker completes
// immediately. The supplied context needs to be nonnil.
//
// Cancellation and the stopped flag are published under the lifecycle lock
// before waiting outside it. Already completed workers win the initial
// nonblocking check; when completion and cancellation race in the later select,
// either permitted result may be returned. A timed-out wait leaves cleanup
// pending, and another StopContext can await the same completion signal.
func (s *ProxyExpiryService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	// Capture the registered completion signal before releasing lifecycle state.
	// Waiting outside mu allows the worker to finish while later stops share its signal.
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("ProxyExpiryService stop: %w", ctx.Err())
	}
}

// Stop seals startup and waits for the expiration worker using a background
// context. The call can wait until repository work returns because this wrapper
// supplies no additional deadline for shutdown.
func (s *ProxyExpiryService) Stop() { _ = s.StopContext(context.Background()) }
