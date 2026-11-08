package creative

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// results.go settles creative outcomes, releases failed-task reservations, and
// reconciles recovery work.
// Creative provider work, image delivery, and funds completion advance
// independently after generation. Results records the provider outcome before
// transient saving, then captures successful-output cost before checking
// whether bytes remain deliverable. A recorded success with missing bytes
// therefore becomes result_lost with its generated work still charged, while a
// storage-read outage leaves settlement available for retry.
//
// Failures and internal cancellation first persist release_pending and a
// recovery action, then release reserved funds before selecting the target
// terminal state. Recovery outbox events restore queue processing from durable
// metadata; terminal transient cleanup removes remaining byte keys after the
// configured age. Usage callbacks receive stable task billing identity
// separately from the local request UUID, and cache invalidation follows funds
// changes rather than image availability alone.

// Outbox limits and timing bound recovery batches and schedule retry polling.
// Cleanup timing selects old terminal tasks whose remaining transient bytes
// can be deleted independently of whether every output was acknowledged.
const (
	creativeOutboxClaimLimit = 50               // maximum outbox batch rows
	creativeOutboxLease      = 2 * time.Minute  // outbox claim ownership duration
	creativeOutboxPoll       = 5 * time.Second  // outbox reconciler polling interval
	creativeOutboxRetry      = 15 * time.Second // failed outbox retry delay

	creativeTransientCleanupInterval = 5 * time.Minute  // terminal byte cleanup interval
	creativeTransientCleanupAge      = 10 * time.Minute // minimum terminal cleanup age
	creativeTransientCleanupLimit    = 100              // maximum terminal cleanup rows

	maxCreativeErrorMessageChars = 500 // maximum public message runes
)

// ErrCreativeSettlementProviderMissing rejects settlement when the run lacks a
// positive provider identity needed to attribute generated work and usage.
var ErrCreativeSettlementProviderMissing = apperror.BadRequest("BATCH_IMAGE_SETTLEMENT_MISSING_PROVIDER_ID", "batch image settlement provider id is missing")

// Results advances provider outcomes, funds recovery, usage submission, and
// terminal transient cleanup.
type Results struct {
	// Repo, TransientStore, Queue, Outbox, Funding, and the callbacks are supplied
	// by application wiring. Keep those dependencies stable while workers and
	// recovery loops share this Results instance.
	Now  func() time.Time      // optional result clock callback
	Repo CreativeRunRepository // run and output metadata repository
	// temporary task payload and media storage
	TransientStore CreativeTransientStore
	Queue          CreativeRunQueue // task recovery queue implementation
	// durable recovery action repository
	// Outbox stores recoverable provisioning, settle, and release actions. A nil
	// repository makes SucceedRun attempt settlement immediately after recording
	// rather than waiting for outbox-driven queue recovery.
	Outbox  CreativeRunOutboxRepository
	Funding Funding // saved task funds action adapter
	// TransientTTL supplies metadata deadlines and Redis output lifetimes through
	// CreativeDelivery. Application wiring configures a positive duration for both.
	TransientTTL time.Duration // generated image retention duration
	// RecordUsage submits the completed image usage row when configured. Its void
	// callback provides no persistence confirmation, so Results cannot retry a
	// rejected row using a returned storage error.
	RecordUsage func(context.Context, *usage.UsageLog)
	// optional user auth cache invalidator
	InvalidateAuth func(context.Context, int64)
	Observe        func(string, ...any) // optional result diagnostic callback
}

// warn sends a result event and attributes to Observe synchronously. A nil
// callback drops the diagnostic; supplied values pass through unchanged for
// funding, cleanup, and outbox recovery observations.
func (s *Results) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

// SanitizeCreativeMessage trims a message, replaces selected control
// characters, and limits it to five hundred Unicode runes. Newlines, carriage
// returns, and tabs remain; callers remove sensitive information before using
// this formatting helper.
func SanitizeCreativeMessage(message string) string {
	message = strings.TrimSpace(message)
	message = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return ' '
	}, message)
	if !utf8.ValidString(message) {
		message = strings.ToValidUTF8(message, "?")
	}
	runes := []rune(message)
	if len(runes) > maxCreativeErrorMessageChars {
		// Limit after rune conversion so the public message cannot end inside a
		// UTF-8 encoding sequence; the retained prefix still contains
		// any supplied
		// secrets because this helper performs formatting rather than redaction.
		message = string(runes[:maxCreativeErrorMessageChars])
	}
	return message
}

// firstAllocatedSubscriptionID returns a copied identifier from the first
// subscription allocation with an ID. It skips other allocation types and
// missing IDs and returns nil when none qualifies.
func firstAllocatedSubscriptionID(allocations []billing.BillingAllocation) *int64 {
	for i := range allocations {
		if allocations[i].Type != billing.BillingAllocationTypeSubscription || allocations[i].SubscriptionID == nil {
			continue
		}
		subscriptionID := *allocations[i].SubscriptionID
		return &subscriptionID
	}
	return nil
}

// ReconcileCreativeTransientOnce deletes byte keys for the repository-selected
// terminal runs at least ten minutes old. Missing dependencies return zero
// successfully; per-run deletion failures are logged and skipped, leaving
// metadata unchanged for later cleanup.
func (s *Results) ReconcileCreativeTransientOnce(ctx context.Context) (int, error) {
	if s == nil || s.Repo == nil || s.TransientStore == nil {
		return 0, nil
	}
	runs, err := s.Repo.ListCreativeRunsDueForTransientCleanup(ctx, s.now().Add(-creativeTransientCleanupAge), creativeTransientCleanupLimit)
	if err != nil {
		return 0, err
	}
	cleaned := 0
	for _, run := range runs {
		if run == nil {
			continue
		}
		if err := s.TransientStore.DeleteRunTransient(ctx, run.RunID, 0, run.RequestedOutputCount); err != nil {
			s.warn("creative.transient_cleanup_failed", "run_id", run.RunID, "error", err)
			// Metadata stays eligible for the next cleanup batch
			// after deletion fails,
			// and Redis TTL remains the eventual byte-retention
			// limit for these keys.
			continue
		}
		cleaned++
	}
	return cleaned, nil
}

// RunCreativeTransientReconciler attempts terminal cleanup immediately and
// then on the five-minute ticker until cancellation. Batch errors are ignored
// by the polling loop, while per-run deletion diagnostics come from
// ReconcileCreativeTransientOnce.
func (s *Results) RunCreativeTransientReconciler(ctx context.Context) {
	if s == nil || s.Repo == nil || s.TransientStore == nil {
		return
	}
	ticker := time.NewTicker(creativeTransientCleanupInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		// A failed cleanup batch should not end the runtime recovery loop. Leave
		// its rows eligible and retry on the next ticker while cancellation still
		// controls the lifetime of this goroutine.
		_, _ = s.ReconcileCreativeTransientOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ReconcileCreativeOutboxOnce claims one recovery batch and returns the count
// successfully completed. Missing required dependencies return zero;
// cancellation returns the completed count and context error.
//
// Events are processed in claim order. A processing failure schedules a
// fifteen-second retry using the same row/token, with retry-write failures
// logged separately. Completion failures remain the last returned error and
// their leases can expire for another claim. The returned error reflects the
// last observed event failure, so earlier completed rows remain counted even
// when the batch returns an error.
func (s *Results) ReconcileCreativeOutboxOnce(ctx context.Context) (int, error) {
	if s == nil || s.Outbox == nil || s.Queue == nil || s.Repo == nil {
		return 0, nil
	}
	events, err := s.Outbox.Claim(ctx, "creative-reconciler", creativeOutboxClaimLimit, creativeOutboxLease)
	if err != nil {
		return 0, err
	}
	processed := 0
	var lastErr error
	// Keep claimed row/token ownership through processing and its following
	// completion or retry write. Per-event failure leaves other claimed actions
	// eligible to progress within the same batch.
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := s.ReconcileCreativeOutboxEvent(ctx, event); err != nil {
			lastErr = err
			if retryErr := s.Outbox.Retry(ctx, event.ID, event.LeaseToken, s.now().Add(creativeOutboxRetry), SanitizeCreativeMessage(err.Error())); retryErr != nil {
				s.warn("creative.outbox_retry_failed", "outbox_id", event.ID, "error", retryErr)
			}
			continue
		}
		if err := s.Outbox.Complete(ctx, event.ID, event.LeaseToken); err != nil {
			lastErr = err
			continue
		}
		processed++
	}
	return processed, lastErr
}

// ReconcileCreativeOutboxEvent restores provisioning or queues pending funds
// recovery for one action. A missing run and terminal settle/release actions
// return success; duplicate enqueue conflicts also succeed, while unknown
// operations return an error.
func (s *Results) ReconcileCreativeOutboxEvent(ctx context.Context, event CreativeRunOutbox) error {
	run, err := s.Repo.GetCreativeRunByRunID(ctx, event.RunID)
	if err != nil {
		// A deleted task leaves no metadata to recover through this
		// outbox action.
		// Accept its absence so the batch can complete the obsolete action rather
		// than repeatedly leasing recovery for a resource that no longer exists.
		if errors.Is(err, ErrCreativeRunNotFound) {
			return nil
		}
		return err
	}
	switch event.Operation {
	case CreativeRunOutboxProvision:
		return s.ReconcileCreativeProvision(ctx, run)
	case CreativeRunOutboxSettle, CreativeRunOutboxRelease:
		if IsTerminalCreativeRunStatus(run.Status) {
			return nil
		}
		// Existing inflight admission already represents this funds
		// recovery in the
		// queue. Accept that conflict so the outbox claim can complete while the
		// represented worker attempt follows the saved task state.
		if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown creative outbox operation %q", event.Operation)
	}
}

// ReconcileCreativeProvision restores queue entry according to the saved
// provisioning phase. Enqueued and complete phases enqueue idempotently and
// then mark complete.
//
// A transient_saved phase can resume only with a configured store and readable
// payload, after which enqueue precedes marking enqueued. Any payload read
// error selects pre-execution result-loss recovery here, including storage
// availability errors. Earlier incomplete phases fail toward release because
// this recovery path cannot rebuild plaintext input from persistent hashes.
// Queue and phase-write errors propagate so a later outbox claim can resume
// the action.
func (s *Results) ReconcileCreativeProvision(ctx context.Context, run *CreativeRun) error {
	if run == nil {
		return nil
	}
	if run.ProvisioningPhase == CreativeProvisioningPhaseEnqueued || run.ProvisioningPhase == CreativeProvisioningPhaseComplete {
		// This task already completed input provisioning, so duplicate
		// queue admission
		// represents an existing attempt. Accept that conflict before persisting
		// complete, allowing later recovery to finish the same creation action.
		if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
			return err
		}
		return s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseComplete)
	}
	// Persistent hashes cannot reconstruct the execution prompt or source bytes.
	// Resume this partial task only from transient_saved with a readable payload;
	// otherwise select release recovery for incomplete pre-execution work.
	if run.ProvisioningPhase != CreativeProvisioningPhaseTransientSaved || s.TransientStore == nil {
		return s.FailRun(ctx, run.RunID, "PROVISIONING_INCOMPLETE", "creative provisioning could not be recovered without transient input")
	}
	// This provisioning recovery treats any failed payload read as pre-execution
	// loss, including outages that Process handles by requeueing. Enter release
	// recovery here because this branch does not resume with an unreadable payload.
	if _, err := s.TransientStore.LoadPayload(ctx, run.RunID); err != nil {
		return s.MarkResultLost(ctx, run.RunID, false)
	}
	// Readable saved inputs can resume an existing inflight attempt as well as
	// create a ready entry. Accept duplicate admission before recording enqueued
	// so repeated provisioning does not fail on work the queue already represents.
	if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
		return err
	}
	return s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseEnqueued)
}

// RunCreativeOutboxReconciler attempts one outbox batch immediately and then
// on five-second ticks until cancellation. Batch errors are ignored by this
// polling loop; action retry and completion handling belongs to
// ReconcileCreativeOutboxOnce.
func (s *Results) RunCreativeOutboxReconciler(ctx context.Context) {
	if s == nil || s.Outbox == nil || s.Queue == nil {
		return
	}
	ticker := time.NewTicker(creativeOutboxPoll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		// Event failures remain represented by pending work or expiring leases.
		// Continue polling after a batch error so those durable actions can be
		// claimed again under the runtime cancellation context.
		_, _ = s.ReconcileCreativeOutboxOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// EnsureCreativeOutbox delegates recovery-row creation with the current result
// clock time. A nil Results or outbox repository returns success; the
// configured repository decides which existing action states can be refreshed.
func (s *Results) EnsureCreativeOutbox(ctx context.Context, runID string, operation CreativeRunOutboxOperation) error {
	if s == nil || s.Outbox == nil {
		return nil
	}
	return s.Outbox.Ensure(ctx, runID, operation, s.now())
}

// GetRunPublic reads a task and its output metadata by internal run
// identifier. This result/recovery helper applies no user/workspace access
// gate, so public callers use Queries ownership checks first.
func (s *Results) GetRunPublic(ctx context.Context, runID string) (*CreativeRunPublic, error) {
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	outputs, err := s.Repo.ListCreativeRunOutputs(ctx, runID)
	if err != nil {
		return nil, err
	}
	return CreativeRunToPublic(run, outputs), nil
}

// InvalidateCreativeAuthCache invokes the optional cache callback for a
// positive user identifier. Nil Results, absent callbacks, or nonpositive
// identifiers return directly, leaving invalidation to configured application
// wiring.
func (s *Results) InvalidateCreativeAuthCache(ctx context.Context, userID int64) {
	if s != nil && s.InvalidateAuth != nil && userID > 0 {
		s.InvalidateAuth(ctx, userID)
	}
}

// MarkRunning advances a queued task through the repository running operation.
// Already running or terminal tasks return success; other nonterminal states
// return ErrCreativeInvalidTransition before any running update.
func (s *Results) MarkRunning(ctx context.Context, runID string, providerID int64) error {
	if s == nil || s.Repo == nil {
		return errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == CreativeRunStatusRunning {
		return nil
	}
	if run.Status != CreativeRunStatusQueued {
		if IsTerminalCreativeRunStatus(run.Status) {
			return nil
		}
		return ErrCreativeInvalidTransition
	}
	return s.Repo.MarkCreativeRunRunning(ctx, runID, providerID, s.now())
}

// SucceedRun records supplied provider work and attempts transient image
// delivery before returning public metadata. Terminal states other than
// cancelled return their current metadata immediately.
//
// Output error messages pass through the formatting helper, and cancelled
// tasks can still record completed provider work. CreativeDelivery requires
// the repository outcome capability so its durable fact precedes byte saving.
// When Outbox is absent, settlement is attempted inline after recording;
// otherwise a caller or queued recovery completes capture. Returning
// provider_succeeded metadata therefore does not by itself mean
// client-deliverable success.
func (s *Results) SucceedRun(ctx context.Context, runID string, providerID int64, results []ProviderOutput) (*CreativeRunPublic, error) {
	if s == nil || s.Repo == nil {
		return nil, errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if IsTerminalCreativeRunStatus(run.Status) && run.Status != CreativeRunStatusCancelled {
		return s.GetRunPublic(ctx, runID)
	}
	outputs := make([]ProviderOutput, 0, len(results))
	for _, result := range results {
		outputs = append(outputs, ProviderOutput{Index: result.Index, Success: result.Success, Bytes: result.Bytes, Mime: result.Mime, ErrorCode: result.ErrorCode, ErrorMessage: SanitizeCreativeMessage(result.ErrorMessage)})
	}
	if err := s.CreativeDelivery().Record(ctx, runID, providerID, outputs); err != nil {
		return nil, err
	}
	if s.Outbox == nil {
		if err := s.SettleRun(ctx, runID); err != nil {
			return nil, err
		}
	}
	return s.GetRunPublic(ctx, runID)
}

// CreativeDelivery binds this Results clock, stores, repository outcome
// capability, and TTL to the delivery helper. A repository lacking
// ProviderOutcomeStore leaves Outcomes nil, which Record reports as a
// configuration error.
func (s *Results) CreativeDelivery() ResultDelivery {
	outcomes, _ := s.Repo.(ProviderOutcomeStore)
	return ResultDelivery{Now: s.Now, Repo: s.Repo, Outcomes: outcomes, Store: s.TransientStore, TTL: s.TransientTTL}
}

// SettleRun captures confirmed provider image work and completes its delivery
// metadata. It accepts provider_succeeded or settlement_pending rows, plus
// cancelled rows with recorded provider work and no actual cost;
// release_pending returns directly.
//
// Capture uses the stable funds identifier before clearing an allowance
// marker, invalidating auth cache, and checking transient output availability.
// Read failure can therefore leave already captured work pending for another
// deduplicated attempt. Confirmed loss selects a billable result_lost
// completion, while cancellation retains cancelled status. Invalid completion
// transitions are tolerated before usage submission, and the usage callback
// has no returned persistence result for this method to validate.
func (s *Results) SettleRun(ctx context.Context, runID string) error {
	if s == nil || s.Repo == nil {
		return errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return nil
	}
	if !IsCreativeRunSettlementPending(run.Status) && (run.Status != CreativeRunStatusCancelled || run.ProviderResultRecordedAt == nil) {
		return nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		return nil
	}
	if run.Status == CreativeRunStatusCancelled && run.ActualCost != nil {
		return nil
	}
	if run.Status == CreativeRunStatusProviderSucceeded {
		if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusSettlementPending, CreativeRunTransitionOptions{}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
			return err
		}
		run.Status = CreativeRunStatusSettlementPending
	}
	if run.ProviderID == nil || *run.ProviderID <= 0 {
		return ErrCreativeSettlementProviderMissing
	}
	outputs, err := s.Repo.ListCreativeRunOutputs(ctx, runID)
	if err != nil {
		return err
	}
	successCount := 0
	run.ActualFrameCount = 0
	for _, output := range outputs {
		if output != nil && output.Status == CreativeRunOutputStatusSucceeded {
			successCount++
			run.ActualFrameCount += output.FrameCount
		}
	}
	// Capture confirmed provider work before delivery reads. If availability
	// checking later fails, another settlement attempt uses the same capture ID
	// so already completed funds work is deduplicated by the billing store.
	billingResult, err := s.Funding.Capture(ctx, run, successCount)
	if err != nil {
		return err
	}
	// Capture completed before this metadata marker is cleared. Persist the
	// allowance state before auth invalidation and terminal delivery completion
	// so subsequent recovery can read the completed reservation state.
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok && run.AllowanceReserved {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, runID, false); err != nil {
			return err
		}
		run.AllowanceReserved = false
	}
	s.InvalidateCreativeAuthCache(ctx, run.UserID)
	actualCost := 0.0
	if run.ActualCost != nil {
		actualCost = *run.ActualCost
	}
	lost, deliveryErr := s.CreativeDelivery().Lost(ctx, runID, outputs)
	if deliveryErr != nil {
		return deliveryErr
	}
	outcomes := s.CreativeDelivery().Outcomes
	if outcomes == nil {
		return errors.New("creative platform outcome store is not configured")
	}
	if err := outcomes.CompleteProviderOutcome(ctx, runID, actualCost, lost, s.now()); err != nil {
		if !errors.Is(err, ErrCreativeInvalidTransition) {
			return err
		}
	}
	// The completion implementation can retain cancelled status for recorded
	// provider work. Submit its usage after completion handling so that in-flight
	// cancellation does not erase the observed image count and captured cost.
	s.RecordCreativeUsageLog(ctx, run, actualCost, successCount, billingResult, s.now())
	return nil
}

// ReleaseRun returns unused funds for a release_pending task before its target
// terminal transition. Other or absent tasks return success.
//
// After funds release, an optional allowance marker is cleared and the saved
// target defaults to failed when empty. Invalid transition errors are
// tolerated to permit another writer state change; auth invalidation follows,
// then transient deletion errors propagate even though funds and task state
// can already have advanced. A later terminal cleanup can remove those
// remaining bytes independently from release recovery.
func (s *Results) ReleaseRun(ctx context.Context, runID string) error {
	if s == nil || s.Repo == nil {
		return errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil || run.Status != CreativeRunStatusReleasePending {
		return nil
	}
	// Return funds before advancing the saved release target. A failed funds
	// operation leaves release_pending available for another queued or outbox
	// recovery attempt with the same release identifier.
	if err := s.Funding.Release(ctx, run); err != nil {
		return err
	}
	// Funds release has completed before clearing its remaining allowance marker.
	// Persist that state before terminal transition and cache invalidation so
	// later metadata reads describe the returned reservation.
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok && run.AllowanceReserved {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, runID, false); err != nil {
			return err
		}
	}
	target := run.ReleaseTargetStatus
	if target == "" {
		target = CreativeRunStatusFailed
	}
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, target, CreativeRunTransitionOptions{}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
		return err
	}
	s.InvalidateCreativeAuthCache(ctx, run.UserID)
	if s.TransientStore != nil {
		if err := s.TransientStore.DeleteRunTransient(ctx, runID, 0, run.RequestedOutputCount); err != nil {
			return err
		}
	}
	return nil
}

// FailRun requests release_pending with failed as the target and schedules
// durable release recovery. Terminal tasks return directly, while a task
// already release_pending follows its existing target through ReleaseRun.
//
// Blank codes use PROVIDER_FAILED and messages pass through formatting before
// the transition. Invalid transition errors are tolerated, so ReleaseRun
// rereads the actual current state rather than guaranteeing that this writer
// wins. The outbox action is ensured before immediate release is attempted,
// leaving recovery available when the funds operation returns an error.
func (s *Results) FailRun(ctx context.Context, runID, errorCode, errorMessage string) error {
	if s == nil || s.Repo == nil {
		return nil
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		return s.ReleaseRun(ctx, runID)
	}
	code := strings.TrimSpace(errorCode)
	if code == "" {
		code = "PROVIDER_FAILED"
	}
	message := SanitizeCreativeMessage(errorMessage)
	// Persist the failed release target before ensuring outbox recovery and trying
	// funds release. A funds error can therefore leave the task recoverable through
	// its stored release_pending state and action.
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusFailed,
	}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
		return err
	}
	if err := s.EnsureCreativeOutbox(ctx, runID, CreativeRunOutboxRelease); err != nil {
		return err
	}
	if err := s.ReleaseRun(ctx, runID); err != nil {
		s.warn("creative.fail_release_failed",
			"run_id", runID,
			"error", err,
		)
		return err
	}
	return nil
}

// CancelRunByWorker requests release toward cancelled for a current
// nonterminal task. Existing terminal states return directly, and an existing
// release_pending row retains its previously saved release target. Transition
// conflicts are tolerated, then the outbox is ensured before ReleaseRun
// attempts funds recovery.
//
// Persisting the release target first lets another worker resume a failed funds
// action from metadata. The method then rereads through ReleaseRun, so a
// concurrent state change can determine the resulting task state even when
// this cancellation request returns successfully.
func (s *Results) CancelRunByWorker(ctx context.Context, runID string) error {
	if s == nil || s.Repo == nil {
		return nil
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		return s.ReleaseRun(ctx, runID)
	}
	// Record the cancellation release target before scheduling recovery and
	// releasing funds. This sequence leaves the task release_pending when funds
	// recovery fails instead of publishing an unfunded cancellation completion.
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ReleaseTargetStatus: CreativeRunStatusCancelled,
	}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
		return err
	}
	if err := s.EnsureCreativeOutbox(ctx, runID, CreativeRunOutboxRelease); err != nil {
		return err
	}
	return s.ReleaseRun(ctx, runID)
}

// MarkResultLost requests pre-execution release toward result_lost when
// providerSucceeded is false. Existing terminal states and confirmed provider
// success return unchanged.
//
// The caller flag distinguishes lost inputs before execution from completed
// work whose bytes later disappeared. For pre-execution loss, the
// release_pending transition persists its target and error before a release
// action is ensured and attempted. A transition or recovery error propagates,
// while confirmed provider work is left for the separate settlement path to
// count and charge.
func (s *Results) MarkResultLost(ctx context.Context, runID string, providerSucceeded bool) error {
	if s == nil || s.Repo == nil {
		return nil
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == CreativeRunStatusResultLost {
		return nil
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return nil
	}
	if providerSucceeded {
		return nil
	}
	code := "RESULT_LOST"
	message := "transient result expired or worker lost before client acknowledgment"
	// Persist the pre-execution loss target before ensuring its release action.
	// Funds release follows that recovery metadata so lost input does not remove
	// the durable task evidence needed to return its reservation.
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusResultLost,
	}); err != nil {
		return err
	}
	if err := s.EnsureCreativeOutbox(ctx, runID, CreativeRunOutboxRelease); err != nil {
		return err
	}
	return s.ReleaseRun(ctx, runID)
}

// RecordCreativeUsageLog submits successful-image usage metadata through the
// optional callback. Nil receivers, callbacks, tasks, or provider IDs return
// directly, as do nonpositive success counts.
//
// The billing key is creative_settle for the task, while RequestID is its
// derived stable UUID. Billing-result allocations are copied; absent
// allocation details fall back to a balance allocation for positive actual
// cost. Subscription amounts choose the subscription billing type, and
// nonpositive saved balance multipliers display as one. The callback receives
// the constructed row outside funds capture and metadata completion
// transactions, so this method does not report whether storage accepted it.
// GroupID points into the supplied run; callers keep that field stable while
// a callback retaining the row still consumes it.
func (s *Results) RecordCreativeUsageLog(ctx context.Context, run *CreativeRun, actualCost float64, successCount int, billingResult *billing.TaskFundsResult, createdAt time.Time) {
	if s == nil || s.RecordUsage == nil || run == nil || run.ProviderID == nil || successCount <= 0 {
		return
	}
	billingMode := "image"
	imageSize := run.ImageSize
	inboundEndpoint := "/v1/creative/runs"
	upstreamEndpoint := "creative:" + run.Operation
	subscriptionAmount := 0.0
	balanceAmount := actualCost
	allocations := []billing.BillingAllocation(nil)
	if billingResult != nil {
		subscriptionAmount = billingResult.SubscriptionAmountUSD
		balanceAmount = billingResult.BalanceAmountUSD
		allocations = cloneBillingAllocations(billingResult.BillingAllocations)
	}
	billingType := usage.BillingTypeBalance
	if subscriptionAmount > 0 {
		billingType = usage.BillingTypeSubscription
	}
	if len(allocations) == 0 && actualCost > 0 {
		// Legacy or incomplete funding results can lack source
		// allocations. Supply
		// a balance allocation for positive cost so the submitted usage
		// still names
		// a funding source instead of carrying an empty allocation breakdown.
		allocations = []billing.BillingAllocation{{Type: billing.BillingAllocationTypeBalance, AmountUSD: actualCost}}
	}
	rateMultiplier := run.BalanceRateMultiplier
	if rateMultiplier <= 0 {
		rateMultiplier = 1
	}
	usageLog := &usage.UsageLog{
		UserID:                run.UserID,
		BillingUserID:         run.UserID,
		APIKeyID:              run.APIKeyID,
		ProviderID:            *run.ProviderID,
		Platform:              run.Platform,
		RequestID:             telemetry.StableRequestID(CreativeSettlementRequestID(run.RunID)),
		BillingKey:            CreativeSettlementRequestID(run.RunID),
		Model:                 run.Model,
		RequestedModel:        run.RequestedModel,
		InboundEndpoint:       &inboundEndpoint,
		UpstreamEndpoint:      &upstreamEndpoint,
		GroupID:               &run.GroupID,
		ImageCount:            successCount,
		ImageFrameCount:       run.ActualFrameCount,
		ImageOutputCost:       actualCost,
		TotalCost:             actualCost,
		ActualCost:            actualCost,
		SubscriptionAmountUSD: subscriptionAmount,
		BalanceAmountUSD:      balanceAmount,
		BillingAllocations:    allocations,
		SubscriptionID:        firstAllocatedSubscriptionID(allocations),
		RateMultiplier:        rateMultiplier,
		BillingType:           billingType,
		RequestType:           usage.RequestTypeSync,
		BillingMode:           &billingMode,
		ImageSize:             &imageSize,
		CreatedAt:             createdAt,
	}
	s.RecordUsage(ctx, usageLog)
}

// now returns the configured result clock value, or current system time
// when the callback is absent. Callers keep Now stable while shared workers
// and reconcilers calculate task completion and recovery timestamps.
func (s *Results) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
