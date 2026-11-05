package payment

import (
	"context"
	"time"
)

// checkout_ports.go defines checkout persistence operations and binds buyer,
// provider, audit, and timing callbacks.
// A checkout creates a local order before asking the selected provider to
// initiate payment, then stores the returned references. The injected
// persistence interface separates those two writes, while runtime callbacks
// supply the buyer profile, applicable WeChat credentials, provider
// construction, audit policy, and dependency timing.

// Buyer retains the account fields needed to validate and snapshot checkout.
type Buyer struct {
	ID int64 // buyer account identity
	// BalanceRechargeMultiplier applies to new balance orders when present.
	// Nil follows the current global setting; historical orders use saved credits.
	BalanceRechargeMultiplier *float64
	// Email: account email address; Username: account display name; Notes:
	// account administration notes; Status: account lifecycle state
	Email, Username, Notes, Status string
}

// CheckoutDraft contains checkout snapshots and inputs for local order
// persistence.
type CheckoutDraft struct {
	// Order holds the snapshot returned by BuildCheckoutDraft after cloning its
	// supported mutable values. CreateCheckout stores these values and allocates
	// the order-owned merchant and recharge identifiers.
	Order Order
	// MaxPending is the draft pending-order ceiling. ValidateCheckoutPending
	// substitutes ConfigDefaultMaxPendingOrders when it is nonpositive, counting
	// pending and provider-processing rows in the checkout store.
	MaxPending int
	// DailyLimit and LimitAmount are compared with prior paid purchases by
	// CreateCheckout. Balance history contributes PayAmount, subscription history
	// contributes Amount, and a nonpositive DailyLimit disables that check.
	// DailyLimit: daily purchase ceiling; LimitAmount: current purchase principal
	DailyLimit, LimitAmount float64
	// TimeoutMinutes is added to the store's current time when allocating
	// expiration. BuildCheckoutDraft supplies the configured timeout or
	// ConfigDefaultOrderTimeoutMin before persistence.
	TimeoutMinutes int
}

// CheckoutStore persists local checkout state before and after provider
// initiation.
type CheckoutStore interface {
	// CreateCheckout persists the draft and its order-owned recharge identifier
	// before returning the stored order. The PostgreSQL adapter owns a
	// transaction for pending/daily checks and both writes, then commits before
	// provider initiation. Those limit reads do not acquire a separate buyer
	// serialization lock. An error leaves provider invocation to be skipped by
	// CreateOrder; callers use the returned stored identity for the later
	// external call.
	CreateCheckout(context.Context, CheckoutDraft) (*Order, error)
	// PersistCheckoutResponse saves nonempty provider references and any returned
	// expiration on the existing order. The selected instance is nonnil; an empty
	// provider response is an error, and these writes occur after external
	// initiation.
	PersistCheckoutResponse(context.Context, int64, *InstanceSelection, *CreatePaymentResponse) (*Order, error)
	// FailCheckout marks the addressed order FAILED after initiation or response
	// persistence fails. The adapter update is unconditional on prior status; the
	// caller treats its error as best-effort cleanup.
	FailCheckout(context.Context, int64) error
	// CancelledCount counts ORDER_CANCELLED audit rows for an exact operator
	// since the inclusive time threshold. Checkout uses this history to limit
	// future creation, and query failure permits creation to continue.
	CancelledCount(context.Context, string, time.Time) (int, error)
}

// CheckoutRuntime supplies buyer, provider, OAuth, observation, and audit
// operations.
type CheckoutRuntime struct {
	// User returns a nonnil buyer on success for CreateOrder. The account state
	// and profile are read before the local order is persisted.
	User func(context.Context, int64) (*Buyer, error)
	// WeChatCredential supplies the MP AppID and secret for applicable official
	// WeChat flows. Selection and OAuth bootstrap require it when the request
	// needs those credentials.
	WeChatCredential func(context.Context) (string, string, error)
	// CreateProvider receives the selected implementation, instance identity, and
	// configuration after local checkout persistence. Constructor errors cause
	// CreateOrder to attempt FailCheckout on that stored order.
	CreateProvider func(string, string, map[string]string) (Provider, error)
	// RememberLocale is optional and runs after the buyer state check. It
	// receives the negotiated language before an order or OAuth response is
	// produced.
	RememberLocale func(context.Context, int64, string, string)
	// Audit records order creation after provider response persistence. Its
	// callback has no error result, so persistence or logging policy belongs to
	// its implementation.
	Audit func(context.Context, int64, string, string, map[string]any)
	// Observe returns a nonnil completion callback invoked after each measured
	// provider call returns. NewCheckout supplies a no-op pair when the callback
	// is omitted.
	Observe func(context.Context) func()
	Error   func(string, ...any) // checkout failure logger
	Now     func() time.Time     // cancel-limit observation clock
}

// Checkout validates purchase inputs and coordinates local order and provider
// creation.
type Checkout struct {
	store         CheckoutStore  // local checkout persistence
	configService *ConfigService // purchase configuration reader
	loadBalancer  LoadBalancer   // checkout instance selector
	// resume is shared with lifecycle recovery. Hosted return URLs can proceed
	// with no signer, while official WeChat OAuth bootstrap requires a configured
	// signing service.
	resume  *PaymentResumeService
	runtime CheckoutRuntime // injected checkout operations
}

// NewCheckout binds the supplied stores, selector, signer, and callbacks for
// order creation. Nil Error, Audit, and Observe callbacks become no-ops, and
// nil Now uses time.Now; buyer/provider and applicable OAuth callbacks remain
// caller-supplied.
func NewCheckout(store CheckoutStore, cfg *ConfigService, balancer LoadBalancer, resume *PaymentResumeService, runtime CheckoutRuntime) *Checkout {
	if runtime.Error == nil {
		runtime.Error = func(string, ...any) {}
	}
	if runtime.Audit == nil {
		runtime.Audit = func(context.Context, int64, string, string, map[string]any) {}
	}
	if runtime.Observe == nil {
		runtime.Observe = func(context.Context) func() { return func() {} }
	}
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	return &Checkout{store: store, configService: cfg, loadBalancer: balancer, resume: resume, runtime: runtime}
}
