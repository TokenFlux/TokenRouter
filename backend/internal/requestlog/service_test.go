package requestlog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
)

// service_test.go exercises snapshot persistence and pending request lookups.
// Writer fixtures retain successful snapshot batches and can fail or
// pause selected persistence attempts. Direct flush cases check which timestamp
// versions remain buffered, while later reads expose pending summaries during
// injected database unavailability.
//
// Save callbacks run before retained rows are appended and outside the fixture
// mutex. They can publish a newer snapshot while an older Flush is in progress,
// revealing timestamp acknowledgment and invalid-version behavior.
// Virtual-clock scenarios advance worker and shutdown deadlines while gates
// hold synchronous overflow work. The fixture's returned slice copies are
// shallow, so assertions leave shared record collections and finish pointers
// unchanged.

// testRepository retains successful snapshots and injects persistence or lookup
// outcomes for writer tests.
type testRepository struct {
	// mu protects saved append and outer-slice copying. Stored record values
	// retain their collection and FinishedAt references, so tests treat that
	// nested data as immutable.
	mu    sync.Mutex                // retained fixture row lock
	saved []telemetry.RequestRecord // shallow retained successful snapshots
	// save optionally runs before rows are retained and outside mu. Configure it
	// between completed calls or before workers start; it can publish another
	// Service snapshot.
	save func(context.Context, []telemetry.RequestRecord) error
	// findErr simulates a database outage for detail and provider name reads.
	// Configure it before Find calls; the fixture ignores the query arguments.
	findErr error
}

// Save runs the configured outcome callback before retaining successful record
// values. Callback failure returns immediately with no append; the fixture
// stores shallow record copies and does not itself honor ctx cancellation.
//
// The callback executes outside mu so it can publish a newer Service snapshot
// while Flush holds an older batch. Only the later successful append takes the
// fixture lock. Configure callbacks before concurrent writes or between
// finished calls, and leave retained nested collections and finish pointers
// unchanged when assertions inspect them.
func (r *testRepository) Save(ctx context.Context, records []telemetry.RequestRecord) error {
	if r.save != nil {
		if err := r.save(ctx, records); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved = append(r.saved, records...)
	return nil
}

// Find returns no stored details and the configured findErr for every request.
// It ignores context, identifier, ownership, and admin inputs so
// pending-summary tests isolate the service's local access decision.
func (r *testRepository) Find(context.Context, string, int64, bool) ([]Detail, error) {
	return nil, r.findErr
}

// FindProviderNames returns no names and the configured database read error.
func (r *testRepository) FindProviderNames(context.Context, []int64) (map[int64]string, error) {
	return nil, r.findErr
}

// Cleanup reports success and retains every snapshot in this writer fixture.
func (r *testRepository) Cleanup(context.Context, time.Time) error { return nil }

// records copies the retained outer slice under mu for test assertions. Nested
// aliases, attempts, timings, and FinishedAt pointers remain shared with
// retained values, so consumers treat them as immutable.
func (r *testRepository) records() []telemetry.RequestRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]telemetry.RequestRecord(nil), r.saved...)
}

// TestFlushRetriesDatabaseFailure injects a transient error into a single
// pending snapshot write. The snapshot remains buffered until a later
// successful flush removes it and retains one saved row.
func TestFlushRetriesDatabaseFailure(t *testing.T) {
	unavailable := errors.New("database unavailable")
	repo := &testRepository{save: func(context.Context, []telemetry.RequestRecord) error { return unavailable }}
	service := NewService(repo, 30, nil)
	service.Observe(telemetry.RequestRecord{RequestID: "id", State: "failed"})
	require.ErrorIs(t, service.Flush(t.Context()), unavailable)
	health, err := service.Health()
	require.NoError(t, err)
	require.Equal(t, 1, health.Pending)
	repo.save = nil
	require.NoError(t, service.Flush(t.Context()))
	health, err = service.Health()
	require.NoError(t, err)
	require.Zero(t, health.Pending)
	require.Len(t, repo.records(), 1)
}

// TestFlushFallsBackPerRecord fails a multi-record batch and one individual
// snapshot. The other row saves, the failed row stays pending with reports, and
// recovery later saves that remaining snapshot.
func TestFlushFallsBackPerRecord(t *testing.T) {
	failure := errors.New("write failed")
	repo := &testRepository{save: func(_ context.Context, records []telemetry.RequestRecord) error {
		if len(records) > 1 || records[0].RequestID == "retry" {
			return failure
		}
		return nil
	}}
	service := NewService(repo, 30, nil)
	service.Observe(telemetry.RequestRecord{RequestID: "saved"})
	service.Observe(telemetry.RequestRecord{RequestID: "retry"})
	require.ErrorIs(t, service.Flush(t.Context()), failure)
	require.Equal(t, "saved", repo.records()[0].RequestID)
	require.Len(t, repo.records(), 1)
	health, err := service.Health()
	require.NoError(t, err)
	require.Equal(t, 1, health.Pending)
	require.Positive(t, health.Failures)
	repo.save = nil
	require.NoError(t, service.Flush(t.Context()))
	require.Len(t, repo.records(), 2)
}

// TestFlushDiscardsInvalidRecords checks invalid snapshots alone and alongside
// a valid row. Invalid versions leave the buffer with a report, valid rows
// save, and another flush performs no repeated invalid attempt.
func TestFlushDiscardsInvalidRecords(t *testing.T) {
	for _, withValid := range []bool{false, true} {
		t.Run(fmt.Sprint(withValid), func(t *testing.T) {
			attempts := 0
			var messages []string
			repo := &testRepository{save: func(_ context.Context, records []telemetry.RequestRecord) error {
				attempts++
				for _, record := range records {
					if record.RequestID == "invalid" {
						return fmt.Errorf("malformed data: %w", ErrInvalidRecord)
					}
				}
				return nil
			}}
			service := NewService(repo, 30, func(format string, args ...any) {
				messages = append(messages, fmt.Sprintf(format, args...))
			})
			service.Observe(telemetry.RequestRecord{RequestID: "invalid"})
			if withValid {
				service.Observe(telemetry.RequestRecord{RequestID: "valid"})
			}
			require.NoError(t, service.Flush(t.Context()))
			health, err := service.Health()
			require.NoError(t, err)
			require.Zero(t, health.Pending)
			require.Positive(t, health.Failures)
			require.Contains(t, messages[len(messages)-1], "discard invalid request invalid")
			if withValid {
				require.Len(t, repo.records(), 1)
				require.Equal(t, "valid", repo.records()[0].RequestID)
			}
			before := attempts
			require.NoError(t, service.Flush(t.Context()))
			require.Equal(t, before, attempts)
			require.NoError(t, service.Stop(t.Context()))
		})
	}
}

// TestDiscardInvalidPreservesNewVersion publishes a newer completed snapshot
// inside a callback that rejects the older version. Discard leaves one pending
// ID, which the next successful flush saves as completed.
func TestDiscardInvalidPreservesNewVersion(t *testing.T) {
	repo := &testRepository{}
	service := NewService(repo, 30, nil)
	now := time.Now()
	service.Observe(telemetry.RequestRecord{RequestID: "id", UpdatedAt: now})
	// Publish a newer same-ID snapshot from the persistence callback so
	// acknowledgment of the captured older batch must leave the new timestamp
	// pending.
	repo.save = func(context.Context, []telemetry.RequestRecord) error {
		service.Observe(telemetry.RequestRecord{RequestID: "id", UpdatedAt: now.Add(time.Second), State: "completed"})
		return ErrInvalidRecord
	}
	require.NoError(t, service.Flush(t.Context()))
	health, err := service.Health()
	require.NoError(t, err)
	require.Equal(t, 1, health.Pending)
	repo.save = nil
	require.NoError(t, service.Flush(t.Context()))
	require.Equal(t, "completed", repo.records()[0].State)
}

// TestObserveCoalescesSnapshots observes newer and stale updates after mutating
// the original alias slice. One saved row keeps its start, failed terminal
// state, and both correctly retained alias identities.
func TestObserveCoalescesSnapshots(t *testing.T) {
	repo := &testRepository{}
	service := NewService(repo, 30, nil)
	now := time.Now()
	first := telemetry.RequestRecord{RequestID: "id", StartedAt: now, UpdatedAt: now, State: "failed", Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "client"}}}
	service.Observe(first)
	// Mutate the source alias after Observe returns to expose collection sharing;
	// the eventual saved alias should remain the captured caller identifier.
	first.Aliases[0].Value = "changed"
	service.Observe(telemetry.RequestRecord{RequestID: "id", UpdatedAt: now.Add(time.Second), State: "completed", Aliases: []telemetry.RequestAlias{{Kind: "upstream", Value: "supplier"}}})
	service.Observe(telemetry.RequestRecord{RequestID: "id", UpdatedAt: now, State: "running"})
	require.NoError(t, service.Flush(t.Context()))
	records := repo.records()
	require.Len(t, records, 1)
	require.Equal(t, "failed", records[0].State)
	require.Equal(t, now, records[0].StartedAt)
	require.ElementsMatch(t, []telemetry.RequestAlias{{Kind: "caller", Value: "client"}, {Kind: "upstream", Value: "supplier"}}, records[0].Aliases)
}

// TestFlushPreservesConcurrentUpdate publishes a newer completed snapshot while
// the earlier version saves. The first acknowledgment leaves it pending, and a
// second flush retains the completed version afterward.
func TestFlushPreservesConcurrentUpdate(t *testing.T) {
	repo := &testRepository{}
	service := NewService(repo, 30, nil)
	now := time.Now()
	service.Observe(telemetry.RequestRecord{RequestID: "id", UpdatedAt: now, State: "running"})
	// Publish a newer same-ID snapshot from the persistence callback so
	// acknowledgment of the captured older batch must leave the new timestamp
	// pending.
	repo.save = func(context.Context, []telemetry.RequestRecord) error {
		service.Observe(telemetry.RequestRecord{RequestID: "id", UpdatedAt: now.Add(time.Second), State: "completed"})
		return nil
	}
	require.NoError(t, service.Flush(t.Context()))
	health, err := service.Health()
	require.NoError(t, err)
	require.Equal(t, 1, health.Pending)
	repo.save = nil
	require.NoError(t, service.Flush(t.Context()))
	require.Equal(t, "completed", repo.records()[1].State)
}

// TestObserveOverflowWritesSynchronously fills the pending map before observing
// new request IDs. Overflow saves immediately, a later injected failure
// increases reporting count, and buffered capacity remains unchanged.
func TestObserveOverflowWritesSynchronously(t *testing.T) {
	repo := &testRepository{}
	service := NewService(repo, 30, nil)
	for i := range requestBufferCapacity {
		service.Observe(telemetry.RequestRecord{RequestID: fmt.Sprint(i)})
	}
	service.Observe(telemetry.RequestRecord{RequestID: "overflow"})
	require.Len(t, repo.records(), 1)
	require.Equal(t, "overflow", repo.records()[0].RequestID)
	repo.save = func(context.Context, []telemetry.RequestRecord) error { return errors.New("database unavailable") }
	service.Observe(telemetry.RequestRecord{RequestID: "failed-overflow"})
	health, err := service.Health()
	require.NoError(t, err)
	require.Equal(t, requestBufferCapacity, health.Pending)
	require.Equal(t, uint64(1), health.Failures)
}

// TestStartFlushesInBackground advances a virtual clock through normal writer
// batching. One row saves, and repeated Start and Stop calls, including Start
// after stopping, return nil errors.
func TestStartFlushesInBackground(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &testRepository{}
		service := NewService(repo, 30, nil)
		require.NoError(t, service.Start(t.Context()))
		require.NoError(t, service.Start(t.Context()))
		service.Observe(telemetry.RequestRecord{RequestID: "id"})
		synctest.Wait()
		time.Sleep(requestFlushInterval)
		synctest.Wait()
		require.Len(t, repo.records(), 1)
		require.NoError(t, service.Stop(t.Context()))
		require.NoError(t, service.Stop(t.Context()))
		require.NoError(t, service.Start(t.Context()))
	})
}

// TestStartSlowsFailedWrites fails the first virtual-clock flush before storage
// recovery. Half the retry interval adds no attempt; the complete interval
// triggers a successful second write and one saved row.
func TestStartSlowsFailedWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int64
		repo := &testRepository{save: func(context.Context, []telemetry.RequestRecord) error {
			if attempts.Add(1) == 1 {
				return errors.New("database unavailable")
			}
			return nil
		}}
		service := NewService(repo, 30, nil)
		service.Observe(telemetry.RequestRecord{RequestID: "id"})
		require.NoError(t, service.Start(t.Context()))
		synctest.Wait()
		time.Sleep(requestFlushInterval)
		synctest.Wait()
		require.Equal(t, int64(1), attempts.Load())
		// Advance half the retry interval to separate the pre-retry observation from
		// the full-delay attempt count checked after the next advance.
		time.Sleep(requestRetryInterval / 2)
		synctest.Wait()
		require.Equal(t, int64(1), attempts.Load())
		// Finish the remaining half interval so the recovered write runs at the
		// complete retry delay before its attempt count is inspected.
		time.Sleep(requestRetryInterval / 2)
		synctest.Wait()
		require.Equal(t, int64(2), attempts.Load())
		require.Len(t, repo.records(), 1)
		require.NoError(t, service.Stop(t.Context()))
	})
}

// TestStopDrainsAllBatches buffers more than one batch before any worker
// starts. Stop saves all buffered rows, and a later observation writes
// synchronously while pending count stays zero.
func TestStopDrainsAllBatches(t *testing.T) {
	repo := &testRepository{}
	service := NewService(repo, 30, nil)
	for i := range requestBatchSize + 1 {
		service.Observe(telemetry.RequestRecord{RequestID: fmt.Sprint(i)})
	}
	require.NoError(t, service.Stop(t.Context()))
	require.Len(t, repo.records(), requestBatchSize+1)
	service.Observe(telemetry.RequestRecord{RequestID: "late"})
	require.Equal(t, "late", repo.records()[requestBatchSize+1].RequestID)
	health, err := service.Health()
	require.NoError(t, err)
	require.Zero(t, health.Pending)
}

// TestStopWaitsForSynchronousWrite holds a synchronous overflow save while
// shutdown starts under a virtual clock. Stop stays pending until the gate
// opens, then saved count includes overflow and the drained buffer.
func TestStopWaitsForSynchronousWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		repo := &testRepository{save: func(_ context.Context, records []telemetry.RequestRecord) error {
			if len(records) == 1 && records[0].RequestID == "overflow" {
				// Hold only the overflow write after entry notification so Stop can close
				// admission while this already registered synchronous persistence remains
				// unfinished.
				close(entered)
				<-release
			}
			return nil
		}}
		service := NewService(repo, 30, nil)
		for i := range requestBufferCapacity {
			service.Observe(telemetry.RequestRecord{RequestID: fmt.Sprint(i)})
		}
		// Start the overflow observation independently and wait for its repository
		// gate before launching Stop, fixing the registered-write ordering for the
		// assertion.
		go service.Observe(telemetry.RequestRecord{RequestID: "overflow"})
		<-entered
		stopped := make(chan error, 1)
		go func() { stopped <- service.Stop(t.Context()) }()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("stop returned before the synchronous write completed")
		default:
		}
		// Release the admitted overflow write before joining Stop so drain can save
		// the fixed pending set and publish its final outcome.
		close(release)
		require.NoError(t, <-stopped)
		require.Len(t, repo.records(), requestBufferCapacity+1)
	})
}

// TestStopTimeoutReportsPending makes the fixture wait for its write context to
// expire. Shutdown returns deadline error, later Stop retains that result, and
// health reports the unsaved snapshot and failure.
func TestStopTimeoutReportsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &testRepository{save: func(ctx context.Context, _ []telemetry.RequestRecord) error {
			// Wait on the supplied write context to model context-aware unavailable
			// storage, making the first stop budget determine this fixture's drain
			// error.
			<-ctx.Done()
			return ctx.Err()
		}}
		service := NewService(repo, 30, nil)
		service.Observe(telemetry.RequestRecord{RequestID: "id"})
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		require.ErrorIs(t, service.Stop(ctx), context.DeadlineExceeded)
		synctest.Wait()
		require.ErrorIs(t, service.Stop(t.Context()), context.DeadlineExceeded)
		health, err := service.Health()
		require.NoError(t, err)
		require.Equal(t, 1, health.Pending)
		require.Positive(t, health.Failures)
	})
}

// TestFindPendingChecksOwnership injects lookup failure while one user's
// summary remains pending. Another user receives that error; the owner and
// administrator see an isolated attempt slice and nil error. A failed provider
// name query leaves the provider ID visible on the administrator result.
func TestFindPendingChecksOwnership(t *testing.T) {
	repo := &testRepository{findErr: errors.New("database unavailable")}
	service := NewService(repo, 30, nil)
	service.Observe(telemetry.RequestRecord{
		RequestID: "id",
		UserID:    1,
		Attempts:  []telemetry.RequestAttempt{{ProviderID: 7, RequestID: "supplier"}},
	})
	items, err := service.Find(t.Context(), "id", 2, false)
	require.Error(t, err)
	require.Empty(t, items)
	items, err = service.Find(t.Context(), "id", 1, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].Pending)
	// Change the returned attempt slice before querying again so shared pending
	// storage would be visible if response collection copying were missing.
	items[0].Attempts[0].RequestID = ""
	items, err = service.Find(t.Context(), "id", 0, true)
	require.NoError(t, err)
	require.Equal(t, "supplier", items[0].Attempts[0].RequestID)
	require.Equal(t, int64(7), items[0].Attempts[0].ProviderID)
	require.Empty(t, items[0].ProviderNames)
}
