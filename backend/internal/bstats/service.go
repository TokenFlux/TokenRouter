package bstats

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// service.go manages the lifetime and persistent identity of bStats reporting.
// The schedule follows MetricsBase.java v3.2.1: a random initial submission,
// another random offset before the periodic series, and a fixed thirty-minute
// period. Sending remains serial, including when two scheduled deadlines fall
// together. The implementation is adapted from Bastian/bstats-metrics; its MIT
// license is retained in this directory.
//
// Control changes are serialized independently of the worker's configuration
// snapshot. Disabling cancels and joins the old worker before acknowledging the
// update, while logging changes publish a snapshot without restarting timers.
// The UUID must reach persistent storage before any worker starts, so a failed
// settings read or write cannot create a transient identity on the remote service.

const (
	// reportPeriod preserves the upstream submission frequency for every enabled reporting generation.
	reportPeriod = 30 * time.Minute

	// initializationTimeout bounds optional storage work so reporting cannot
	// indefinitely block startup or administrator updates.
	initializationTimeout = 5 * time.Second
)

// Reporter sends one metrics observation using a previously persisted identity.
type Reporter interface {
	// Submit collects and sends one observation, honoring context cancellation.
	// Implementations bound network time and return errors without retrying; the
	// service retains its original schedule after a failed submission.
	Submit(context.Context, string, Config) error
}

// Service owns one reporting worker and its serialized lifecycle transitions.
type Service struct {
	store    *settings.Store      // shared settings persistence
	reporter Reporter             // bounded metrics transport
	log      func(string, ...any) // reporting error logger
	// mu serializes Start, Apply, and Stop. The worker uses only config and
	// its own arguments, so joining it while holding mu cannot deadlock.
	mu         sync.Mutex
	started    bool   // lifecycle startup admission
	serverUUID string // durably stored identifier
	// cancel and done belong to the same worker. They remain installed after
	// an interrupted join so the next transition must finish that join first.
	cancel context.CancelFunc     // active worker cancellation
	done   chan struct{}          // worker completion notification
	config atomic.Pointer[Config] // immutable reporting controls
	// delays supplies the official random durations. Tests replace it before
	// startup to advance virtual time through exact submission boundaries.
	delays func() (time.Duration, time.Duration)
}

// New constructs an idle reporter using the shared settings store.
// Construction performs no I/O. The owner calls Start after application
// assembly and Stop before closing settings storage or logging resources.
// Store and reporter are required when reporting starts; log may be nil.
func New(store *settings.Store, reporter Reporter, log func(string, ...any)) *Service {
	return &Service{store: store, reporter: reporter, log: log, delays: submissionDelays}
}

// Start loads persisted controls before admitting any reporting work.
// Read failures leave reporting paused and return their cause; Apply can recover
// after a later settings publication. Repeated starts do not replace an active schedule.
// The lifecycle admission flag remains set after initialization failure so a
// committed administrator update can recover without restarting the application.
// Concurrent control changes wait for this load to finish; a later disable
// therefore cannot be overwritten by settings read before that change.
// Settings and identity initialization share a five-second budget, shortened
// when the caller's context has an earlier deadline.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	s.started = true
	loadCtx, cancel := context.WithTimeout(ctx, initializationTimeout)
	defer cancel()
	values, err := s.store.GetMultiple(loadCtx, []string{
		SettingKeyEnabled,
		SettingKeyLogErrors,
		SettingKeyLogSentData,
		SettingKeyLogResponseStatusText,
	})
	if err != nil {
		return fmt.Errorf("read bStats settings: %w", err)
	}
	return s.applyLocked(loadCtx, ReadSettings(values))
}

// Apply reconciles committed controls after lifecycle startup and otherwise
// does nothing. Disabling cancels and waits for the current worker;
// cancellation of that wait returns an error but leaves the worker cancelled.
func (s *Service) Apply(ctx context.Context, config Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return nil
	}
	return s.applyLocked(ctx, config)
}

// applyLocked reconciles one committed configuration with the current worker.
// The caller holds mu throughout persistence and worker replacement. An enabled
// worker sees logging updates through the atomic snapshot without changing its
// scheduled deadlines, while a disabled configuration first cancels that worker.
//
// A previous interrupted stop still owns its completion channel. Its cancelled
// configuration forces a join before replacement, preventing two reporting
// generations from sending concurrently. Identity persistence precedes admission
// of the first worker and is reused for all later generations.
// Initial identity reads and writes share a five-second budget within the
// caller's deadline; timing out leaves reporting paused until another update.
func (s *Service) applyLocked(ctx context.Context, config Config) error {
	// Publish one immutable value before reconciling the timer. The old snapshot
	// distinguishes logging-only updates from enables that must replace a cancelled worker.
	previous := s.config.Swap(&config)
	if s.done != nil && config.Enabled && previous != nil && previous.Enabled {
		return nil
	}
	if err := s.stopLocked(ctx); err != nil {
		// Keep the published state disabled after an interrupted join. A later
		// enable must finish joining the cancelled worker before admitting another.
		paused := config
		paused.Enabled = false
		s.config.Store(&paused)
		return err
	}
	if !config.Enabled {
		return nil
	}
	if s.serverUUID == "" {
		identityCtx, cancel := context.WithTimeout(ctx, initializationTimeout)
		defer cancel()
		id, err := s.store.GetValue(identityCtx, SettingKeyServerUUID)
		if errors.Is(err, settings.ErrSettingNotFound) || err == nil && id == "" {
			id = uuid.NewString()
			// Persist identity before starting the timer so failed storage never
			// advertises an identifier that would be replaced on the next restart.
			if err = s.store.Set(identityCtx, SettingKeyServerUUID, id); err != nil {
				return fmt.Errorf("persist bStats instance identifier: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("read bStats instance identifier: %w", err)
		}
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("invalid bStats instance identifier: %w", err)
		}
		s.serverUUID = id
	}
	// The startup request context expires after assembly. This worker instead
	// lives until an explicit disable or application shutdown cancels and joins it.
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	initial, second := s.delays()
	go s.run(runCtx, s.done, s.serverUUID, time.Now().Add(initial), second)
	return nil
}

// Stop prevents further publication from restarting the reporting worker.
// It cancels and waits for active work within ctx. Repeated calls finish any
// previously interrupted join and otherwise leave the service idle.
//
// The disabled snapshot is published before cancellation so an interrupted join
// cannot leave a cancelled worker looking active to a later Start. Apply after
// shutdown does nothing until Start explicitly admits lifecycle work again.
// Storage and transport dependencies remain owned by application assembly and
// must stay usable until the completion barrier succeeds.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = false
	// Publish a disabled snapshot before joining so a later restart cannot
	// mistake a cancelled generation for an active worker after an interrupted stop.
	if previous := s.config.Load(); previous != nil {
		paused := *previous
		paused.Enabled = false
		s.config.Store(&paused)
	}
	return s.stopLocked(ctx)
}

// stopLocked cancels and joins the worker while its caller holds mu.
// An expired context preserves the handles for a later join; successful
// completion clears them so a subsequent enable may start another generation.
func (s *Service) stopLocked(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	// Cancellation also interrupts a reporter blocked in HTTP. Joining before
	// clearing these handles keeps disabled saves and later enables ordered.
	s.cancel()
	select {
	case <-s.done:
		s.cancel = nil
		s.done = nil
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run submits observations in the same fixed-rate sequence as MetricsBase.
// The first deadline is independent of the periodic series, whose first
// deadline adds second to that initial deadline. Failed or slow submissions
// never shift later deadlines, and a single worker prevents overlapping sends.
//
// Configuration is sampled at each submission. Cancellation is checked after
// the timer fires as well as by the transport, so a simultaneous disable and
// timer wake cannot admit uncancelled work from the old generation.
func (s *Service) run(ctx context.Context, done chan struct{}, id string, next time.Time, second time.Duration) {
	// Signal completion only after the final submission returns; lifecycle
	// callers rely on this barrier before replacing a worker or closing dependencies.
	defer close(done)
	first := true
	for {
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		config := *s.config.Load()
		if !config.Enabled {
			return
		}
		if err := s.reporter.Submit(ctx, id, config); err != nil && config.LogErrors && s.log != nil {
			// A failed report consumes its scheduled attempt without retries,
			// preserving the official cadence even during prolonged network outages.
			s.log("Could not submit bStats metrics data: %v", err)
		}
		if first {
			next = next.Add(second)
			first = false
		} else {
			next = next.Add(reportPeriod)
		}
	}
}

// submissionDelays matches the millisecond truncation in upstream MetricsBase.
// The first delay is three to six minutes, and the second is zero to thirty
// minutes. Neither upper bound is included in the random range.
func submissionDelays() (time.Duration, time.Duration) {
	// Java casts each floating-point millisecond count to long before scheduling;
	// truncate at the same boundary instead of introducing nanosecond precision.
	initial := time.Duration(60_000*(3+rand.Float64()*3)) * time.Millisecond
	second := time.Duration(60_000*rand.Float64()*30) * time.Millisecond
	return initial, second
}
