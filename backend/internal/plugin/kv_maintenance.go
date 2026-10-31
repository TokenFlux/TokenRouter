package plugin

import (
	"context"
	"time"
)

// kv_maintenance.go owns periodic quota measurement and expired-record cleanup
// for the shared plugin KV capability. One background worker samples usage at
// startup and every five minutes, then removes expired rows every ten minutes.
// Cleanup commits bounded batches under a thirty-second run budget, allowing
// cancellation to stop further work while retaining completed deletions.
//
// Start publishes cancellation and completion ownership before launching work.
// Stop seals startup, cancels an existing worker, and waits for its actual return
// within the caller's deadline. A failed quota query retains the last successful
// snapshot; readers never observe a partially filled map. Database dependencies
// remain available until the application's plugin shutdown hook completes.

// Start launches at most one maintenance worker without delaying HTTP startup.
// Its owned context survives the callback. Repeated starts and starts after Stop
// do nothing, so lifecycle retries cannot duplicate sampling or expiration work.
//
// Cancellation and completion handles are installed under the same lock used
// by Stop before the worker can run. Shutdown therefore either prevents launch
// or observes the worker it must await. The initial quota sample runs inside
// that worker, keeping database failures from blocking unrelated host startup.
func (s *KV) Start(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done, s.started = cancel, make(chan struct{}), true
	// Publish lifetime ownership before launching so concurrent Stop can cancel and await the same worker without missing late activity.
	go func() {
		defer close(s.done)
		s.run(ctx)
	}()
	return nil
}

// Stop seals maintenance startup and waits for the owned worker to return.
// Cancellation does not undo completed cleanup batches. If the caller's deadline
// expires, later Stop calls can continue waiting for the same worker completion.
//
// The terminal flag and cancellation signal are published under the lifecycle
// lock before waiting. A concurrent Start cannot create another worker after
// that point. Database operations remain owned by the original worker until
// they actually return, even when this caller has exhausted its wait budget.
func (s *KV) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run schedules independent measurement and cleanup intervals until lifecycle cancellation.
// Each operation receives a fresh thirty-second budget. Errors are diagnosed
// without clearing quota samples or retrying in a tight loop during database outages.
func (s *KV) run(ctx context.Context) {
	measure := time.NewTicker(5 * time.Minute)
	expire := time.NewTicker(10 * time.Minute)
	defer measure.Stop()
	defer expire.Stop()
	s.maintain(ctx, "measurement", s.RefreshUsage)
	for {
		select {
		case <-ctx.Done():
			return
		case <-measure.C:
			s.maintain(ctx, "measurement", s.RefreshUsage)
		case <-expire.C:
			s.maintain(ctx, "expiration", s.expire)
		}
	}
}

// maintain runs one selected maintenance operation within its independent database budget.
// Cleanup may commit several batches before time expires. Cancellation and
// deadlines end that run; ordinary database errors remain visible in host diagnostics.
func (s *KV) maintain(ctx context.Context, name string, operation func(context.Context) error) {
	budget, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := operation(budget); err != nil && ctx.Err() == nil {
		s.logger.Warn("Plugin KV maintenance failed", "operation", name, "error", err)
	}
}

// RefreshUsage publishes one complete sample after successful database measurement.
// Failed reads preserve the previous limits. The store returns an independently
// owned map, which this capability treats as immutable after publication.
//
// The complete map replaces the previous map under the admission lock, so a
// successfully empty database clears old quota blocks. A failed scan cannot
// publish an incomplete namespace set or accidentally reopen growth. Requests
// already admitted continue under their captured soft-limit decision until the
// next sample changes the policy observed by later calls.
func (s *KV) RefreshUsage(ctx context.Context) error {
	usage, err := s.store.KVMeasure(ctx)
	if err != nil {
		return err
	}
	// Publish only a complete successful observation so request admission never sees missing namespaces from a partially failed database scan.
	s.mu.Lock()
	s.usage = usage
	s.mu.Unlock()
	return nil
}

// expire removes expired rows in bounded committed batches until none remain.
// The caller supplies the run deadline. Earlier deletions remain committed when
// cancellation or a later batch failure prevents the remainder from being removed.
func (s *KV) expire(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Small committed batches release row locks between calls and bound each statement's effect during normal host shutdown.
		count, err := s.store.KVExpire(ctx, 1000)
		if err != nil {
			return err
		}
		if count < 1000 {
			return nil
		}
	}
}
