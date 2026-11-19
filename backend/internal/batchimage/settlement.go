package batchimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// settlement.go captures batch-job charges from indexed results and records
// settlement completion and usage.
// Indexed successful custom IDs determine the batch charge, while failed
// items contribute no billable count. Settlement uses submitted price
// snapshots for current jobs and the quote callback for legacy records, then
// captures against the original funds allocation before saving completed
// status and output expiry. Stable capture identities allow a later queue
// visit to recover after capture succeeded but completion persistence failed.
//
// Failures recorded through RecordSettlementFailure increase settlement attempts. At the
// limit, the service releases the original reservation before changing the
// job to failed; a release failure retains settling state for another
// attempt. The original RequestHash identifies releases across submission,
// cancellation and recovery, while the manifest binds capture to result
// counts and provider resources. Usage reporting follows successful
// completion persistence and records the separate client, internal and
// upstream model snapshots.

// Settlement defaults bound billing retry attempts and delays, while the USD
// cost tolerance handles tiny legacy hold comparison differences.
const (
	BatchImageSettlementRetryDelay = time.Minute
	BatchImageSettlementMaxRetries = 5
	BatchImageCostEpsilon          = 0.00000001
)

// Settlement captures indexed task costs and coordinates completion
// persistence with bounded funding recovery.
type Settlement struct {
	Now     func() time.Time                                               // injected current time
	Repo    BatchImageRepository                                           // persistent settlement state store
	Funding Funding                                                        // recorded task funding adapter
	Quote   func(context.Context, string, *int64, string) (float64, error) // legacy per image quote callback
	// RecordUsage is called after capture and completion persistence have
	// succeeded. Its callback owns usage-write behavior, and completed-job
	// replays return before invoking this optional observer.
	RecordUsage    func(context.Context, *usage.UsageLog) // optional post completion usage callback
	InvalidateAuth func(context.Context, int64)           // optional payer cache invalidation
	Retention      time.Duration                          // completed output retention duration
	Observe        func(string, ...any)                   // optional settlement diagnostic callback
}

// BatchImageSettlementResult reports the captured cost, source allocations
// and completed-replay state.
type BatchImageSettlementResult struct {
	BatchID               string                      // public batch identifier
	SuccessCount          int                         // successful item count
	FailCount             int                         // failed item count
	ActualCost            float64                     // USD captured charge
	ManifestHash          string                      // settlement result fingerprint
	RequestID             string                      // capture billing action identity
	SubscriptionAmountUSD float64                     // USD subscription charge
	BalanceAmountUSD      float64                     // USD wallet charge
	BillingAllocations    []billing.BillingAllocation // copied captured funding sources
	// AlreadySettled is set when the persisted job is completed before this
	// attempt. That replay returns its stored cost and counts without
	// reconstructing allocation details from billing.
	AlreadySettled bool // existing completed job indicator
}

// warn sends a settlement diagnostic through the configured observation
// callback.
func (s *Settlement) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

// Settle captures the charge for a settling batch and persists its completed
// result. Repo, Funding.Store and Quote are required even for saved-price or
// completed jobs. A completed record returns its existing cost as already settled;
// other stages reject settlement. The job needs key and provider identities,
// valid indexed counts and a compatible manifest before funding capture.
//
// Current snapshots use submitted rates, with newer versions sending
// successful-item base cost to billing for source-specific allocation.
// Capture precedes completion persistence, and the stable capture identity
// allows replay after interruption between those operations. Successful
// completion sets an absent output expiry and then writes usage. Recorded
// failures increment the retry counter; exhausted attempts release the
// original hold before saving a failed outcome. Missing identities and a
// negative BaseUnitPrice return before failure bookkeeping.
func (s *Settlement) Settle(ctx context.Context, batchID string) (*BatchImageSettlementResult, error) {
	if s == nil || s.Repo == nil || s.Funding.Store == nil || s.Quote == nil {
		return nil, ErrBatchImageSettlementBillingFailed.WithCause(errors.New("batch image settlement service is not configured"))
	}
	job, err := s.Repo.GetBatchImageJobByBatchID(ctx, batchID)
	if err != nil {
		return nil, err
	}

	manifestHash := BuildBatchImageSettlementManifestHash(job)
	result := &BatchImageSettlementResult{
		BatchID:      job.BatchID,
		SuccessCount: job.SuccessCount,
		FailCount:    job.FailCount,
		ManifestHash: manifestHash,
		RequestID:    BatchImageCaptureRequestID(job.BatchID),
	}
	if job.ActualCost != nil {
		result.ActualCost = *job.ActualCost
	}
	if job.Status == BatchImageJobStatusCompleted {
		result.AlreadySettled = true
		return result, nil
	}
	if job.Status != BatchImageJobStatusSettling {
		return nil, ErrBatchImageSettlementInvalidStatus
	}
	if job.APIKeyID == nil || *job.APIKeyID <= 0 {
		return nil, ErrBatchImageSettlementMissingAPIKeyID
	}
	if job.ProviderID == nil || *job.ProviderID <= 0 {
		return nil, ErrBatchImageSettlementMissingProviderID
	}
	// Check exhausted retries before count, manifest, price, and hold validation.
	// Otherwise repeated validation errors can requeue forever with funds still held.
	if IsBatchImageSettlementRetryExhausted(job) {
		return nil, s.FailExhaustedSettlement(ctx, job, "settlement retry limit reached: "+BatchImageDerefString(job.LastErrorCode))
	}
	if job.SuccessCount < 0 || job.FailCount < 0 || job.ItemCount < 0 || job.SuccessCount+job.FailCount > job.ItemCount {
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_INVALID_COUNTS",
			fmt.Sprintf("success=%d fail=%d item_count=%d", job.SuccessCount, job.FailCount, job.ItemCount)); failErr != nil {
			return nil, failErr
		}
		return nil, ErrBatchImageSettlementInvalidCounts
	}
	if strings.TrimSpace(BatchImageDerefString(job.ManifestHash)) != "" && BatchImageDerefString(job.ManifestHash) != manifestHash {
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_MANIFEST_CONFLICT", "manifest hash conflict"); failErr != nil {
			return nil, failErr
		}
		return nil, ErrBatchImageSettlementManifestConflict
	}

	unitPrice, err := s.SettlementUnitPrice(ctx, job)
	if err == nil && unitPrice < 0 {
		err = ErrBatchImageSettlementPricingMissing
	}
	if err != nil {
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_PRICING_MISSING", err.Error()); failErr != nil {
			return nil, failErr
		}
		return nil, err
	}
	actualBillingInput := float64(job.SuccessCount) * unitPrice
	actualCost := actualBillingInput
	// Newer funding snapshots price each source from base cost; keep capture
	// input separate from the estimated combined cost used for legacy records.
	if job.PricingSnapshotVersion >= 2 {
		if job.BaseUnitPrice < 0 {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		actualBillingInput = float64(job.SuccessCount) * job.BaseUnitPrice
	}
	holdAmount := job.EstimatedCost
	if job.HoldAmount != nil {
		holdAmount = *job.HoldAmount
	}
	// Legacy capture uses a combined amount, so permit only the small comparison
	// tolerance before rejecting a charge larger than its reservation.
	if job.PricingSnapshotVersion < 2 && actualCost-holdAmount > BatchImageCostEpsilon {
		msg := fmt.Sprintf("actual cost %.10f exceeds held amount %.10f", actualCost, holdAmount)
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_COST_EXCEEDS_HOLD", msg); failErr != nil {
			return nil, failErr
		}
		return nil, ErrBatchImageSettlementCostExceedsHold
	}

	// Capture owns the funding transaction and deduplication identity; save
	// completed state afterward so interruption can replay that committed action
	// on the next visit.
	billingResult, err := s.Funding.Capture(ctx, job, actualBillingInput, manifestHash)
	if err != nil {
		msg := TruncateBatchImageMessage(err.Error(), BatchImageMaxErrorMessageLength)
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_BILLING_FAILED", msg); failErr != nil {
			return nil, failErr
		}
		return nil, err
	}
	if billingResult != nil {
		if job.PricingSnapshotVersion >= 2 {
			actualCost = billingResult.ActualAmountUSD
		}
		result.SubscriptionAmountUSD = billingResult.SubscriptionAmountUSD
		result.BalanceAmountUSD = billingResult.BalanceAmountUSD
		result.BillingAllocations = billing.CloneBillingAllocations(billingResult.BillingAllocations)
	}
	result.ActualCost = actualCost
	s.InvalidateAuthCache(ctx, BillingUserID(job))

	now := s.now()
	outputExpiresAt := now.Add(s.OutputRetentionAfterTerminal())
	// Funding capture has returned successfully; persist the matching manifest
	// and completed charge before emitting usage so the task can resume safely
	// after interruption.
	if err := s.Repo.MarkBatchImageJobSettled(ctx, MarkBatchImageJobSettledParams{
		BatchID:         job.BatchID,
		ActualCost:      actualCost,
		ManifestHash:    manifestHash,
		Now:             &now,
		OutputExpiresAt: &outputExpiresAt,
		EventPayload: map[string]any{
			"batch_id":      job.BatchID,
			"request_id":    result.RequestID,
			"success_count": job.SuccessCount,
			"fail_count":    job.FailCount,
			"actual_cost":   actualCost,
			"manifest_hash": manifestHash,
		},
	}); err != nil {
		return nil, err
	}
	// Completion persistence establishes the task outcome before the optional
	// usage callback runs; an already-completed retry returns above and skips
	// this reporting step.
	s.RecordUsageLog(ctx, job, actualCost, result.RequestID, now, billingResult)

	return result, nil
}

// IsBatchImageSettlementRetryExhausted recognizes a settling job at the retry
// limit whose latest diagnostic begins SETTLEMENT_. Missing jobs, other
// stages or differently classified errors do not trigger exhausted-settlement
// recovery through this predicate.
func IsBatchImageSettlementRetryExhausted(job *BatchImageJob) bool {
	return job != nil &&
		job.Status == BatchImageJobStatusSettling &&
		job.RetryCount >= BatchImageSettlementMaxRetries &&
		strings.HasPrefix(BatchImageDerefString(job.LastErrorCode), "SETTLEMENT_")
}

// RecordSettlementFailure persists the diagnostic and increments the retry
// counter used by later settlement visits. Callers return a nonnil result
// immediately because reaching the limit can already release and fail the
// task. Earlier failures leave the job settling.
//
// A repository error is observed and returns nil, allowing the caller to
// retain its original validation or billing error. The storage counter may
// already have changed before a later audit append failed. On a successful
// write, local retry and diagnostic fields are refreshed; an exhausted
// counter then invokes FailExhaustedSettlement.
func (s *Settlement) RecordSettlementFailure(ctx context.Context, job *BatchImageJob, code, message string) error {
	retryCount, recordErr := s.Repo.SetBatchImageJobSettlementFailed(ctx, job.BatchID, code, TruncateBatchImageMessage(message, BatchImageMaxErrorMessageLength))
	if recordErr != nil {
		s.warn("batch_image.settlement_failure_record_failed",
			"batch_id", job.BatchID,
			"code", code,
			"error", recordErr,
		)
		// A counter write or its later audit can fail; keep the caller's original
		// settlement error while observing this bookkeeping failure for diagnosis.
		return nil
	}
	job.RetryCount = retryCount
	job.LastErrorCode = &code
	if retryCount >= BatchImageSettlementMaxRetries {
		return s.FailExhaustedSettlement(ctx, job, message)
	}
	return nil
}

// FailExhaustedSettlement releases the task's original reservation and then
// persists a failed settlement outcome. It uses RequestHash because terminal
// processing, cancellation and recovery share the release action identity. A
// successful failure transition still returns the billing-failed category for
// PipelineProcessor to inspect.
//
// If release fails, the method records a release diagnostic and leaves the
// job settling for another visit. If the later state transition fails after
// release, a retry replays the same refund identity before attempting that
// transition again. Authentication cache invalidation follows returned funds,
// and the recorded message is trimmed or replaced with the exhaustion
// diagnosis.
func (s *Settlement) FailExhaustedSettlement(ctx context.Context, job *BatchImageJob, message string) error {
	if s == nil || s.Repo == nil {
		return ErrBatchImageSettlementBillingFailed
	}
	// Use RequestHash for release as processor, Cancel, and recovery do. These paths
	// share a request ID; differing payload hashes cause ErrUsageBillingRequestConflict.
	// Consistent fingerprints let cancellation and terminal-job retries share the release.
	if err := s.Funding.Release(ctx, job, BatchImageDerefString(job.RequestHash)); err != nil {
		msg := TruncateBatchImageMessage(err.Error(), BatchImageMaxErrorMessageLength)
		if _, recordErr := s.Repo.SetBatchImageJobSettlementFailed(ctx, job.BatchID, "SETTLEMENT_RELEASE_FAILED", msg); recordErr != nil {
			s.warn("batch_image.settlement_release_failure_record_failed",
				"batch_id", job.BatchID,
				"error", recordErr,
			)
		}
		return ErrBatchImageSettlementBillingFailed.WithCause(err)
	}
	s.InvalidateAuthCache(ctx, BillingUserID(job))
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "settlement billing retry limit reached"
	}
	if err := s.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusFailed, BatchImageTransitionOptions{
		ErrorCode:    BatchImageStringPtr("SETTLEMENT_BILLING_RETRY_EXHAUSTED"),
		ErrorMessage: BatchImageStringPtr(msg),
		EventType:    "settlement_retry_exhausted",
		EventPayload: map[string]any{
			"batch_id":    job.BatchID,
			"retry_count": job.RetryCount,
		},
	}); err != nil {
		return err
	}
	return ErrBatchImageSettlementBillingFailed
}

// RecordUsageLog sends one usage record after completion persistence, using
// the submitting actor, payer and retained model stages. Absent callbacks,
// jobs or key and provider pointers skip reporting. Billing allocations are
// copied when a result is available; a positive charge with no allocations
// receives a balance entry.
//
// The billing type is subscription when its amount exceeds the cost
// tolerance, including mixed funding. Newer snapshots derive the effective
// group multiplier from the charged amount, successful custom-ID count, base
// price and provider multiplier so different source rates fit one reported
// value. RequestID is the stable local identity and BillingKey remains the
// capture action supplied by the caller.
func (s *Settlement) RecordUsageLog(ctx context.Context, job *BatchImageJob, actualCost float64, requestID string, createdAt time.Time, billingResult *billing.TaskFundsResult) {
	if s == nil || s.RecordUsage == nil || job == nil || job.APIKeyID == nil || job.ProviderID == nil {
		return
	}
	billingMode := "image"
	providerRateMultiplier := job.ProviderRateMultiplier
	inboundEndpoint := "/v1/images/batches"
	upstreamEndpoint := "vertex:batchPredictionJobs"
	imageSize := "1K"
	billingType := usage.BillingTypeBalance
	subscriptionAmount := 0.0
	balanceAmount := actualCost
	allocations := []billing.BillingAllocation(nil)
	if billingResult != nil {
		subscriptionAmount = billingResult.SubscriptionAmountUSD
		balanceAmount = billingResult.BalanceAmountUSD
		allocations = billing.CloneBillingAllocations(billingResult.BillingAllocations)
	}
	// Treat subscription amounts at or below the monetary comparison tolerance as
	// balance-classified usage when choosing its billing type.
	if subscriptionAmount > BatchImageCostEpsilon {
		billingType = usage.BillingTypeSubscription
	}
	// Legacy or incomplete billing results can omit allocations; record a
	// balance allocation so a positive captured charge still has a funding
	// source in usage.
	if len(allocations) == 0 && actualCost > 0 {
		allocations = []billing.BillingAllocation{{Type: billing.BillingAllocationTypeBalance, AmountUSD: actualCost}}
	}
	rateMultiplier := job.GroupRateMultiplier * job.BatchDiscountMultiplier
	if job.PricingSnapshotVersion >= 2 && job.SuccessCount > 0 && job.BaseUnitPrice > 0 && providerRateMultiplier > 0 {
		// Positive base cost, count and provider rate make this ratio defined. The
		// resulting multiplier represents the combined subscription and wallet charge.
		rateMultiplier = actualCost / (job.BaseUnitPrice * float64(job.SuccessCount) * providerRateMultiplier)
	}
	requestedModel := BatchImageRequestedModel(job)
	internalModel := BatchImageInternalModel(job)
	usageLog := &usage.UsageLog{
		UserID:                 job.UserID,
		BillingUserID:          job.BillingUserID,
		TeamID:                 job.TeamID,
		APIKeyID:               *job.APIKeyID,
		ProviderID:             *job.ProviderID,
		Platform:               BatchImageProviderPlatform(job.Platform),
		RequestID:              telemetry.StableRequestID(BatchImageCaptureRequestID(job.BatchID)),
		BillingKey:             strings.TrimSpace(requestID),
		Model:                  internalModel,
		RequestedModel:         requestedModel,
		UpstreamModel:          optionalTrimmedStringPtr(job.Model),
		ModelMappingChain:      optionalTrimmedStringPtr(routing.BuildModelMappingChain(requestedModel, internalModel, job.Model)),
		InboundEndpoint:        &inboundEndpoint,
		UpstreamEndpoint:       &upstreamEndpoint,
		ImageCount:             job.SuccessCount,
		ImageOutputCost:        actualCost,
		TotalCost:              actualCost,
		ActualCost:             actualCost,
		SubscriptionAmountUSD:  subscriptionAmount,
		BalanceAmountUSD:       balanceAmount,
		BillingAllocations:     allocations,
		SubscriptionID:         firstAllocatedSubscriptionID(allocations),
		RateMultiplier:         rateMultiplier,
		ProviderRateMultiplier: &providerRateMultiplier,
		BillingType:            billingType,
		RequestType:            usage.RequestTypeSync,
		BillingMode:            &billingMode,
		ImageSize:              &imageSize,
		SessionID:              job.SessionID,
		CreatedAt:              createdAt,
	}
	s.RecordUsage(ctx, usageLog)
}

// InvalidateAuthCache invokes the optional payer cache callback after a
// funding change for a positive identity. Nil services, absent callbacks and
// nonpositive users return immediately, allowing settlement and release paths
// to share it.
func (s *Settlement) InvalidateAuthCache(ctx context.Context, userID int64) {
	if s != nil && s.InvalidateAuth != nil && userID > 0 {
		s.InvalidateAuth(ctx, userID)
	}
}

// SettlementUnitPrice reads a saved billable unit price for snapshot version
// one or later and rejects negative values. Legacy jobs require a nonnil job
// and Quote callback, then query its upstream model, group and 1K size; lookup
// errors propagate for retry accounting.
func (s *Settlement) SettlementUnitPrice(ctx context.Context, job *BatchImageJob) (float64, error) {
	if job != nil && job.PricingSnapshotVersion >= 1 {
		if job.BillableUnitPrice < 0 {
			return 0, ErrBatchImageSettlementPricingMissing
		}
		return job.BillableUnitPrice, nil
	}
	unitPrice, err := s.Quote(ctx, job.Model, job.GroupID, "1K")
	if err != nil {
		return 0, err
	}
	return unitPrice, nil
}

// OutputRetentionAfterTerminal returns the configured positive output
// retention interval. Nonpositive settings use seventy-two hours, which
// successful settlement adds to its completion time when supplying an absent
// output expiry.
func (s *Settlement) OutputRetentionAfterTerminal() time.Duration {
	if s.Retention > 0 {
		return s.Retention
	}
	return 72 * time.Hour
}

// BuildBatchImageSettlementManifestHash binds capture to the batch, backend,
// model, provider resources and reconciled item counts. Nil jobs return empty
// text. Strings are trimmed and counts use decimal text before the fixed-order
// values are joined with NUL separators and hashed.
//
// Activity timestamps can advance while a worker retries an indexed task. The
// fingerprint omits those timestamps and pricing, so stable result metadata
// replays the same capture; settlement separately validates the submitted
// pricing before using that action identity.
func BuildBatchImageSettlementManifestHash(job *BatchImageJob) string {
	if job == nil {
		return ""
	}
	parts := []string{
		strings.TrimSpace(job.BatchID),
		strings.TrimSpace(job.Platform),
		strings.TrimSpace(job.Model),
		BatchImageDerefString(job.ProviderJobName),
		BatchImageDerefString(job.ProviderOutputRef),
		strconv.Itoa(job.SuccessCount),
		strconv.Itoa(job.FailCount),
		strconv.Itoa(job.ItemCount),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// String formats the batch identity, outcome counts, charge and replay flag
// for a settlement diagnostic. A nil result returns empty text, allowing
// logging callers to handle an absent result.
func (r *BatchImageSettlementResult) String() string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("batch_id=%s success=%d fail=%d actual_cost=%0.10f already_settled=%t",
		r.BatchID, r.SuccessCount, r.FailCount, r.ActualCost, r.AlreadySettled)
}

// optionalTrimmedStringPtr returns pointer storage for a trimmed nonblank
// string. Empty or whitespace-only text returns nil, matching the optional
// upstream-model and model-mapping-chain fields written into batch usage
// records.
func optionalTrimmedStringPtr(raw string) *string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	return &v
}

// firstAllocatedSubscriptionID returns a copied identity from the first
// subscription allocation carrying one. An allocation without an identity is
// skipped, and no qualifying source returns nil for the usage record.
func firstAllocatedSubscriptionID(values []billing.BillingAllocation) *int64 {
	for _, v := range values {
		if v.Type == billing.BillingAllocationTypeSubscription && v.SubscriptionID != nil {
			id := *v.SubscriptionID
			return &id
		}
	}
	return nil
}

// now reads the settlement clock when a callback was injected at
// construction. Otherwise it uses time.Now, supplying the completion
// timestamp and the base time for an absent output expiry.
func (s *Settlement) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
