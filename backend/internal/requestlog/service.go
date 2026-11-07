package requestlog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// service.go buffers request snapshots, flushes versioned batches, and drains
// admitted writes during shutdown.
// Request summaries continue independently of Ops sampling and may receive
// later snapshots under the same local request ID. Pending snapshots merge by
// increasing UpdatedAt, retaining earlier identity and terminal failure
// information through telemetry's merge rules. Normalization bounds diagnostic
// text and copies collections, while FinishedAt pointers remain shared
// immutable values.
//
// A bounded map admits existing-request updates even when full; new requests
// then save synchronously. Flush copies a batch under mu, writes outside that
// lock, and acknowledges only the timestamp version it wrote, leaving later
// snapshots pending. Batch failure can isolate invalid content through
// individual retries. Stop closes map admission before waiting for registered
// overflow writes and draining; later observations save on their own, and the
// first stop context governs the shared drain result.

// These limits bound pending request count, batch size, worker cadence, retry
// spacing, and the write context supplied to persistence.
const (
	requestBufferCapacity = 4096                  // maximum distinct buffered request IDs
	requestBatchSize      = 256                   // maximum snapshots per flush
	requestFlushInterval  = 20 * time.Millisecond // normal background flush interval
	requestRetryInterval  = time.Second           // background interval after flush error
	requestWriteTimeout   = 5 * time.Second       // persistence context timeout limit
)

// ErrInvalidRecord marks content that cannot be stored by retrying its same
// timestamp version; Flush removes that version after individual confirmation
// and reports the discard.
var ErrInvalidRecord = errors.New("invalid request record")

// Repository persists request summaries and supplies ownership-filtered
// diagnostic detail queries.
type Repository interface {
	// Save persists the supplied snapshot batch using ctx and its version
	// timestamps. Mark permanent content errors with ErrInvalidRecord so the
	// service can isolate invalid versions; other failures leave snapshots
	// retryable.
	Save(context.Context, []telemetry.RequestRecord) error
	// Find resolves a local or external identifier and filters every retained
	// source by userID and trusted admin. Returned details can include historical
	// records whose request summary has expired, with inferred Legacy metadata.
	Find(context.Context, string, int64, bool) ([]Detail, error)
	// Cleanup removes request summaries whose start time precedes cutoff using
	// ctx. Usage, billing, and other diagnostic data follow their own retention,
	// so later Find can still synthesize retained historical details.
	Cleanup(context.Context, time.Time) error
}

// Usage exposes retained usage-record cost and token counts in request
// diagnostics.
type Usage struct {
	RequestSource string             `json:"request_source"`          // recorded customer or system source
	SystemUsage   *usage.SystemUsage `json:"system_usage,omitempty"`  // optional internal execution evidence
	ProviderCost  *float64           `json:"provider_cost,omitempty"` // known provider cost including multiplier
	DurationMs    *int64             `json:"duration_ms"`             // measured upstream execution duration

	Timings *timing.Performance `json:"timings,omitempty"` // usage-owned performance snapshot
	TTFTMs  *int                `json:"ttft_ms"`           // selected upstream latency milliseconds
	TPS     *float64            `json:"tps"`               // estimated average output speed

	ID           int64   `json:"id"`            // retained usage row ID
	Model        string  `json:"model"`         // requested model or stored model
	InputTokens  int64   `json:"input_tokens"`  // recorded input token count
	OutputTokens int64   `json:"output_tokens"` // recorded output token count
	ActualCost   float64 `json:"actual_cost"`   // recorded charged amount in USD
}

// Failure references a retained error row selected after its ownership check.
type Failure struct {
	ID     int64  `json:"id"`          // retained error row ID
	Status int    `json:"status_code"` // recorded HTTP error status
	Phase  string `json:"phase"`       // recorded request error phase
}

// Detail joins a stored or inferred request summary with retained diagnostic
// references.
type Detail struct {
	// RequestRecord holds the response summary. Pending lookup clones its
	// collections but keeps FinishedAt pointers shared, so consumers treat
	// pointed-to times as immutable.
	telemetry.RequestRecord
	Usage  []Usage   `json:"usage"`  // retained usage row summaries
	Errors []Failure `json:"errors"` // retained error row references
	// AuditIDs contains retained audit row IDs. The PostgreSQL Find path attaches
	// these references only when its trusted admin argument is true.
	AuditIDs []int64 `json:"audit_ids,omitempty"`
	// Legacy marks summaries inferred from usage, error, or audit rows when no
	// indexed persisted request detail exists for their actual request ID.
	Legacy bool `json:"legacy"`
	// Pending marks an accessible exact-ID buffer snapshot considered by
	// Service.Find. A newer stored timestamp can still win the merge while this
	// flag remains true.
	Pending bool `json:"pending,omitempty"`
}

// Health reports local buffered request count and cumulative
// persistence-reporting failures.
type Health struct {
	// Pending counts distinct request IDs still buffered, including retained
	// transient failures. It is sampled under mu before the separate atomic
	// failure count.
	Pending int `json:"pending"`
	// Failures counts report calls across missing IDs, writes, cleanup, and
	// invalid discards. Multiple stages can report one snapshot failure, so this
	// is not a distinct-request count.
	Failures uint64 `json:"failures"`
}

// Service coalesces request snapshots and coordinates background writes with
// shutdown draining.
type Service struct {
	// repo is the shared persistence dependency. Keep it available until
	// background work, drain, and any later synchronous Observe calls finish.
	repo Repository
	// retention is elapsed age based on twenty-four-hour days. Hourly cleanup
	// subtracts it from the current clock and compares stored request start times.
	retention time.Duration
	// logf optionally receives reports synchronously from observations, flushing,
	// drain, or the worker. It can run under flushMu; return without synchronously
	// flushing or stopping this service.
	logf func(string, ...any)
	// failures counts reporting events atomically across concurrent writers and
	// cleanup. Health reads it separately from the pending count, so the pair is
	// not one synchronized snapshot.
	failures atomic.Uint64

	// mu protects pending and stopped and serializes writes.Add against closing
	// admission. Stop marks stopped before waiting, so subsequently received
	// observations never add to its overflow-write group.
	mu      sync.Mutex                         // admission and pending version lock
	pending map[string]telemetry.RequestRecord // latest snapshots keyed by request ID
	stopped bool                               // permanently closed memory admission
	writes  sync.WaitGroup                     // pre-stop synchronous overflow writes
	// flushMu serializes the complete Flush operation, including persistence and
	// reporting. Lock acquisition itself is not context-aware, and callbacks must
	// avoid synchronously calling Flush again.
	flushMu sync.Mutex

	// ctx and cancel govern the one worker selected by startOnce. stopOnce closes
	// admission and starts one drain; done publishes worker exit, while stopDone
	// publishes its final stopErr to every stop waiter.
	ctx       context.Context    // background worker lifetime context
	cancel    context.CancelFunc // worker lifetime cancellation function
	startOnce sync.Once          // single worker-or-closed notification choice
	stopOnce  sync.Once          // single shutdown drain launch
	done      chan struct{}      // background worker exit notification
	stopDone  chan struct{}      // shared drain result notification
	// stopErr is written by the shared drain using the first Stop caller's
	// context. Read it after stopDone closes; later callers do not replace that
	// result.
	stopErr error
}

// NewService constructs an inactive request writer with a bounded pending map
// and elapsed retention policy. Nonpositive retention selects thirty days;
// supply a repository before query or cleanup use, and Start begins background
// work.
// @project-doc docs/operations/request_lookup.md#request_storage
func NewService(repo Repository, retentionDays int, logf func(string, ...any)) *Service {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Convert retention days to fixed twenty-four-hour elapsed durations,
	// independent of local calendar-day lengths.
	return &Service{
		repo: repo, retention: time.Duration(retentionDays) * 24 * time.Hour, logf: logf,
		pending: make(map[string]telemetry.RequestRecord), ctx: ctx, cancel: cancel,
		done: make(chan struct{}), stopDone: make(chan struct{}),
	}
}

// Observe normalizes and admits one snapshot, merges an existing ID by
// timestamp, or writes synchronously when memory admission is full or stopped.
// Nil service or repository is inert; empty IDs are reported and rejected.
// Callers supply increasing UpdatedAt revisions and keep FinishedAt's
// pointed-to time immutable.
//
// Existing IDs can update while the map is full. New overflow writes register
// under mu before stop closes admission, so shutdown waits for those
// operations. Calls after admission closes use independent five-second write
// contexts without joining that stop wait. Synchronous errors are reported;
// this method returns no write result and retains no failed overflow snapshot.
func (s *Service) Observe(record telemetry.RequestRecord) {
	if s == nil || s.repo == nil {
		return
	}
	if record.RequestID == "" {
		s.report("write request record", fmt.Errorf("missing request ID"))
		return
	}
	// Normalize and copy mutable collections before retaining the snapshot so
	// later caller changes cannot alter queued diagnostic text, aliases, attempts,
	// or timings.
	record = telemetry.NormalizeRequestRecord(record)
	s.mu.Lock()
	if !s.stopped {
		// Merge same-ID updates before capacity checks so an existing request's
		// terminal metadata can advance even when every map slot is occupied.
		if previous, ok := s.pending[record.RequestID]; ok {
			s.pending[record.RequestID] = telemetry.MergeRequestRecord(previous, record)
			s.mu.Unlock()
			return
		}
		// Admit new IDs while buffer capacity remains; reaching the cap routes their
		// persistence to the caller after mu is released.
		if len(s.pending) < requestBufferCapacity {
			s.pending[record.RequestID] = record
			s.mu.Unlock()
			return
		}
		// Register overflow work before unlocking admission; Stop sets stopped under
		// this mutex before waiting, preventing late observations from joining its
		// completed write group.
		s.writes.Add(1)
		defer s.writes.Done()
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), requestWriteTimeout)
	defer cancel()
	if err := s.repo.Save(ctx, []telemetry.RequestRecord{record}); err != nil {
		s.report("write request "+record.RequestID, err)
	}
}

// Start launches the background writer once and returns nil, ignoring its
// context. Repeated calls or calls after Stop's startOnce claim create no new
// worker; observations can be buffered before Start.
func (s *Service) Start(context.Context) error {
	// Share this once guard with Stop-before-Start so exactly one worker can
	// launch, or shutdown can publish its exit without a worker.
	s.startOnce.Do(func() { go s.run() })
	return nil
}

// run owns flush and hourly-retention tickers until the service context is
// canceled. It closes done on return; each operation receives a derived
// context, and repository or logging callbacks need to return before worker
// exit completes.
//
// A failed Flush switches the next tick interval to one second; a successful
// Flush restores twenty milliseconds, including success after an invalid
// version is discarded. Errors observed during shutdown cancellation are
// suppressed by this worker, while Stop's drain reports its own failures.
// Retention runs in the same goroutine, so its work can delay later flush
// ticks.
func (s *Service) run() {
	defer close(s.done)
	ticker := time.NewTicker(requestFlushInterval)
	defer ticker.Stop()
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, requestWriteTimeout)
			if err := s.Flush(ctx); err != nil {
				// Suppress worker reports after service cancellation while retaining failed
				// snapshots for Stop's separate drain attempt and its own error reporting.
				if s.ctx.Err() == nil {
					s.report("flush request records", err)
				}
				// Retain failed snapshots while spacing later writes by one second; any
				// successful flush returns the worker to its normal batching cadence.
				ticker.Reset(requestRetryInterval)
			} else {
				ticker.Reset(requestFlushInterval)
			}
			cancel()
		case <-cleanup.C:
			// Give hourly retention its own thirty-second context while inheriting
			// worker cancellation; its synchronous work still finishes before the worker
			// can exit.
			ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
			if err := s.repo.Cleanup(ctx, time.Now().Add(-s.retention)); err != nil && s.ctx.Err() == nil {
				s.report("clean request records", err)
			}
			cancel()
		}
	}
}

// Stop closes buffered admission, cancels the worker, and launches one shared
// drain using the first caller's nonnil context. Each caller separately waits
// using its context, and timeout can return while worker exit, pre-stop
// synchronous writes, or drain remain unfinished.
//
// The stopped flag is set under the same mu that registers overflow writes,
// then the drain goroutine waits for done and those registered writes before
// flushing pending snapshots. Stopping before Start claims startOnce and closes
// done directly. Later observations save outside this wait group, and later
// Stop calls retain the first drain context and published result.
func (s *Service) Stop(ctx context.Context) error {
	// Close admission before launching the shared drain so the overflow-write
	// group has a fixed set and later observations save on their own.
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.mu.Unlock()
		s.cancel()
		// When no writer started, claim its once guard and close done directly so the
		// drain can proceed instead of waiting for an absent goroutine.
		s.startOnce.Do(func() { close(s.done) })
		// Drain independently of each stop waiter's deadline; the first context still
		// governs storage while worker exit and registered overflow writes finish
		// first.
		go func() {
			defer close(s.stopDone)
			<-s.done
			s.writes.Wait()
			s.stopErr = s.drain(ctx)
		}()
	})
	select {
	case <-s.stopDone:
		return s.stopErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// drain repeatedly flushes while Health reports buffered snapshots, using the
// first Stop context. A flush error is reported and returned immediately;
// confirmed invalid versions can be removed without a failing drain result.
func (s *Service) drain(ctx context.Context) error {
	for {
		// Use the local buffered count to decide whether another drain batch is
		// needed; Health's current implementation supplies no error to propagate.
		health, _ := s.Health()
		if health.Pending == 0 {
			return nil
		}
		if err := s.Flush(ctx); err != nil {
			s.report("drain request records", err)
			return err
		}
	}
}

// Flush writes up to two hundred fifty-six pending snapshots under one
// serialized flush operation. It checks ctx after acquiring flushMu and
// supplies at most five seconds for persistence; repository and log callbacks
// must return, and the mutex wait itself does not respond to cancellation.
//
// The pending map is copied under mu and remains writable while storage runs.
// Successful writes acknowledge only matching UpdatedAt values, preserving
// newer arrivals. Batch errors retry individually within the same write
// context; permanent content errors are discarded only after single-record
// confirmation, while transient failures stay buffered. Report counts can
// include both batch failure and later per-record discard.
func (s *Service) Flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	batch := make([]telemetry.RequestRecord, 0, min(len(s.pending), requestBatchSize))
	for _, record := range s.pending {
		batch = append(batch, record)
		if len(batch) == requestBatchSize {
			break
		}
	}
	s.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	writeCtx, cancel := context.WithTimeout(ctx, requestWriteTimeout)
	defer cancel()
	if err := s.repo.Save(writeCtx, batch); err != nil {
		if len(batch) == 1 {
			if s.discardInvalid(batch[0], err) {
				return nil
			}
			return err
		}
		if writeCtx.Err() != nil {
			return err
		}
		// Isolate each snapshot after batch failure within the same write deadline,
		// acknowledging successful versions while leaving transient failures
		// available for retry.
		s.report("batch write request records", err)
		var failures []error
		for _, record := range batch {
			if err := writeCtx.Err(); err != nil {
				return errors.Join(append(failures, err)...)
			}
			if err := s.repo.Save(writeCtx, []telemetry.RequestRecord{record}); err != nil {
				if !s.discardInvalid(record, err) {
					failures = append(failures, fmt.Errorf("request %s: %w", record.RequestID, err))
				}
			} else {
				s.ack(record)
			}
		}
		return errors.Join(failures...)
	}
	for _, record := range batch {
		s.ack(record)
	}
	return nil
}

// discardInvalid acknowledges a timestamp version when err contains
// ErrInvalidRecord and reports that discard. Other errors return false and
// leave it pending; a newer same-ID buffer version remains available for
// another flush.
func (s *Service) discardInvalid(record telemetry.RequestRecord, err error) bool {
	if !errors.Is(err, ErrInvalidRecord) {
		return false
	}
	// Acknowledge the confirmed invalid version before reporting it so subsequent
	// flushes retain newer revisions and avoid repeating this failed content.
	s.ack(record)
	s.report("discard invalid request "+record.RequestID, err)
	return true
}

// ack removes a buffered snapshot whose ID and UpdatedAt match the completed
// write. The method holds mu during comparison and deletion; acknowledgments of
// older versions leave later arrivals pending.
//
// Persistence and buffering advance independently, so a write's success or
// invalid-content confirmation cannot delete a newer timestamp accepted while that
// write was running. Equality identifies the acknowledged version. Callers
// therefore publish strictly increasing timestamps for different snapshots; two
// distinct values with the same timestamp would be treated as the same version.
func (s *Service) ack(record telemetry.RequestRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Compare the written timestamp with the current buffered revision before
	// deletion so a newer arrival during persistence remains eligible for a later
	// batch.
	if current, ok := s.pending[record.RequestID]; ok && current.UpdatedAt.Equal(record.UpdatedAt) {
		delete(s.pending, record.RequestID)
	}
}

// Find queries persistence and then considers an accessible exact-ID local
// pending snapshot. Administrators can see it, while ordinary callers need its
// positive matching UserID; pending lookup does not expand aliases or grant
// team-owner access.
//
// The local record's collections are cloned before returning, with FinishedAt
// pointers kept shared and immutable. A matching stored summary is merged by
// timestamp; otherwise a Pending detail is appended. Finding an accessible
// local record returns nil error even after repository failure, supporting
// local diagnostics during storage outages. Other results retain the repository
// error and its returned items.
func (s *Service) Find(ctx context.Context, id string, userID int64, admin bool) (items []Detail, err error) {
	defer func() {
		for i := range items {
			writeFailure := items[i].UsageWriteState == "unknown" || items[i].UsageWriteState == "not_persisted" || items[i].UsageWriteState == "skipped"
			if len(items[i].Usage) > 0 || (!writeFailure && items[i].State != "failed" && items[i].State != "canceled") {
				items[i].FailureTimings = nil
			}
		}
	}()
	items, err = s.repo.Find(ctx, id, userID, admin)
	s.mu.Lock()
	record, found := s.pending[id]
	s.mu.Unlock()
	// Use a permitted exact local snapshot even when storage failed; its Pending
	// flag reports local availability while the repository's error is suppressed.
	if found && (admin || record.UserID > 0 && record.UserID == userID) {
		record = telemetry.CloneRequestRecord(record)
		for i := range items {
			if items[i].RequestID == id {
				items[i].RequestRecord = telemetry.MergeRequestRecord(items[i].RequestRecord, record)
				items[i].Pending = true
				return items, nil
			}
		}
		return append(items, Detail{RequestRecord: record, Pending: true}), nil
	}
	return items, err
}

// Health returns local pending-ID count and cumulative reporting failures with
// nil error. The map count and atomic failure counter are sampled separately,
// so callers should interpret them as nearby observations rather than one
// simultaneous state.
func (s *Service) Health() (Health, error) {
	s.mu.Lock()
	pending := len(s.pending)
	s.mu.Unlock()
	return Health{Pending: pending, Failures: s.failures.Load()}, nil
}

// report increments the failure counter and synchronously invokes logf when
// configured. It accepts a persistence action and error; callbacks may run
// concurrently or under flushMu and must return without reentering Flush or
// Stop.
func (s *Service) report(action string, err error) {
	s.failures.Add(1)
	if s.logf != nil {
		s.logf("%s: %v", action, fmt.Errorf("request records: %w", err))
	}
}
