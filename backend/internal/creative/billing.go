package creative

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// billing.go builds and submits creative reservation, capture, and release
// commands from saved run funding data.
// Creative tasks reserve estimated funds before their inputs enter the worker
// queue. Capture and release address the same reservation through a task
// reference, while each action has its own stable billing request identifier.
// The funds store owns deduplication and persistent funding updates; this
// adapter constructs commands from the saved run snapshot.
//
// The snapshot keeps base image cost separate from subscription and balance
// multipliers so later actions use the prices selected at creation. Allocation
// copies retain source identities independently. Successful reservation
// updates the caller run for subsequent compensation, and release treats an
// existing conflicting billing fingerprint as completed to stop repeated
// recovery attempts on that release identifier. GIF capture accepts verified
// frames beyond the request and charges their uncovered cost to the wallet.

const (
	// Funds operations use the hold, capture, and release identifiers for billing
	// deduplication. The settlement identifier names the separately recorded usage
	// row and its stable local request identity.
	// reservation funds action prefix.
	creativeHoldRequestPrefix = "creative_hold:"
	// successful work charge prefix.
	creativeCaptureRequestPrefix = "creative_capture:"
	// unused reservation release prefix.
	creativeReleaseRequestPrefix = "creative_release:"
	// usage recording billing prefix.
	creativeSettlementRequestPrefix = "creative_settle:"

	// creativePricingSnapshotVersion identifies snapshots allocated by base amount.
	// Creative uses scale 1 with the same reservation and settlement price,
	// without batch discounts.
	creativePricingSnapshotVersion = 2

	// FundingScope identifies the creative task registration used by billing
	// transaction participant selection and the saved task funds reference.
	FundingScope billing.TaskScope = "creative"
)

// FundingStore applies creative task reservations and their later funding actions.
type FundingStore interface {
	// Reserve freezes funds using the command reservation identifier and task
	// reference. Implementations return the funding allocation snapshot for later
	// capture or release and report admission failures before success.
	Reserve(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
	// Capture charges confirmed task work against the reservation referenced by
	// the command. Implementations use the capture identifier to handle retries
	// and return actual amounts and allocations for usage recording.
	Capture(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
	// Release returns unused task reservations using the command task reference.
	// Implementations identify repeated release actions through RequestID and
	// report billing fingerprint conflicts for the creative recovery caller to
	// classify.
	Release(context.Context, *billing.TaskFundsCommand) (*billing.TaskFundsResult, error)
}

// Funding translates saved creative pricing and reservation metadata into
// billing funds actions.
type Funding struct {
	Store FundingStore // task reservation funds implementation
	// Observe optionally records release fingerprint conflicts treated as completed;
	// leave it nil when this funding adapter needs no diagnostic callback.
	Observe func(string, ...any) // optional funding diagnostic callback
}

// CreativeHoldRequestID returns the stable reservation identifier for runID
// after trimming whitespace. Reserve uses this value to associate repeat
// processing with that run action; an empty identifier suffix is not rejected
// here.
func CreativeHoldRequestID(runID string) string {
	return creativeHoldRequestPrefix + strings.TrimSpace(runID)
}

// CreativeCaptureRequestID returns the stable capture identifier for runID
// after trimming whitespace. Capture uses this value to associate repeat
// processing with that run action; an empty identifier suffix is not rejected
// here.
func CreativeCaptureRequestID(runID string) string {
	return creativeCaptureRequestPrefix + strings.TrimSpace(runID)
}

// CreativeReleaseRequestID returns the stable release identifier for runID
// after trimming whitespace. Release uses this value to associate repeat
// processing with that run action; an empty identifier suffix is not rejected
// here.
func CreativeReleaseRequestID(runID string) string {
	return creativeReleaseRequestPrefix + strings.TrimSpace(runID)
}

// CreativeSettlementRequestID returns the stable usage settlement identifier
// for runID after trimming whitespace. RecordCreativeUsageLog uses this value
// to associate repeat processing with that run action; an empty identifier
// suffix is not rejected here.
func CreativeSettlementRequestID(runID string) string {
	return creativeSettlementRequestPrefix + strings.TrimSpace(runID)
}

// BuildHoldCommand constructs a billing command from a nonnil run with a
// positive APIKeyID. The recorded HoldAmount overrides EstimatedCost, negative
// base values and multipliers are clamped to zero, and subscription
// allocations are copied with their optional identity pointers.
//
// Task links the run to its original reservation identifier, while requestID
// identifies the current funds action. Version two uses base amounts and
// separate source multipliers with unit scales, allowing capture to use
// actualBaseAmount against the same saved funding allocation. Callers retain
// the run snapshot for later retries.
func BuildHoldCommand(run *CreativeRun, requestID string, actualBaseAmount float64) (*billing.TaskFundsCommand, error) {
	if run == nil {
		return nil, ErrCreativeBillingHoldFailed
	}
	if run.APIKeyID <= 0 {
		return nil, ErrCreativeBillingHoldFailed
	}
	holdAmount := run.EstimatedCost
	if run.HoldAmount != nil {
		holdAmount = *run.HoldAmount
	}
	if holdAmount < 0 {
		holdAmount = 0
	}
	if actualBaseAmount < 0 {
		actualBaseAmount = 0
	}
	groupID := run.GroupID
	cmd := &billing.TaskFundsCommand{
		RequestID:                       requestID,
		APIKeyID:                        run.APIKeyID,
		UserID:                          run.UserID,
		ActorUserID:                     run.UserID,
		GroupID:                         &groupID,
		Task:                            FundingReference(run.RunID),
		APIKeyBillingMode:               billing.APIKeyBillingModeAuto,
		HoldAmount:                      holdAmount,
		ActualAmount:                    0,
		PricingSnapshotVersion:          creativePricingSnapshotVersion,
		BaseAmountUSD:                   math.Max(run.BaseUnitPrice, 0) * float64(max(run.RequestedOutputCount, 0)) * float64(max(1, run.FrameCount)),
		ActualBaseAmountUSD:             actualBaseAmount,
		SubscriptionRateMultiplier:      math.Max(run.SubscriptionRateMultiplier, 0),
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           math.Max(run.BalanceRateMultiplier, 0),
		SettlementRateScale:             1,
		DisablePlanGroupRateMultiplier:  !run.PlanGroupRateEnabled,
		BalanceHoldAmount:               run.BalanceHoldAmount,
		SubscriptionHoldAllocations:     cloneBillingAllocations(run.SubscriptionHoldAllocations),
		AllowanceReserved:               run.AllowanceReserved,

		ReservedAt:         run.CreatedAt,
		RequestPayloadHash: strings.TrimSpace(run.RequestFingerprint),
	}
	return cmd, nil
}

// Reserve applies the estimated reservation and updates the supplied nonnil
// run with returned funding amounts. A configured store is required even for
// zero-cost runs, which clear balance and subscription holds directly.
// Successful paid reservations mark AllowanceReserved so later compensation
// uses the reserved allowance state.
func (funding Funding) Reserve(ctx context.Context, run *CreativeRun) error {
	if funding.Store == nil {
		return ErrCreativeBillingHoldFailed.WithCause(errors.New("creative billing repository is not configured"))
	}
	if run.EstimatedCost <= 0 {
		// Free task creation clears these funding sources before
		// provisioning continues.
		// The caller can use the resulting run snapshot for the same
		// recovery flow as
		// paid tasks even though no funds-store reservation was submitted.
		run.BalanceHoldAmount = 0
		run.SubscriptionHoldAllocations = nil
		return nil
	}
	cmd, err := BuildHoldCommand(run, CreativeHoldRequestID(run.RunID), 0)
	if err != nil {
		return err
	}
	// Reservation counts toward key and member quotas and persists that
	// fact on the run.
	// Capture and release use the persisted run.AllowanceReserved flag.
	cmd.AllowanceReserved = true
	result, err := funding.Store.Reserve(ctx, cmd)
	if err != nil {
		if errors.Is(err, billing.ErrTaskInsufficientBalance) {
			return ErrCreativeInsufficientBalance
		}
		if errors.Is(err, billing.ErrAPIKeyQuotaExhausted) || errors.Is(err, billing.ErrAPIKeyRateLimit5hExceeded) || errors.Is(err, billing.ErrAPIKeyRateLimit1dExceeded) ||
			errors.Is(err, billing.ErrAPIKeyRateLimit7dExceeded) || errors.Is(err, billing.ErrTeamMemberDailyExceeded) || errors.Is(err, billing.ErrTeamMemberWeeklyExceeded) ||
			errors.Is(err, billing.ErrTeamMemberMonthlyExceeded) {
			return err
		}
		return ErrCreativeBillingHoldFailed.WithCause(err)
	}
	if result != nil {
		run.BalanceHoldAmount = result.BalanceAmountUSD
		run.SubscriptionHoldAllocations = batchImageSubscriptionAllocations(result.BillingAllocations)
		holdAmount := result.HoldAmountUSD
		run.HoldAmount = &holdAmount
		run.EstimatedCost = result.EstimatedAmountUSD
	}
	// The store may persist allowance reservation through its transaction
	// participant.
	// Update this caller snapshot as well so creation compensation carries the same
	// reservation state into the later release command.
	run.AllowanceReserved = true
	return nil
}

// Capture charges the nonnegative successful-image count using the run base
// price and saved funding sources. Callers supply a nonnil run; a returned
// funds result also updates its ActualCost for settlement completion. GIF calls
// require ActualFrameCount rebuilt from durable successful output metadata;
// requested frames determine only the hold, never the actual capture quantity.
//
// When verified GIF frames exceed the estimate and bounded capture rejects the
// charge, capture authorizes a wallet debit beyond the hold using saved pricing,
// including an animation returned for a static request.
// Other captures retain their historical fingerprints, including zero-rate GIFs
// that earlier workers could already have charged successfully.
func (funding Funding) Capture(ctx context.Context, run *CreativeRun, successCount int) (*billing.TaskFundsResult, error) {
	if funding.Store == nil {
		return nil, ErrCreativeSettlementBillingFail.WithCause(errors.New("creative billing repository is not configured"))
	}
	units := successCount
	if run.FrameCount > 0 || run.ActualFrameCount > 0 {
		units = run.ActualFrameCount
	}
	actualBase := math.Max(run.BaseUnitPrice, 0) * float64(max(units, 0))
	cmd, err := BuildHoldCommand(run, CreativeCaptureRequestID(run.RunID), actualBase)
	if err != nil {
		return nil, err
	}
	if run.ActualFrameCount > 0 && actualBase > cmd.BaseAmountUSD {
		// Check the original plan first: zero-rate and sub-tolerance captures may
		// already have committed under the historical fingerprint before recovery.
		if _, planErr := billing.PlanTaskCapture(cmd); errors.Is(planErr, billing.ErrTaskSettlementCostExceedsHold) {
			cmd.OveragePolicy = billing.TaskOverageChargeBalance
		}
	}
	result, err := funding.Store.Capture(ctx, cmd)
	if err != nil {
		return nil, ErrCreativeSettlementBillingFail.WithCause(err)
	}
	if result != nil {
		run.ActualCost = &result.ActualAmountUSD
	}
	return result, nil
}

// Release returns the recorded hold through the run release identifier.
// Missing stores, nil runs, and nonpositive holds succeed directly; a billing
// fingerprint conflict is observed and treated as an already completed
// release.
func (funding Funding) Release(ctx context.Context, run *CreativeRun) error {
	if funding.Store == nil || run == nil {
		return nil
	}
	holdAmount := run.EstimatedCost
	if run.HoldAmount != nil {
		holdAmount = *run.HoldAmount
	}
	if holdAmount <= 0 {
		return nil
	}
	cmd, err := BuildHoldCommand(run, CreativeReleaseRequestID(run.RunID), 0)
	if err != nil {
		return err
	}
	if _, err := funding.Store.Release(ctx, cmd); err != nil {
		// A conflicting release identifier cannot be reapplied by
		// another identical
		// recovery attempt. Treat it as completed so this run can leave
		// release_pending
		// instead of cycling on that conflict indefinitely.
		if errors.Is(err, billing.ErrUsageBillingRequestConflict) {
			if funding.Observe != nil {
				funding.Observe("creative.release_fingerprint_conflict_treated_as_released", "run_id", run.RunID)
			}
			return nil
		}
		return ErrCreativeBillingHoldFailed.WithCause(err)
	}
	return nil
}

// cloneBillingAllocations copies allocation records and their optional source
// identity pointers. A nil input remains nil, and each copy keeps its supplied
// amount and scalar pricing values.
func cloneBillingAllocations(values []billing.BillingAllocation) []billing.BillingAllocation {
	if values == nil {
		return nil
	}
	out := make([]billing.BillingAllocation, len(values))
	for i, v := range values {
		out[i] = billing.CloneBillingAllocation(v, v.AmountUSD)
	}
	return out
}

// batchImageSubscriptionAllocations copies positive subscription allocations
// that have a subscription identifier. Other allocation types and unusable
// subscription rows are omitted from the run reservation snapshot; nil input
// returns an empty slice.
func batchImageSubscriptionAllocations(values []billing.BillingAllocation) []billing.BillingAllocation {
	out := make([]billing.BillingAllocation, 0, len(values))
	for _, v := range values {
		if v.Type == billing.BillingAllocationTypeSubscription && v.AmountUSD > 0 && v.SubscriptionID != nil {
			out = append(out, billing.CloneBillingAllocation(v, v.AmountUSD))
		}
	}
	return out
}

// FundingReference identifies a creative run and its original reservation
// request after trimming whitespace. Capture and release use this reference to
// find the reserved funds even though their own action identifiers differ.
func FundingReference(id string) billing.TaskReference {
	id = strings.TrimSpace(id)
	return billing.TaskReference{Scope: FundingScope, ID: id, ReserveRequestID: CreativeHoldRequestID(id)}
}
