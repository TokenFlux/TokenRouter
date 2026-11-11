//go:build integration

package app

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// payment_refund_scenario_integration_test.go exercises the refund workflow,
// payment store and real transaction-bound billing participants together. It
// inspects grants and order state in migrated PostgreSQL while provider fixtures
// control acceptance, lost replies and terminal outcomes.

// subscriptionRefundChannel returns predetermined channel observations and can
// pause the first submission while a competing refund attempts to claim it.
type subscriptionRefundChannel struct {
	payment.Provider               // unused base provider operations
	initial          string        // first normalized outcome
	terminal         string        // queried normalized outcome
	submissions      atomic.Int64  // accepted provider calls
	started          chan struct{} // optional submission notification
	resume           chan struct{} // optional submission release
	queriedID        string        // recorded query reference
	referenceOnly    bool          // query requires fixture refund ID
}

// ProviderKey identifies the channel fixture as Stripe to the workflow.
func (*subscriptionRefundChannel) ProviderKey() string { return payment.TypeStripe }

// Refund reports the configured initial result after publishing any optional
// concurrency checkpoint. Unknown results model a provider accepting the
// request while the application loses its response.
func (c *subscriptionRefundChannel) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	c.submissions.Add(1)
	if c.started != nil {
		// Keep the channel request open so a second caller must contend with
		// the already-committed local claim while the result is still unknown.
		close(c.started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.resume:
		}
	}
	if c.initial == "unknown" {
		return nil, payment.ErrRefundOutcomeUnknown
	}
	return &payment.RefundResponse{RefundID: "re_fixture", Status: c.initial}, nil
}

// QueryRefund supplies the terminal observation without creating a refund.
// A reference-only fixture rejects missing or mismatched identifiers so the
// workflow can exercise verification before updating the local refund.
func (c *subscriptionRefundChannel) QueryRefund(_ context.Context, req payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	c.queriedID = req.RefundID
	if c.referenceOnly && req.RefundID != "re_fixture" {
		return nil, fmt.Errorf("fixture refund reference could not be verified")
	}
	return &payment.RefundResponse{RefundID: "re_fixture", Status: c.terminal}, nil
}

// TestSubscriptionRefundWorkflow verifies full refunds, grant availability and
// order claims with the production stores. It covers both channel terminal
// outcomes, an unknown response, an expiring hold, and administrator opt-out.
func TestSubscriptionRefundWorkflow(t *testing.T) {
	db := postgrescontainer.New(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	now := time.Now().UTC().Truncate(time.Second)
	cases := []struct {
		name                   string // scenario identifier
		initial                string // first provider outcome
		terminal               string // queried provider outcome
		queued                 bool   // grant has an earlier sibling
		expire                 bool   // grant expires during the hold
		optOut                 bool   // refund retains subscription
		concurrent             bool   // attempt a second simultaneous refund
		legacy                 bool   // refund already applied a historical revocation
		missing                bool   // target already revoked before submission
		rejectCompletion       bool   // database rejects the first finalization
		cancelBeforeSubmission bool   // disconnect after preparation
		manualReference        bool   // refund requires reference recovery
	}{
		{name: "immediate", initial: payment.ProviderStatusSuccess},
		{name: "pending success", initial: payment.ProviderStatusPending, terminal: payment.ProviderStatusSuccess, concurrent: true},
		{name: "queued success", initial: payment.ProviderStatusPending, terminal: payment.ProviderStatusSuccess, queued: true},
		{name: "pending failure", initial: payment.ProviderStatusPending, terminal: payment.ProviderStatusFailed, queued: true},
		{name: "lost reply", initial: "unknown", terminal: payment.ProviderStatusSuccess},
		{name: "lost reply then failed", initial: "unknown", terminal: payment.ProviderStatusFailed, manualReference: true},
		{name: "cancel before provider submission", cancelBeforeSubmission: true},
		{name: "expires while pending", initial: payment.ProviderStatusPending, terminal: payment.ProviderStatusFailed, expire: true},
		{name: "keep subscription", initial: payment.ProviderStatusSuccess, optOut: true},
		{name: "force without a grant", initial: payment.ProviderStatusSuccess, missing: true},
		{name: "local completion failure", initial: payment.ProviderStatusSuccess, terminal: payment.ProviderStatusSuccess, rejectCompletion: true},
		{name: "legacy revocation", terminal: payment.ProviderStatusSuccess, legacy: true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			user, err := client.User.Create().
				SetEmail(fmt.Sprintf("refund-%d@example.invalid", i)).
				SetPasswordHash("fixture").
				Save(ctx)
			require.NoError(t, err)
			plan, err := client.SubscriptionPlan.Create().
				SetName(fmt.Sprintf("Refund fixture %d", i)).
				SetPrice(39.9).
				SetValidityDays(30).
				SetValidityUnit("days").
				Save(ctx)
			require.NoError(t, err)
			order, err := client.PaymentOrder.Create().
				SetUserID(user.ID).
				SetUserEmail(user.Email).
				SetUserName(user.Username).
				SetAmount(39.9).
				SetPayAmount(42.6).
				SetFeeRate(0).
				SetRechargeCode(fmt.Sprintf("REFUND-FIXTURE-%d", i)).
				SetOutTradeNo(fmt.Sprintf("refund_fixture_%d", i)).
				SetPaymentType(payment.TypeStripe).
				SetPaymentTradeNo("pi_fixture").
				SetOrderType(payment.OrderTypeSubscription).
				SetPlanID(plan.ID).
				SetPlanSnapshot(billing.SubscriptionPlanSnapshot{Currency: "CNY", ValidityDays: 30}).
				SetStatus(payment.OrderStatusCompleted).
				SetExpiresAt(now.Add(time.Hour)).
				SetPaidAt(now).
				SetClientIP("127.0.0.1").
				SetSrcHost("example.invalid").
				Save(ctx)
			require.NoError(t, err)

			start := now.Add(-time.Hour)
			status := billing.SubscriptionStatusActive
			if tc.queued {
				start = now.Add(time.Hour)
				status = billing.SubscriptionStatusPending
				_, err = client.UserSubscription.Create().
					SetUserID(user.ID).SetPlanID(plan.ID).
					SetStartsAt(now.Add(-24 * time.Hour)).SetExpiresAt(start).Save(ctx)
				require.NoError(t, err)
			}
			sub, err := client.UserSubscription.Create().
				SetUserID(user.ID).SetPlanID(plan.ID).SetSourceOrderID(order.ID).
				SetStartsAt(start).SetExpiresAt(start.Add(30 * 24 * time.Hour)).
				SetStatus(status).SetMonthlyUsageUsd(7).Save(ctx)
			require.NoError(t, err)
			next, err := client.UserSubscription.Create().
				SetUserID(user.ID).SetPlanID(plan.ID).
				SetStartsAt(sub.ExpiresAt).SetExpiresAt(sub.ExpiresAt.Add(30 * 24 * time.Hour)).
				SetStatus(billing.SubscriptionStatusPending).Save(ctx)
			require.NoError(t, err)

			channel := &subscriptionRefundChannel{initial: tc.initial, terminal: tc.terminal, referenceOnly: tc.manualReference}
			clock := billing.DateRuntime{Now: func() time.Time { return now }}
			store := paymentpostgres.NewRefundStore(client, func(tx *ent.Tx) payment.RefundRights {
				return newPaymentRefundRights(tx, nil, clock)
			})
			requestCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			workflow := payment.NewRefundWorkflow(store, payment.RefundRuntime{
				Instance: func(context.Context, *payment.Order) (*payment.ProviderInstance, error) {
					return &payment.ProviderInstance{RefundEnabled: true}, nil
				},
				Provider: func(context.Context, *payment.Order) (payment.Provider, error) {
					if tc.cancelBeforeSubmission {
						// Disconnect after the preparation commit but before a
						// refund provider can send a request.
						cancel()
						return nil, requestCtx.Err()
					}
					return channel, nil
				},
			})
			if tc.legacy {
				// Model the stored state from an old pending refund whose attempt
				// to restore the original soft-deleted grant failed.
				require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).SetDeletedAt(now).Exec(ctx))
				require.NoError(t, client.PaymentOrder.UpdateOneID(order.ID).
					SetStatus(payment.OrderStatusRefundPending).SetRefundAmount(39.9).Exec(ctx))
				_, err := client.PaymentAuditLog.Create().
					SetOrderID(strconv.FormatInt(order.ID, 10)).
					SetAction("REFUND_PENDING").
					SetOperator("admin").
					SetDetail(fmt.Sprintf(`{"refundID":"re_fixture","deductBalance":true,"deductionType":"subscription","subDaysDeducted":30,"subscriptionID":%d,"deductionRollbackOK":false}`, sub.ID)).
					Save(ctx)
				require.NoError(t, err)
				result, err := workflow.QueryAndFinalizeRefund(ctx, order.ID)
				require.NoError(t, err)
				require.True(t, result.Success)
				unchanged, err := client.UserSubscription.Get(ctx, next.ID)
				require.NoError(t, err)
				require.Equal(t, next.StartsAt, unchanged.StartsAt)
				require.Zero(t, channel.submissions.Load())
				return
			}
			_, _, err = workflow.PrepareRefund(ctx, order.ID, 39.89, "fixture", false, true)
			require.Equal(t, "SUBSCRIPTION_REFUND_FULL_ONLY", apperror.Reason(err))
			draft, _, err := workflow.PrepareRefund(ctx, order.ID, 39.9, "fixture", false, !tc.optOut)
			require.NoError(t, err)
			require.Equal(t, 42.6, draft.GatewayAmount)
			if tc.cancelBeforeSubmission {
				result, err := workflow.ExecuteRefund(requestCtx, draft)
				require.NoError(t, err)
				require.Equal(t, payment.RefundResultFailed, result.Status)
				require.Zero(t, channel.submissions.Load())
				persisted, err := store.Order(ctx, order.ID)
				require.NoError(t, err)
				require.Equal(t, payment.OrderStatusCompleted, persisted.Status)
				restored, err := client.UserSubscription.Get(ctx, sub.ID)
				require.NoError(t, err)
				require.Nil(t, restored.RefundHoldOperationID)
				require.Equal(t, sub.ExpiresAt, restored.ExpiresAt)
				return
			}
			if tc.missing {
				require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).SetDeletedAt(now).Exec(ctx))
				result, err := workflow.ExecuteRefund(ctx, draft)
				require.NoError(t, err)
				require.True(t, result.RequireForce)
				require.Zero(t, channel.submissions.Load())
				draft, _, err = workflow.PrepareRefund(ctx, order.ID, 39.9, "fixture", true, true)
				require.NoError(t, err)
				result, err = workflow.ExecuteRefund(ctx, draft)
				require.NoError(t, err)
				require.True(t, result.Success)
				require.Zero(t, draft.SubscriptionID)
				scheduled, err := client.UserSubscription.Get(ctx, next.ID)
				require.NoError(t, err)
				require.Equal(t, next.StartsAt, scheduled.StartsAt)
				return
			}
			if tc.rejectCompletion {
				// Fail only the terminal update, after the provider reference
				// has been recorded. Recovery must use that exact reference.
				_, err := db.ExecContext(ctx, fmt.Sprintf(`
					CREATE FUNCTION reject_refund_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN
						IF NEW.id = %d AND NEW.status = 'REFUNDED' THEN
							RAISE EXCEPTION 'refund fixture completion failure';
						END IF;
						RETURN NEW;
					END $$;
					CREATE TRIGGER reject_refund_fixture BEFORE UPDATE ON payment_orders
					FOR EACH ROW EXECUTE FUNCTION reject_refund_fixture();`, order.ID))
				require.NoError(t, err)
			}

			if tc.concurrent {
				channel.started = make(chan struct{})
				channel.resume = make(chan struct{})
				competitor := *draft
				finished := make(chan error, 1)
				// Allow the first submission to reach the channel only after its
				// local claim commits, then challenge that claim with a stale draft.
				go func() {
					_, executeErr := workflow.ExecuteRefund(ctx, draft)
					finished <- executeErr
				}()
				select {
				case <-channel.started:
					_, err = workflow.ExecuteRefund(ctx, &competitor)
					close(channel.resume)
					require.Error(t, err)
				case err = <-finished:
					t.Fatalf("refund ended before submission: %v", err)
				case <-time.After(10 * time.Second):
					close(channel.resume)
					t.Fatal("refund did not reach provider")
				}
				require.NoError(t, <-finished)
			} else {
				_, err = workflow.ExecuteRefund(ctx, draft)
				if tc.initial == "unknown" || tc.rejectCompletion {
					require.Equal(t, "REFUND_RECOVERY_REQUIRED", apperror.Reason(err))
				} else {
					require.NoError(t, err)
				}
			}
			require.Equal(t, int64(1), channel.submissions.Load())

			if tc.terminal != "" {
				observed, err := client.UserSubscription.Get(ctx, sub.ID)
				require.NoError(t, err)
				require.NotNil(t, observed.RefundHoldOperationID)
				require.Equal(t, billing.SubscriptionStatusSuspended, billingpostgres.SubscriptionFromEntity(observed).EffectiveStatus(now))
				scheduled, err := client.UserSubscription.Get(ctx, next.ID)
				require.NoError(t, err)
				require.Equal(t, next.StartsAt, scheduled.StartsAt)
				active, err := billingpostgres.NewUserSubscriptionRepository(client).ListActiveByUserID(ctx, user.ID)
				require.NoError(t, err)
				for _, grant := range active {
					require.NotEqual(t, sub.ID, grant.ID)
				}
				_, _, err = workflow.PrepareRefund(ctx, order.ID, 39.9, "duplicate", false, true)
				require.Equal(t, "REFUND_RECOVERY_REQUIRED", apperror.Reason(err))
				if tc.expire {
					// Advance the effective lifecycle without sleeping or removing
					// the operation-owned marker that failure still must release.
					sub.ExpiresAt = now.Add(-time.Minute)
					require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).SetExpiresAt(sub.ExpiresAt).Exec(ctx))
				}
				if tc.rejectCompletion {
					_, err := db.ExecContext(ctx, "DROP TRIGGER reject_refund_fixture ON payment_orders")
					require.NoError(t, err)
				}
				refundID := ""
				if tc.manualReference {
					_, err := workflow.QueryAndFinalizeRefund(ctx, order.ID)
					require.Equal(t, "REFUND_REFERENCE_REQUIRED", apperror.Reason(err))
					_, err = workflow.QueryAndFinalizeRefundWithReference(ctx, order.ID, "re_unrelated")
					require.Equal(t, "REFUND_REFERENCE_UNVERIFIED", apperror.Reason(err))
					persisted, err := store.Order(ctx, order.ID)
					require.NoError(t, err)
					receipt, err := store.RefundRecovery(ctx, persisted)
					require.NoError(t, err)
					require.Empty(t, receipt.RefundID)
					stillHeld, err := client.UserSubscription.Get(ctx, sub.ID)
					require.NoError(t, err)
					require.NotNil(t, stillHeld.RefundHoldOperationID)
					refundID = "re_fixture"
				}
				result, err := workflow.QueryAndFinalizeRefundWithReference(ctx, order.ID, refundID)
				require.NoError(t, err)
				require.Equal(t, tc.terminal == payment.ProviderStatusSuccess, result.Success)
				if tc.initial != "unknown" {
					require.Equal(t, "re_fixture", channel.queriedID)
				}
				require.Equal(t, int64(1), channel.submissions.Load())
			}
			saved, err := client.UserSubscription.Get(mixins.SkipSoftDelete(ctx), sub.ID)
			require.NoError(t, err)
			require.Nil(t, saved.RefundHoldOperationID)
			require.Equal(t, 7.0, saved.MonthlyUsageUsd)
			revoked := !tc.optOut && tc.terminal != payment.ProviderStatusFailed
			require.Equal(t, revoked, saved.DeletedAt != nil)
			scheduled, err := client.UserSubscription.Get(ctx, next.ID)
			require.NoError(t, err)
			if revoked {
				expectedStart := now
				if tc.queued {
					expectedStart = sub.StartsAt
				}
				require.Equal(t, expectedStart, scheduled.StartsAt)
			} else {
				require.Equal(t, next.StartsAt, scheduled.StartsAt)
				require.Equal(t, sub.ExpiresAt, saved.ExpiresAt)
			}
		})
	}

	t.Run("balance compensation", func(t *testing.T) {
		ctx := t.Context()
		user, err := client.User.Create().
			SetEmail("refund-balance@example.invalid").
			SetPasswordHash("fixture").
			SetBalance(50).SetTotalRecharged(75).Save(ctx)
		require.NoError(t, err)
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		txctx := ent.NewTxContext(ctx, tx)
		rights := newPaymentRefundRights(tx, nil, billing.DateRuntime{})
		amount, err := rights.DeductBalance(txctx, user.ID, 20)
		require.NoError(t, err)
		require.Equal(t, 20.0, amount)
		require.NoError(t, rights.CompensateBalance(txctx, user.ID, amount))
		require.NoError(t, tx.Commit())
		saved, err := client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, 50.0, saved.Balance)
		require.Equal(t, 75.0, saved.TotalRecharged)
	})
}
