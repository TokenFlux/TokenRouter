package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_log_repo_insert.go persists usage records through direct and queued
// writes with duplicate recovery and cancellation-aware results.
// A usage write can be canceled before admission, while queued, or after
// database processing starts. Result-aware writes distinguish an inserted row
// from an existing row and can confirm that a canceled queued submission never
// began; an expired wait after processing starts leaves persistence unresolved.
//
// Two bounded queues combine pool-backed records into SQL batches under
// independent background deadlines. Result-aware batches retain the caller's row
// for identity updates and group equal request/key pairs before recovering
// database state. Best-effort batches acknowledge SQL completion and briefly
// cache successful keys. Shutdown excludes admissions before closing both
// channels. Prepared arguments copy many values but retain optional multiplier,
// provider-cost, and response-mismatch pointers, so callers keep those
// referenced values stable until processing completes.

// Result-aware states arbitrate cancellation against worker admission; batch
// limits bound collection time and queue storage separately for identity and
// acknowledgement writes.
const (
	usageLogCreateStateQueued     int32 = iota // waiting for worker ownership
	usageLogCreateStateProcessing              // worker owns database processing
	usageLogCreateStateCompleted               // result publication has begun
	usageLogCreateStateCanceled                // caller canceled before processing

	usageLogCreateBatchMaxSize  = 64                   // result-aware records per flush
	usageLogCreateBatchWindow   = 3 * time.Millisecond // result-aware collection window
	usageLogCreateBatchQueueCap = 4096                 // buffered result-aware admissions
	usageLogCreateCancelWait    = 2 * time.Second      // result wait after processing cancellation

	usageLogBestEffortBatchMaxSize  = 256                   // acknowledgement records per flush
	usageLogBestEffortBatchWindow   = 20 * time.Millisecond // acknowledgement collection window
	usageLogBestEffortBatchQueueCap = 32768                 // buffered best-effort admissions
	usageLogBestEffortRecentTTL     = 30 * time.Second      // successful submission suppression window
)

var (
	// usageLogInsertSchema defines every insert column and its PostgreSQL type in
	// the order used by prepareUsageLogInsert's explicit argument construction.
	usageLogInsertSchema = [...]usageLogInsertColumn{
		{name: "user_id", dataType: "bigint"},
		{name: "billing_user_id", dataType: "bigint"},
		{name: "team_id", dataType: "bigint"},
		{name: "api_key_id", dataType: "bigint"},
		{name: "provider_id", dataType: "bigint"},
		{name: "request_id", dataType: "text"},
		{name: "model", dataType: "text"},
		{name: "requested_model", dataType: "text"},
		{name: "upstream_model", dataType: "text"},
		{name: "group_id", dataType: "bigint"},
		{name: "subscription_id", dataType: "bigint"},
		{name: "input_tokens", dataType: "integer"},
		{name: "output_tokens", dataType: "integer"},
		{name: "cache_creation_tokens", dataType: "integer"},
		{name: "cache_read_tokens", dataType: "integer"},
		{name: "cache_creation_5m_tokens", dataType: "integer"},
		{name: "cache_creation_1h_tokens", dataType: "integer"},
		{name: "image_output_tokens", dataType: "integer"},
		{name: "image_output_cost", dataType: "numeric"},
		{name: "image_input_tokens", dataType: "integer"},
		{name: "image_input_cost", dataType: "numeric"},
		{name: "input_cost", dataType: "numeric"},
		{name: "output_cost", dataType: "numeric"},
		{name: "cache_creation_cost", dataType: "numeric"},
		{name: "cache_read_cost", dataType: "numeric"},
		{name: "total_cost", dataType: "numeric"},
		{name: "actual_cost", dataType: "numeric"},
		{name: "subscription_amount_usd", dataType: "numeric"},
		{name: "balance_amount_usd", dataType: "numeric"},
		{name: "billing_allocations", dataType: "jsonb"},
		{name: "rate_multiplier", dataType: "numeric"},
		{name: "provider_rate_multiplier", dataType: "numeric"},
		{name: "billing_type", dataType: "smallint"},
		{name: "request_type", dataType: "smallint"},
		{name: "stream", dataType: "boolean"},
		{name: "openai_ws_mode", dataType: "boolean"},
		{name: "duration_ms", dataType: "integer"},
		{name: "timings", dataType: "jsonb"},
		{name: "user_agent", dataType: "text"},
		{name: "ip_address", dataType: "text"},
		{name: "image_count", dataType: "integer"},
		{name: "image_frame_count", dataType: "integer"},
		{name: "image_cache_read_tokens", dataType: "integer"},
		{name: "image_size", dataType: "text"},
		{name: "image_input_size", dataType: "text"},
		{name: "image_output_size", dataType: "text"},
		{name: "image_size_source", dataType: "text"},
		{name: "image_size_breakdown", dataType: "jsonb"},
		{name: "video_count", dataType: "integer"},
		{name: "video_resolution", dataType: "text"},
		{name: "video_duration_seconds", dataType: "integer"},
		{name: "service_tier", dataType: "text"},
		{name: "reasoning_effort", dataType: "text"},
		{name: "inbound_endpoint", dataType: "text"},
		{name: "upstream_endpoint", dataType: "text"},
		{name: "cache_ttl_overridden", dataType: "boolean"},
		{name: "long_context_billing_applied", dataType: "boolean"},
		{name: "pricing_config_id", dataType: "bigint"},
		{name: "model_mapping_chain", dataType: "text"},
		{name: "billing_tier", dataType: "text"},
		{name: "billing_mode", dataType: "text"},
		{name: "provider_stats_cost", dataType: "numeric"},
		{name: "upstream_request_id", dataType: "text"},
		{name: "session_id", dataType: "text"},
		{name: "created_at", dataType: "timestamptz"},
		{name: "requested_reasoning_effort", dataType: "text"},
		{name: "native_compaction_v2", dataType: "boolean"},
		{name: "platform", dataType: "text"},
		{name: "upstream_response_model", dataType: "text"},
		{name: "upstream_model_mismatch", dataType: "boolean"},
		{name: "billing_key", dataType: "text"},
		{name: "request_source", dataType: "text"},
		{name: "system_usage", dataType: "jsonb"},
	}

	// usageLogInsert caches fixed SQL metadata once for the direct and batch
	// writers; all callers leave its strings and derived slices unchanged.
	usageLogInsert = buildUsageLogInsertMetadata()
)

// usageLogInsertColumn pairs one persisted field with its batch-input SQL type.
type usageLogInsertColumn struct {
	name     string // persisted column name
	dataType string // PostgreSQL parameter type
}

// usageLogInsertMetadata holds fixed SQL fragments for every usage write path.
// Its columns, argTypes, casts, and statement share the descriptor's order and
// remain unchanged after package initialization.
type usageLogInsertMetadata struct {
	columns   string   // ordered SQL column list
	argTypes  []string // ordered PostgreSQL parameter types
	casts     []string // ordered SQL cast suffixes
	statement string   // conflict-tolerant single-row insert SQL
}

// usageLogCreateRequest carries a result-aware write and its caller-shared row
// state.
type usageLogCreateRequest struct {
	// log stays shared and receives ID/CreatedAt after SQL identity recovery. A
	// processing-stage timeout can return while the worker still owns the row, so
	// the caller leaves it stable until that outstanding work finishes.
	log      *usage.UsageLog
	prepared usageLogInsertPrepared // captured positional write arguments
	// shared arbitrates queued cancellation against worker processing and marks
	// completion before result delivery; nil is accepted by direct flush fixtures.
	shared *usageLogCreateShared
	// resultCh is buffered by production submissions so callers can stop waiting;
	// worker delivery is nonblocking when the channel has no room or receiver.
	// Callers leave it open until all worker delivery attempts have finished.
	resultCh chan usageLogCreateResult
}

// usageLogCreateResult reports insertion evidence and an optional write outcome
// error.
type usageLogCreateResult struct {
	// inserted belongs to the first caller in an inserted request/key group;
	// duplicate waiters receive false even when their shared SQL input inserted.
	inserted bool  // this caller confirmed insertion
	err      error // write or resolution failure
}

// usageLogBestEffortRequest carries prepared data and an acknowledgement for one
// submission.
type usageLogBestEffortRequest struct {
	prepared usageLogInsertPrepared // captured positional write arguments
	apiKeyID int64                  // request grouping key identity
	// resultCh acknowledges the worker's final SQL result; caller cancellation can
	// abandon it while accepted database work continues independently. Callers
	// leave it open until the worker's delivery attempt has finished.
	resultCh chan error
}

// usageLogInsertPrepared stores insert values after diagnostic and legacy-field
// normalization.
type usageLogInsertPrepared struct {
	// createdAt, requestID, rateMultiplier, and requestType repeat values encoded
	// in args. Grouping and result publication read these summaries without
	// decoding the positional SQL argument list again.
	createdAt      time.Time // supplied or generated record instant
	requestID      string    // trimmed diagnostic request identity
	rateMultiplier float64   // supplied user rate multiplier
	requestType    int16     // normalized request category code
	// args follows usageLogInsertSchema. Optional provider multiplier, provider
	// cost, and response-mismatch pointers stay shared with their input owners
	// until the SQL writer finishes reading them.
	args []any
}

// usageLogBatchState carries decoded database identity values for one grouped
// usage write.
type usageLogBatchState struct {
	ID        int64     // decoded database row identifier
	CreatedAt time.Time // decoded stored creation instant
}

// usageLogBatchRow decodes one SQL JSON result keyed by request and key
// identity.
type usageLogBatchRow struct {
	// RequestID and APIKeyID identify the caller's input group even when SQL
	// recovers a stored row carrying a different request ID through its billing key.
	RequestID string `json:"request_id"` // diagnostic request grouping value
	APIKeyID  int64  `json:"api_key_id"` // grouping key record identifier
	// ID and CreatedAt receive SQL identity values. JSON null leaves their zero
	// values, and batch decoding does not reject those unresolved identity values.
	ID        int64     `json:"id"`         // decoded database row identifier
	CreatedAt time.Time `json:"created_at"` // decoded stored creation instant
	// Inserted records SQL's request-ID insertion match; a billing-key match to a
	// different request ID can resolve identity while this flag remains false.
	Inserted bool `json:"inserted"` // statement insertion evidence flag
}

// usageLogCreateShared coordinates cancellation and worker ownership for one
// queued request.
type usageLogCreateShared struct {
	// state starts queued at zero, changes by admission CAS, and becomes completed
	// before result delivery; losing cancellation leaves worker ownership intact.
	state atomic.Int32
}

// buildUsageLogInsertMetadata derives fixed SQL fragments once during package
// initialization. Prepared argument construction must follow the schema's order;
// callers treat the returned type and cast slices as immutable.
func buildUsageLogInsertMetadata() usageLogInsertMetadata {
	names := make([]string, len(usageLogInsertSchema))
	argTypes := make([]string, len(usageLogInsertSchema))
	casts := make([]string, len(usageLogInsertSchema))
	var values strings.Builder
	for i, column := range usageLogInsertSchema {
		names[i] = column.name
		argTypes[i] = column.dataType
		casts[i] = "::" + column.dataType
		if i > 0 {
			// Retain the single-row statement's line groups while deriving every
			// numbered placeholder from the shared column order.
			switch i {
			case 9, 11, 15, 19, 25:
				_, _ = values.WriteString(",\n\t\t\t")
			default:
				_, _ = values.WriteString(", ")
			}
		}
		_, _ = values.WriteString("$")
		_, _ = values.WriteString(strconv.Itoa(i + 1))
	}
	columns := "\n\t\t\t" + strings.Join(names, ",\n\t\t\t") + "\n\t\t"
	return usageLogInsertMetadata{
		columns:  columns,
		argTypes: argTypes,
		casts:    casts,
		statement: `
		INSERT INTO usage_logs (` + columns + `) VALUES (
			` + values.String() + `
		)
		ON CONFLICT DO NOTHING
	`,
	}
}

// Create writes a usage record and reports whether this submission inserted it.
// Nil rows return false without work; an Ent transaction in ctx or a blank
// request ID takes the direct path, and other records can use the result-aware
// queue.
//
// Preparation trims RequestID and normalizes transport flags. Confirmed SQL row
// identities populate ID and CreatedAt, while partial batch-resolution failures
// follow flushCreateBatch's per-waiter rules. Queue cancellation can confirm
// nonpersistence before processing; a later timeout leaves worker ownership and
// persistence unresolved. Callers retain stable row fields and referenced values
// while that work remains outstanding.
func (r *Store) Create(ctx context.Context, log *usage.UsageLog) (bool, error) {
	if log == nil {
		return false, nil
	}

	if tx := dbent.TxFromContext(ctx); tx != nil {
		return r.createSingle(ctx, tx.Client(), log)
	}
	if log.EffectiveRequestSource() != usage.SourceUser {
		// System attribution has no API-key identity, so customer-key batch
		// recovery cannot correlate it. Provider-only settlement supplies its own
		// transaction and the direct writer retains that connection.
		return r.createSingle(ctx, r.sql, log)
	}
	requestID := strings.TrimSpace(log.RequestID)
	if requestID == "" {
		return r.createSingle(ctx, r.sql, log)
	}
	log.RequestID = requestID
	return r.createBatched(ctx, log)
}

// CreateBestEffort attempts an acknowledged analytical write and returns its
// error. A stopped batcher rejects the call, while nil input otherwise succeeds.
// Transaction or nonpool executors use createSingle, including its row-identity
// updates; pool-backed records can enter the best-effort queue.
//
// Recent successful request/key submissions can return immediately. The caller's
// context bounds admission and result waiting, while accepted queued work uses
// the worker's background deadline. A Dropped result after admission leaves
// persistence unresolved. Queued acknowledgements do not populate row identity,
// and callers keep retained argument pointers stable until processing finishes.
func (r *Store) CreateBestEffort(ctx context.Context, log *usage.UsageLog) error {
	if log != nil && log.EffectiveRequestSource() != usage.SourceUser {
		_, err := r.Create(ctx, log)
		return err
	}

	// Hold admission open through the caller wait, so shutdown closes initialized
	// channels only after active writers have stopped sending or awaiting results.
	r.batchLifecycleMu.RLock()
	defer r.batchLifecycleMu.RUnlock()
	if r.batchStopped {
		return usage.MarkUsageLogCreateNotPersisted(fmt.Errorf("usage log batcher stopped"))
	}

	if log == nil {
		return nil
	}

	if tx := dbent.TxFromContext(ctx); tx != nil {
		_, err := r.createSingle(ctx, tx.Client(), log)
		return err
	}
	if r.db == nil {
		_, err := r.createSingle(ctx, r.sql, log)
		return err
	}

	r.ensureBestEffortBatcher()
	if r.bestEffortBatchCh == nil {
		_, err := r.createSingle(ctx, r.sql, log)
		return err
	}

	req := usageLogBestEffortRequest{
		prepared: prepareUsageLogInsert(log),
		apiKeyID: log.APIKeyID,
		resultCh: make(chan error, 1),
	}
	if key, ok := r.bestEffortRecentKey(req.prepared.requestID, req.apiKeyID); ok {
		if _, exists := r.bestEffortRecent.Get(key); exists {
			return nil
		}
	}

	// Completion can already have charged this request, so queue saturation waits
	// for room until ctx ends. A rejected acknowledgement lets completion try
	// Create.
	select {
	case r.bestEffortBatchCh <- req:
	case <-ctx.Done():
		return usage.MarkUsageLogCreateNotPersisted(ctx.Err())
	}

	// Once admitted, the worker owns a background deadline. Abandoning this wait
	// marks the result dropped even though that database operation can still
	// finish.
	select {
	case err := <-req.resultCh:
		return err
	case <-ctx.Done():
		return usage.MarkUsageLogCreateDropped(ctx.Err())
	}
}

// createSingle prepares and writes one usage row through the selected executor.
// Nil sqlq selects the store executor, and an already canceled nonnil context
// receives a confirmed-unwritten error before SQL execution.
//
// INSERT ignores conflicts and returns new row identity when insertion occurs. A
// nonempty request ID with no returned row triggers lookup by request identity
// or the financial key, scoped to APIKeyID; that recovery returns false and
// fills existing ID/CreatedAt. The lookup binds supplied BillingKey directly,
// while insert preparation stores its trimmed value. Lookup and scan errors
// remain errors, and prepared RateMultiplier is published only after the write
// or recovery succeeds.
func (r *Store) createSingle(ctx context.Context, sqlq sqlExecutor, log *usage.UsageLog) (bool, error) {
	prepared := prepareUsageLogInsert(log)
	if sqlq == nil {
		sqlq = r.sql
	}
	if ctx != nil && ctx.Err() != nil {
		return false, usage.MarkUsageLogCreateNotPersisted(ctx.Err())
	}

	query := usageLogInsert.statement + " RETURNING id, created_at"

	if err := scanSingleRow(ctx, sqlq, query, prepared.args, &log.ID, &log.CreatedAt); err != nil {
		// A uniqueness conflict may refer to a retained legacy financial key; recover
		// its row identity under the same key ID before reporting a duplicate.
		if errors.Is(err, sql.ErrNoRows) && prepared.requestID != "" {
			billingKey := log.BillingKey
			if billingKey == "" {
				billingKey = prepared.requestID
			}
			selectQuery := "SELECT id, created_at FROM usage_logs WHERE (request_id = $1 OR COALESCE(billing_key, request_id) = $3) AND api_key_id = $2"
			selectArgs := []any{prepared.requestID, log.APIKeyID, billingKey}
			if log.EffectiveRequestSource() != usage.SourceUser {
				selectQuery = "SELECT id, created_at FROM usage_logs WHERE request_id=$1 AND request_source=$2"
				selectArgs = []any{prepared.requestID, log.RequestSource}
			}
			if err := scanSingleRow(ctx, sqlq, selectQuery, selectArgs, &log.ID, &log.CreatedAt); err != nil {
				return false, err
			}
			log.RateMultiplier = prepared.rateMultiplier
			return false, nil
		}
		return false, err
	}
	log.RateMultiplier = prepared.rateMultiplier
	return true, nil
}

// createBatched submits a result-aware record while holding the lifecycle read
// lock through its wait. A stopped queue returns a confirmed-unwritten error;
// nonpool or unavailable queue paths delegate to the direct writer.
//
// Admission waits for space under ctx. After admission, cancellation claims the
// queued state to prevent processing, but a worker that already claimed
// processing can still write. That case waits up to two additional seconds for
// the result before returning the context error with unresolved persistence. The
// worker retains log for result updates, so callers keep it stable while
// processing remains outstanding.
func (r *Store) createBatched(ctx context.Context, log *usage.UsageLog) (bool, error) {
	// Hold admission open through the caller wait, so shutdown closes initialized
	// channels only after active writers have stopped sending or awaiting results.
	r.batchLifecycleMu.RLock()
	defer r.batchLifecycleMu.RUnlock()
	if r.batchStopped {
		return false, usage.MarkUsageLogCreateNotPersisted(fmt.Errorf("usage log batcher stopped"))
	}

	// Nonpool executors may be caller transactions; use their statement path
	// directly so queued work cannot escape the selected transaction connection.
	if r.db == nil {
		return r.createSingle(ctx, r.sql, log)
	}
	r.ensureCreateBatcher()
	if r.createBatchCh == nil {
		return r.createSingle(ctx, r.sql, log)
	}

	req := usageLogCreateRequest{
		log:      log,
		prepared: prepareUsageLogInsert(log),
		shared:   &usageLogCreateShared{},
		resultCh: make(chan usageLogCreateResult, 1),
	}

	// Queue saturation waits for worker progress until ctx ends; rejecting
	// admission can then report confirmed nonpersistence because no worker owns
	// this request.
	select {
	case r.createBatchCh <- req:
	case <-ctx.Done():
		return false, usage.MarkUsageLogCreateNotPersisted(ctx.Err())
	}

	select {
	case res := <-req.resultCh:
		return res.inserted, res.err
	case <-ctx.Done():
		// Claiming queued state prevents the worker's processing CAS; when that claim
		// loses, allow a short result wait because persistence may already be
		// underway.
		if req.shared != nil && req.shared.state.CompareAndSwap(usageLogCreateStateQueued, usageLogCreateStateCanceled) {
			return false, usage.MarkUsageLogCreateNotPersisted(ctx.Err())
		}
		// Processing already won admission. Bound this extra wait while accepting that
		// a later timeout can leave the shared row and database outcome unresolved.
		timer := time.NewTimer(usageLogCreateCancelWait)
		defer timer.Stop()
		select {
		case res := <-req.resultCh:
			return res.inserted, res.err
		case <-timer.C:
			return false, ctx.Err()
		}
	}
}

// ensureCreateBatcher initializes the result-aware worker once for a pool-backed
// store. Nil stores and nonpool executors do nothing; callers coordinate
// initialization with the lifecycle read lock so shutdown cannot close a
// concurrently created queue.
func (r *Store) ensureCreateBatcher() {
	if r == nil || r.db == nil {
		return
	}
	// Inspect and publish the create channel inside Once so concurrent first
	// writers observe one initialized worker while lifecycle locking excludes
	// shutdown.
	r.createBatchOnce.Do(func() {
		if r.createBatchCh == nil {
			r.createBatchCh = make(chan usageLogCreateRequest, usageLogCreateBatchQueueCap)
			r.batchWG.Go(func() { ; r.runCreateBatcher(r.db) })
		}
	})
}

// ensureBestEffortBatcher initializes the acknowledgement-only worker once when
// a database pool exists. Nil stores and nonpool executors do nothing; callers
// hold the lifecycle read lock to coordinate queue publication with shutdown.
func (r *Store) ensureBestEffortBatcher() {
	if r == nil || r.db == nil {
		return
	}
	// Inspect and publish the acknowledgement channel inside Once; first writers
	// share its initialization and the lifecycle read lock excludes concurrent
	// close.
	r.bestEffortBatchOnce.Do(func() {
		if r.bestEffortBatchCh == nil {
			r.bestEffortBatchCh = make(chan usageLogBestEffortRequest, usageLogBestEffortBatchQueueCap)
			r.batchWG.Go(func() { ; r.runBestEffortBatcher(r.db) })
		}
	})
}

// runCreateBatcher drains result-aware admissions until its queue closes. It
// collects up to the batch-size cap or the short collection window, then flushes
// that group before reading further submissions.
//
// Channel close ends collection but still flushes the records already taken. The
// worker stops each timer and consumes any available tick before database work
// begins. Each collection cycle creates its own timer, and the worker remains in
// the store's wait group, so normal shutdown waits for every accepted group to
// be processed.
func (r *Store) runCreateBatcher(db *sql.DB) {
	for {
		// Drain accepted requests until the channel closes; already received records
		// remain owned by this worker and reach its final flush before exit.
		first, ok := <-r.createBatchCh
		if !ok {
			return
		}

		// Bound each result-aware flush while later admissions remain queued.
		batch := make([]usageLogCreateRequest, 0, usageLogCreateBatchMaxSize)
		batch = append(batch, first)

		timer := time.NewTimer(usageLogCreateBatchWindow)
	batchLoop:
		for len(batch) < usageLogCreateBatchMaxSize {
			select {
			case req, ok := <-r.createBatchCh:
				if !ok {
					break batchLoop
				}
				batch = append(batch, req)
			case <-timer.C:
				break batchLoop
			}
		}
		// Collection can end through size, close, or the timer. Stop or drain the
		// fired timer before flushing so its event cannot remain as unused batch work.
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		r.flushCreateBatch(db, batch)
	}
}

// runBestEffortBatcher drains acknowledgement-only requests until the queue is
// closed. It collects a bounded group for its batch window, then flushes before
// accepting the next group for processing.
//
// A closed channel stops collection while keeping records already received in
// the current flush. Timer cleanup consumes any available fired event before
// leaving collection, and each subsequent collection creates a new timer. The
// lifecycle wait group retains this worker through final drainage; callers can
// abandon acknowledgements independently of the worker's background database
// context.
func (r *Store) runBestEffortBatcher(db *sql.DB) {
	for {
		// Queue close ends new reads after accepted entries drain. Every received
		// group still reaches SQL acknowledgement before this worker exits.
		first, ok := <-r.bestEffortBatchCh
		if !ok {
			return
		}

		// Bound each acknowledgement flush while later submissions remain queued.
		batch := make([]usageLogBestEffortRequest, 0, usageLogBestEffortBatchMaxSize)
		batch = append(batch, first)

		timer := time.NewTimer(usageLogBestEffortBatchWindow)
	bestEffortLoop:
		for len(batch) < usageLogBestEffortBatchMaxSize {
			select {
			case req, ok := <-r.bestEffortBatchCh:
				if !ok {
					break bestEffortLoop
				}
				batch = append(batch, req)
			case <-timer.C:
				break bestEffortLoop
			}
		}
		// Collection can end through size, close, or the timer. Stop or drain the
		// fired timer before flushing so its event cannot remain as unused batch work.
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		r.flushBestEffortBatch(db, batch)
	}
}

// flushCreateBatch processes result-aware admissions and attempts each request's
// result publication. Empty batches do nothing, and queued cancellation is
// acknowledged as unwritten before SQL grouping.
//
// Equal request/key pairs share the first prepared payload. SQL/scan errors can
// permit individual recovery with detached deadlines; decoded-result errors
// retain available identity evidence and disable that recovery. Confirmed
// insertion belongs to the first grouped caller. On a partial-state error, every
// caller in an unresolved group receives the error. A nil acknowledgement requires
// evidence of an inserted or existing row, including for duplicate waiters.
func (r *Store) flushCreateBatch(db *sql.DB, batch []usageLogCreateRequest) {
	if len(batch) == 0 {
		return
	}

	uniqueOrder := make([]string, 0, len(batch))
	preparedByKey := make(map[string]usageLogInsertPrepared, len(batch))
	requestsByKey := make(map[string][]usageLogCreateRequest, len(batch))
	fallback := make([]usageLogCreateRequest, 0)

	for _, req := range batch {
		if req.log == nil {
			completeUsageLogCreateRequest(req, usageLogCreateResult{inserted: false, err: nil})
			continue
		}
		// The worker claims processing before SQL grouping. A caller's winning queued
		// cancellation is acknowledged separately and never enters the prepared batch.
		if req.shared != nil && !req.shared.state.CompareAndSwap(usageLogCreateStateQueued, usageLogCreateStateProcessing) {
			if req.shared.state.Load() == usageLogCreateStateCanceled {
				completeUsageLogCreateRequest(req, usageLogCreateResult{
					inserted: false,
					err:      usage.MarkUsageLogCreateNotPersisted(context.Canceled),
				})
				continue
			}
		}
		prepared := req.prepared
		if prepared.requestID == "" {
			fallback = append(fallback, req)
			continue
		}
		key := usageLogBatchKey(prepared.requestID, req.log.APIKeyID)
		if _, exists := requestsByKey[key]; !exists {
			uniqueOrder = append(uniqueOrder, key)
			preparedByKey[key] = prepared
		}
		requestsByKey[key] = append(requestsByKey[key], req)
	}

	if len(uniqueOrder) > 0 {
		insertedMap, stateMap, safeFallback, err := r.batchInsertUsageLogs(db, uniqueOrder, preparedByKey)
		if err != nil {
			// SQL/scan failures permit individual conflict-safe writes. Decoded-result
			// failures instead retain whatever identity evidence the batch returned.
			if safeFallback {
				for _, key := range uniqueOrder {
					fallback = append(fallback, requestsByKey[key]...)
				}
			} else {
				for _, key := range uniqueOrder {
					reqs := requestsByKey[key]
					state, hasState := stateMap[key]
					inserted := insertedMap[key]
					for idx, req := range reqs {
						req.log.RateMultiplier = preparedByKey[key].rateMultiplier
						if hasState {
							req.log.ID = state.ID
							req.log.CreatedAt = state.CreatedAt
						}
						// Partial resolution confirms only rows whose identities were returned.
						// Every unresolved duplicate receives the uncertainty so none can claim persistence.
						switch {
						case inserted && idx == 0:
							completeUsageLogCreateRequest(req, usageLogCreateResult{inserted: true, err: nil})
						case inserted:
							completeUsageLogCreateRequest(req, usageLogCreateResult{inserted: false, err: nil})
						case hasState:
							completeUsageLogCreateRequest(req, usageLogCreateResult{inserted: false, err: nil})
						default:
							completeUsageLogCreateRequest(req, usageLogCreateResult{inserted: false, err: err})
						}
					}
				}
			}
		} else {
			for _, key := range uniqueOrder {
				reqs := requestsByKey[key]
				state, ok := stateMap[key]
				if !ok {
					for _, req := range reqs {
						completeUsageLogCreateRequest(req, usageLogCreateResult{
							inserted: false,
							err:      fmt.Errorf("usage log batch state missing for key=%s", key),
						})
					}
					continue
				}
				for idx, req := range reqs {
					req.log.ID = state.ID
					req.log.CreatedAt = state.CreatedAt
					req.log.RateMultiplier = preparedByKey[key].rateMultiplier
					completeUsageLogCreateRequest(req, usageLogCreateResult{
						inserted: idx == 0 && insertedMap[key],
						err:      nil,
					})
				}
			}
		}
	}

	if len(fallback) == 0 {
		return
	}

	for _, req := range fallback {
		// Accepted requests have left caller admission. Give each individual recovery
		// write a detached budget before acknowledging its result to the original
		// waiter.
		fallbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		inserted, err := r.createSingle(fallbackCtx, db, req.log)
		cancel()
		completeUsageLogCreateRequest(req, usageLogCreateResult{inserted: inserted, err: err})
	}
}

// flushBestEffortBatch groups prepared acknowledgements by request/key and
// executes one insert for each unique payload. Empty request IDs receive
// separate synthetic group keys, keeping unrelated unidentified rows from
// merging.
//
// The SQL batch uses a detached ten-second deadline and infra's bounded deadlock
// retry. A remaining error triggers one direct insert per group within that same
// deadline, and each member receives its group's final error. Successful SQL
// execution records the request/key in the recent cache even when conflict
// handling inserted no row. Input row identities are not updated, and
// acknowledgement sends never block an abandoned caller.
func (r *Store) flushBestEffortBatch(db *sql.DB, batch []usageLogBestEffortRequest) {
	if len(batch) == 0 {
		return
	}

	// bestEffortGroup shares the first captured payload among request/key
	// duplicates. prepared, apiKeyID, and key identify one SQL input; reqs collects
	// all callers acknowledged from that input's final execution result.
	type bestEffortGroup struct {
		prepared usageLogInsertPrepared      // first grouped write payload
		apiKeyID int64                       // grouped key record identity
		key      string                      // request key or synthetic identity
		reqs     []usageLogBestEffortRequest // waiters sharing the acknowledgement
	}

	groupsByKey := make(map[string]*bestEffortGroup, len(batch))
	groupOrder := make([]*bestEffortGroup, 0, len(batch))
	preparedList := make([]usageLogInsertPrepared, 0, len(batch))

	for idx, req := range batch {
		prepared := req.prepared
		// Missing request IDs cannot identify duplicates; give each such row its own
		// batch key before grouping named requests by their request and key identity.
		key := fmt.Sprintf("__best_effort_%d", idx)
		if prepared.requestID != "" {
			key = usageLogBatchKey(prepared.requestID, req.apiKeyID)
		}
		group, exists := groupsByKey[key]
		if !exists {
			group = &bestEffortGroup{
				prepared: prepared,
				apiKeyID: req.apiKeyID,
				key:      key,
			}
			groupsByKey[key] = group
			groupOrder = append(groupOrder, group)
			preparedList = append(preparedList, prepared)
		}
		group.reqs = append(group.reqs, req)
	}

	if len(preparedList) == 0 {
		for _, req := range batch {
			sendUsageLogBestEffortResult(req.resultCh, nil)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	query, args := buildUsageLogBestEffortInsertQuery(preparedList)
	startedAt := time.Now()
	// Reissue the entire conflict-tolerant batch after a deadlock before trying
	// individual records, using the same background budget for all permitted
	// retries.
	_, err := retryPostgresDeadlock(ctx, "usage_log_best_effort_batch_insert", len(preparedList), func() (sql.Result, error) {
		return db.ExecContext(ctx, query, args...)
	})
	if err != nil {
		sqlState := postgresSQLState(err)
		if sqlState == "" {
			sqlState = "unknown"
		}
		attempts := 1
		if isPostgresDeadlock(err) {
			attempts = postgresDeadlockMaxAttempts
		}
		logger.LegacyPrintf(
			"repository.usage_log",
			"operation=usage_log_best_effort_batch_insert sqlstate=%s attempt=%d batch_size=%d retry_succeeded=false fallback_failed=false elapsed_ms=%d error=%v",
			sqlState,
			attempts,
			len(preparedList),
			time.Since(startedAt).Milliseconds(),
			err,
		)
		// A failed batch retries each grouped payload in the remaining shared
		// deadline; every original waiter receives its group's individual execution
		// result.
		fallbackFailures := 0
		for _, group := range groupOrder {
			singleErr := execUsageLogInsertNoResult(ctx, db, group.prepared)
			if singleErr != nil {
				fallbackFailures++
				fallbackSQLState := postgresSQLState(singleErr)
				if fallbackSQLState == "" {
					fallbackSQLState = "unknown"
				}
				logger.LegacyPrintf(
					"repository.usage_log",
					"operation=usage_log_best_effort_single_fallback sqlstate=%s attempt=1 batch_size=1 retry_succeeded=false fallback_failed=true elapsed_ms=%d error=%v",
					fallbackSQLState,
					time.Since(startedAt).Milliseconds(),
					singleErr,
				)
			} else if group.prepared.requestID != "" && r != nil && r.bestEffortRecent != nil {
				// Successful individual execution suppresses repeated named
				// submissions for the cache interval; conflict success also
				// counts as an acknowledgement.
				r.bestEffortRecent.SetDefault(group.key, struct{}{})
			}
			for _, req := range group.reqs {
				sendUsageLogBestEffortResult(req.resultCh, singleErr)
			}
		}
		logger.LegacyPrintf(
			"repository.usage_log",
			"operation=usage_log_best_effort_single_fallback sqlstate=%s attempt=1 batch_size=%d retry_succeeded=false fallback_failed=%t elapsed_ms=%d fallback_failures=%d",
			sqlState,
			len(groupOrder),
			fallbackFailures > 0,
			time.Since(startedAt).Milliseconds(),
			fallbackFailures,
		)
		return
	}
	for _, group := range groupOrder {
		if group.prepared.requestID != "" && r != nil && r.bestEffortRecent != nil {
			// The full batch executed successfully. Publish its named grouping
			// key before acknowledgement so a following submission can use
			// recent-write suppression.
			r.bestEffortRecent.SetDefault(group.key, struct{}{})
		}
		for _, req := range group.reqs {
			sendUsageLogBestEffortResult(req.resultCh, nil)
		}
	}
}

// sendUsageLogBestEffortResult attempts one nonblocking acknowledgement to a
// supplied channel. Nil or unavailable channels discard delivery, allowing a
// worker to complete even after its caller stopped receiving the result.
// Callers leave ch open until delivery has finished; a closed channel panics.
func sendUsageLogBestEffortResult(ch chan error, err error) {
	if ch == nil {
		return
	}
	// A caller can abandon result waiting; attempt delivery without blocking worker
	// drainage when the channel is absent, full, or has no ready receiver.
	select {
	case ch <- err:
	default:
	}
}

// completeUsageLogCreateRequest publishes completed state and then attempts to
// deliver the supplied result. Callers finish row updates before invoking it,
// and an absent shared state is accepted by direct flush fixtures.
//
// The state store precedes channel delivery so a concurrent cancellation cannot
// claim the request as still queued after completion reaches this step.
// createBatched then uses its bounded grace wait when cancellation wins result
// selection. Delivery remains nonblocking for abandoned waiters; callers leave
// a supplied result channel open until worker delivery has finished.
func completeUsageLogCreateRequest(req usageLogCreateRequest, res usageLogCreateResult) {
	// Publish completed state before sending the result, so a concurrent
	// cancellation cannot later claim this request as a still-queued unwritten
	// submission.
	if req.shared != nil {
		req.shared.state.Store(usageLogCreateStateCompleted)
	}
	sendUsageLogCreateResult(req.resultCh, res)
}

// batchInsertUsageLogs returns insertion flags and stored row identities for the
// requested group keys. Empty keys return allocated empty maps, and database
// work runs under a detached ten-second context.
//
// SQL or payload-scan failure returns recovery permission to the caller. Once
// payload bytes are obtained, JSON decoding or state-count failure denies that
// permission because the SQL may already have written records. Decoded rows are
// keyed by request/APIKey identity; a count mismatch returns available maps with
// an error, leaving the flush layer to decide each caller's observable result.
// Missing row identities are excluded from the confirmed map, so a concurrent
// conflict unresolved in the SQL snapshot cannot be acknowledged as persistence.
func (r *Store) batchInsertUsageLogs(db *sql.DB, keys []string, preparedByKey map[string]usageLogInsertPrepared) (map[string]bool, map[string]usageLogBatchState, bool, error) {
	if len(keys) == 0 {
		return map[string]bool{}, map[string]usageLogBatchState{}, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	query, args := buildUsageLogBatchInsertQuery(keys, preparedByKey)
	var payload []byte
	if err := db.QueryRowContext(ctx, query, args...).Scan(&payload); err != nil {
		return nil, nil, true, err
	}
	var rows []usageLogBatchRow
	// Payload bytes follow executed SQL. A decoding error cannot establish that no
	// write happened, so return it with individual recovery permission disabled.
	if err := json.Unmarshal(payload, &rows); err != nil {
		return nil, nil, false, err
	}
	insertedMap := make(map[string]bool, len(keys))
	stateMap := make(map[string]usageLogBatchState, len(keys))
	for _, row := range rows {
		if row.ID <= 0 || row.CreatedAt.IsZero() {
			continue
		}
		key := usageLogBatchKey(row.RequestID, row.APIKeyID)
		insertedMap[key] = row.Inserted
		stateMap[key] = usageLogBatchState{
			ID:        row.ID,
			CreatedAt: row.CreatedAt,
		}
	}
	// Preserve decoded identity evidence on a partial or expanded result set; the
	// flush layer resolves each caller while avoiding another full SQL write.
	if len(stateMap) != len(keys) {
		return insertedMap, stateMap, false, fmt.Errorf("usage log batch state count mismatch: got=%d want=%d", len(stateMap), len(keys))
	}
	return insertedMap, stateMap, false, nil
}

// buildUsageLogBatchInsertQuery builds typed SQL input and ordered bound
// parameters for result-aware writes. Callers supply nonempty keys and a
// prepared payload for every key, with positional values matching the shared
// insert schema.
//
// A synthetic input index carries caller order through the SQL result. INSERT
// ignores conflicts, while the resolved CTE matches inserted or existing rows by
// request identity or financial key plus API-key ID. JSON transports the
// resulting identity and insertion evidence back to batchInsertUsageLogs.
// Maintaining these relationships together prevents a reordered column from
// attaching another record's state to a waiter.
func buildUsageLogBatchInsertQuery(keys []string, preparedByKey map[string]usageLogInsertPrepared) (string, []any) {
	// Builder writes append to memory and return nil errors. The following sequence
	// emits SQL fragments while bound arguments remain ordered in their separate
	// slice.
	var query strings.Builder
	_, _ = query.WriteString(`
		WITH input (
			input_idx,` + usageLogInsert.columns + `) AS (VALUES `)

	args := make([]any, 0, len(keys)*(len(usageLogInsert.argTypes)+1))
	argPos := 1
	for idx, key := range keys {
		if idx > 0 {
			_, _ = query.WriteString(",")
		}
		_, _ = query.WriteString("(")
		_, _ = query.WriteString("$")
		_, _ = query.WriteString(strconv.Itoa(argPos))
		args = append(args, idx)
		argPos++
		prepared := preparedByKey[key]
		for i := range len(prepared.args) {
			_, _ = query.WriteString(",")
			_, _ = query.WriteString("$")
			_, _ = query.WriteString(strconv.Itoa(argPos))
			if i < len(usageLogInsert.casts) {
				_, _ = query.WriteString(usageLogInsert.casts[i])
			}
			argPos++
		}
		_, _ = query.WriteString(")")
		args = append(args, prepared.args...)
	}
	_, _ = query.WriteString(`
		),
		inserted AS (
			INSERT INTO usage_logs (` + usageLogInsert.columns + `)
			SELECT` + usageLogInsert.columns + `FROM input
			ON CONFLICT DO NOTHING
			RETURNING request_id, billing_key, api_key_id, id, created_at
		),
		resolved AS (
			SELECT
				input.input_idx,
				input.request_id,
				input.api_key_id,
				COALESCE(inserted.id, existing.id) AS id,
				COALESCE(inserted.created_at, existing.created_at) AS created_at,
				(inserted.request_id = input.request_id) AS inserted
			FROM input
			LEFT JOIN inserted
				ON (inserted.request_id = input.request_id OR COALESCE(inserted.billing_key, inserted.request_id) = COALESCE(input.billing_key, input.request_id))
				AND inserted.api_key_id = input.api_key_id
			LEFT JOIN usage_logs existing
				ON (existing.request_id = input.request_id OR COALESCE(existing.billing_key, existing.request_id) = COALESCE(input.billing_key, input.request_id))
				AND existing.api_key_id = input.api_key_id
		)
		SELECT COALESCE(
			json_agg(
				json_build_object(
					'request_id', resolved.request_id,
					'api_key_id', resolved.api_key_id,
					'id', resolved.id,
					'created_at', resolved.created_at,
					'inserted', resolved.inserted
				)
				ORDER BY resolved.input_idx
			),
			'[]'::json
		)
		FROM resolved
	`)
	return query.String(), args
}

// buildUsageLogBestEffortInsertQuery builds typed multirow INSERT SQL and bound
// arguments in prepared-list order. Callers supply a nonempty list whose
// payloads match the shared insert schema; conflicts are ignored and no row
// identity is returned.
func buildUsageLogBestEffortInsertQuery(preparedList []usageLogInsertPrepared) (string, []any) {
	// Builder writes append to memory and return nil errors. The following sequence
	// emits SQL fragments while bound arguments remain ordered in their separate
	// slice.
	var query strings.Builder
	_, _ = query.WriteString(`
		WITH input (` + usageLogInsert.columns + `) AS (VALUES `)

	args := make([]any, 0, len(preparedList)*len(usageLogInsert.argTypes))
	argPos := 1
	for idx, prepared := range preparedList {
		if idx > 0 {
			_, _ = query.WriteString(",")
		}
		_, _ = query.WriteString("(")
		for i := range len(prepared.args) {
			if i > 0 {
				_, _ = query.WriteString(",")
			}
			_, _ = query.WriteString("$")
			_, _ = query.WriteString(strconv.Itoa(argPos))
			if i < len(usageLogInsert.casts) {
				_, _ = query.WriteString(usageLogInsert.casts[i])
			}
			argPos++
		}
		_, _ = query.WriteString(")")
		args = append(args, prepared.args...)
	}

	_, _ = query.WriteString(`
		)
		INSERT INTO usage_logs (` + usageLogInsert.columns + `)
		SELECT` + usageLogInsert.columns + `FROM input
		ON CONFLICT DO NOTHING
	`)

	return query.String(), args
}

// execUsageLogInsertNoResult executes the shared insert SQL for a prepared
// payload. Conflict handling follows the statement's DO NOTHING rule, so a nil
// error acknowledges execution and does not distinguish a new row from an
// existing row.
func execUsageLogInsertNoResult(ctx context.Context, sqlq sqlExecutor, prepared usageLogInsertPrepared) error {
	_, err := sqlq.ExecContext(ctx, usageLogInsert.statement, prepared.args...)
	return err
}

// prepareUsageLogInsert builds the positional values consumed by every insert
// shape. It trims RequestID and synchronizes request type with legacy flags on
// the supplied row, while zero CreatedAt selects the current system time for the
// prepared payload.
//
// Missing payer identity uses UserID, and missing requested display model uses
// the trimmed model value. Allocation JSON uses an empty array for absent or
// unencodable allocations, while optional image metadata can become SQL NULL.
// Most pointer values are dereferenced into nullable scalars, but provider
// multiplier, provider cost, and response mismatch remain pointers in args.
// Callers keep them stable until the SQL consumer finishes.
func prepareUsageLogInsert(log *usage.UsageLog) usageLogInsertPrepared {
	createdAt := log.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	requestID := strings.TrimSpace(log.RequestID)
	log.RequestID = requestID

	rateMultiplier := log.RateMultiplier
	// Resolve unknown categories from legacy flags before encoding all three fields;
	// explicit transport categories replace inconsistent flags, while special
	// categories retain recorded transport evidence.
	log.SyncRequestTypeAndLegacyFields()
	requestType := int16(log.RequestType)

	groupID := nullInt64(log.GroupID)
	subscriptionID := nullInt64(log.SubscriptionID)
	duration := nullInt(log.DurationMs)
	var timingJSON any
	if log.Timings != nil {
		// The snapshot contains only finite integer observations and a mode string,
		// so encoding cannot fail or retain mutable request-owned collections.
		encoded, _ := json.Marshal(log.Timings)
		timingJSON = string(encoded)
	}
	userAgent := nullString(log.UserAgent)
	ipAddress := nullString(log.IPAddress)
	imageSize := nullString(log.ImageSize)
	imageInputSize := nullString(log.ImageInputSize)
	imageOutputSize := nullString(log.ImageOutputSize)
	imageSizeSource := nullString(log.ImageSizeSource)
	imageSizeBreakdown := nullStringIntMapJSON(log.ImageSizeBreakdown)
	videoResolution := nullString(log.VideoResolution)
	videoDurationSeconds := nullInt(log.VideoDurationSeconds)
	serviceTier := nullString(log.ServiceTier)
	reasoningEffort := nullString(log.ReasoningEffort)
	requestedReasoningEffort := nullString(log.RequestedReasoningEffort)
	inboundEndpoint := nullString(log.InboundEndpoint)
	upstreamEndpoint := nullString(log.UpstreamEndpoint)
	pricingConfigID := nullInt64(log.PricingConfigID)
	modelMappingChain := nullString(log.ModelMappingChain)
	billingTier := nullString(log.BillingTier)
	billingMode := nullString(log.BillingMode)
	upstreamRequestID := nullString(log.UpstreamRequestID)
	sessionID := nullString(log.SessionID)
	requestedModel := strings.TrimSpace(log.RequestedModel)
	// Older completion callers can omit requested display text. Use their supplied
	// model as display evidence while storing the model field as provided.
	if requestedModel == "" {
		requestedModel = strings.TrimSpace(log.Model)
	}
	upstreamModel := nullString(log.UpstreamModel)
	billingAllocations := log.BillingAllocations
	if len(billingAllocations) == 0 {
		billingAllocations = nil
	}
	billingAllocationsJSON, err := json.Marshal(billingAllocations)
	if err != nil {
		// Failed allocation encoding omits optional detail as an empty array. The
		// record's separate monetary fields still populate the positional insert
		// arguments.
		billingAllocationsJSON = []byte("[]")
	}
	// JSON null represents absent allocations, but the persisted allocation field
	// uses an array so downstream decoding can treat absence as an empty
	// collection.
	if len(billingAllocationsJSON) == 0 || string(billingAllocationsJSON) == "null" {
		billingAllocationsJSON = []byte("[]")
	}

	var requestIDArg any
	if requestID != "" {
		requestIDArg = requestID
	}
	systemJSON, _ := json.Marshal(log.SystemUsage)
	var systemArg any
	if log.SystemUsage != nil {
		systemArg = systemJSON
	}
	var userIDArg, keyIDArg, payerIDArg any
	userIDArg, keyIDArg = log.UserID, log.APIKeyID
	billingUserID := log.BillingUserID
	// Missing payer identity attributes historical personal records to their actor;
	// a supplied positive billing identity remains available for team-owner ranking.
	if billingUserID <= 0 {
		billingUserID = log.UserID
	}

	payerIDArg = billingUserID
	if log.EffectiveRequestSource() != usage.SourceUser {
		userIDArg, keyIDArg, payerIDArg = nil, nil, nil
	}
	return usageLogInsertPrepared{
		createdAt:      createdAt,
		requestID:      requestID,
		rateMultiplier: rateMultiplier,
		requestType:    requestType,
		args: []any{
			userIDArg,
			payerIDArg,
			nullInt64(log.TeamID),
			keyIDArg,
			log.ProviderID,
			requestIDArg,
			log.Model,
			nullString(&requestedModel),
			upstreamModel,
			groupID,
			subscriptionID,
			log.InputTokens,
			log.OutputTokens,
			log.CacheCreationTokens,
			log.CacheReadTokens,
			log.CacheCreation5mTokens,
			log.CacheCreation1hTokens,
			log.ImageOutputTokens,
			log.ImageOutputCost,
			log.ImageInputTokens,
			log.ImageInputCost,
			log.InputCost,
			log.OutputCost,
			log.CacheCreationCost,
			log.CacheReadCost,
			log.TotalCost,
			log.ActualCost,
			log.SubscriptionAmountUSD,
			log.BalanceAmountUSD,
			billingAllocationsJSON,
			rateMultiplier,
			log.ProviderRateMultiplier,
			log.BillingType,
			requestType,
			log.Stream,
			log.OpenAIWSMode,
			duration,
			timingJSON,
			userAgent,
			ipAddress,
			log.ImageCount,
			log.ImageFrameCount,
			log.ImageCacheReadTokens,
			imageSize,
			imageInputSize,
			imageOutputSize,
			imageSizeSource,
			imageSizeBreakdown,
			log.VideoCount,
			videoResolution,
			videoDurationSeconds,
			serviceTier,
			reasoningEffort,
			inboundEndpoint,
			upstreamEndpoint,
			log.CacheTTLOverridden,
			log.LongContextBillingApplied,
			pricingConfigID,
			modelMappingChain,
			billingTier,
			billingMode,
			log.ProviderStatsCost, // provider_stats_cost
			upstreamRequestID,     // upstream_request_id
			sessionID,             // session_id
			createdAt,
			requestedReasoningEffort,
			log.NativeCompactionV2,
			usagePlatformSnapshot(log.Platform),
			nullString(log.UpstreamResponseModel),
			log.UpstreamModelMismatch,
			nullStringValue(log.BillingKey),
			log.EffectiveRequestSource(),
			systemArg,
		},
	}
}

// usageLogBatchKey joins request identity with decimal key identity for
// grouping.
func usageLogBatchKey(requestID string, apiKeyID int64) string {
	return requestID + "\x1f" + strconv.FormatInt(apiKeyID, 10)
}

// sendUsageLogCreateResult attempts a single nonblocking result delivery to ch.
// Nil or unavailable channels discard that notification, letting the batch
// worker finish when its caller has already abandoned result waiting.
// Callers leave ch open until delivery has finished; a closed channel panics.
func sendUsageLogCreateResult(ch chan usageLogCreateResult, res usageLogCreateResult) {
	if ch == nil {
		return
	}
	// A caller can abandon result waiting; attempt delivery without blocking worker
	// drainage when the channel is absent, full, or has no ready receiver.
	select {
	case ch <- res:
	default:
	}
}

// bestEffortRecentKey returns the trimmed request/key grouping identity when the
// recent cache is available. Empty request IDs, nil stores, or missing caches
// return false so the caller skips cache suppression.
func (r *Store) bestEffortRecentKey(requestID string, apiKeyID int64) (string, bool) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || r == nil || r.bestEffortRecent == nil {
		return "", false
	}
	return usageLogBatchKey(requestID, apiKeyID), true
}

// usagePlatformSnapshot returns the trimmed recorded execution-platform value
// for persistent analytics. Empty input becomes unknown, keeping records with no
// execution-platform evidence in a defined reporting bucket.
func usagePlatformSnapshot(platform string) string {
	if value := strings.TrimSpace(platform); value != "" {
		return value
	}
	return "unknown"
}

// nullStringValue returns trimmed nonblank text for optional financial-key
// persistence. Blank text becomes SQL NULL so the expression-index identity can
// use the historical request_id when billing_key is absent.
func nullStringValue(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}
