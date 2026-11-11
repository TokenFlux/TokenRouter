package postgres

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// participants.go binds billing entitlement operations to caller-owned Ent
// transactions.
// Payment, promotion and identity flows can change billing entitlements inside
// an Ent transaction they already own. These participants bind balance,
// subscription and redemption operations to that transaction client, leaving
// commit, rollback and post-commit publication with the outer use case.
//
// RedeemParticipant carries its transaction into each balance/concurrency
// mutation call. An optional concurrency writer supplies the identity-owned
// update, while balance changes use the bound balance store. Refund deductions
// cap the requested amount at positive raw balance, and compensation uses the
// balance-only credit path so restoring previously held money does not increase
// the cumulative recharge total.

// RedeemParticipant binds redemption balance and concurrency mutations to one
// caller-owned Ent transaction.
type RedeemParticipant struct {
	// tx is the caller's live Ent transaction. ApplyBalance and ApplyConcurrency
	// install it into each operation context, and the outer caller controls
	// completion.
	tx        *dbent.Tx        // bound redemption participation transaction
	mutations *RedeemMutations // bound entitlement mutation adapter
}

// BalanceInTx returns a balance store bound to the non-nil transaction client.
// The caller owns commit, rollback and post-commit effects; a later context
// transaction can still take precedence in BalanceStore methods.
func BalanceInTx(tx *dbent.Tx) *BalanceStore {
	return NewBalanceStore(tx.Client())
}

// SubscriptionsInTx constructs subscription reads, writes and mutation support
// from a non-nil transaction client. Group reads and the supplied clock remain
// injected dependencies, while the caller owns commit, rollback and
// post-commit entitlement effects.
func SubscriptionsInTx(tx *dbent.Tx, groups billing.SubscriptionGroupReader, clock billing.DateRuntime) *billing.SubscriptionService {
	return billing.NewSubscriptionService(groups, NewUserSubscriptionRepository(tx.Client()), SubscriptionMutationsInTx(tx), clock)
}

// RedeemInTx returns balance/concurrency participation for a non-nil caller
// transaction. Only the first optional concurrency writer is retained; absent
// concurrency support returns its domain error, and the caller owns
// transaction completion and later cache/notification effects.
func RedeemInTx(tx *dbent.Tx, concurrency ...RedeemConcurrencyWriter) *RedeemParticipant {
	var writer RedeemConcurrencyWriter
	if len(concurrency) > 0 {
		writer = concurrency[0]
	}
	return &RedeemParticipant{tx: tx, mutations: NewRedeemMutations(tx.Client(), RedeemWriters{Balances: BalanceInTx(tx), Concurrency: writer})}
}

// ApplyBalance forwards the entitlement amount with this participant's Ent
// transaction in context. That transaction replaces any incoming Ent
// transaction selection; the caller keeps it open and owns commit or rollback.
func (p *RedeemParticipant) ApplyBalance(ctx context.Context, id int64, amount float64) error {
	return p.mutations.ApplyBalance(dbent.NewTxContext(ctx, p.tx), id, amount)
}

// ApplyConcurrency forwards the delta with this participant's Ent transaction
// in context. Negative changes need the mutation adapter's floor capability,
// and the caller keeps the bound transaction open until all outer writes
// complete.
func (p *RedeemParticipant) ApplyConcurrency(ctx context.Context, id int64, delta int) error {
	return p.mutations.ApplyConcurrency(dbent.NewTxContext(ctx, p.tx), id, delta)
}

// DeductRefundBalance returns the amount deducted from positive raw user
// balance through the context-selected client. The caller supplies a
// nonnegative finite amount and owns any transaction; existing nonpositive
// funds yield no deduction.
func (r *BalanceStore) DeductRefundBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	_, deducted, err := DeductAvailableBalance(ctx, clientFromContext(ctx, r.client), id, amount)
	return deducted, err
}

// CompensateRefundBalance restores the previously deducted amount without changing
// the recharge total. It uses the context-selected transaction, whose owner
// commits the restoration together with the refund state.
func (r *BalanceStore) CompensateRefundBalance(ctx context.Context, id int64, amount float64) error {
	return r.AddBalance(ctx, id, amount)
}
