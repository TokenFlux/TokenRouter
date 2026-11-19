package testkit

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
)

// recording_stores.go captures completion storage calls and returns configured
// or synthesized billing outcomes. Log and settlement fixtures capture pointer
// inputs and the context's error at call time, while configured outcomes drive
// the production recorder's next decision.
//
// A settlement error wins before preset or default results. With no configured
// result, the fixture synthesizes an applied result and assigns the command's
// billable amount to its selected balance or subscription source. Log insertion
// flags, quota updates and rate observations remain separately controlled.
// These serial recorders retain log, command, provider and configured result
// pointers, so tests arrange ownership and ordering when inspecting or mutating
// them.

// BestEffortUsageLogStore captures both log write paths with separate
// configured errors.
type BestEffortUsageLogStore struct {
	usagecore.UsageLogRepository // unused inherited fixture repository operations

	BestEffortErr   error // injected best-effort log write error
	CreateErr       error // injected synchronous log write error
	BestEffortCalls int   // best-effort log creation count
	CreateCalls     int   // synchronous log creation count
	// LastLog retains the supplied usage pointer; later source mutations remain
	// visible here. Tests inspect the record after their writing path completes.
	LastLog    *usagecore.UsageLog // latest retained usage log pointer
	LastCtxErr error               // context error observed at call
}

// UsageLogStore captures synchronous log writes and supplies insertion and
// error results.
type UsageLogStore struct {
	usagecore.UsageLogRepository // unused inherited fixture repository operations

	Inserted bool  // configured log insertion result flag
	Err      error // injected fixture operation error
	Calls    int   // fixture operation invocation count
	// LastLog retains the supplied usage pointer; later source mutations remain
	// visible here. Tests inspect the record after their writing path completes.
	LastLog    *usagecore.UsageLog // latest retained usage log pointer
	LastCtxErr error               // context error observed at call
}

// SettlementStore captures commands and supplies configured or synthesized
// billing outcomes.
type SettlementStore struct {
	completion.Store // unused inherited fixture repository operations

	// Result is returned by reference when supplied after the Err check. Tests
	// keep that object stable while production recording consumes the preset outcome.
	Result *billing.UsageBillingApplyResult // configured billing apply result pointer
	Err    error                            // injected fixture operation error
	Calls  int                              // fixture operation invocation count
	// LastCmd retains the submitted command pointer for assertions after Apply.
	// The fixture makes no command snapshot, so tests control subsequent mutation.
	LastCmd    *billing.UsageBillingCommand // latest retained billing command pointer
	LastCtxErr error                        // context error observed at call
	// ResolveSub is returned directly for every user and group lookup. Tests
	// retain ownership of this configured subscription while production readers use it.
	ResolveSub   *billing.UserSubscription // configured subscription lookup result pointer
	ResolveCalls int                       // subscription resolution invocation count
}

// ProviderLookup counts lookup calls and returns one retained provider
// fixture.
type ProviderLookup struct {
	// Provider is returned directly for every requested identifier. Tests keep
	// the configured record stable while credential resolution consumes this fixture.
	Provider *providercore.Record // configured provider lookup result pointer
	Calls    int                  // fixture operation invocation count
}

// UserStore records deduction inputs and injects balance operation failures.
type UserStore struct {
	identity.UserRepository // unused inherited fixture repository operations

	DeductCalls int     // balance deduction invocation count
	DeductErr   error   // injected balance deduction error
	LastAmount  float64 // latest supplied USD amount
	LastCtxErr  error   // context error observed at call
}

// SubscriptionStore counts usage increments and injects their configured write
// error.
type SubscriptionStore struct {
	billing.UserSubscriptionRepository // unused inherited fixture repository operations

	IncrementCalls int   // subscription usage increment count
	IncrementErr   error // injected subscription increment error
	LastCtxErr     error // context error observed at call
}

// KeyQuotaUpdater counts quota, rate, and auth-invalidation updates with shared
// amount observations.
type KeyQuotaUpdater struct {
	QuotaCalls      int      // API Key quota update count
	RateLimitCalls  int      // API Key rate-usage update count
	InvalidatedKeys []string // API Key auth-cache invalidations
	Err             error    // injected fixture operation error
	// LastAmount is shared by quota and rate updates; whichever method runs last
	// replaces this observation. Their separate counters and context errors identify
	// which operations the production recorder attempted.
	LastAmount          float64 // latest supplied USD amount
	LastQuotaCtxErr     error   // context error during quota update
	LastRateLimitCtxErr error   // context error during rate update
}

// GroupRateStore counts user-group rate reads and supplies a retained
// multiplier.
type GroupRateStore struct {
	billing.UserGroupRateRepository // unused inherited fixture repository operations

	// Rate is returned by reference when Err is nil. A nil rate remains
	// absent so the production user-group resolver can choose its default multiplier.
	Rate  *float64 // configured user-group multiplier pointer
	Err   error    // injected fixture operation error
	Calls int      // fixture operation invocation count
}

// CreateBestEffort counts the attempted write and retains its log pointer and
// current context error. It returns BestEffortErr; tests inspect these serial
// observations after the production recording path returns.
func (s *BestEffortUsageLogStore) CreateBestEffort(ctx context.Context, log *usagecore.UsageLog) error {
	s.BestEffortCalls++
	s.LastLog = log
	s.LastCtxErr = ctx.Err()
	return s.BestEffortErr
}

// Create records synchronous log-write inputs and returns false with
// CreateErr. This fixture always reports no insertion, while its separate
// counter distinguishes a retry from an earlier best-effort call.
func (s *BestEffortUsageLogStore) Create(ctx context.Context, log *usagecore.UsageLog) (bool, error) {
	s.CreateCalls++
	s.LastLog = log
	s.LastCtxErr = ctx.Err()
	return false, s.CreateErr
}

// Create records the supplied log pointer and context state before returning
// Inserted and Err. Tests configure those outcomes independently and serialize
// pointer inspection with the production recording operation.
func (s *UsageLogStore) Create(ctx context.Context, log *usagecore.UsageLog) (bool, error) {
	s.Calls++
	s.LastLog = log
	s.LastCtxErr = ctx.Err()
	return s.Inserted, s.Err
}

// GetByID counts the call and returns the configured provider pointer for any
// requested identifier. Nil configured providers remain nil; this serial
// fixture delegates credential-parent decisions to production resolution.
func (s *ProviderLookup) GetByID(_ context.Context, _ int64) (*providercore.Record, error) {
	s.Calls++
	return s.Provider, nil
}

// Apply records the command and context state before selecting its configured
// or default billing result. Err returns first, and a configured Result is
// returned by reference for the caller's recording decision.
//
// Otherwise a fresh result reports Applied and assigns the command's
// BillableAmountUSD to subscription usage when BillingType selects
// subscription, or to balance for another type. A nil command produces an
// applied result with zero amounts. This fixture models the outcome consumed
// by completion tests; it does not persist balances or perform billing
// idempotency.
func (s *SettlementStore) Apply(ctx context.Context, cmd *billing.UsageBillingCommand) (*billing.UsageBillingApplyResult, error) {
	s.Calls++
	s.LastCmd = cmd
	s.LastCtxErr = ctx.Err()
	if s.Err != nil {
		return nil, s.Err
	}
	if s.Result != nil {
		return s.Result, nil
	}
	result := &billing.UsageBillingApplyResult{Applied: true}
	if cmd != nil {
		switch cmd.BillingType {
		case usagecore.BillingTypeSubscription:
			result.SubscriptionAmountUSD = cmd.BillableAmountUSD
		default:
			result.BalanceAmountUSD = cmd.BillableAmountUSD
		}
	}
	return result, nil
}

// ResolveUsableSubscriptionForGroup counts resolution and returns the
// configured subscription pointer regardless of user and group. Nil configured
// subscriptions remain nil; this fixture supplies funding-selection
// observations for completion tests.
func (s *SettlementStore) ResolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*billing.UserSubscription, error) {
	s.ResolveCalls++
	return s.ResolveSub, nil
}

// DeductBalance records the requested amount and context error, then returns
// DeductErr when configured. Successful calls return the amount as their
// balance result; tests use the observation to assert the attempted deduction.
func (s *UserStore) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	s.DeductCalls++
	s.LastAmount = amount
	s.LastCtxErr = ctx.Err()
	if s.DeductErr != nil {
		return 0, s.DeductErr
	}
	return amount, nil
}

// AdjustBalance panics when a production path attempts an unsupported balance
// adjustment. This fixture implements deduction for completion tests; callers
// provide another user repository when their scenario needs adjustment
// behavior.
func (s *UserStore) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

// SetBalance panics when a production path attempts unsupported absolute
// balance replacement. Tests using this fixture exercise deduction, and supply
// another repository when their scenario needs the replacement operation.
func (s *UserStore) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

// IncrementUsage counts the write and records its current context error before
// returning IncrementErr. The fixture accepts the subscription and amount
// arguments for the interface but retains no amount observation.
func (s *SubscriptionStore) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	s.IncrementCalls++
	s.LastCtxErr = ctx.Err()
	return s.IncrementErr
}

// UpdateQuotaUsed counts the quota update and stores its amount and context
// error before returning Err. LastAmount also receives rate updates, so tests
// inspect method-specific counters alongside the latest amount.
func (s *KeyQuotaUpdater) UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error {
	s.QuotaCalls++
	s.LastAmount = cost
	s.LastQuotaCtxErr = ctx.Err()
	return s.Err
}

// UpdateRateLimitUsage counts the rate update and stores its amount and
// context error before returning Err. LastAmount also receives quota updates;
// callers serialize both methods before interpreting their separate
// observations.
func (s *KeyQuotaUpdater) UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error {
	s.RateLimitCalls++
	s.LastAmount = cost
	s.LastRateLimitCtxErr = ctx.Err()
	return s.Err
}

// InvalidateAuthCacheByKey records credential cache invalidations triggered
// after committed key quota exhaustion.
func (s *KeyQuotaUpdater) InvalidateAuthCacheByKey(_ context.Context, key string) {
	s.InvalidatedKeys = append(s.InvalidatedKeys, key)
}

// GetByUserAndGroup counts the read and returns Err before the preset Rate
// pointer. A missing rate remains nil, and this fixture returns the same
// configured multiplier for every supplied user and group.
func (s *GroupRateStore) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	s.Calls++
	if s.Err != nil {
		return nil, s.Err
	}
	return s.Rate, nil
}
