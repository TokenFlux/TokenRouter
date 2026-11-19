package ops

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// ingress_reject.go aggregates ingress rejection counts and queues their
// deltas for persistent batch delivery.
// Ingress rejections are counted by captured UTC minute and bounded caller
// dimensions before reaching PostgreSQL. Distinct dimensions consume a budget
// shared across all shards, with one slot reserved for an anonymous overflow
// row. Periodic snapshots remove positive count deltas while retaining
// dimension keys, so periodic extraction itself keeps the existing dimension
// reservations.
//
// A pending FIFO keeps failed batches for another flush attempt and drops new
// batches once its bounded capacity is full. PostgreSQL adds each delta to the
// matching aggregate, making successful removal from the FIFO part of delivery
// accounting rather than an idempotency guarantee. Stop closes admission, joins
// the worker, then attempts a final snapshot and flush; pending failures remain
// visible in health for the application lifecycle to report. Start and Stop
// require serialized lifecycle use.

// These bounds govern shared dimension capacity, minute grouping, pending-batch
// retention, and periodic database flush attempts for ingress rejection counts.
const (
	ingressRejectShardCount        = 16              // dimension map shard count
	ingressRejectMaxEntries        = 8192            // distinct dimensions plus overflow capacity
	ingressRejectMaxPendingBatches = 4               // retained FIFO batch capacity
	ingressRejectBucketSize        = time.Minute     // captured arrival bucket width
	ingressRejectFlushInterval     = 5 * time.Second // periodic delta snapshot delay
	ingressRejectFlushTimeout      = 5 * time.Second // per-batch write context lifetime
)

// OpsIngressRejectAggregate is one minute's rejection count for a stored
// dimension combination.
type OpsIngressRejectAggregate struct {
	ID           int64     `json:"id"`            // stored aggregate row identity
	BucketStart  time.Time `json:"bucket_start"`  // UTC minute start
	RejectReason string    `json:"reject_reason"` // bounded rejection reason label
	RouteFamily  string    `json:"route_family"`  // bounded request route category
	Protocol     string    `json:"protocol"`      // bounded observed protocol category
	ClientIP     string    `json:"client_ip"`     // recorded aggregate address string
	// UserID and APIKeyID identify loaded caller records, including middleware's
	// fallback Key lookup. The aggregator supplies positive IDs; PostgreSQL stores
	// nil as zero and returns positive values as present.
	UserID       *int64 `json:"user_id,omitempty"`    // loaded user record identity
	APIKeyID     *int64 `json:"api_key_id,omitempty"` // loaded API-key record identity
	RequestCount int64  `json:"request_count"`        // request count or pending delta
	// FirstSeen and LastSeen describe recorded arrival times for the dimension.
	// PostgreSQL merges earliest and latest times when successive count deltas
	// update the same stored aggregate.
	FirstSeen time.Time `json:"first_seen"` // first retained arrival time
	LastSeen  time.Time `json:"last_seen"`  // latest recorded arrival time
}

// OpsIngressRejectFilter selects stored aggregate dimensions and a paginated
// minute range.
type OpsIngressRejectFilter struct {
	// StartTime and EndTime independently constrain bucket starts after UTC
	// conversion. Nil omits the respective comparison; storage does not validate
	// their relative order here.
	StartTime    *time.Time // inclusive bucket start cutoff
	EndTime      *time.Time // exclusive bucket start cutoff
	RejectReason string     // exact rejection reason filter
	RouteFamily  string     // exact route family filter
	Protocol     string     // exact observed protocol filter
	// ClientIP is compared as a PostgreSQL inet value when nonblank. HTTP
	// normalizes mapped IPv4 and IPv6 /64 addresses before constructing this
	// filter.
	ClientIP string // exact aggregate address filter
	// UserID and APIKeyID independently add exact stored-identity comparisons when
	// nonnil. The HTTP parser requires positive values, while direct repository
	// callers can query zero.
	UserID   *int64 // optional stored user identity
	APIKeyID *int64 // optional stored Key identity
	// Page and PageSize are normalized by PostgreSQL listing to page one and size
	// fifty when nonpositive. PageSize is then capped at two hundred rows.
	Page     int // one-based aggregate page
	PageSize int // aggregate rows per page
}

// OpsIngressRejectList contains aggregate rows and their separately queried
// pagination metadata.
type OpsIngressRejectList struct {
	// Items owns the result slice and newly scanned rows from PostgreSQL. Callers
	// can retain the result after the database cursor closes.
	Items    []*OpsIngressRejectAggregate `json:"items"`     // returned aggregate row pointers
	Total    int                          `json:"total"`     // matching aggregate row count
	Page     int                          `json:"page"`      // normalized returned page number
	PageSize int                          `json:"page_size"` // normalized returned page size
}

// OpsIngressRejectHealth reports current aggregation backlog and cumulative
// delivery counters.
type OpsIngressRejectHealth struct {
	// Cardinality includes a retained overflow row even after its delta is zero.
	// Periodic snapshots keep dimension reservations until bucket rollover.
	Cardinality    int64  `json:"cardinality"`           // reserved dimensions plus overflow row
	Capacity       int    `json:"capacity"`              // dimension capacity including overflow
	PendingBatches int    `json:"pending_batches"`       // queued database write batches
	PendingRows    int    `json:"pending_rows"`          // rows across retained batches
	Overflowed     uint64 `json:"overflowed_count"`      // overflow-routed request count
	Dropped        uint64 `json:"dropped_count"`         // queue-overflow dropped request count
	Flushed        uint64 `json:"flushed_request_count"` // successfully removed request count
	FlushFailures  uint64 `json:"flush_failure_count"`   // failed batch write attempts
	Accepting      bool   `json:"accepting"`             // current record admission flag
	// LastError is overwritten by a failed write and cleared by a successful
	// batch. It can outlive the attempt that produced it.
	LastError string `json:"last_error,omitempty"` // last retained flush diagnostic
}

// OpsIngressRejectRepository writes rejection deltas and lists their stored
// minute aggregates.
type OpsIngressRejectRepository interface {
	// BatchUpsertIngressRejects writes supplied count deltas for their aggregate
	// dimensions. The aggregator retains a failed batch for another attempt;
	// PostgreSQL adds counts on conflict, so replaying a committed delta can count
	// it again.
	BatchUpsertIngressRejects(ctx context.Context, items []*OpsIngressRejectAggregate) error
	// ListIngressRejects returns stored rows matching dimension and bucket filters
	// with pagination metadata. PostgreSQL defaults a nil filter, applies a
	// two-hundred-row cap, and queries total and page separately.
	ListIngressRejects(ctx context.Context, filter *OpsIngressRejectFilter) (*OpsIngressRejectList, error)
}

// ingressRejectKey is the comparable dimension identity used within a minute's
// shard maps.
type ingressRejectKey struct {
	reason      string // bounded rejection reason identity
	routeFamily string // bounded route family identity
	protocol    string // bounded protocol identity value
	clientIP    string // bounded recorded address identity
	// userID and apiKeyID participate in map identity as supplied, including
	// nonpositive values. aggregateFromKey exposes pointers only for positive
	// identities when writing a delta.
	userID   int64 // raw loaded user identity
	apiKeyID int64 // raw loaded Key identity
}

// ingressRejectShard holds dimension counters accessed under its own mutex.
type ingressRejectShard struct {
	// mu guards items and each aggregate's changing count and last-arrival time.
	// Snapshots take this same lock before copying positive deltas or deleting
	// keys.
	mu    sync.Mutex                                      // dimension counter access mutex
	items map[ingressRejectKey]*OpsIngressRejectAggregate // retained dimension counter map
}

// OpsIngressRejectAggregator buffers minute rejection deltas with shared
// capacity and retryable batch writes.
type OpsIngressRejectAggregator struct {
	// repo is retained for flushes and list integration. An absent dependency
	// prevents Start and RecordIngressReject from admitting work.
	repo OpsIngressRejectRepository // aggregate delta persistence dependency

	shards [ingressRejectShardCount]ingressRejectShard // dimension maps by key hash
	// recordMu keeps an accepted record in its chosen bucket while updating a
	// shard or overflow. ensureBucket takes exclusive access before resetting
	// reservations and installing another bucket.
	recordMu sync.RWMutex // record versus rollover barrier
	// snapshotMu serializes snapshots and bucket changes. A rollover additionally
	// takes recordMu for exclusive access before clearing dimension keys.
	snapshotMu sync.Mutex // snapshot and rollover serialization
	// bucket and cardinality track the installed minute and its ordinary
	// dimensions. Periodic snapshots retain both; rollover clears reservations
	// after extracting pending deltas.
	bucket      atomic.Int64 // active bucket Unix seconds
	cardinality atomic.Int64 // ordinary dimension reservations count
	// overflowMu guards overflow creation, count updates, and delta snapshots. The
	// overflow row replaces excess dimension identities with shared anonymous
	// values for that minute.
	overflowMu sync.Mutex                 // overflow aggregate access mutex
	overflow   *OpsIngressRejectAggregate // anonymous overflow aggregate pointer

	// pendingMu protects the pending FIFO and its bounded admission. Database
	// writes release this mutex while using the retained head; flushPending
	// callers are serialized externally.
	pendingMu sync.Mutex                     // pending FIFO access mutex
	pending   [][]*OpsIngressRejectAggregate // retained count-delta batches

	// ctx and cancel belong to the constructor-created worker lifetime. wg joins
	// its admitted worker before Stop snapshots and writes final deltas with
	// independent contexts.
	ctx    context.Context    // worker termination context
	cancel context.CancelFunc // worker termination cancellation function
	wg     sync.WaitGroup     // started flush-worker join
	// flushCh has one buffered wakeup slot. Rollover sends without waiting because
	// an existing signal already requests pending-batch flushing.
	flushCh chan struct{} // coalesced pending-flush wakeup signal
	// started admits the first Start through compare-and-swap. Stop does not set
	// it, so callers start before stopping and serialize lifecycle calls.
	started atomic.Bool // single worker start admission
	// accepting is rechecked while RecordIngressReject holds recordMu. Stop stores
	// false before waiting for that record barrier.
	accepting atomic.Bool // new record admission state
	// stopOnce joins the worker and performs the final flush once. Subsequent Stop
	// calls cannot retry batches left by that flush.
	stopOnce sync.Once // single stop-and-final-flush barrier

	overflowed atomic.Uint64 // overflow-routed requests since construction
	dropped    atomic.Uint64 // discarded pending requests since construction
	// flushed counts positive deltas removed after a nil repository result. Missing
	// storage can also return nil; this counter describes successful callback
	// handling rather than independently confirmed persistence.
	flushed       atomic.Uint64 // successful callback request removals
	flushFailures atomic.Uint64 // failed batch attempts since construction
	// lastError stores strings and is initialized by construction. Failed writes
	// replace it and successful batch removals clear it.
	lastError atomic.Value // atomic string flush diagnostic
}

// NewOpsIngressRejectAggregator allocates empty shard maps, a cancelable worker
// context, and a coalesced flush signal. Call Start before recording, and Stop
// after serialized lifecycle use; construction alone admits no requests or
// database work.
func NewOpsIngressRejectAggregator(repo OpsIngressRejectRepository) *OpsIngressRejectAggregator {
	ctx, cancel := context.WithCancel(context.Background())
	a := &OpsIngressRejectAggregator{
		repo: repo, ctx: ctx, cancel: cancel, flushCh: make(chan struct{}, 1),
	}
	for i := range a.shards {
		a.shards[i].items = make(map[ingressRejectKey]*OpsIngressRejectAggregate)
	}
	a.lastError.Store("")
	return a
}

// Start admits one periodic snapshot-and-flush worker when a repository is
// present. Call it on a constructor-created aggregator before Stop, with the
// two lifecycle operations serialized.
//
// The start flag prevents repeated workers, but it is separate from stopOnce
// and worker cancellation. Stop before the first Start leaves that admission
// flag unused; a later Start can set accepting true even though its context is
// canceled. The worker snapshots periodically and responds to rollover wakeups,
// while cancellation ends its loop before final flushing performed by Stop.
func (a *OpsIngressRejectAggregator) Start() {
	if a == nil || a.repo == nil || !a.started.CompareAndSwap(false, true) {
		return
	}
	// Publish record admission before starting the worker; lifecycle callers
	// serialize startup so Stop cannot finish before this worker is registered.
	a.accepting.Store(true)
	a.wg.Go(func() {
		// A periodic wakeup snapshots deltas before flushing; rollover signals only
		// flush already extracted batches, and cancellation leaves final flushing to
		// Stop.
		ticker := time.NewTicker(ingressRejectFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				a.snapshotAndEnqueue(false)
				a.flushPending()
			case <-a.flushCh:
				a.flushPending()
			}
		}
	})
}

// Stop closes record admission, waits for the admitted worker, and attempts a
// final delta snapshot and pending-batch flush. A nil receiver is ignored, and
// stopOnce makes the operation repeatable without another final attempt.
//
// The record barrier waits for accepted updates before worker cancellation and
// final collection. Batch writes use independent contexts and can leave
// retained failures after Stop returns. The app lifecycle checks PendingBatches
// afterward to distinguish that state from completed draining. Callers
// serialize Start and Stop; Stop does not close unused start admission.
func (a *OpsIngressRejectAggregator) Stop() {
	if a == nil {
		return
	}
	a.stopOnce.Do(func() {
		// Reject new records, wait through the record barrier, cancel and join the
		// worker, then snapshot and flush remaining deltas once.
		a.accepting.Store(false)
		a.recordMu.Lock()
		a.cancel()
		a.recordMu.Unlock()
		a.wg.Wait()
		a.snapshotAndEnqueue(false)
		a.flushPending()
	})
}

// RecordIngressReject counts one rejection in the minute captured at invocation
// while admission is enabled. Strings are trimmed, defaulted, and byte-bounded;
// callers supply the address representation and loaded record IDs.
//
// A record retries bucket installation if another record switches the active
// minute before its read lock is acquired. That lock then protects its shard or
// overflow update against rollover. Existing dimensions keep accumulating after
// capacity is reached; new dimensions reserve from one global budget or use the
// anonymous overflow row. Periodic snapshots retain those reservations until
// rollover, and captured older minutes can be installed again by delayed calls.
func (a *OpsIngressRejectAggregator) RecordIngressReject(reason, routeFamily, protocol, clientIP string, userID, apiKeyID int64) {
	if a == nil || a.repo == nil || !a.accepting.Load() {
		return
	}
	now := time.Now().UTC()
	// Group each invocation by its captured UTC minute.
	bucket := now.Truncate(ingressRejectBucketSize)
	key := ingressRejectKey{
		reason: boundedDimension(reason, "unknown"), routeFamily: boundedDimension(routeFamily, "other"),
		protocol: boundedDimension(protocol, "other"), clientIP: boundedDimension(clientIP, "0.0.0.0"),
		userID: userID, apiKeyID: apiKeyID,
	}
	// Keep bucket installation and locked rechecking together: another record can
	// switch the minute while this caller waits for the record barrier.
	for {
		a.ensureBucket(bucket)
		a.recordMu.RLock()
		if !a.accepting.Load() {
			a.recordMu.RUnlock()
			return
		}
		bucketUnix := a.bucket.Load()
		if bucketUnix != bucket.Unix() {
			a.recordMu.RUnlock()
			continue
		}
		// Use the hash to choose a shard, then protect lookup, reservation, and
		// counter updates while rollover remains excluded by recordMu.
		shard := &a.shards[ingressRejectHash(key)%ingressRejectShardCount]
		shard.mu.Lock()
		if item := shard.items[key]; item != nil {
			item.RequestCount++
			item.LastSeen = now
			shard.mu.Unlock()
			a.recordMu.RUnlock()
			return
		}
		if a.reserveDimension() {
			shard.items[key] = aggregateFromKey(key, bucket, now)
			shard.mu.Unlock()
			a.recordMu.RUnlock()
			return
		}
		shard.mu.Unlock()
		a.recordOverflow(bucket, now)
		a.recordMu.RUnlock()
		return
	}
}

// ensureBucket installs the supplied minute when it differs from the active
// bucket, extracting old deltas first. Callers pass a captured bucket and can
// therefore switch backward as well as forward in time.
//
// snapshotMu precedes recordMu, keeping other snapshots serialized and
// excluding record updates while keys are cleared. The extracted batch is
// admitted to the bounded FIFO before bucket and cardinality reset. A
// nonblocking wakeup requests flushing after the state transition; an already
// buffered wakeup covers another transition without blocking request recording.
func (a *OpsIngressRejectAggregator) ensureBucket(bucket time.Time) {
	if a.bucket.Load() == bucket.Unix() {
		return
	}
	// Acquire snapshot serialization before the exclusive record barrier, matching
	// rollover lock order while extracting deltas and resetting the shared
	// dimension budget.
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()
	a.recordMu.Lock()
	defer a.recordMu.Unlock()
	if a.bucket.Load() == bucket.Unix() {
		return
	}
	items := a.snapshotLocked(true)
	a.enqueue(items)
	a.bucket.Store(bucket.Unix())
	a.cardinality.Store(0)
	// Coalesce rollover wakeups because the pending FIFO already retains deltas; a
	// full signal slot need not delay request recording.
	select {
	case a.flushCh <- struct{}{}:
	default:
	}
}

// reserveDimension atomically claims one ordinary dimension slot or reports
// capacity exhaustion. Callers hold the record barrier so rollover cannot reset
// reservations during this claim.
//
// Compare-and-swap retries when another shard wins the same counter value. The
// global limit leaves one slot for the shared overflow aggregate, allowing a
// heavily populated shard to consume ordinary capacity from the same budget as
// other shards. Periodic snapshots leave reservations intact even when their
// counters have been extracted.
func (a *OpsIngressRejectAggregator) reserveDimension() bool {
	// Reserve from the global counter with compare-and-swap so concurrent shards
	// share capacity while leaving one overflow slot available.
	for {
		current := a.cardinality.Load()
		if current >= ingressRejectMaxEntries-1 {
			return false
		}
		if a.cardinality.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

// recordOverflow adds a capacity-exceeded request to the minute's anonymous
// aggregate and cumulative overflow counter. Callers hold recordMu while this
// helper uses overflowMu for its aggregate.
//
// The first request for a different bucket replaces the overflow row with
// generic reason, route, protocol, and address values. Later requests increase
// its delta and last-arrival time. The row carries no caller identities, so
// overload retains total counts while losing the excess dimensions' individual
// attribution. snapshotLocked uses the same mutex when copying or clearing this
// row.
func (a *OpsIngressRejectAggregator) recordOverflow(bucket, now time.Time) {
	a.overflowed.Add(1)
	// Use the overflow mutex for replacement and incrementing so snapshots cannot
	// observe a partially updated anonymous count or arrival time.
	a.overflowMu.Lock()
	defer a.overflowMu.Unlock()
	if a.overflow == nil || !a.overflow.BucketStart.Equal(bucket) {
		a.overflow = &OpsIngressRejectAggregate{
			BucketStart: bucket, RejectReason: "other", RouteFamily: "other", Protocol: "other",
			ClientIP: "0.0.0.0", RequestCount: 1, FirstSeen: now, LastSeen: now,
		}
		return
	}
	a.overflow.RequestCount++
	a.overflow.LastSeen = now
}

// snapshotAndEnqueue serializes delta extraction and then attempts bounded FIFO
// admission. It ignores a nil receiver or missing repository; reset clears keys
// without resetting cardinality or excluding records, while ensureBucket owns
// the additional rollover coordination.
func (a *OpsIngressRejectAggregator) snapshotAndEnqueue(reset bool) {
	if a == nil || a.repo == nil {
		return
	}
	// Serialize extraction with rollover, then release snapshotMu before bounded
	// FIFO admission so pending bookkeeping does not retain that serialization
	// lock.
	a.snapshotMu.Lock()
	items := a.snapshotLocked(reset)
	a.snapshotMu.Unlock()
	a.enqueue(items)
}

// snapshotLocked copies positive count deltas and zeros their source counts
// while the caller holds snapshotMu. With reset false, dimension keys and
// original arrival metadata remain for subsequent observations in that bucket.
//
// Each shard is copied under its own mutex, and overflow uses its separate
// mutex, so a periodic snapshot is not one atomic view across all dimensions.
// Copies retain optional ID pointers that the aggregator treats as immutable.
// reset deletes retained keys and overflow; the rollover caller separately
// excludes records and resets the global reservation counter.
func (a *OpsIngressRejectAggregator) snapshotLocked(reset bool) []*OpsIngressRejectAggregate {
	items := make([]*OpsIngressRejectAggregate, 0)
	// For each shard, copy a positive delta before zeroing its count; retained
	// keys preserve the current minute's dimension budget on periodic flushes.
	for i := range a.shards {
		shard := &a.shards[i]
		shard.mu.Lock()
		for key, item := range shard.items {
			if item.RequestCount > 0 {
				copyItem := *item
				items = append(items, &copyItem)
				item.RequestCount = 0
			}
			if reset {
				delete(shard.items, key)
			}
		}
		shard.mu.Unlock()
	}
	// Copy and reset the shared overflow delta under its own mutex, keeping its
	// request count consistent with its arrival metadata.
	a.overflowMu.Lock()
	if a.overflow != nil && a.overflow.RequestCount > 0 {
		copyItem := *a.overflow
		items = append(items, &copyItem)
		a.overflow.RequestCount = 0
	}
	if reset {
		a.overflow = nil
	}
	a.overflowMu.Unlock()
	return items
}

// enqueue retains a nonempty delta batch when the pending FIFO has space. The
// supplied slice and row pointers become shared with flushing and must remain
// unchanged after admission.
//
// pendingMu serializes admission and keeps the bound global across snapshots.
// When all batch slots are occupied, this batch's positive request counts are
// added to dropped and its already extracted deltas are discarded. No database
// attempt occurs on that overflow path. The FIFO limit counts batches, so it is
// independent of the number of rows each batch contains.
func (a *OpsIngressRejectAggregator) enqueue(items []*OpsIngressRejectAggregate) {
	if len(items) == 0 {
		return
	}
	// Serialize batch admission and count discarded requests when the FIFO is
	// full, because extraction has already removed their source deltas.
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	if len(a.pending) >= ingressRejectMaxPendingBatches {
		a.dropped.Add(batchRequestCount(items))
		return
	}
	a.pending = append(a.pending, items)
}

// flushPending writes FIFO heads until the queue is empty or a batch write
// fails. Callers serialize this method; pendingMu protects selection and
// removal but is released around the repository call.
//
// Each write uses an independent background-derived timeout so worker
// cancellation still permits Stop's final attempt. A failure leaves the head
// for a later call and records its diagnostic. Success removes the head before
// increasing flushed and clearing LastError. Additive repository writes make
// retries sensitive to ambiguous commit errors; neither this queue nor its
// counters provides an exactly-once guarantee.
func (a *OpsIngressRejectAggregator) flushPending() {
	for {
		// Borrow the retained FIFO head while releasing pendingMu for database I/O.
		// The worker or joined Stop serializes flush callers.
		a.pendingMu.Lock()
		if len(a.pending) == 0 {
			a.pendingMu.Unlock()
			return
		}
		batch := a.pending[0]
		a.pendingMu.Unlock()

		// Give each batch a fresh write context so cancellation of the periodic
		// worker does not cancel Stop's final persistence attempt.
		ctx, cancel := context.WithTimeout(context.Background(), ingressRejectFlushTimeout)
		err := a.repo.BatchUpsertIngressRejects(ctx, batch)
		cancel()
		// Keep failed deltas at the FIFO head for the next attempt; update diagnostic
		// state and stop this flush before processing later batches.
		if err != nil {
			a.flushFailures.Add(1)
			a.lastError.Store(err.Error())
			logging.LegacyPrintf("service.ops_ingress_reject", "[IngressRejectAggregator] flush failed: %v", err)
			return
		}
		// Remove this completed head before advancing the flushed counter, so later
		// attempts cannot select the same retained slice as the next FIFO head.
		a.pendingMu.Lock()
		if len(a.pending) > 0 {
			a.pending = a.pending[1:]
		}
		a.pendingMu.Unlock()
		a.flushed.Add(batchRequestCount(batch))
		a.lastError.Store("")
	}
}

// batchRequestCount sums positive request deltas from nonnil rows for dropped
// and flushed accounting. An empty batch or one containing no positive deltas
// returns zero; addition has no overflow check.
func batchRequestCount(items []*OpsIngressRejectAggregate) uint64 {
	var count uint64
	for _, item := range items {
		if item != nil && item.RequestCount > 0 {
			count += uint64(item.RequestCount)
		}
	}
	return count
}

// Health samples dimension reservations, backlog sizes, and cumulative delivery
// counters for administrators. A nil receiver returns the configured capacity
// alone.
//
// The retained overflow row contributes a cardinality slot even when its latest
// delta has already been snapshotted. Backlog rows are counted under pendingMu,
// while other groups use separate locks or atomic reads, so the returned fields
// can describe different moments. LastError is the latest failed-write string
// until a successful batch clears it, and Accepting reports the current
// admission flag.
func (a *OpsIngressRejectAggregator) Health() OpsIngressRejectHealth {
	h := OpsIngressRejectHealth{Capacity: ingressRejectMaxEntries}
	if a == nil {
		return h
	}
	h.Cardinality = a.cardinality.Load()
	// Count retained overflow storage separately from ordinary reservations,
	// including a zero-delta row whose dimension slot remains occupied after
	// snapshots.
	a.overflowMu.Lock()
	if a.overflow != nil {
		h.Cardinality++
	}
	a.overflowMu.Unlock()
	// Read FIFO batch and row counts under the same mutex so these two backlog
	// fields describe one queue view.
	a.pendingMu.Lock()
	h.PendingBatches = len(a.pending)
	for _, batch := range a.pending {
		h.PendingRows += len(batch)
	}
	a.pendingMu.Unlock()
	h.Overflowed = a.overflowed.Load()
	h.Dropped = a.dropped.Load()
	h.Flushed = a.flushed.Load()
	h.FlushFailures = a.flushFailures.Load()
	h.Accepting = a.accepting.Load()
	if v := a.lastError.Load(); v != nil {
		h.LastError, _ = v.(string)
	}
	return h
}

// boundedDimension trims and byte-bounds nonblank dimension text. Blank input
// returns fallback unchanged, so callers supply a fallback already in their
// accepted form. The sixty-four-byte cut can split UTF-8; callers perform
// required validation and address normalization.
func boundedDimension(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	// Bound dimension cardinality inputs by byte length; UTF-8 validity is not
	// restored after slicing.
	if len(value) > 64 {
		return value[:64]
	}
	return value
}

// ingressRejectHash mixes dimension bytes and raw IDs into a nonnegative shard
// selector. Hash collisions share a shard but retain separate map keys; callers
// apply the shard-count modulus.
func ingressRejectHash(k ingressRejectKey) int {
	// Use fixed multiplicative mixing to select shards; dimension equality is
	// still checked by the complete map key.
	h := uint64(1469598103934665603)
	for _, value := range []string{k.reason, k.routeFamily, k.protocol, k.clientIP} {
		for i := range len(value) {
			h ^= uint64(value[i])
			h *= 1099511628211
		}
	}
	h ^= uint64(k.userID)
	h *= 1099511628211
	h ^= uint64(k.apiKeyID)
	// Keep the selector within a signed thirty-one-bit range.
	return int(h & 0x7fffffff)
}

// aggregateFromKey creates a one-request delta using the supplied bucket,
// arrival time, and dimensions. Positive IDs receive new optional pointers;
// nonpositive IDs stay absent even though raw IDs distinguished the in-memory
// keys.
func aggregateFromKey(k ingressRejectKey, bucket, now time.Time) *OpsIngressRejectAggregate {
	item := &OpsIngressRejectAggregate{
		BucketStart: bucket, RejectReason: k.reason, RouteFamily: k.routeFamily, Protocol: k.protocol,
		ClientIP: k.clientIP, RequestCount: 1, FirstSeen: now, LastSeen: now,
	}
	if k.userID > 0 {
		value := k.userID
		item.UserID = &value
	}
	if k.apiKeyID > 0 {
		value := k.apiKeyID
		item.APIKeyID = &value
	}
	return item
}

// SetIngressRejectAggregator binds the supplied aggregator for service
// recording and health reads. A nil service is ignored; callers bind during
// setup before concurrent recording because the pointer assignment has no
// synchronization.
func (s *OpsService) SetIngressRejectAggregator(a *OpsIngressRejectAggregator) {
	if s != nil {
		s.ingressRejectAggregator = a
	}
}

// RecordIngressReject forwards rejection dimensions and loaded caller IDs to
// the bound aggregator. A nil service or unbound aggregator silently omits the
// observation; aggregator admission determines whether a forwarded request is
// counted.
func (s *OpsService) RecordIngressReject(reason, routeFamily, protocol, clientIP string, userID, apiKeyID int64) {
	if s != nil && s.ingressRejectAggregator != nil {
		s.ingressRejectAggregator.RecordIngressReject(reason, routeFamily, protocol, clientIP, userID, apiKeyID)
	}
}

// GetIngressRejectHealth returns the bound aggregator's sampled health. A nil
// service or unbound aggregator returns capacity alone; this accessor itself
// does not check whether monitoring is enabled.
func (s *OpsService) GetIngressRejectHealth() OpsIngressRejectHealth {
	if s == nil || s.ingressRejectAggregator == nil {
		return OpsIngressRejectHealth{Capacity: ingressRejectMaxEntries}
	}
	return s.ingressRejectAggregator.Health()
}

// ListIngressRejects checks monitoring and delegates aggregate listing when the
// Ops repository implements the dedicated interface. An unavailable capability
// returns an empty page one with size fifty, ignoring requested pagination.
func (s *OpsService) ListIngressRejects(ctx context.Context, filter *OpsIngressRejectFilter) (*OpsIngressRejectList, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	repo, ok := s.opsRepo.(OpsIngressRejectRepository)
	// After monitoring succeeds, a missing aggregate-list capability returns an
	// empty Items array with fixed pagination so the HTTP result remains usable.
	if !ok {
		return &OpsIngressRejectList{Items: []*OpsIngressRejectAggregate{}, Page: 1, PageSize: 50}, nil
	}
	return repo.ListIngressRejects(ctx, filter)
}
