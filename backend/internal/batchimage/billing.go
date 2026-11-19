package batchimage

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// billing.go converts frozen batch-job funding snapshots into reservation,
// capture, and release commands.
// A batch job reserves funds before provider submission and later captures or
// releases that reservation through billing.Funds. Separate action prefixes
// distinguish those operations while the task reference keeps the original
// reserve identity. The job carries the payer, funding mode, held
// allocations, allowance flag and submitted pricing needed after request
// authentication has ended.
//
// Pricing versions two and three send base costs and source multipliers to
// billing, allowing recorded subscriptions and balance to use their own
// rates. Funding publishes returned allocations into the in-memory job after
// successful reserve and clears its allowance flag after successful capture
// or an ordinary successful release. Cancellation, failed submission and
// recovery share the same release identity and request hash.

// Reservation, capture and release prefixes give each batch funding action a
// separate billing deduplication identity.
const (
	batchImageHoldRequestPrefix    = "batch_image_hold:"
	batchImageCaptureRequestPrefix = "batch_image_capture:"
	batchImageReleaseRequestPrefix = "batch_image_release:"

	// FundingScope selects the batchimage task participant registered by app for
	// reservation allocation and allowance writes.
	FundingScope billing.TaskScope = "batchimage"
)

// FundingStore applies job reservations, charges and refunds through stable
// billing action identities.
type FundingStore interface {
	// Reserve applies the task reservation described by the command and returns
	// its allocated sources. Callers retain the action identity and request
	// fingerprint across retries and persist the returned allocation snapshot.
	Reserve(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
	// Capture applies actual task costs against its recorded reservation and
	// returns the charged allocation. Callers supply the original reserve
	// reference and a stable capture identity so resumed settlement can replay
	// the action.
	Capture(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
	// Release returns the unused task reservation identified by the command and
	// its original reserve reference. Callers reuse the same release identity
	// and request fingerprint across cancellation, failed submission and
	// recovery.
	Release(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
}

// Funding adapts persisted batch snapshots to the shared task funding
// implementation.
type Funding struct {
	Store   FundingStore         // shared task funds implementation
	Observe func(string, ...any) // optional funding diagnostic callback
}

// BatchImageHoldRequestID builds the reservation action key from a trimmed
// batch ID.
func BatchImageHoldRequestID(batchID string) string {
	return batchImageHoldRequestPrefix + strings.TrimSpace(batchID)
}

// BatchImageCaptureRequestID builds the capture action key from a trimmed
// batch ID.
func BatchImageCaptureRequestID(batchID string) string {
	return batchImageCaptureRequestPrefix + strings.TrimSpace(batchID)
}

// BatchImageReleaseRequestID builds the refund action key from a trimmed
// batch ID.
func BatchImageReleaseRequestID(batchID string) string {
	return batchImageReleaseRequestPrefix + strings.TrimSpace(batchID)
}

// BillingUserID returns the payer retained by the job, or zero for nil input.
// Older jobs with no positive payer snapshot use the submitting user as their
// payer.
func BillingUserID(job *BatchImageJob) int64 {
	if job == nil {
		return 0
	}
	if job.BillingUserID > 0 {
		return job.BillingUserID
	}
	return job.UserID
}

// BuildHoldCommand converts a job snapshot into the funding command for
// reserve, capture or release. It requires a positive API key identity and
// clamps negative held and actual amounts to zero. The request ID and payload
// hash come from the operation caller.
//
// Version two and later carry base image cost separately from source rates:
// provider and hold multipliers scale the reservation, while the
// discount-to-hold ratio scales capture. Allocation identity pointers and the
// preferred subscription are copied; other job pointers remain shared with
// the command.
func BuildHoldCommand(job *BatchImageJob, requestID string, actualAmount float64, payloadHash string) (*billing.TaskFundsCommand, error) {
	if job == nil {
		return nil, ErrBatchImageBillingHoldFailed
	}
	if job.APIKeyID == nil || *job.APIKeyID <= 0 {
		return nil, ErrBatchImageSettlementMissingAPIKeyID
	}
	holdAmount := job.EstimatedCost
	if job.HoldAmount != nil {
		holdAmount = *job.HoldAmount
	}
	if holdAmount < 0 {
		holdAmount = 0
	}
	if actualAmount < 0 {
		actualAmount = 0
	}
	billingUserID := BillingUserID(job)
	command := &billing.TaskFundsCommand{
		RequestID:                   requestID,
		APIKeyID:                    *job.APIKeyID,
		UserID:                      billingUserID,
		ActorUserID:                 job.UserID,
		TeamID:                      job.TeamID,
		Task:                        FundingReference(job.BatchID),
		APIKeyBillingMode:           job.BillingMode,
		PreferredSubscriptionID:     CloneInt64Ptr(job.PreferredSubscriptionID),
		HoldAmount:                  holdAmount,
		ActualAmount:                actualAmount,
		BalanceHoldAmount:           job.BalanceHoldAmount,
		SubscriptionHoldAllocations: billing.CloneBillingAllocations(job.SubscriptionHoldAllocations),
		AllowanceReserved:           job.AllowanceReserved,
		ReservedAt:                  job.CreatedAt,
		RequestPayloadHash:          strings.TrimSpace(payloadHash),
	}
	// Newer snapshots retain source rates, so billing receives base cost and a
	// settlement ratio instead of a precombined charge.
	if job.PricingSnapshotVersion >= 2 {
		scale := math.Max(job.ProviderRateMultiplier, 0) * math.Max(job.HoldMultiplier, 0)
		settlementScale := 0.0
		if job.HoldMultiplier > 0 {
			settlementScale = math.Max(job.BatchDiscountMultiplier, 0) / job.HoldMultiplier
		}
		command.PricingSnapshotVersion = job.PricingSnapshotVersion
		command.BaseAmountUSD = math.Max(job.BaseUnitPrice, 0) * float64(max(job.ItemCount, 0))
		command.ActualBaseAmountUSD = math.Max(actualAmount, 0)
		command.ActualAmount = 0
		command.SubscriptionRateMultiplier = math.Max(job.SubscriptionRateMultiplier, 0) * scale
		command.SubscriptionRateMultiplierScale = scale
		command.BalanceRateMultiplier = math.Max(job.BalanceRateMultiplier, 0) * scale
		command.SettlementRateScale = settlementScale
		command.DisablePlanGroupRateMultiplier = !job.PlanGroupRateEnabled
	}
	return command, nil
}

// Reserve submits the reservation of a nonnil persisted job and copies
// returned funding details into that job. Callers provide the selected group
// and request fingerprint before invoking the upstream provider. A command
// with neither held nor base cost returns immediately.
//
// Subscription eligibility and key or member quota errors remain available to
// their callers; other funding errors use the hold-failed category. After
// success the returned wallet and subscription allocations replace the local
// snapshot, newer pricing versions take their total and estimate from
// billing, and the local allowance flag records the reservation.
func (f Funding) Reserve(ctx context.Context, job *BatchImageJob, groupID *int64, payloadHash string) error {
	if f.Store == nil {
		return ErrBatchImageBillingHoldFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := BuildHoldCommand(job, BatchImageHoldRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if cmd.HoldAmount <= 0 && cmd.BaseAmountUSD <= 0 {
		return nil
	}
	cmd.GroupID = CloneInt64Ptr(groupID)
	result, err := f.Store.Reserve(ctx, cmd)
	if err != nil {
		if errors.Is(err, ErrBatchImageInsufficientBalance) {
			return ErrBatchImageInsufficientBalance
		}
		// Preferred-subscription errors identify the selected source that prevented
		// reservation. Keep those categories available to admission callers for
		// reporting the configured funding choice.
		if errors.Is(err, billing.ErrPreferredSubscriptionInvalid) || errors.Is(err, billing.ErrPreferredSubscriptionGroup) || errors.Is(err, billing.ErrPreferredSubscriptionInsufficient) {
			return err
		}
		// Key and member limits reject admission before provider work. Keep their
		// categories distinguishable from a funding-store failure in public responses.
		if errors.Is(err, billing.ErrAPIKeyQuotaExhausted) || errors.Is(err, billing.ErrAPIKeyRateLimit5hExceeded) || errors.Is(err, billing.ErrAPIKeyRateLimit1dExceeded) || errors.Is(err, billing.ErrAPIKeyRateLimit7dExceeded) ||
			errors.Is(err, billing.ErrTeamMemberDailyExceeded) || errors.Is(err, billing.ErrTeamMemberWeeklyExceeded) || errors.Is(err, billing.ErrTeamMemberMonthlyExceeded) {
			return err
		}
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	// Billing returns the reservation actually allocated; publish those values
	// after the funding call so subsequent capture reads the committed sources.
	if result != nil {
		job.BalanceHoldAmount = result.BalanceAmountUSD
		job.SubscriptionHoldAllocations = SubscriptionAllocations(result.BillingAllocations)
		if job.PricingSnapshotVersion >= 2 {
			holdAmount := result.HoldAmountUSD
			job.HoldAmount = &holdAmount
			job.EstimatedCost = result.EstimatedAmountUSD
		}
	}
	job.AllowanceReserved = true
	return nil
}

// Capture charges the supplied actual amount for a nonnil job through the job
// capture identity and returns billing allocations. Newer snapshots interpret
// that amount as base cost; success clears the local allowance reservation
// marker.
func (f Funding) Capture(ctx context.Context, job *BatchImageJob, actualAmount float64, payloadHash string) (*billing.TaskFundsResult, error) {
	if f.Store == nil {
		return nil, ErrBatchImageSettlementBillingFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := BuildHoldCommand(job, BatchImageCaptureRequestID(job.BatchID), actualAmount, payloadHash)
	if err != nil {
		return nil, err
	}
	result, err := f.Store.Capture(ctx, cmd)
	if err != nil {
		return nil, ErrBatchImageSettlementBillingFailed.WithCause(err)
	}
	job.AllowanceReserved = false
	return result, nil
}

// Release refunds the job reservation through its stable release identity and
// request fingerprint. Missing funding, nil jobs, nonpositive holds and an
// existing conflicting release fingerprint return successfully. Those early
// returns leave the local allowance marker unchanged; a successful Store.Release
// clears that marker.
func (f Funding) Release(ctx context.Context, job *BatchImageJob, payloadHash string) error {
	if f.Store == nil || job == nil {
		return nil
	}
	cmd, err := BuildHoldCommand(job, BatchImageReleaseRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if cmd.HoldAmount <= 0 {
		return nil
	}
	if _, err := f.Store.Release(ctx, cmd); err != nil {
		// A fingerprint conflict means this release request ID already committed with a different
		// payloadHash and returned the funds. Treat it as success so historical fingerprint
		// mismatches do not leave the job retrying release indefinitely.
		if errors.Is(err, billing.ErrUsageBillingRequestConflict) {
			f.warn("batch_image.release_fingerprint_conflict_treated_as_released",
				"batch_id", job.BatchID,
			)
			return nil
		}
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	job.AllowanceReserved = false
	return nil
}

// SubscriptionAllocations retains positive subscription allocations that
// carry a subscription identity. The result owns its slice and copies present
// subscription and plan identity pointers through
// billing.SubscriptionHoldAllocations.
func SubscriptionAllocations(allocations []billing.BillingAllocation) []billing.BillingAllocation {
	return billing.SubscriptionHoldAllocations(allocations)
}

// CloneInt64Ptr returns fresh integer pointer storage for a present value and
// nil for an absent value. Callers can retain or change that copied identity
// independently of the source pointer.
func CloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// warn sends a funding diagnostic to the configured observation callback.
func (f Funding) warn(event string, values ...any) {
	if f.Observe != nil {
		f.Observe(event, values...)
	}
}

// FundingReference trims the batch identity and pairs its funding scope with
// the original reservation key.
func FundingReference(id string) billing.TaskReference {
	id = strings.TrimSpace(id)
	return billing.TaskReference{Scope: FundingScope, ID: id, ReserveRequestID: "batch_image_hold:" + id}
}
