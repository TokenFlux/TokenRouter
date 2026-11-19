package site

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// expiry.go runs periodic announcement expiry scans and coordinates worker
// shutdown.
// Active announcements become archived when their end time reaches the scan's
// current time. Expiry scans start once immediately and then use a fixed ticker;
// each scan passes a bounded context to the repository and reports failures in
// the log so a later scheduled scan can try the same eligible records again.
//
// Start and Stop share a lifecycle mutex, making worker registration precede
// any shutdown wait. Stop records a terminal state and closes the exit signal
// once, then waits outside the mutex for scans to finish. The stop signal does
// not cancel an in-progress repository call; that call receives its own timeout
// context and must return before Stop can finish.

// announcementExpiryUpdateTimeout gives each archive scan a five-second context
// budget consumed by the repository, independent of the ticker's configured interval.
const announcementExpiryUpdateTimeout = 5 * time.Second

// AnnouncementExpiryService periodically archives active announcements that have reached their end time.
type AnnouncementExpiryService struct {
	// lifecycleMu protects started and stopped, and orders worker registration
	// against shutdown. Once stopped is set, later Start calls cannot register
	// work even if the service never started a worker.
	lifecycleMu      sync.Mutex             // lifecycle transition lock
	started          bool                   // worker registration completed
	stopped          bool                   // terminal shutdown state
	announcementRepo AnnouncementRepository // archive persistence dependency
	interval         time.Duration          // time between scans
	// stopCh and stopOnce publish one exit signal, while wg keeps Stop waiting
	// until the registered worker returns after completing any selected scan.
	stopCh   chan struct{}  // worker exit signal
	stopOnce sync.Once      // exit signal guard
	wg       sync.WaitGroup // registered worker lifetime
}

// NewAnnouncementExpiryService constructs a dormant worker with the requested scan interval.
// Start launches scanning only when the repository exists and interval is
// positive. Construction allocates an exit signal but performs no repository calls.
func NewAnnouncementExpiryService(announcementRepo AnnouncementRepository, interval time.Duration) *AnnouncementExpiryService {
	return &AnnouncementExpiryService{
		announcementRepo: announcementRepo,
		interval:         interval,
		stopCh:           make(chan struct{}),
	}
}

// Start launches one worker that scans immediately and then at the configured interval.
// Nil services, missing repositories, and nonpositive intervals produce no work.
// Repeated starts and starts after Stop are ignored.
//
// Lifecycle locking covers the started check and WaitGroup registration. Stop
// obtains that same lock before waiting, so it observes either a registered
// worker or the terminal stopped state that prevents registration. Each scan
// runs to completion before the worker can observe its next tick or stop signal.
func (s *AnnouncementExpiryService) Start() {
	if s == nil || s.announcementRepo == nil || s.interval <= 0 {
		return
	}
	// Hold lifecycle ownership through worker registration so shutdown cannot
	// begin waiting for zero workers while this Start is still adding its worker.
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true

	s.wg.Go(func() {
		// The ticker belongs to this worker. Release it when the worker observes
		// shutdown so later ticks do not retain the worker's periodic resource.
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		s.runOnce()
		// Scans are serial in this worker. A stop signal is observed between
		// repository calls, while a call already in progress uses its own bounded context.
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	})
}

// Stop records terminal shutdown, signals the worker, and waits for registered work.
// Nil receivers are ignored, and repeated calls can wait for the same worker.
// Calling Stop before Start prevents any later scan from being registered.
//
// Closing stopCh is guarded once while lifecycleMu excludes a racing Start.
// The mutex is released before waiting because the worker has its own lifetime
// and repository timeout. Stop does not cancel the current scan; it finishes
// when the repository call and worker have returned.
func (s *AnnouncementExpiryService) Stop() {
	if s == nil {
		return
	}
	// Publish terminal state and the exit signal under the registration lock;
	// a racing Start either registered already or observes stopped before adding work.
	s.lifecycleMu.Lock()
	s.stopped = true

	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.lifecycleMu.Unlock()
	// A scan can still own repository work after the signal closes. Waiting
	// here keeps the dependency alive until the worker has completed that call.
	s.wg.Wait()
}

// runOnce archives announcements eligible at the current time and logs the outcome.
// Each call gives the repository its own timeout context. Failures end this
// scan, leaving later ticker-driven scans to examine eligible records again.
func (s *AnnouncementExpiryService) runOnce() {
	// Stop does not cancel repository calls. Give this scan its own deadline
	// and release the context timer when the call finishes or reports an error.
	ctx, cancel := context.WithTimeout(context.Background(), announcementExpiryUpdateTimeout)
	defer cancel()

	updated, err := s.announcementRepo.ArchiveExpired(ctx, time.Now())
	if err != nil {
		// A scan failure ends this attempt while the worker remains available;
		// the next ticker selection rechecks whichever active announcements are eligible then.
		slog.Error("[AnnouncementExpiry] archive expired announcements failed", "error", err)
		return
	}
	if updated > 0 {
		slog.Info("[AnnouncementExpiry] archived expired announcements", "count", updated)
	}
}
