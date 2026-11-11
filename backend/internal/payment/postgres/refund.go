package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// refund.go persists refund preparation, compensation, and finalization with
// transaction-bound entitlement changes and recovery evidence.
// Refund recovery combines an order timestamp with a mandatory audit record so
// a provider result can be applied to the preparation that issued it.
// Preparation commits local entitlement deductions, refund metadata, and a
// versioned receipt before the workflow calls the gateway. Completion or
// compensation then opens another transaction and verifies that identity
// before accepting its state change.
//
// PostgreSQL row locks protect status/time comparisons and required audit
// replacement, while SQLite scenario clients omit those locks. Repeated audit
// actions archive their prior detail, and missing or contradictory recovery
// data returns manual-recovery errors. Pending records retain compatibility
// rules for legacy deductions and empty local operation IDs. Each transaction
// owns rollback cleanup, but commit errors remain observable errors; the
// workflow performs provider calls outside these transactions.

type (
	// RefundRightsFactory binds entitlement mutations to the refund store's owned
	// Ent transaction. Returned rights participate in that transaction so
	// deduction, compensation, order metadata and mandatory audit can commit
	// together.
	RefundRightsFactory func(*dbent.Tx) payment.RefundRights
	// RefundStore persists order refund states and mandatory recovery evidence
	// through owned transactions.
	RefundStore struct {
		// client opens each short refund transaction and reads persisted recovery
		// records. NewRefundStore retains the supplied client; its lifecycle stays
		// with the application.
		client *dbent.Client
		// rights constructs entitlement operations for each owned transaction before
		// a mutation callback runs. Callers provide the factory for preparation,
		// compensation, or optional final deduction.
		rights RefundRightsFactory
	}
)

// NewRefundStore binds the shared database client and transaction-specific
// entitlement factory for refund persistence. It retains both dependencies as
// supplied; mutation paths require a usable factory and callback.
func NewRefundStore(client *dbent.Client, rights RefundRightsFactory) *RefundStore {
	return &RefundStore{client: client, rights: rights}
}

// Order reads a persisted payment order by numeric identity and converts its
// fields. It returns the storage error with the converted observation;
// workflows use that error before refund eligibility or recovery.
func (s *RefundStore) Order(ctx context.Context, id int64) (*payment.Order, error) {
	o, e := s.client.PaymentOrder.Get(ctx, id)
	return OrderFromEntity(o), e
}

// refundConflict returns the conflict reported when observed refund state
// changes.
func refundConflict() error { return apperror.Conflict("CONFLICT", "order status changed") }

// refundAudit writes required recovery detail for one order and action.
// Callers hold the order row lock through the supplied transaction and pass
// detail that encodes as a nonnull JSON object.
//
// A later attempt archives the preceding detail and preserves earlier history.
// Adding a provider reference to the same prepared attempt preserves its
// original timestamp and history. Damaged history requires manual recovery
// before any evidence can be replaced.
func refundAudit(ctx context.Context, client *dbent.Client, id int64, action string, detail any) error {
	data, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal refund audit: %w", err)
	}
	var current map[string]json.RawMessage
	if err = json.Unmarshal(data, &current); err != nil {
		return err
	}
	current["recordedAt"], err = json.Marshal(time.Now().UTC())
	if err != nil {
		return err
	}
	// An unversioned current detail receives the supported recovery format marker
	// before persistence. Existing supplied versions remain available for the
	// decoder to validate.
	if _, ok := current["version"]; !ok {
		current["version"] = json.RawMessage("1")
	}
	existing, err := client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10)), paymentauditlog.ActionEQ(action)).Only(ctx)
	if err != nil && !dbent.IsNotFound(err) {
		return fmt.Errorf("read refund audit: %w", err)
	}
	// A repeated action must preserve its earlier attempt before replacing
	// detail. Decode prior history first so damaged recovery evidence cannot be
	// silently overwritten.
	if existing != nil {
		var previous map[string]json.RawMessage
		if json.Unmarshal([]byte(existing.Detail), &previous) != nil || previous == nil {
			return payment.RefundRecoveryRequired("previous audit record malformed")
		}
		var history []json.RawMessage
		if raw, ok := previous["history"]; ok {
			if json.Unmarshal(raw, &history) != nil {
				return payment.RefundRecoveryRequired("previous audit history malformed")
			}
		}
		delete(previous, "history")
		// Historical records can predate recordedAt. Use their persisted creation
		// time for the archived attempt so the new observation keeps its separate
		// current timestamp.
		if _, ok := previous["recordedAt"]; !ok {
			previous["recordedAt"], err = json.Marshal(existing.CreatedAt)
			if err != nil {
				return err
			}
		}
		// Attaching a channel reference enriches the current preparation.
		// Archive only a different operation so the audit counts each attempt once.
		previousOperation := string(previous["operationID"])
		if action == "REFUND_PREPARED" && previousOperation != "" &&
			previousOperation == string(current["operationID"]) {
			current["recordedAt"] = previous["recordedAt"]
		} else {
			prior, marshalErr := json.Marshal(previous)
			if marshalErr != nil {
				return marshalErr
			}
			history = append(history, prior)
		}

		current["history"], err = json.Marshal(history)
		if err != nil {
			return err
		}
	}
	data, err = json.Marshal(current)
	if err != nil {
		return err
	}
	// Store the assembled detail at the existing order/action identity or create
	// it when absent. The enclosing locked refund transaction owns its
	// publication with state.
	if existing == nil {
		_, err = client.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(id, 10)).SetAction(action).SetOperator("admin").SetDetail(string(data)).Save(ctx)
	} else {
		_, err = client.PaymentAuditLog.UpdateOneID(existing.ID).SetOperator("admin").SetDetail(string(data)).Save(ctx)
	}
	if err != nil {
		return fmt.Errorf("write refund audit: %w", err)
	}
	return nil
}

// lockedRefundOrder reads one order with FOR UPDATE for non-SQLite dialects.
// Callers use a transaction and retain the row lock through their status/time
// comparison and subsequent refund writes; SQLite scenario clients omit the
// lock.
func lockedRefundOrder(ctx context.Context, client *dbent.Client, id int64) (*dbent.PaymentOrder, error) {
	return client.PaymentOrder.Query().Where(paymentorder.IDEQ(id)).Where(func(q *sql.Selector) {
		// SQLite scenario clients omit row locking; production dialects retain FOR
		// UPDATE through the caller's transaction while refund state and audit are
		// compared and written.
		if q.Dialect() != "sqlite3" {
			q.ForUpdate()
		}
	}).Only(ctx)
}

// claimRefund locks the current order and checks the caller's observed status
// and updated_at before writing the requested refund state. Supply a nonnil
// order and a client participating in the surrounding refund transaction.
//
// The timestamp comparison uses time values rather than formatted strings, so
// database precision is taken from prior stored observations. A mismatched
// status or timestamp returns conflict before the update. The lock remains
// with the caller's transaction while entitlement changes and mandatory
// recovery records are written.
func claimRefund(ctx context.Context, client *dbent.Client, o *payment.Order, status string) error {
	current, err := lockedRefundOrder(ctx, client, o.ID)
	if err != nil {
		return err
	}
	if current.Status != o.Status || !current.UpdatedAt.Equal(o.UpdatedAt) {
		return refundConflict()
	}
	_, err = client.PaymentOrder.UpdateOneID(o.ID).SetStatus(status).Save(ctx)
	return err
}

// PrepareRefund commits entitlement preparation and a mandatory receipt before
// the workflow initiates the provider refund. Supply a nonnil plan/order,
// transaction-specific rights and a nonnil mutation; completed,
// refund-requested and failed observations are eligible for its
// locked status/time claim.
//
// An existing rollback-failed audit blocks preparation because the earlier
// deduction is unknown. The mutation changes a local plan copy, then refund
// metadata is stored and reread for database timestamp precision. The receipt
// records local and gateway quantities and the committed wallet deduction or
// subscription hold under a fresh operation ID. Contradictory deductions or
// audit errors return before commit. Only successful commit publishes the
// local plan copy and receipt to the caller.
func (s *RefundStore) PrepareRefund(ctx context.Context, p *payment.RefundPlan, apply payment.RefundMutation) (*payment.RefundReceipt, error) {
	switch p.Order.Status {
	case payment.OrderStatusCompleted, payment.OrderStatusRefundRequested, payment.OrderStatusRefundFailed:
	case payment.OrderStatusRefunding, payment.OrderStatusRefundPending:
		return nil, payment.RefundRecoveryRequired("a refund already awaits confirmation")
	default:
		return nil, refundConflict()
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	// Keep rollback cleanup around preparation's claim, deduction and required
	// receipt writes. An early failure leaves those transaction changes
	// unpublished before any provider initiation.
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = claimRefund(txctx, tx.Client(), p.Order, payment.OrderStatusRefunding); err != nil {
		return nil, err
	}
	// A legacy rollback-failed action means actual earlier deductions are
	// unknown. Stop before applying another mutation so recovery requires
	// verification of that surviving preparation.
	previousFailure, err := tx.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(p.OrderID, 10)), paymentauditlog.ActionEQ("REFUND_ROLLBACK_FAILED")).Exist(txctx)
	if err != nil {
		return nil, err
	}
	if previousFailure {
		return nil, payment.RefundRecoveryRequired("legacy deduction rollback failure")
	}
	local := *p
	local.OperationID = uuid.NewString()
	if err = apply(txctx, s.rights(tx), &local); err != nil {
		return nil, err
	}
	order, err := tx.PaymentOrder.UpdateOneID(p.OrderID).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).SetForceRefund(p.Force).Save(txctx)
	if err != nil {
		return nil, err
	}
	// Reread the persisted update time before constructing operationVersion.
	// PostgreSQL stores microsecond precision, and later locked time comparisons
	// need that stored observation rather than a finer local value.
	order, err = tx.PaymentOrder.Get(txctx, order.ID)
	if err != nil {
		return nil, err
	}
	version := 1
	if local.SubscriptionHold {
		version = 2
	}
	receipt := &payment.RefundReceipt{
		Version:          version,
		OperationID:      local.OperationID,
		OrderID:          p.OrderID,
		OperationVersion: order.UpdatedAt,
		PreviousStatus:   p.Order.Status,
		RefundAmount:     p.RefundAmount,
		GatewayAmount:    p.GatewayAmount,
		Reason:           p.Reason,
		Force:            p.Force,
		RefundPendingDetail: payment.RefundPendingDetail{
			DeductBalance:    local.DeductBalance,
			DeductionType:    local.DeductionType,
			BalanceDeducted:  local.BalanceToDeduct,
			SubDaysDeducted:  local.SubDaysToDeduct,
			SubscriptionID:   local.SubscriptionID,
			SubscriptionHold: local.SubscriptionHold,
		},
	}
	if !validRefundDeduction(receipt.RefundPendingDetail, false) {
		return nil, payment.RefundRecoveryRequired("preparation deduction is contradictory")
	}
	// Write mandatory preparation evidence after deduction and order metadata but
	// before commit. The gateway caller receives a receipt only once all three
	// local changes commit.
	if err = refundAudit(txctx, tx.Client(), p.OrderID, "REFUND_PREPARED", receipt); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	// Publish the mutation's local plan copy only after commit succeeds. A failed
	// preparation leaves the caller's plan unchanged for its error and recovery
	// handling.
	*p = local
	return receipt, nil
}

// RecordRefundProviderID persists a channel reference before attempting rights
// finalization. It locks the order without changing its version, then checks
// that the stored preparation owns this observation before updating it.
//
// A repeated observation of the same reference succeeds without another audit
// write. A different reference requires manual verification. A committed
// reference survives a later compensation or completion failure and lets
// recovery query the exact refund rather than rely on discovery.
func (s *RefundStore) RecordRefundProviderID(ctx context.Context, receipt *payment.RefundReceipt, id string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	// Roll back any uncommitted observation so a later recovery reads a
	// complete receipt with the same order version and channel identity.
	defer func() { _ = tx.Rollback() }()
	ctx = dbent.NewTxContext(ctx, tx)
	current, err := lockedRefundOrder(ctx, tx.Client(), receipt.OrderID)
	if err != nil {
		return err
	}
	if current.Status != payment.OrderStatusRefunding || !current.UpdatedAt.Equal(receipt.OperationVersion) {
		return refundConflict()
	}
	stored, err := readRefundReceipt(ctx, tx.Client(), receipt.OrderID)
	if err != nil {
		return err
	}
	if stored.OperationID != receipt.OperationID || !stored.OperationVersion.Equal(receipt.OperationVersion) {
		return refundConflict()
	}
	if stored.RefundID != "" && stored.RefundID != id {
		return payment.RefundRecoveryRequired("provider refund identity changed")
	}
	// Keep the local operation version unchanged so the same committed receipt
	// can still claim order finalization after the provider reference is recorded.
	if stored.RefundID == "" {
		stored.RefundID = id
		if err = refundAudit(ctx, tx.Client(), receipt.OrderID, "REFUND_PREPARED", stored); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	receipt.RefundID = id
	return nil
}

// expected verifies the refund preparation or pending action identity before
// claiming the order for a subsequent transaction. Supply a nonnil plan/order;
// a receipt selects prepared recovery, while nil requires a refund-pending
// observation and matching local/channel IDs.
//
// Prepared recovery compares the stored receipt operation ID and version, then
// claims REFUNDING against that version. Pending recovery checks its latest
// action identity before claiming against the observed order time. Those
// record reads precede claimRefund's row lock; its final status/time check
// detects a changed order before later mutation writes proceed.
func (s *RefundStore) expected(ctx context.Context, client *dbent.Client, p *payment.RefundPlan, r *payment.RefundReceipt) error {
	expected := *p.Order
	if r != nil {
		expected.Status = payment.OrderStatusRefunding
		expected.UpdatedAt = r.OperationVersion
		current, err := readRefundReceipt(ctx, client, p.OrderID)
		if err != nil {
			return err
		}
		if current.OperationID != r.OperationID || !current.OperationVersion.Equal(r.OperationVersion) {
			return refundConflict()
		}
	} else if expected.Status != payment.OrderStatusRefundPending {
		return refundConflict()
	}
	if r == nil {
		if err := matchPendingRefundIdentity(ctx, client, p.OrderID, p.OperationID, p.ChannelRefundID); err != nil {
			return err
		}
	}
	return claimRefund(ctx, client, &expected, payment.OrderStatusRefunding)
}

// CompleteRefund commits the accepted provider success, optional entitlement
// deduction and mandatory success audit in one owned transaction. A prepared
// receipt verifies REFUNDING identity; nil receipt uses the plan's pending
// observation and local/channel identity.
//
// A supplied mutation operates on a local plan copy after the claim. Refund
// amount below the recorded order amount selects partially refunded; other
// amounts select refunded. The order stores refund time/reason/force with the
// success audit before commit. Early failures retain rollback cleanup, and
// successful commit publishes the plan copy and its reported balance/day
// bookkeeping to the caller.
func (s *RefundStore) CompleteRefund(ctx context.Context, p *payment.RefundPlan, r *payment.RefundReceipt, apply payment.RefundMutation) (*payment.RefundResult, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	// Keep rollback cleanup around finalization's identity check, optional
	// deduction and required success audit. Early failure must leave the
	// previously committed preparation or pending state available for recovery.
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = s.expected(txctx, tx.Client(), p, r); err != nil {
		return nil, err
	}
	local := *p
	if apply != nil {
		if err = apply(txctx, s.rights(tx), &local); err != nil {
			return nil, err
		}
	}
	status := payment.OrderStatusRefunded
	if local.RefundAmount < local.Order.Amount {
		status = payment.OrderStatusPartiallyRefunded
	}
	// Persist success state and quantities before its mandatory success audit,
	// then commit both with optional deduction. Only the committed plan copy is
	// returned as completion bookkeeping.
	_, err = tx.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(status).SetRefundAmount(local.RefundAmount).SetRefundReason(local.Reason).SetRefundAt(time.Now()).SetForceRefund(local.Force).Save(txctx)
	if err != nil {
		return nil, fmt.Errorf("mark refund: %w", err)
	}
	detail := map[string]any{
		"refundAmount":    local.RefundAmount,
		"reason":          local.Reason,
		"balanceDeducted": local.BalanceToDeduct,
		"subDaysDeducted": local.SubDaysToDeduct,
		"force":           local.Force,
		"operationID":     local.OperationID,
	}
	if local.SubscriptionHold {
		detail["subscriptionRevoked"] = local.SubscriptionID > 0
	}
	if err = refundAudit(txctx, tx.Client(), p.OrderID, "REFUND_SUCCESS", detail); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit refund finalization: %w", err)
	}
	*p = local
	return &payment.RefundResult{Status: payment.RefundResultSucceeded, Success: true, BalanceDeducted: local.BalanceToDeduct, SubDaysDeducted: local.SubDaysToDeduct}, nil
}

// CompensateRefund commits entitlement compensation and the corresponding
// pending or gateway-failed recovery record after checking the preparation.
// Supply a nonnil plan/order, receipt and compensation callback; the callback
// receives rights in the owned transaction.
//
// Nonempty failure text restores refund-requested only when preparation began
// there, otherwise completed. Empty failure text selects pending, clears prior
// failure/refund time and records provider/local identities with preparation
// deduction facts and successful compensation. State and mandatory audit
// commit together. The callback receives a local plan copy, but this method
// does not publish that copy back to the caller; commit errors remain visible
// to recovery.
func (s *RefundStore) CompensateRefund(ctx context.Context, p *payment.RefundPlan, r *payment.RefundReceipt, refundID, failure string, apply payment.RefundMutation) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	// Keep rollback cleanup around compensation and its required pending/failure
	// record. If either step fails before commit, recovery still uses the earlier
	// preparation evidence.
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = s.expected(txctx, tx.Client(), p, r); err != nil {
		return err
	}
	local := *p
	if err = apply(txctx, s.rights(tx), &local); err != nil {
		return fmt.Errorf("refund compensation failed: %w", err)
	}
	update := tx.PaymentOrder.UpdateOneID(p.OrderID)
	var action string
	var detail any
	// Gateway failure restores the preparation's eligible pre-request state after
	// compensation. Pending progress instead keeps a recoverable provider/local
	// identity and clears obsolete refund/failure timestamps.
	if failure != "" {
		status := payment.OrderStatusCompleted
		if r.PreviousStatus == payment.OrderStatusRefundRequested {
			status = payment.OrderStatusRefundRequested
		}
		update.SetStatus(status)
		action = "REFUND_GATEWAY_FAILED"
		detail = map[string]any{"detail": failure, "operationID": r.OperationID, "deductionRollbackOK": true}
	} else {
		update.SetStatus(payment.OrderStatusRefundPending).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).ClearRefundAt().SetForceRefund(p.Force).ClearFailedAt().ClearFailedReason()
		action = "REFUND_PENDING"
		detail = map[string]any{
			"refundID":            refundID,
			"refundAmount":        p.RefundAmount,
			"reason":              p.Reason,
			"force":               p.Force,
			"deductBalance":       p.DeductBalance,
			"deductionType":       p.DeductionType,
			"balanceDeducted":     p.BalanceToDeduct,
			"subDaysDeducted":     p.SubDaysToDeduct,
			"subscriptionID":      p.SubscriptionID,
			"balanceRolledBack":   p.BalanceToDeduct,
			"subDaysRolledBack":   p.SubDaysToDeduct,
			"deductionRollbackOK": !r.SubscriptionHold,
			"subscriptionHold":    r.SubscriptionHold,
			"operationID":         r.OperationID,
			"version":             r.Version,
		}
	}
	// Persist the compensated state before its required recovery action and
	// commit them together. Audit failure returns through cleanup while the
	// earlier committed preparation remains available.
	if _, err = update.Save(txctx); err != nil {
		return err
	}
	if err = refundAudit(txctx, tx.Client(), p.OrderID, action, detail); err != nil {
		return err
	}
	return tx.Commit()
}

// FailPendingRefund records terminal provider failure only when the observed
// pending status, timestamp and action identities still match. Supply a nonnil
// pending order and its previously decoded claim; a nonpending input returns
// conflict.
//
// The transaction locks and rereads the order first. Matching status/time
// triggers identity verification, failure fields and a mandatory audit before
// commit. If the row changed, it is returned as the current observation after
// the transaction commits, allowing the workflow to recognize a competing
// success instead of overwriting it. Early errors retain rollback cleanup.
func (s *RefundStore) FailPendingRefund(ctx context.Context, o *payment.Order, claim payment.RefundPendingDetail, failure string) (*payment.Order, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	// Keep rollback cleanup around the pending failure update and required
	// failure audit. An early error prevents publishing that failure separately
	// from its associated evidence.
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if o.Status != payment.OrderStatusRefundPending {
		return nil, refundConflict()
	}
	current, err := lockedRefundOrder(txctx, tx.Client(), o.ID)
	if err != nil {
		return nil, err
	}
	// Only the still-matching observation can receive terminal failure and its
	// audit. A changed locked row bypasses those writes and returns current state
	// for competing-result recovery.
	if current.Status == o.Status && current.UpdatedAt.Equal(o.UpdatedAt) {
		if err = matchPendingRefundIdentity(txctx, tx.Client(), o.ID, claim.OperationID, claim.RefundID); err != nil {
			return nil, err
		}
		// Release only a verified operation-owned hold. Legacy subscription
		// deductions without successful compensation need an operator to
		// reconstruct the original entitlement before recording failure.
		if claim.SubscriptionHold && claim.SubscriptionID > 0 {
			if err = s.rights(tx).ResolveSubscriptionHold(txctx, claim.SubscriptionID, claim.OperationID, false); err != nil {
				return nil, err
			}
		} else if o.OrderType == payment.OrderTypeSubscription && claim.DeductBalance && !claim.DeductionRollbackOK && !claim.SubscriptionHold {
			return nil, payment.RefundRecoveryRequired("legacy subscription deduction needs manual compensation")
		}
		current, err = tx.PaymentOrder.UpdateOneID(o.ID).SetStatus(payment.OrderStatusRefundFailed).SetFailedAt(time.Now()).SetFailedReason(failure).Save(txctx)
		if err != nil {
			return nil, err
		}
		if err = refundAudit(txctx, tx.Client(), o.ID, "REFUND_FAILED", map[string]any{"detail": failure}); err != nil {
			return nil, err
		}
	}
	// Commit the failure audit and order write, or finish the unchanged
	// observation transaction. The returned row then lets the workflow
	// distinguish failure from a newer completion.
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return OrderFromEntity(current), nil
}

// readRefundReceipt decodes the latest prepared audit and requires every
// versioned recovery field to be present and nonnull. Missing, unreadable,
// malformed or contradictory data becomes REFUND_RECOVERY_REQUIRED because a
// deduction cannot be reconstructed from absent evidence.
//
// A supported version, nonempty operation ID, matching order ID and nonzero stored
// time are required. Money values must be finite/nonnegative and the deduction
// representation must agree with its type and intent. The result preserves
// explicit false intent and recorded subscription days; it validates recovery
// bookkeeping rather than proving a particular elapsed-day removal.
func readRefundReceipt(ctx context.Context, client *dbent.Client, id int64) (*payment.RefundReceipt, error) {
	log, err := client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10)), paymentauditlog.ActionEQ("REFUND_PREPARED")).Order(paymentauditlog.ByCreatedAt(sql.OrderDesc()), paymentauditlog.ByID(sql.OrderDesc())).First(ctx)
	// Any preparation read failure leaves deduction evidence unavailable. Convert
	// it to manual recovery so the workflow cannot assume missing facts mean a
	// zero deduction.
	if err != nil {
		return nil, payment.RefundRecoveryRequired("preparation record unavailable")
	}
	var required map[string]json.RawMessage
	if json.Unmarshal([]byte(log.Detail), &required) != nil {
		return nil, payment.RefundRecoveryRequired("preparation record malformed")
	}
	for _, field := range []string{
		"version",
		"operationID",
		"orderID",
		"operationVersion",
		"previousStatus",
		"refundAmount",
		"gatewayAmount",
		"reason",
		"force",
		"deductBalance",
		"deductionType",
		"balanceDeducted",
		"subDaysDeducted",
		"subscriptionID",
	} {
		if raw, ok := required[field]; !ok || string(raw) == "null" {
			return nil, payment.RefundRecoveryRequired("preparation record incomplete: " + field)
		}
	}
	var receipt payment.RefundReceipt
	if err = json.Unmarshal([]byte(log.Detail), &receipt); err != nil {
		return nil, payment.RefundRecoveryRequired("preparation record malformed")
	}
	if !validRefundVersion(receipt.Version, receipt.SubscriptionHold) || receipt.OperationID == "" || receipt.OrderID != id || receipt.OperationVersion.IsZero() || !validRefundAmount(receipt.RefundAmount) || !validRefundAmount(receipt.GatewayAmount) || !validRefundAmount(receipt.BalanceDeducted) || receipt.SubDaysDeducted < 0 || !validRefundDeduction(receipt.RefundPendingDetail, false) {
		return nil, payment.RefundRecoveryRequired("preparation record invalid")
	}
	return &receipt, nil
}

// RefundRecovery reads preparation evidence and verifies the supplied
// REFUNDING order observation before the workflow queries or applies another
// provider outcome. Supply a nonnil order with the current stored timestamp
// and refund fields.
//
// Status, updated_at, refund amount, force and a nonnil matching reason bind
// the order to its receipt. Missing evidence and contradictions require manual
// recovery because the stored deduction cannot be assumed absent. This read
// does not lock or claim the row; finalization separately rechecks operation
// identity and the order version in its owned transaction.
func (s *RefundStore) RefundRecovery(ctx context.Context, o *payment.Order) (*payment.RefundReceipt, error) {
	r, err := readRefundReceipt(ctx, s.client, o.ID)
	if err != nil {
		return nil, err
	}
	if o.Status != payment.OrderStatusRefunding || !o.UpdatedAt.Equal(r.OperationVersion) || o.RefundAmount != r.RefundAmount || o.ForceRefund != r.Force || o.RefundReason == nil || *o.RefundReason != r.Reason {
		return nil, payment.RefundRecoveryRequired("preparation record contradicts order")
	}
	return r, nil
}

// validRefundAmount accepts finite nonnegative recovery quantities, including
// an explicit zero.
func validRefundAmount(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

// PendingDetail decodes the latest pending audit while retaining supported
// legacy deduction behavior. Reading failure or missing/null deduction intent
// requires manual recovery, so an absent value cannot silently become false.
//
// Legacy decoding starts with successful rollback and can omit deduction type.
// Explicitly stored rollback false remains false. A supplied version requires
// a supported version, an operation ID and consistent entitlement data.
// Malformed versioned data cannot fall through to legacy handling. Any nonempty
// operation ID also matches preparation intent/type/quantities/grant identity
// before the result is returned for query finalization.
func (s *RefundStore) PendingDetail(ctx context.Context, id int64) (payment.RefundPendingDetail, error) {
	var d payment.RefundPendingDetail
	log, err := s.client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10)), paymentauditlog.ActionEQ("REFUND_PENDING")).Order(paymentauditlog.ByCreatedAt(sql.OrderDesc()), paymentauditlog.ByID(sql.OrderDesc())).First(ctx)
	// Any pending audit read failure prevents reconstruction of request identity
	// and deduction facts. Return manual recovery before a provider query result
	// can alter local entitlements.
	if err != nil {
		return d, payment.RefundRecoveryRequired("pending record unavailable")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(log.Detail), &fields) != nil || (fields["deductBalance"] == nil || string(fields["deductBalance"]) == "null") {
		return d, payment.RefundRecoveryRequired("pending record malformed or incomplete")
	}
	// Legacy pending records can omit compensation success. Start with true for
	// that compatibility rule; JSON decoding still retains an explicitly stored
	// false for later finalization.
	d.DeductionRollbackOK = true
	if json.Unmarshal([]byte(log.Detail), &d) != nil || !validRefundAmount(d.BalanceDeducted) || d.SubDaysDeducted < 0 || !validRefundDeduction(d, true) {
		return d, payment.RefundRecoveryRequired("pending deduction invalid")
	}
	if d.DeductBalance && fields["balanceDeducted"] == nil && fields["subDaysDeducted"] == nil {
		return d, payment.RefundRecoveryRequired("pending deduction missing")
	}
	// Once version is present, require its supported value, preparation identity
	// and modern deduction consistency. Reject corrupt versioned data before any
	// legacy interpretation can accept it.
	if raw, ok := fields["version"]; ok {
		var version int
		if json.Unmarshal(raw, &version) != nil || !validRefundVersion(version, d.SubscriptionHold) || d.OperationID == "" || !validRefundDeduction(d, false) {
			return d, payment.RefundRecoveryRequired("pending record version or deduction invalid")
		}
	}
	if d.SubscriptionHold && (d.OperationID == "" || fields["version"] == nil || d.DeductionRollbackOK) {
		return d, payment.RefundRecoveryRequired("pending subscription hold is inconsistent")
	}
	d.ObservedAt = log.CreatedAt
	// A supplied operation ID binds pending deductions to preparation quantities
	// and intent. Read and compare that receipt so finalization cannot associate
	// a different deduction with this provider result.
	if d.OperationID != "" {
		r, e := readRefundReceipt(ctx, s.client, id)
		if e != nil || r.OperationID != d.OperationID || r.DeductBalance != d.DeductBalance || r.DeductionType != d.DeductionType || r.BalanceDeducted != d.BalanceDeducted || r.SubDaysDeducted != d.SubDaysDeducted || r.SubscriptionID != d.SubscriptionID || r.SubscriptionHold != d.SubscriptionHold || (r.RefundID != "" && r.RefundID != d.RefundID) {
			return d, payment.RefundRecoveryRequired("pending preparation mismatch")
		}
		d.ObservedAt = r.OperationVersion
	}
	return d, nil
}

// AppendObservation persists a provider audit event with decimal-text order
// identity and returns encoding or storage errors. The workflow chooses the
// action/operator and can retain this observation separately from required
// refund state transactions.
func (s *RefundStore) AppendObservation(ctx context.Context, id int64, action, operator string, detail map[string]any) error {
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.client.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(id, 10)).SetAction(action).SetOperator(operator).SetDetail(string(data)).Save(ctx)
	return err
}

// RequestRefund conditionally marks the addressed user's completed balance
// order refund-requested and stores its request metadata. It returns the
// affected count for caller race handling; eligibility and amount validation
// occur before this write.
func (s *RefundStore) RequestRefund(ctx context.Context, id, userID int64, amount float64, reason string, now time.Time, by string) (int, error) {
	return s.client.PaymentOrder.Update().Where(paymentorder.IDEQ(id), paymentorder.UserIDEQ(userID), paymentorder.StatusEQ(payment.OrderStatusCompleted), paymentorder.OrderTypeEQ(payment.OrderTypeBalance)).SetStatus(payment.OrderStatusRefundRequested).SetRefundRequestedAt(now).SetRefundRequestReason(reason).SetRefundRequestedBy(by).SetRefundAmount(amount).Save(ctx)
}

// matchPendingRefundIdentity compares the latest pending action's local
// operation and provider refund IDs to the supplied values. The operation ID
// names preparation, while the provider ID names the channel refund; both must
// match that same stored action.
//
// Legacy empty IDs compare as stored. An unreadable record requires manual
// recovery, and a mismatched pair returns conflict. This helper checks
// identity rather than validating every deduction field; PendingDetail
// performs that validation, and expected or FailPendingRefund separately
// checks the order status/time around the claim.
func matchPendingRefundIdentity(ctx context.Context, client *dbent.Client, id int64, operationID, refundID string) error {
	row, err := client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10)), paymentauditlog.ActionEQ("REFUND_PENDING")).Order(paymentauditlog.ByCreatedAt(sql.OrderDesc()), paymentauditlog.ByID(sql.OrderDesc())).First(ctx)
	// An unavailable pending identity cannot be treated as a matching legacy
	// empty pair. Convert the read failure to manual recovery before accepting
	// the observed provider result.
	if err != nil {
		return payment.RefundRecoveryRequired("pending identity unavailable")
	}
	var actual payment.RefundPendingDetail
	if json.Unmarshal([]byte(row.Detail), &actual) != nil {
		return payment.RefundRecoveryRequired("pending identity malformed")
	}
	if actual.OperationID != operationID || actual.RefundID != refundID {
		return refundConflict()
	}
	return nil
}

// validRefundDeduction checks finite nonnegative quantities and their
// agreement with deduction type and grant identity. Modern records with false
// deduction intent require zero reported balance/days; legacy mode can retain
// requested quantities or omit type.
//
// None requires zero quantities and grant ID. Balance rejects subscription
// quantities/identity, while subscription rejects balance quantity but can
// retain positive days with no grant. This representation check therefore
// supports forced preparation bookkeeping without promising that every
// recorded day was actually removed.
func validRefundDeduction(d payment.RefundPendingDetail, legacy bool) bool {
	if d.SubscriptionID < 0 || !validRefundAmount(d.BalanceDeducted) || d.SubDaysDeducted < 0 {
		return false
	}
	if !legacy && !d.DeductBalance && (d.BalanceDeducted != 0 || d.SubDaysDeducted != 0) {
		return false
	}
	if d.SubscriptionHold {
		return d.DeductBalance && d.DeductionType == payment.DeductionTypeSubscription &&
			d.BalanceDeducted == 0 && d.SubDaysDeducted == 0
	}
	switch d.DeductionType {
	case payment.DeductionTypeNone:
		return d.BalanceDeducted == 0 && d.SubDaysDeducted == 0 && d.SubscriptionID == 0
	case payment.DeductionTypeBalance:
		return d.SubDaysDeducted == 0 && d.SubscriptionID == 0
	case payment.DeductionTypeSubscription:
		return d.BalanceDeducted == 0
	case "":
		return legacy
	default:
		return false
	}
}

// validRefundVersion checks which entitlement encoding a receipt version permits.
func validRefundVersion(version int, hold bool) bool {
	return (version == 1 && !hold) || (version == 2 && hold)
}
