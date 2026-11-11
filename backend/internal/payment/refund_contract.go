package payment

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// refund_contract.go defines refund plans, recovery receipts,
// transaction-bound entitlement operations, and workflow dependencies.
// Refund orchestration retains entitlement intent separately from the gateway
// currency amount and the recorded recovery quantities. A prepared receipt ties
// those actual deductions to the local order version and operation identity,
// while pending detail records compensation and the provider refund reference
// used by later queries.
//
// RefundStore owns short transactions and supplies transaction-bound rights to
// mutation callbacks. The workflow calls providers between those transactions,
// so external success can precede a failed local completion. Recovery
// therefore requires persisted identity and deduction facts before
// finalization; a missing boolean choice is different from an explicitly
// stored false. Ordinary observation callbacks have their own audit policy,
// while preparation and success records participate in the transaction that
// establishes refund progress.

// These labels describe the workflow result independently of its display text so
// callers can choose whether to refresh, recover, or ask for force confirmation.
const (
	RefundResultSucceeded     = "succeeded"      // final refund committed
	RefundResultPending       = "pending"        // channel settlement incomplete
	RefundResultFailed        = "failed"         // known channel failure
	RefundResultRequiresForce = "requires_force" // fresh force choice needed
)

// ErrRefundOutcomeUnknown marks an attempted channel refund whose outcome
// requires verification. The workflow retains its prepared deduction and
// recovery receipt when an adapter wraps this sentinel.
var ErrRefundOutcomeUnknown = errors.New("refund outcome is unknown")

// RefundPlan retains an order observation, refund intent, and mutable
// deduction results.
type RefundPlan struct {
	OrderID int64 // local payment order ID
	// Order is the observation used for status/time claims and entitlement
	// ownership. Callers keep OrderID consistent with Order.ID; the plan retains
	// this order pointer through its refund attempt.
	Order *Order
	// RefundAmount and GatewayAmount retain different purchase quantities.
	// Preparation uses order.Amount units for the local reversal and derives the
	// proportional gateway charge from order.PayAmount with currency rounding.
	RefundAmount  float64 // requested amount in order.Amount units
	GatewayAmount float64 // requested gateway currency amount
	Reason        string  // administrator refund reason text
	Force         bool    // forced entitlement recovery flag
	// DeductBalance, DeductionType, BalanceToDeduct, SubDaysToDeduct, and
	// SubscriptionID describe planned local recovery. Balance uses a wallet
	// quantity; subscription recovery uses days and an identified source-order
	// grant.
	// DeductBalance selects entitlement planning in PrepareRefund. False leaves
	// the initial deduction kind and quantities at none/zero, while gateway
	// refund intent remains in the plan.
	DeductBalance bool
	DeductionType string // local entitlement deduction kind
	// BalanceToDeduct starts as the planned wallet recovery and becomes the
	// actual deducted amount after successful preparation. The persisted receipt
	// uses that actual value for compensation and later recovery.
	BalanceToDeduct float64
	// SubDaysToDeduct retains the requested plan-day quantity.
	// ApplyRefundDeduction requires a positive SubscriptionID and can choose
	// revocation when adjustment would expire the grant; this value is not
	// recalculated afterward.
	SubDaysToDeduct  int
	SubscriptionID   int64 // order-linked grant identity
	SubscriptionHold bool  // use refund suspension
	// VerifyLegacyRevocation requires the already-applied historical revocation
	// to be verified before an uncompensated pending refund is marked successful.
	VerifyLegacyRevocation bool
	// OperationID correlates committed local preparation and audit records.
	// Providers receive their own order/amount identifiers for idempotency; the
	// local operation ID remains outside their request encoding.
	OperationID     string
	ChannelRefundID string // provider refund identity for recovery
}

// RefundResult reports local finalization or a warning requiring a caller
// decision.
type RefundResult struct {
	Status string `json:"status,omitempty"` // structured workflow outcome
	// Success reports that this call accepted local finalization. A warning
	// result can follow preparation requiring force or an external refund attempt
	// that remains pending or failed.
	Success bool `json:"success"`
	// caller-visible refund decision explanation
	Warning string `json:"warning,omitempty"`
	// RequireForce reports that preparation needs a force decision before
	// execution. The caller inspects Warning and submits the chosen force flag on
	// a later preparation attempt.
	RequireForce bool `json:"require_force,omitempty"`
	// actual wallet amount recovered
	BalanceDeducted float64 `json:"balance_deducted,omitempty"`
	// reported subscription recovery days
	SubDaysDeducted int `json:"subscription_days_deducted,omitempty"`
}

// RefundPendingDetail records provider identity and entitlement intent for
// pending recovery.
type RefundPendingDetail struct {
	ObservedAt time.Time `json:"-"`        // local pending audit time
	RefundID   string    `json:"refundID"` // provider refund operation identity
	// DeductBalance retains the administrator's entitlement recovery choice,
	// including false. PendingDetail requires this JSON field to be present, and
	// a missing or null value demands manual verification.
	DeductBalance bool `json:"deductBalance"`
	// recorded entitlement deduction kind
	DeductionType string `json:"deductionType"`
	// recorded wallet amount recovered
	BalanceDeducted float64 `json:"balanceDeducted"`
	// recorded subscription recovery days
	SubDaysDeducted int `json:"subDaysDeducted"`
	// recorded order-linked grant identity
	SubscriptionHold bool  `json:"subscriptionHold,omitempty"` // version two suspension
	SubscriptionID   int64 `json:"subscriptionID"`
	// DeductionRollbackOK records whether preparation deductions were
	// compensated. Legacy PendingDetail decoding starts with true when this field
	// is omitted; an explicitly stored false remains false.
	DeductionRollbackOK bool `json:"deductionRollbackOK"`
	// OperationID names the local preparation for versioned pending records.
	// Legacy records can omit it, while a supplied identity requires matching
	// persisted preparation data during recovery.
	OperationID string `json:"operationID,omitempty"`
}

// RefundReceipt records committed preparation identity, wallet recovery, and
// subscription plan quantities.
type RefundReceipt struct {
	// Version is one for receipts emitted by PrepareRefund. The persisted
	// preparation reader rejects other format values before its deduction data is
	// used for recovery.
	Version int `json:"version"`
	// OperationID identifies the committed preparation compared during completion
	// or compensation. This outer field records receipt identity independently of
	// the embedded pending-detail operation field.
	OperationID string `json:"operationID"`
	OrderID     int64  `json:"orderID"` // prepared local order identity
	// OperationVersion is the persisted order updated_at read inside preparation.
	// Recovery and subsequent transaction claims compare it with the current
	// order state to reject an intervening change.
	OperationVersion time.Time `json:"operationVersion"`
	// order state before preparation
	PreviousStatus string `json:"previousStatus"`
	// prepared order.Amount refund quantity
	RefundAmount float64 `json:"refundAmount"`
	// prepared gateway currency refund amount
	GatewayAmount float64 `json:"gatewayAmount"`
	// prepared administrator reason text
	Reason              string `json:"reason"`
	Force               bool   `json:"force"` // prepared force recovery flag
	RefundPendingDetail        // prepared entitlement deduction facts
}

// RefundRights changes entitlements through the transaction supplied and
// completed by RefundStore.
type RefundRights interface {
	// HoldSubscription selects and suspends the exact user and order grant under
	// the transaction locks. A missing or ineligible grant returns the billing
	// not-found error, while a conflicting hold or storage failure propagates.
	HoldSubscription(context.Context, int64, int64, string) (int64, error)
	// ResolveSubscriptionHold clears the operation-owned hold and optionally
	// revokes the grant in the same transaction. A mismatch leaves all rights
	// untouched so another refund or operator action cannot be overwritten.
	ResolveSubscriptionHold(context.Context, int64, string, bool) error
	// VerifyRevokedSubscription confirms the source grant of a legacy refund
	// is already revoked before finalization records financial success.
	VerifyRevokedSubscription(context.Context, int64, int64, int64) error
	// DeductBalance returns the quantity selected for wallet recovery in the
	// supplied transaction. Production billing locks a non-deleted user's raw
	// balance and caps a finite nonnegative request at its positive value;
	// nonpositive funds yield zero.
	//
	// The request must be compatible with stored numeric precision because the
	// selected deduction is reported before column rounding. RefundStore records
	// that quantity for compensation and owns transaction completion; frozen
	// balance is outside this cap calculation.
	DeductBalance(context.Context, int64, float64) (float64, error)
	// CompensateBalance restores a prior actual wallet deduction in the selected
	// transaction. It restores available balance without increasing cumulative
	// recharges. The outer store owns the commit.
	CompensateBalance(context.Context, int64, float64) error
	// AdjustSubscription changes the issued subscription by a signed day delta
	// inside the selected transaction. A deduction that would expire it returns
	// billing.ErrAdjustWouldExpire, letting ApplyRefundDeduction choose
	// revocation.
	AdjustSubscription(context.Context, int64, int) error
	// RevokeSubscription revokes the addressed grant in the supplied refund
	// transaction. ApplyRefundDeduction uses it after an adjustment would expire
	// the grant; the store remains responsible for committing or recovering that
	// transaction.
	RevokeSubscription(context.Context, int64) error
}

// RefundMutation applies or compensates a plan through transaction-bound
// entitlement operations.
type RefundMutation func(context.Context, RefundRights, *RefundPlan) error

// RefundStore owns the short transactions and recovery records used by refund
// orchestration.
type RefundStore interface {
	// RequestRefund marks the supplied user's completed balance order
	// refund-requested and records request details. Its affected count reports
	// whether the ownership/type/status conditions applied; it performs no
	// provider refund or entitlement deduction.
	RequestRefund(context.Context, int64, int64, float64, string, time.Time, string) (int, error)
	// Order reads the persisted payment order by its local numeric ID.
	Order(context.Context, int64) (*Order, error)
	// PrepareRefund claims the observed eligible order as REFUNDING and executes
	// the nonnil deduction callback in an owned transaction. It records actual
	// wallet deduction and subscription recovery quantities with a mandatory preparation receipt before commit. The
	// caller's plan receives successful local mutation results after commit.
	// Legacy rollback-failure history or contradictory deduction data requires
	// manual verification. ExecuteRefund invokes the provider only after this
	// operation returns its committed receipt successfully.
	PrepareRefund(context.Context, *RefundPlan, RefundMutation) (*RefundReceipt, error)
	// RecordRefundProviderID binds an observed channel reference to a still-current
	// preparation. Its commit precedes entitlement finalization so a later local
	// failure can query the same channel operation.
	RecordRefundProviderID(context.Context, *RefundReceipt, string) error
	// CompleteRefund claims the prepared receipt or pending operation identity
	// and writes final refund state with a mandatory success audit in one
	// transaction. A supplied receipt identifies deductions already performed; an
	// optional callback can instead recover compensated pending rights in that
	// transaction. The plan receives mutation results after successful commit.
	// Competing status/version or operation identity returns conflict before
	// those entitlement changes are accepted.
	CompleteRefund(context.Context, *RefundPlan, *RefundReceipt, RefundMutation) (*RefundResult, error)
	// CompensateRefund verifies the prepared receipt, executes its nonnil
	// compensation callback, and writes pending or gateway-failed recovery data
	// in an owned transaction. Empty failure text selects pending state and
	// stores provider refund identity with compensated deduction facts. Nonempty
	// failure text restores completed or refund-requested state according to
	// preparation. Required audit and compensation failures return before commit,
	// retaining the earlier committed preparation for later recovery.
	CompensateRefund(context.Context, *RefundPlan, *RefundReceipt, string, string, RefundMutation) error
	// FailPendingRefund records provider failure only while pending status,
	// timestamp and both operation identities match the supplied observation.
	// The production store locks and rereads the order, then writes failure
	// state and required audit before one commit.
	//
	// A changed order is returned in its current state after the observation
	// transaction commits, so query recovery can recognize a newer refund
	// success. Errors retain the store's rollback cleanup; callers inspect the
	// returned status before reporting gateway failure.
	FailPendingRefund(context.Context, *Order, RefundPendingDetail, string) (*Order, error)
	// RefundRecovery returns a valid preparation receipt for a supplied
	// REFUNDING order observation. It compares recorded timestamp, refund amount,
	// force choice and nonnil reason with that order before exposing earlier
	// deduction facts for query recovery.
	//
	// Missing, malformed or contradictory evidence returns RefundRecoveryRequired,
	// so callers cannot infer that absent preparation means zero prior deduction.
	// This read does not claim or lock the order; completion and compensation
	// recheck its state/version and preparation identity in their own transactions.
	RefundRecovery(context.Context, *Order) (*RefundReceipt, error)
	// PendingDetail decodes the latest pending audit and returns the entitlement
	// choice and operation identities needed for refund recovery. Legacy records
	// retain explicitly false deduction intent and default omitted compensation
	// success to true; missing or null deduction intent requires manual
	// verification.
	//
	// A supplied version and any nonempty operation ID require supported format
	// and matching preparation data. Malformed or contradictory amounts,
	// deduction kind or grant identity return a recovery error before
	// QueryAndFinalizeRefund can use a channel result for local rights changes.
	PendingDetail(context.Context, int64) (RefundPendingDetail, error)
}

// RefundUser retains the raw current wallet balance observed during refund
// planning before the transaction locks the user's funds.
type RefundUser struct {
	Balance float64 // observed raw wallet balance
}

// RefundRuntime supplies current rights observations, historical provider
// resolution, audit, and timing.
type RefundRuntime struct {
	// User supplies a nonnil successful balance observation for request and
	// deduction preparation. Forced preparation can continue after a failed read,
	// leaving its deduction plan unchanged.
	User func(context.Context, int64) (*RefundUser, error)
	// Instance resolves the saved historical provider binding for refund
	// eligibility. A guessed legacy instance does not satisfy that path; the
	// flags come from the resolved current instance.
	Instance func(context.Context, *Order) (*ProviderInstance, error)
	Warn     func(string, ...any) // refund planning warning logger
	Now      func() time.Time     // refund request observation clock
	// Provider constructs the adapter resolved for the order's historical
	// instance. Gateway refund and query use this callback after persisted local
	// preparation or pending recovery reads.
	Provider func(context.Context, *Order) (Provider, error)
	// Observe returns a nonnil completion callback for the measured provider
	// call. NewRefundWorkflow supplies the no-op pair when dependency observation
	// is omitted.
	Observe func(context.Context) func()
	// Audit records ordinary request, provider, and query observations through an
	// errorless callback. Mandatory preparation and success records remain owned
	// by RefundStore and can fail the transaction.
	Audit func(context.Context, int64, string, string, map[string]any)
}

// RefundWorkflow coordinates preparation, provider invocation, compensation,
// and local refund finalization.
type RefundWorkflow struct {
	store   RefundStore   // short refund transaction persistence
	runtime RefundRuntime // injected refund planning operations
}

// NewRefundWorkflow binds persistence and the supplied refund operations
// without starting work. Missing Warn and Audit callbacks become no-ops,
// missing Now uses time.Now, and missing Observe supplies a no-op completion
// callback; rights/provider readers remain caller-supplied.
func NewRefundWorkflow(store RefundStore, runtime RefundRuntime) *RefundWorkflow {
	if runtime.Warn == nil {
		runtime.Warn = func(string, ...any) {}
	}
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	if runtime.Observe == nil {
		runtime.Observe = func(context.Context) func() { return func() {} }
	}
	if runtime.Audit == nil {
		runtime.Audit = func(context.Context, int64, string, string, map[string]any) {}
	}
	return &RefundWorkflow{store: store, runtime: runtime}
}

// RefundRecoveryRequired returns the manual-verification conflict with its
// diagnostic reason metadata. Recovery callers use this error when persisted
// progress or a provider observation cannot support safe local finalization.
func RefundRecoveryRequired(reason string) error {
	return apperror.Conflict("REFUND_RECOVERY_REQUIRED", "refund recovery requires manual verification").WithMetadata(map[string]string{"reason": reason})
}
