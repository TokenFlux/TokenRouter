package postgres

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/apikey"
	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/ent/usersubscription"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// subscription_transactions.go supplies transaction scopes, row locks and key
// rebinding for subscription mutations.
// Subscription grants and maintenance coordinate on the recipient's user row
// before locking a subscription row. Mutation adapters choose a bound
// transaction first, then context's transaction, and create an owned
// transaction only when neither exists and a client is configured. Reused
// transaction callers retain commit/rollback ownership.
//
// Without persistence the callback runs directly and lock helpers return
// success for fixtures. Production lock helpers protect subsequent service
// rereads rather than return a complete locked grant view themselves. Key
// rebinding updates nondeleted Keys already using the old preferred
// subscription; its base-client guard can still return zero even if context
// contains a transaction. These choices keep transaction lifetime and the
// service's eligibility decisions distinct.

// SubscriptionMutations binds transaction selection, ordered grant locks and
// preferred-Key rebinding for services.
type SubscriptionMutations struct {
	// client and bound determine transaction availability together. bound wins
	// context selection; otherwise client starts an owned transaction, and a nil
	// client with no selected transaction runs fixture callbacks directly.
	client *dbent.Client // base Ent persistence client
	// bound has precedence over a transaction carried in context.
	// SubscriptionMutationsInTx requires a nonnil transaction; the binding's
	// caller retains its commit and rollback ownership.
	bound *dbent.Tx // optional caller-owned Ent transaction
}

// NewSubscriptionMutations retains a base client without opening a transaction
// or acquiring locks. A nil client enables direct callbacks when context also
// lacks a transaction; service construction starts no persistence work.
func NewSubscriptionMutations(client *dbent.Client) *SubscriptionMutations {
	return &SubscriptionMutations{client: client}
}

// SubscriptionMutationsInTx binds a nonnil caller-owned transaction and its
// client for entitlement operations. That binding wins context transaction
// selection; the caller commits or rolls back after all joined operations
// finish.
func SubscriptionMutationsInTx(tx *dbent.Tx) *SubscriptionMutations {
	return &SubscriptionMutations{bound: tx, client: tx.Client()}
}

// transaction selects the adapter's bound transaction before reading context's
// Ent transaction. It returns nil when neither is present; methods then
// determine whether to start owned work, use the base client or skip fixture
// locking.
func (m *SubscriptionMutations) transaction(ctx context.Context) *dbent.Tx {
	// A bound adapter stays on its caller's transaction even if the incoming
	// context carries another one; all subsequent locks and reads use this
	// choice.
	if m.bound != nil {
		return m.bound
	}
	return dbent.TxFromContext(ctx)
}

// HasPersistence reports a selected transaction or configured base client for
// later mutation work. It performs no database probe, so true indicates
// available bindings rather than connection health or an already acquired row
// lock.
func (m *SubscriptionMutations) HasPersistence(ctx context.Context) bool {
	return m.transaction(ctx) != nil || m.client != nil
}

// Within runs a nonnil callback in the selected transaction or starts an owned
// transaction from the base client. Bound/context transactions are passed
// through a replacement Ent transaction context, and their caller retains
// commit and rollback ownership. If neither transaction nor client exists, the
// callback runs with the original context.
//
// Owned work begins before the callback and commits after its success;
// callback failure triggers deferred rollback and propagates unchanged. Begin
// and commit failures are wrapped, while rollback cleanup errors remain
// secondary. Reusing a transaction creates no savepoint, so a callback error
// alone does not undo writes already made in that outer transaction.
func (m *SubscriptionMutations) Within(ctx context.Context, fn func(context.Context) error) error {
	// Joined work receives the selected transaction context, with no inner commit
	// or savepoint rollback; its caller decides whether an error aborts outer
	// work.
	if tx := m.transaction(ctx); tx != nil {
		return fn(dbent.NewTxContext(ctx, tx))
	}
	// Fixtures without any persistence binding execute directly, so their
	// callback can exercise service rules while these adapters acquire no
	// database transaction or locks.
	if m.client == nil {
		return fn(ctx)
	}
	tx, err := m.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Rollback owns cleanup for callback or commit failure. Its error is
	// discarded so it cannot replace the operation's primary result after
	// transaction work.
	defer func() { _ = tx.Rollback() }()
	if err := fn(dbent.NewTxContext(ctx, tx)); err != nil {
		return err
	}
	// Commit only after the callback's writes succeed; joined transactions take
	// the earlier path and leave publication ownership with their caller.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// GetPlan returns a converted plan using the selected transaction's client
// before the base client. No client produces an error; Ent lookup errors
// propagate, and the method performs no plan-sale or grant eligibility
// filtering.
func (m *SubscriptionMutations) GetPlan(ctx context.Context, id int64) (*billing.SubscriptionPlan, error) {
	// Use the selected transaction client so this grant can see plan writes made
	// within its ongoing transaction; otherwise read through the configured base
	// client.
	client := m.client
	if tx := m.transaction(ctx); tx != nil {
		client = tx.Client()
	}
	if client == nil {
		return nil, fmt.Errorf("ent client is nil")
	}
	plan, err := client.SubscriptionPlan.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return PlanFromEntity(plan), nil
}

// LockGrantUser acquires FOR UPDATE on the supplied recipient in the selected
// transaction. No transaction returns nil without locking; service callers
// invoke it inside Within, and query failures retain the wrapped user
// ID/cause.
func (m *SubscriptionMutations) LockGrantUser(ctx context.Context, id int64) error {
	tx := m.transaction(ctx)
	if tx == nil {
		return nil
	}
	if _, err := tx.User.Query().Where(dbuser.IDEQ(id)).ForUpdate().Only(ctx); err != nil {
		return fmt.Errorf("lock user %d: %w", id, err)
	}
	return nil
}

// LockSelfRevoke locks the supplied recipient user and then the target
// subscription row within the selected transaction. The caller establishes
// that these IDs describe the same owned grant; this helper's subscription
// query filters record ID alone. No selected transaction returns nil without
// locking.
//
// User-lock failures propagate before the subscription query. Every
// subscription-lock query failure becomes ErrSubscriptionNotFound, including
// storage failures. Successful locks remain held by transaction ownership
// while the service rereads status and quota before any revoke, chain shift or
// Key rebind writes.
func (m *SubscriptionMutations) LockSelfRevoke(ctx context.Context, userID, subscriptionID int64) error {
	tx := m.transaction(ctx)
	if tx == nil {
		return nil
	}
	// Acquire the recipient lock before the grant row so self-revocation follows
	// the same user-first order as issuance and later subscription maintenance.
	if err := m.LockGrantUser(ctx, userID); err != nil {
		return err
	}
	if _, err := tx.UserSubscription.Query().Where(usersubscription.IDEQ(subscriptionID)).ForUpdate().Only(ctx); err != nil {
		// Subscription-lock failures all become the not-found result here, so the
		// service does not receive the query's original storage error
		// classification.
		return billing.ErrSubscriptionNotFound
	}
	return nil
}

// RebindKeys updates nondeleted subscription-mode Keys pointing to oldID and
// returns the affected count. Nonpositive IDs or a missing base client return
// zero even with context transaction binding; otherwise the selected
// transaction's client wins. The service caller owns eligibility, transaction
// commit and cache invalidation.
func (m *SubscriptionMutations) RebindKeys(ctx context.Context, oldID, newID int64) (int, error) {
	if m.client == nil || oldID <= 0 || newID <= 0 {
		return 0, nil
	}
	// Use the selected transaction client for rebinding so joined revocation
	// keeps these Key changes in its caller's unit of work; otherwise the base
	// client executes them.
	client := m.client
	if tx := m.transaction(ctx); tx != nil {
		client = tx.Client()
	}
	return client.APIKey.Update().Where(apikey.BillingModeEQ(billing.APIKeyBillingModeSubscription), apikey.PreferredSubscriptionIDEQ(oldID), apikey.DeletedAtIsNil()).SetPreferredSubscriptionID(newID).SetUpdatedAt(time.Now()).Save(ctx)
}

// LockSubscription reads the target's owner ID, locks that user, then locks
// the subscription record in the selected transaction. The initial read only
// discovers which user to serialize; service callbacks reread the full grant
// after successful locks before applying their mutation. No transaction
// returns success without locking.
//
// This user-before-subscription order follows grant and self-revocation
// operations and coordinates with settlement's payer-first locking. Missing
// rows translate to ErrSubscriptionNotFound, other persistence failures retain
// their translated classification, and a failed user lock stops before the
// subscription lock. Transaction ownership controls when successful locks are
// released.
func (m *SubscriptionMutations) LockSubscription(ctx context.Context, id int64) error {
	tx := m.transaction(ctx)
	if tx == nil {
		return nil
	}
	// Read just the owner before locking, then serialize that user before the
	// grant. The caller rereads full subscription state after both locks succeed.
	sub, err := tx.Client().UserSubscription.Query().Where(usersubscription.IDEQ(id)).Select(usersubscription.FieldUserID).Only(ctx)
	if err != nil {
		return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
	}
	if err := m.LockGrantUser(ctx, sub.UserID); err != nil {
		return err
	}
	_, err = tx.UserSubscription.Query().Where(usersubscription.IDEQ(id)).ForUpdate().Only(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
}

// HoldRefundSubscription locks the owner and the exact purchase grant before
// writing the refund marker. A missing or expired grant returns not-found for
// the caller's force decision. An already held grant or ambiguous source
// blocks the refund because a second operation cannot safely claim it.
//
// The caller supplies the transaction and operation identity that it records
// with the payment order. The hold changes neither quota nor renewal dates,
// so the grant keeps its position in the chain while funding excludes it.
func (m *SubscriptionMutations) HoldRefundSubscription(ctx context.Context, userID, orderID int64, operationID string) (int64, error) {
	tx := m.transaction(ctx)
	if tx == nil || operationID == "" {
		return 0, fmt.Errorf("refund hold requires a transaction and operation identity")
	}
	// Match issuance and settlement's user-first lock order so the purchase
	// grant and its eligibility cannot change before the hold commits.
	if err := m.LockGrantUser(ctx, userID); err != nil {
		return 0, err
	}
	sub, err := tx.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID), usersubscription.SourceOrderIDEQ(orderID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		return 0, translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
	}
	if sub.RefundHoldOperationID != nil {
		return 0, billing.ErrSubscriptionRefundHeld
	}
	status := SubscriptionFromEntity(sub).EffectiveStatus(time.Now())
	if status != billing.SubscriptionStatusActive && status != billing.SubscriptionStatusPending {
		return 0, billing.ErrSubscriptionNotFound
	}
	err = tx.UserSubscription.UpdateOneID(sub.ID).SetRefundHoldOperationID(operationID).Exec(ctx)
	return sub.ID, err
}

// ClearRefundSubscriptionHold releases the specified operation's marker while
// retaining the transaction locks. The caller can revoke the same grant before
// commit on success, or commit the release alone on a definitive failure.
func (m *SubscriptionMutations) ClearRefundSubscriptionHold(ctx context.Context, id int64, operationID string) error {
	tx := m.transaction(ctx)
	if tx == nil || operationID == "" {
		return fmt.Errorf("refund hold release requires a transaction and operation identity")
	}
	if err := m.LockSubscription(ctx, id); err != nil {
		return err
	}
	// The identity predicate prevents a stale refund from clearing a newer
	// hold. Keep the same transaction open if the grant is being revoked.
	err := tx.UserSubscription.UpdateOneID(id).
		Where(usersubscription.RefundHoldOperationIDEQ(operationID)).
		ClearRefundHoldOperationID().
		Exec(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionRefundHeld, nil)
}

// VerifyRevokedRefundSubscription confirms that a legacy refund already revoked
// the matching purchase grant. The caller holds the refund transaction open;
// a missing or ambiguous match leaves finalization for manual recovery.
func (m *SubscriptionMutations) VerifyRevokedRefundSubscription(ctx context.Context, userID, orderID, id int64) error {
	tx := m.transaction(ctx)
	if tx == nil {
		return fmt.Errorf("legacy refund verification requires a transaction")
	}
	if err := m.LockGrantUser(ctx, userID); err != nil {
		return err
	}
	query := tx.UserSubscription.Query().Where(
		usersubscription.UserIDEQ(userID),
		usersubscription.SourceOrderIDEQ(orderID),
	)
	if id > 0 {
		query.Where(usersubscription.IDEQ(id))
	}
	sub, err := query.ForUpdate().Only(mixins.SkipSoftDelete(ctx))
	if err != nil {
		return err
	}
	if sub.DeletedAt == nil {
		return billing.ErrSubscriptionRefundHeld
	}
	return nil
}
