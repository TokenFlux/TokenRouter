package moderation

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// queue.go coordinates asynchronous observe and deferred-record queueing,
// bounded byte reservations, worker pools, periodic log cleanup, and shutdown
// draining for ContentModerationService. Enqueue paths hold runtimeMu read
// locks across nonblocking channel sends and reserve estimated task bytes
// atomically so concurrent producers cannot write to a closed queue or exceed
// memory budgets.
//
// Workers recheck current policy for observe tasks while retaining saved policy
// snapshots on deferred record tasks. Both Stop and StopContext seal queue
// admission synchronously under runtimeMu before waiting on one shared
// worker-drain completion channel, ensuring even expired caller contexts close
// admission deterministically and report any remaining undrained tasks.

// ContentModerationCleanupResult reports successful retention-delete counts and
// the operation completion timestamp.
type ContentModerationCleanupResult struct {
	// DeletedHit, DeletedNonHit, and DeletedCyber come from separate delete
	// commands whose RowsAffected errors are suppressed. Earlier deletes remain
	// applied if a later command fails and no result is returned.
	// reported deleted flagged log count
	DeletedHit int64 `json:"deleted_hit"`
	// reported deleted unflagged log count
	DeletedNonHit int64 `json:"deleted_non_hit"`
	// reported deleted warning count
	DeletedCyber int64 `json:"deleted_cyber"`
	// successful operation completion time
	FinishedAt time.Time `json:"finished_at"`
}

// ContentModerationRuntimeStatus combines configured policy with independently
// sampled queue, audit, credential, and cleanup observations.
type ContentModerationRuntimeStatus struct {
	Enabled bool `json:"enabled"` // ordinary audit enablement switch
	// separately read risk center switch
	RiskControlEnabled bool   `json:"risk_control_enabled"`
	Mode               string `json:"mode"` // ordinary audit policy mode
	// configured admitted worker count
	WorkerCount int `json:"worker_count"`
	MaxWorkers  int `json:"max_workers"` // fixed maximum worker population
	// ActiveWorkers and IdleWorkers use current WorkerCount and sampled active
	// tasks. GetStatus clamps activity to configured count, so reducing worker
	// configuration can hide larger work still finishing.
	// sampled clamped active worker count
	ActiveWorkers int `json:"active_workers"`
	// configured count minus activity
	IdleWorkers int `json:"idle_workers"`
	// configured queue admission limit
	QueueSize   int `json:"queue_size"`
	QueueLength int `json:"queue_length"` // sampled buffered task count
	// QueueUsagePercent divides sampled queue length by configured QueueSize.
	// Queue length checks are concurrent samples, and the configured limit can
	// also shrink after tasks were queued, allowing values above one hundred.
	// percent
	QueueUsagePercent float64 `json:"queue_usage_percent"`
	// cumulative accepted queue sends
	Enqueued int64 `json:"enqueued"`
	Dropped  int64 `json:"dropped"` // cumulative capacity rejection count
	// counted record or checkSync completions
	Processed int64 `json:"processed"`
	Errors    int64 `json:"errors"` // asynchronous all-unit failure count
	// sampled active synchronous audits
	PreBlockActive int `json:"pre_block_active"`
	// counted synchronous decision outcomes
	PreBlockChecked int64 `json:"pre_block_checked"`
	// counted synchronous allowed outcomes
	PreBlockAllowed int64 `json:"pre_block_allowed"`
	// counted synchronous blocked outcomes
	PreBlockBlocked int64 `json:"pre_block_blocked"`
	// counted all-unit audit failures
	PreBlockErrors int64 `json:"pre_block_errors"`
	// milliseconds
	PreBlockAvgLatencyMS int64 `json:"pre_block_avg_latency_ms"`
	// sampled synchronous attempt activity
	PreBlockAPIKeyActive int64 `json:"pre_block_api_key_active"`
	// sampled unfrozen configured credentials
	PreBlockAPIKeyAvailableCount int64 `json:"pre_block_api_key_available_count"`
	// sampled completed synchronous attempts
	PreBlockAPIKeyTotalCalls int64 `json:"pre_block_api_key_total_calls"`
	// per-key sampled synchronous counters
	PreBlockAPIKeyLoads []ContentModerationAPIKeyLoad `json:"pre_block_api_key_loads"`
	// per-key sampled credential health
	APIKeyStatuses []ContentModerationAPIKeyStatus `json:"api_key_statuses"`
	// FlaggedHashCount is zero when no hash adapter is bound or its count read
	// fails. A failure is logged, so a displayed zero is not proof that an
	// available Redis set was empty.
	// observed Redis flagged-member count
	FlaggedHashCount int64 `json:"flagged_hash_count"`
	// last successful cleanup timestamp
	LastCleanupAt *time.Time `json:"last_cleanup_at,omitempty"`
	// last reported deleted flagged count
	LastCleanupDeletedHit int64 `json:"last_cleanup_deleted_hit"`
	// last reported deleted unflagged count
	LastCleanupDeletedNonHit int64 `json:"last_cleanup_deleted_non_hit"`
}

// contentModerationTask carries deferred audit or record work with its
// estimated byte reservation and enqueue time.
type contentModerationTask struct {
	// input and content retain identity pointers and selected content collections
	// after Body is dropped. The producer keeps those shared values stable through
	// deferred execution.
	input     ContentModerationCheckInput // attributed request metadata
	content   ContentModerationInput      // queued observe input values
	inputHash string                      // full extracted input identity
	// log and config distinguish record work from observe work. Records retain a
	// mutable log pointer and cloned policy; observe tasks reload policy and
	// rebuild aggregate text when a worker executes.
	log              *ContentModerationLog    // shared record awaiting persistence
	config           *ContentModerationConfig // saved record policy copy
	recordHash       bool                     // flagged-set recording choice
	applySideEffects bool                     // ordinary action delivery choice
	// bufferedBytes is the successful admission reservation released by either
	// rejected sending or task completion. Its estimate limits chosen retained
	// costs rather than measuring all Go heap allocations.
	bufferedBytes int64     // bytes
	enqueuedAt    time.Time // queue send preparation time
}

// enqueueAsync admits an observe task and reports whether the queue accepted
// it. It strips raw Body and compacts recoverable aggregate text, but retains
// the supplied content collections and identity pointers. Callers keep those
// references stable until the worker finishes; execution reloads current
// configuration rather than storing cfg in this task.
//
// Admission holds runtimeMu against queue closure, samples configured queue
// length, and reserves an estimated byte cost atomically before sending.
// Concurrent readers can exceed the configured length sample, while channel
// capacity and byte CAS remain enforced. A rejected send returns its byte
// reservation and increments dropped; stopped or unavailable queues simply
// reject.
func (s *ContentModerationService) enqueueAsync(input ContentModerationCheckInput, cfg *ContentModerationConfig, content ContentModerationInput, hashText string) bool {
	if s == nil || s.asyncQueue == nil {
		return false
	}
	input.Body = nil
	task := contentModerationTask{
		input:      input,
		content:    compactContentModerationInputForQueue(content),
		inputHash:  hashText,
		enqueuedAt: time.Now(),
	}
	return s.enqueueTask(task, cfg, "async", "")
}

// enqueueRecord admits an already decided log for deferred persistence and
// reports queue acceptance. It strips Body and clones cfg, while log remains a
// shared mutable pointer that the caller stops changing after successful
// admission. The worker uses that saved policy even if administrator
// configuration changes before execution.
//
// The lifecycle read lock prevents closure during admission, and an estimated
// byte reservation precedes the nonblocking send. Concurrent senders use
// sampled configured length, with channel capacity as the hard queue cap.
// Rejection after reservation compensates the byte count; the decision caller
// then persists the same log synchronously.
func (s *ContentModerationService) enqueueRecord(input ContentModerationCheckInput, cfg *ContentModerationConfig, log *ContentModerationLog, inputHash string, recordHash bool, applySideEffects bool) bool {
	if s == nil || s.asyncQueue == nil || log == nil {
		return false
	}
	input.Body = nil
	task := contentModerationTask{
		input:            input,
		inputHash:        inputHash,
		log:              log,
		config:           cloneContentModerationConfig(cfg),
		recordHash:       recordHash,
		applySideEffects: applySideEffects,
		enqueuedAt:       time.Now(),
	}
	return s.enqueueTask(task, cfg, "record", log.Action)
}

// enqueueTask coordinates shared queue admission, configured queue-size checks,
// atomic byte-budget reservations, and rollback on nonblocking channel send
// failure for observe and record tasks.
//
// Holding runtimeMu.RLock across the channel send prevents sealAdmission from
// closing asyncQueue mid-send. When a concurrent sender fills the channel after
// the queue-size check, enqueueTask returns its reserved bytes and increments
// asyncDropped before reporting false.
func (s *ContentModerationService) enqueueTask(task contentModerationTask, cfg *ContentModerationConfig, kind, action string) bool {
	// Hold runtimeMu.RLock across the nonblocking send so sealAdmission cannot
	// close asyncQueue while a sender is admitting a task.
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if s.runtimeStopped {
		return false
	}

	queueSize := defaultContentModerationQueueSize
	if cfg != nil && cfg.QueueSize > 0 {
		queueSize = cfg.QueueSize
	}
	if len(s.asyncQueue) >= queueSize {
		warnModerationQueueDrop("content_moderation."+kind+"_queue_full", task.input, action, "queue_size", queueSize)
		s.asyncDropped.Add(1)
		return false
	}
	task.bufferedBytes = estimateContentModerationTaskBytes(task)
	if !s.reserveContentModerationBufferedBytes(task.bufferedBytes) {
		warnModerationQueueDrop(
			"content_moderation."+kind+"_queue_bytes_full",
			task.input,
			action,
			"task_bytes", task.bufferedBytes,
			"max_bytes", maxContentModerationBufferedBytes,
		)
		s.asyncDropped.Add(1)
		return false
	}
	select {
	case s.asyncQueue <- task:
		s.asyncEnqueued.Add(1)
		return true
	default:
		// Return reserved byte capacity when a concurrent sender filled the channel
		// between len(asyncQueue) sampling and select execution.
		s.releaseContentModerationBufferedBytes(task.bufferedBytes)
		warnModerationQueueDrop("content_moderation."+kind+"_queue_full", task.input, action)
		s.asyncDropped.Add(1)
		return false
	}
}

// warnModerationQueueDrop emits structured queue-capacity warnings with
// optional record action attribution.
func warnModerationQueueDrop(event string, input ContentModerationCheckInput, action string, extra ...any) {
	attrs := make([]any, 0, 6+len(extra))
	attrs = append(attrs, "user_id", input.UserID, "endpoint", input.Endpoint)
	if action != "" {
		attrs = append(attrs, "action", action)
	}
	attrs = append(attrs, extra...)
	slog.Warn(event, attrs...)
}

// worker drains tasks when its fixed ID is admitted by current WorkerCount.
// Each loop loads a snapshot before dequeuing; a read failure or reduced limit
// waits and can leave tasks behind at shutdown. Record tasks use saved policy
// when present; observe tasks recheck current ordinary enablement, mode, group,
// and model scope. Check settles the risk master gate and sampling before queue
// admission, and those decisions remain when an observe task executes.
//
// Reservations stay held through task execution and are released on return.
// Policy skips precede activity and processed counters; accepted
// record/checkSync paths increment processed even when independent persistence
// fails. Task panics are logged and cleanup defers run. Stop joins workers
// while their background contexts retain their own deadlines, and missing
// usable policy can make draining incomplete.
func (s *ContentModerationService) worker(id int) {
	for {
		select {
		case <-s.runtimeDone:
			if len(s.asyncQueue) == 0 {
				return
			}
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), maxContentModerationTimeoutMS*time.Millisecond+10*time.Second)
		runtimeSnapshot, err := s.loadRuntimeSnapshot(ctx)
		// This worker has no usable policy or is beyond the current admitted count;
		// wait before another load, and allow shutdown to report remaining queued tasks.
		if err != nil || runtimeSnapshot == nil || runtimeSnapshot.config == nil || id >= runtimeSnapshot.config.WorkerCount {
			cancel()
			select {
			case <-s.runtimeDone:
				return
			case <-time.After(time.Second):
			}
			continue
		}
		cfg := runtimeSnapshot.config
		task, ok := s.dequeueAsyncTask(ctx, time.Second)
		if !ok {
			cancel()
			continue
		}
		// Release task resources on every return. A recovered panic ends this task
		// and records diagnostics; independent side effects already
		// completed are not replayed here.
		func() {
			defer cancel()
			defer s.releaseContentModerationBufferedBytes(task.bufferedBytes)
			defer func() {
				if r := recover(); r != nil {
					slog.Error("content_moderation.worker_panic", "worker_id", id, "recover", r)
				}
			}()
			if task.log != nil {
				s.asyncActive.Add(1)
				defer s.asyncActive.Add(-1)
				// Queue delay is sampled at execution in truncated integer milliseconds.
				queueDelay := int(time.Since(task.enqueuedAt).Milliseconds())
				task.log.QueueDelayMS = &queueDelay
				taskCfg := task.config
				// A record admitted without saved policy uses the valid configuration
				// loaded by this worker loop, so its persistence can still evaluate
				// ordinary side effects.
				if taskCfg == nil {
					taskCfg = cfg
				}
				s.persistContentModerationLog(ctx, taskCfg, task.log, task.inputHash, task.recordHash, task.applySideEffects)
				s.asyncProcessed.Add(1)
				return
			}
			if !cfg.Enabled || cfg.Mode == ContentModerationModeOff {
				return
			}
			if !cfg.includesGroup(task.input.GroupID) {
				return
			}
			if !cfg.includesModel(task.input.Model) {
				return
			}
			task.content.Text = contentModerationTextFromItems(task.content.Items)
			s.asyncActive.Add(1)
			defer s.asyncActive.Add(-1)
			// Observe delay samples elapsed queue waiting in truncated integer milliseconds.
			queueDelay := int(time.Since(task.enqueuedAt).Milliseconds())
			_ = s.checkSync(ctx, task.input, cfg, task.content, task.inputHash, &queueDelay, false)
			s.asyncProcessed.Add(1)
		}()
	}
}

// estimateContentModerationTaskBytes approximates queue retention using
// selected strings, media bytes, and fixed structural allowances. Record tasks
// count their retained log material; audit tasks include item text twice for
// reconstruction, while other fields and standalone content.Text are not
// comprehensively measured.
func estimateContentModerationTaskBytes(task contentModerationTask) int64 {
	// Fixed structural allowances approximate selected retained costs,
	// rather than measuring
	// all object, slice-capacity, or shared-string heap storage for a queued task.
	size := int64(1024 + len(task.input.Endpoint) + len(task.input.Provider) + len(task.input.Model) + len(task.input.Body))
	if task.log != nil {
		size += int64(len(task.log.InputExcerpt) + len(task.log.Error) + len(task.log.MatchedKeyword))
		for _, item := range task.log.InputItems {
			size += int64(len(item.Text) + len(item.ImageRef) + 64)
		}
		for _, media := range task.log.Media {
			size += int64(len(media.OriginalRef) + len(media.Content) + 128)
		}
		return size
	}
	textBytes := int64(0)
	for _, item := range task.content.Items {
		textBytes += int64(len(item.Text))
		size += int64(len(item.Text) + len(item.ImageRef) + 64)
	}
	// Charge item text again for aggregate reconstruction during worker execution;
	// standalone content.Text and other omitted fields keep this a
	// selected-cost estimate.
	return size + textBytes
}

// reserveContentModerationBufferedBytes claims an estimated task cost under the
// shared byte cap. Nonpositive sizes require no reservation and return true;
// nil services or oversized positive claims return false.
//
// The current total and requested cost determine whether a claim fits. A lost
// CAS repeats the decision using the updated total, preventing competing
// senders from spending the same sampled remaining bytes. The sender releases a
// successful reservation if channel admission fails, and the worker releases it
// after admitted task completion.
func (s *ContentModerationService) reserveContentModerationBufferedBytes(size int64) bool {
	if s == nil || size <= 0 || size > maxContentModerationBufferedBytes {
		return size <= 0
	}
	for {
		// A competing sender can claim remaining bytes after this read; retry a
		// lost CAS against the updated total before accepting or
		// rejecting the reservation.
		current := s.asyncBufferedBytes.Load()
		if current > maxContentModerationBufferedBytes-size {
			return false
		}
		if s.asyncBufferedBytes.CompareAndSwap(current, current+size) {
			return true
		}
	}
}

// releaseContentModerationBufferedBytes subtracts a positive task reservation
// from the shared counter. Nil services and nonpositive sizes do nothing;
// callers release each successful reservation once, because this subtraction
// has no per-task idempotency guard.
func (s *ContentModerationService) releaseContentModerationBufferedBytes(size int64) {
	if s == nil || size <= 0 {
		return
	}
	s.asyncBufferedBytes.Add(-size)
}

// dequeueAsyncTask waits for a queue value, context cancellation, or idle
// timeout and reports whether a task was received. Nonpositive idleWait selects
// one second. When several cases are ready, select chooses among them; the
// caller owns byte release for a received task.
//
// A closed, empty queue returns no task; a buffered receive can still precede
// closure detection. The timer stops on return. Canceled or idle waits leave
// queued reservations intact, and this helper releases no costs for values it
// did not receive.
func (s *ContentModerationService) dequeueAsyncTask(ctx context.Context, idleWait time.Duration) (contentModerationTask, bool) {
	var zero contentModerationTask
	if s == nil || s.asyncQueue == nil {
		return zero, false
	}
	if idleWait <= 0 {
		idleWait = time.Second
	}
	timer := time.NewTimer(idleWait)
	defer timer.Stop()
	// A selected task reaches the worker with its byte reservation still held;
	// canceled or idle waits leave ownership of queued reservations with the
	// channel.
	select {
	case task, ok := <-s.asyncQueue:
		return task, ok
	case <-ctx.Done():
		return zero, false
	case <-timer.C:
		return zero, false
	}
}

// GetStatus returns administrator configuration and independently sampled
// runtime observations. A nil service returns empty, while configuration errors
// propagate. Worker activity is clamped to the configured limit, pre-block
// average uses observed completed checks, and queue usage divides sampled
// length by current configured size.
//
// The counters, key-health views, risk switch, and hash count are read at
// separate moments, so their combination is diagnostic rather than a consistent
// transaction snapshot. Hash-count failures log and report zero. Last-cleanup
// fields update separately and represent the latest successfully returned
// cleanup result.
func (s *ContentModerationService) GetStatus(ctx context.Context) (*ContentModerationRuntimeStatus, error) {
	if s == nil {
		return &ContentModerationRuntimeStatus{}, nil
	}
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	riskEnabled := s.isRiskControlEnabled(ctx)
	// Clamp sampled work for administrator occupancy under the current worker
	// limit.
	active := min(max(int(s.asyncActive.Load()), 0), cfg.WorkerCount)
	preBlockActive := max(int(s.preBlockActive.Load()), 0)
	preBlockChecked := s.preBlockChecked.Load()
	preBlockAvgLatency := int64(0)
	// Integer division reports sampled completed-check mean latency in whole
	// milliseconds.
	if preBlockChecked > 0 {
		preBlockAvgLatency = s.preBlockLatencyTotalMS.Load() / preBlockChecked
	}
	queueLength := 0
	if s.asyncQueue != nil {
		queueLength = len(s.asyncQueue)
	}
	queueUsage := 0.0
	// Current configured size determines the diagnostic percentage of queued
	// tasks.
	if cfg.QueueSize > 0 {
		queueUsage = float64(queueLength) * 100 / float64(cfg.QueueSize)
	}
	var flaggedHashCount int64
	if s.hashCache != nil {
		// Hash availability is a separate diagnostic read; its failure leaves the
		// displayed count zero and does not discard the other sampled
		// runtime observations.
		if n, err := s.hashCache.CountFlaggedInputHashes(ctx); err == nil {
			flaggedHashCount = n
		} else {
			slog.Warn("content_moderation.hash_count_failed", "error", err)
		}
	}
	var lastCleanupAt *time.Time
	if unix := s.lastCleanupUnix.Load(); unix > 0 {
		// Convert stored cleanup seconds to an administrator timestamp.
		t := time.Unix(unix, 0)
		lastCleanupAt = &t
	}
	return &ContentModerationRuntimeStatus{
		Enabled:                      cfg.Enabled,
		RiskControlEnabled:           riskEnabled,
		Mode:                         cfg.Mode,
		WorkerCount:                  cfg.WorkerCount,
		MaxWorkers:                   maxContentModerationWorkerCount,
		ActiveWorkers:                active,
		IdleWorkers:                  cfg.WorkerCount - active,
		QueueSize:                    cfg.QueueSize,
		QueueLength:                  queueLength,
		QueueUsagePercent:            queueUsage,
		Enqueued:                     s.asyncEnqueued.Load(),
		Dropped:                      s.asyncDropped.Load(),
		Processed:                    s.asyncProcessed.Load(),
		Errors:                       s.asyncErrors.Load(),
		PreBlockActive:               preBlockActive,
		PreBlockChecked:              preBlockChecked,
		PreBlockAllowed:              s.preBlockAllowed.Load(),
		PreBlockBlocked:              s.preBlockBlocked.Load(),
		PreBlockErrors:               s.preBlockErrors.Load(),
		PreBlockAvgLatencyMS:         preBlockAvgLatency,
		PreBlockAPIKeyActive:         s.preBlockAPIKeyActive(cfg),
		PreBlockAPIKeyAvailableCount: s.preBlockAPIKeyAvailableCount(cfg),
		PreBlockAPIKeyTotalCalls:     s.preBlockAPIKeyTotalCalls(cfg),
		PreBlockAPIKeyLoads:          s.preBlockAPIKeyLoads(cfg),
		APIKeyStatuses:               s.apiKeyStatuses(cfg),
		FlaggedHashCount:             flaggedHashCount,
		LastCleanupAt:                lastCleanupAt,
		LastCleanupDeletedHit:        s.lastCleanupDeletedHit.Load(),
		LastCleanupDeletedNonHit:     s.lastCleanupDeletedNonHit.Load(),
	}, nil
}

// cleanupWorker waits five minutes before its first retention cleanup, then
// repeats every day. runtimeDone stops future rounds; a cleanup already
// underway uses its own background deadline and remains joined by runtimeWG
// before Stop finishes.
//
// The timer resets after each completed round, so work duration extends the
// interval between round starts. When timer and stop are ready together,
// select can admit another round before the next loop observes shutdown.
func (s *ContentModerationService) cleanupWorker() {
	timer := time.NewTimer(contentModerationCleanupDelay)
	defer timer.Stop()
	// Shutdown competes with each timer before another round begins; an admitted
	// round keeps its separate background budget and finishes before this worker
	// can observe shutdown again.
	for {
		select {
		case <-s.runtimeDone:
			return
		case <-timer.C:
		}
		s.runCleanupOnce()
		timer.Reset(contentModerationCleanupInterval)
	}
}

// runCleanupOnce reads policy and requests retention deletion with a
// thirty-minute background context. Missing dependencies skip the round; read
// or cleanup errors log and leave the last-success observations unchanged.
// PostgreSQL cleanup can retain earlier successful deletions before a later
// failure. Flagged hashes belong to the separate Redis set and survive this
// log and warning cleanup.
func (s *ContentModerationService) runCleanupOnce() {
	if s == nil || s.repo == nil || s.settingRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), contentModerationCleanupTimeout)
	defer cancel()
	cfg, err := s.loadConfig(ctx)
	// Skip deletion after a policy read failure because this round has no current
	// retention cutoffs. The last successful cleanup observations remain visible
	// until a later round completes.
	if err != nil {
		slog.Warn("content_moderation.cleanup_load_config_failed", "error", err)
		return
	}
	now := time.Now()
	// Compute both cutoffs from one local calendar instant, preserving calendar
	// days across timezone offset changes.
	hitBefore := now.AddDate(0, 0, -cfg.HitRetentionDays)
	nonHitBefore := now.AddDate(0, 0, -cfg.NonHitRetentionDays)
	result, err := s.repo.CleanupExpiredLogs(ctx, hitBefore, nonHitBefore)
	// Keep the previous success display after a failed round; the PostgreSQL
	// adapter can have applied earlier deletes before a later command reports
	// failure.
	if err != nil {
		slog.Warn("content_moderation.cleanup_failed", "error", err)
		return
	}
	if result == nil {
		return
	}
	// Publish successful cleanup time as Unix seconds and its reported counts
	// through separate stores. A status reader can sample between them, so this
	// display describes successful work at potentially different observation times.
	s.lastCleanupUnix.Store(result.FinishedAt.Unix())
	s.lastCleanupDeletedHit.Store(result.DeletedHit)
	s.lastCleanupDeletedNonHit.Store(result.DeletedNonHit)
}

// Start launches the fixed worker population and delayed cleanup after
// dependencies have been bound. Missing storage or prior start/stop state skips
// startup. runtimeMu serializes opening the runtime with stop, publishes
// runtimeDone, and registers workers before releasing the lock.
//
// Workers individually consult current configured WorkerCount to decide whether
// to dequeue; changing that setting does not create or remove the fixed
// goroutines. A stopped service remains closed and cannot restart. Construction
// and binding precede this call so workers share the same repositories and
// Runtime callbacks.
func (s *ContentModerationService) Start() {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.runtimeStarted || s.runtimeStopped || s.settingRepo == nil || s.repo == nil {
		return
	}
	// Publish startup and register work while Stop cannot acquire the lifecycle
	// lock, ensuring its wait includes every worker accepted by this startup.
	s.runtimeStarted = true
	s.runtimeDone = make(chan struct{})
	for i := range s.workerCount {
		s.runtimeWG.Go(func() { ; s.worker(i) })
	}
	s.runtimeWG.Go(func() { ; s.cleanupWorker() })
}

// sealAdmission marks the runtime stopped and closes runtimeDone and asyncQueue
// under runtimeMu so no new queue tasks can enter after Stop or StopContext
// starts. Repeated calls observe runtimeStopped under the write lock and return
// without closing either channel a second time.
func (s *ContentModerationService) sealAdmission() {
	if s == nil {
		return
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	// Exclude senders under the write lock before closing queue and stop signals;
	// a sender holding the read lock finishes before either channel can close.
	if !s.runtimeStopped {
		s.runtimeStopped = true
		if s.runtimeDone != nil {
			close(s.runtimeDone)
		}
		if s.asyncQueue != nil {
			close(s.asyncQueue)
		}
	}
}

// Stop seals queue admission synchronously and waits without a caller deadline
// for registered workers and cleanup to finish. It returns an error when queued
// tasks remain undrained after workers exit.
func (s *ContentModerationService) Stop() error {
	return s.StopContext(context.Background())
}

// StopContext seals queue admission synchronously under runtimeMu before
// waiting on one shared worker-drain goroutine or ctx cancellation.
//
// Sealing admission before spawning the background wait guarantees that even an
// already-expired ctx leaves asyncQueue closed and rejects subsequent enqueues.
// Concurrent or repeated StopContext calls join the same drain completion
// channel and report either the final undrained queue count or the caller's
// context error alongside remaining buffered and active tasks.
func (s *ContentModerationService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.sealAdmission()
	// Launch a single shared waiter for runtimeWG so repeated StopContext calls do
	// not spawn duplicate drain goroutines or race on stopWaitErr publication.
	s.stopWaitOnce.Do(func() {
		s.stopWaitDone = make(chan struct{})
		go func() {
			s.runtimeWG.Wait()
			if n := len(s.asyncQueue); n > 0 {
				s.stopWaitErr = fmt.Errorf("content moderation drain incomplete: %d tasks", n)
			}
			close(s.stopWaitDone)
		}()
	})
	select {
	case <-s.stopWaitDone:
		return s.stopWaitErr
	case <-ctx.Done():
		return fmt.Errorf("content moderation drain incomplete: %d tasks: %w", int64(len(s.asyncQueue))+s.asyncActive.Load(), ctx.Err())
	}
}
