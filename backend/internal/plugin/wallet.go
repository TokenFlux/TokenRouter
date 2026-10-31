package plugin

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"
)

// wallet.go applies plugin balance credits and caller-authorized debits through
// the shared ledger transaction. Requests normalize their monetary amount before
// forming replay identity, while temporary caller grants stay outside that
// identity. A debit requires a current grant matching both plugin and target
// user, including when retrieving an already committed debit result.
//
// Credit ceilings cover one operation, one recipient's host-calendar day, and
// the plugin's aggregate day. Debit has its separate operation ceiling. All
// quota checks and balance changes occur under the ledger lock and transaction.
// Cache invalidation and adjustment history run only after a new commit, with
// an independent bounded context; their failure remains diagnostic and cannot
// turn a successful credit into a second financial operation on retry.

// WalletInput carries one caller-supplied financial operation before normalization.
type WalletInput struct {
	UserID int64  // target account identifier
	Amount string // positive decimal request amount
	Key    string // plugin-wide operation replay key
	Reason string // user-visible adjustment explanation
	Grant  string // optional or debit-required caller authorization
}

// WalletChange preserves the balance pair returned by the existing atomic adjustment.
type WalletChange struct {
	Before   string `json:"balance_before"` // displayed prior balance amount
	After    string `json:"balance_after"`  // displayed resulting balance amount
	Replayed bool   `json:"replayed"`       // original recorded result was reused
}

// WalletEffects adapts existing billing mutation and post-commit cache/history operations.
type WalletEffects interface {
	// AdjustBalance atomically applies a signed delta using the supplied ledger transaction.
	// It must preserve cumulative recharge totals and return the actual before/after
	// balances, mapping absent users and insufficient funds to plugin capability errors.
	AdjustBalance(context.Context, int64, float64) (WalletChange, error)
	// AfterWallet invalidates authentication and balance caches and records plugin adjustment history.
	// It runs after commit with an independent bounded context. Errors are diagnostic;
	// implementations must not repeat the balance mutation or trigger affiliate rewards.
	AfterWallet(context.Context, int64, float64, string) error
}

// WalletService validates financial authority and coordinates ledger-backed balance changes.
type WalletService struct {
	writes   *Writes        // shared replay and transaction engine
	grants   *Grants        // plugin-scoped caller authorization verifier
	effects  WalletEffects  // existing billing and auxiliary-effect adapter
	location *time.Location // host calendar used for daily quotas
	logger   *slog.Logger   // post-commit diagnostic logging destination
}

// NewWalletService binds existing billing operations to plugin authorization and quota policy.
// A nil timezone uses the application's time.Local, and a nil logger uses slog.
// Construction performs no database operation and starts no worker or subprocess.
func NewWalletService(writes *Writes, grants *Grants, effects WalletEffects, location *time.Location, logger *slog.Logger) *WalletService {
	if logger == nil {
		logger = slog.Default()
	}
	if location == nil {
		location = time.Local
	}
	return &WalletService{writes: writes, grants: grants, effects: effects, location: location, logger: logger}
}

// Credit applies an authorized positive balance credit without increasing cumulative recharge.
// The binding must grant wallet.credit. An optional caller grant affects later
// audit attribution but is not required to credit an arbitrary existing user.
func (s *WalletService) Credit(ctx context.Context, binding Binding, input WalletInput) (*WalletChange, error) {
	if !slices.Contains(binding.Permissions, "wallet.credit") {
		return nil, ErrPermissionNotGranted
	}
	return s.adjust(ctx, binding, input, LedgerWalletCredit)
}

// Debit applies a positive requested debit only with the target user's current grant.
// The binding must grant wallet.debit. Expired or mismatched grants fail even
// for replayed operations, allowing retries with a newly issued matching grant.
func (s *WalletService) Debit(ctx context.Context, binding Binding, input WalletInput) (*WalletChange, error) {
	if !slices.Contains(binding.Permissions, "wallet.debit") {
		return nil, ErrPermissionNotGranted
	}
	return s.adjust(ctx, binding, input, LedgerWalletDebit)
}

// adjust normalizes intent, applies quotas, and commits the signed balance effect once.
// Its callers have already checked capability permission. Debit grants are checked
// after parameter validation and before the transaction begins. A replay
// returns the stored balance pair even when the user's current balance has since
// changed, and never repeats cache invalidation or adjustment-history insertion.
//
// The business callback receives the ledger's transaction context, keeping balance
// mutation and replay evidence atomic. Auxiliary effects use the original request's
// values but a fresh cancellation budget after commit. Their failure is logged
// without changing the committed response, preserving safe retries when a plugin
// loses the original RPC reply or a cache service is temporarily unavailable.
func (s *WalletService) adjust(ctx context.Context, binding Binding, input WalletInput, kind LedgerKind) (*WalletChange, error) {
	if input.UserID <= 0 {
		return nil, ErrInvalidArgument
	}
	if err := validateOperation(input.Key, input.Reason); err != nil {
		return nil, err
	}
	amount, err := ParseAmount(input.Amount)
	if err != nil {
		return nil, err
	}
	if kind == LedgerWalletDebit {
		if err := s.grants.Verify(binding.PluginID, input.UserID, input.Grant); err != nil {
			return nil, err
		}
	}
	intent := struct {
		UserID int64  `json:"user_id"` // target account identifier
		Amount string `json:"amount"`  // canonical positive decimal amount
		Reason string `json:"reason"`  // exact operation explanation text
	}{UserID: input.UserID, Amount: amount.String(), Reason: input.Reason}
	request, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	delta := amount.Float64()
	if kind == LedgerWalletDebit {
		delta = -delta
	}
	mutation := Mutation{Key: input.Key, Kind: kind, UserID: &input.UserID, Amount: amount.String(), Request: request, State: LedgerCompleted, Reason: input.Reason}
	result, err := s.writes.Apply(ctx, binding, mutation, func(bound context.Context, tx LedgerTransaction, limits Limits, now time.Time) error {
		return s.checkQuota(bound, tx, limits.Wallet, now, input.UserID, amount, kind)
	}, func(bound context.Context) (json.RawMessage, error) {
		change, err := s.effects.AdjustBalance(bound, input.UserID, delta)
		if err != nil {
			return nil, err
		}
		return json.Marshal(change)
	})
	if err != nil {
		return nil, err
	}
	var change WalletChange
	if err := json.Unmarshal(result.Entry.Result, &change); err != nil {
		return nil, err
	}
	change.Replayed = result.Replayed
	if !result.Replayed {
		// Balance and ledger have committed; cleanup retains tracing values while receiving its own finite budget independent of plugin cancellation.
		after, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err := s.effects.AfterWallet(after, input.UserID, delta, "["+binding.PluginID+"] "+input.Reason)
		if err != nil {
			s.logger.Warn("Plugin balance post-commit effects failed", "plugin_id", binding.PluginID, "user_id", input.UserID, "error", err)
		}
	}
	return &change, nil
}

// checkQuota enforces operation and calendar-day credit ceilings under the ledger lock.
// Debits use only their separate operation ceiling. Every comparison uses exact
// decimal arithmetic, and failures identify the administrator's corresponding policy key.
//
// Daily windows come from the accounting instant captured after lock acquisition.
// Per-user and aggregate observations therefore describe the same calendar day
// and transaction snapshot. Replayed operations never enter this checker because
// their original recorded result has already determined the accepted financial effect.
func (s *WalletService) checkQuota(ctx context.Context, tx LedgerTransaction, limits WalletLimits, now time.Time, userID int64, amount Amount, kind LedgerKind) error {
	if kind == LedgerWalletDebit {
		if exceedsAmount("0", amount, limits.MaxDebitPerOp) {
			return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "wallet.max_debit_per_op"})
		}
		return nil
	}
	if exceedsAmount("0", amount, limits.MaxCreditPerOp) {
		return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "wallet.max_credit_per_op"})
	}
	since, until := dayWindow(now, s.location)
	query := LedgerQuery{Kind: kind, UserID: userID, Since: since, Until: until}
	user, err := tx.Totals(ctx, query)
	if err != nil {
		return err
	}
	if exceedsAmount(user.Amount, amount, limits.MaxCreditPerUserPerDay) {
		return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "wallet.max_credit_per_user_per_day"})
	}
	query.UserID = 0
	all, err := tx.Totals(ctx, query)
	if err != nil {
		return err
	}
	if exceedsAmount(all.Amount, amount, limits.MaxCreditPerDay) {
		return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "wallet.max_credit_per_day"})
	}
	return nil
}
