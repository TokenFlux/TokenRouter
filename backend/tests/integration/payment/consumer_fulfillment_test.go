//go:build unit

package payment_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"
	sqlitetest "github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type paymentFulfillmentAffiliateAccrueCall struct {
	inviterID     int64
	inviteeUserID int64
	amount        float64
	freezeHours   int
	sourceOrderID *int64
}

type paymentFulfillmentAffiliateRepoStub struct {
	inviteeSummary *promotion.AffiliateSummary
	inviterSummary *promotion.AffiliateSummary
	accruedRebate  float64
	accrueCalls    []paymentFulfillmentAffiliateAccrueCall
}

func (r *paymentFulfillmentAffiliateRepoStub) EnsureUserAffiliate(_ context.Context, userID int64) (*promotion.AffiliateSummary, error) {
	switch {
	case r.inviteeSummary != nil && r.inviteeSummary.UserID == userID:
		cp := *r.inviteeSummary
		return &cp, nil
	case r.inviterSummary != nil && r.inviterSummary.UserID == userID:
		cp := *r.inviterSummary
		return &cp, nil
	default:
		return &promotion.AffiliateSummary{UserID: userID, AffCode: "AFFTEST", CreatedAt: time.Now().Add(-time.Hour)}, nil
	}
}

func (r *paymentFulfillmentAffiliateRepoStub) GetAffiliateByCode(context.Context, string) (*promotion.AffiliateSummary, error) {
	panic("unexpected GetAffiliateByCode call")
}

func (r *paymentFulfillmentAffiliateRepoStub) BindInviter(context.Context, int64, int64) (bool, error) {
	panic("unexpected BindInviter call")
}

func (r *paymentFulfillmentAffiliateRepoStub) AccrueQuota(_ context.Context, inviterID, inviteeUserID int64, amount float64, freezeHours int, sourceOrderID *int64) (bool, error) {
	var sourceCopy *int64
	if sourceOrderID != nil {
		v := *sourceOrderID
		sourceCopy = &v
	}
	r.accrueCalls = append(r.accrueCalls, paymentFulfillmentAffiliateAccrueCall{
		inviterID:     inviterID,
		inviteeUserID: inviteeUserID,
		amount:        amount,
		freezeHours:   freezeHours,
		sourceOrderID: sourceCopy,
	})
	return true, nil
}

func (r *paymentFulfillmentAffiliateRepoStub) GetAccruedRebateFromInvitee(context.Context, int64, int64) (float64, error) {
	return r.accruedRebate, nil
}

func (r *paymentFulfillmentAffiliateRepoStub) ThawFrozenQuota(context.Context, int64) (float64, error) {
	panic("unexpected ThawFrozenQuota call")
}

func (r *paymentFulfillmentAffiliateRepoStub) TransferQuotaToBalance(context.Context, int64) (float64, float64, error) {
	panic("unexpected TransferQuotaToBalance call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListInvitees(context.Context, int64, int) ([]promotion.AffiliateInvitee, error) {
	panic("unexpected ListInvitees call")
}

func (r *paymentFulfillmentAffiliateRepoStub) UpdateUserAffCode(context.Context, int64, string) error {
	panic("unexpected UpdateUserAffCode call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ResetUserAffCode(context.Context, int64) (string, error) {
	panic("unexpected ResetUserAffCode call")
}

func (r *paymentFulfillmentAffiliateRepoStub) SetUserRebateRate(context.Context, int64, *float64) error {
	panic("unexpected SetUserRebateRate call")
}

func (r *paymentFulfillmentAffiliateRepoStub) BatchSetUserRebateRate(context.Context, []int64, *float64) error {
	panic("unexpected BatchSetUserRebateRate call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListUsersWithCustomSettings(context.Context, promotion.AffiliateAdminFilter) ([]promotion.AffiliateAdminEntry, int64, error) {
	panic("unexpected ListUsersWithCustomSettings call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListAffiliateInviteRecords(context.Context, promotion.AffiliateRecordFilter) ([]promotion.AffiliateInviteRecord, int64, error) {
	panic("unexpected ListAffiliateInviteRecords call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListAffiliateRebateRecords(context.Context, promotion.AffiliateRecordFilter) ([]promotion.AffiliateRebateRecord, int64, error) {
	panic("unexpected ListAffiliateRebateRecords call")
}

func (r *paymentFulfillmentAffiliateRepoStub) ListAffiliateTransferRecords(context.Context, promotion.AffiliateRecordFilter) ([]promotion.AffiliateTransferRecord, int64, error) {
	panic("unexpected ListAffiliateTransferRecords call")
}

func (r *paymentFulfillmentAffiliateRepoStub) GetAffiliateUserOverview(context.Context, int64) (*promotion.AffiliateUserOverview, error) {
	panic("unexpected GetAffiliateUserOverview call")
}

type paymentFulfillmentSettingRepoStub struct {
	values map[string]string
}

func (s *paymentFulfillmentSettingRepoStub) Get(context.Context, string) (*settings.Setting, error) {
	return nil, settings.ErrSettingNotFound
}

func (s *paymentFulfillmentSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if s.values == nil {
		return "", settings.ErrSettingNotFound
	}
	value, ok := s.values[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return value, nil
}

func (s *paymentFulfillmentSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return nil
}

func (s *paymentFulfillmentSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = s.values[key]
	}
	return out, nil
}

func (s *paymentFulfillmentSettingRepoStub) SetMultiple(_ context.Context, values map[string]string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	for key, value := range values {
		s.values[key] = value
	}
	return nil
}

func (s *paymentFulfillmentSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return s.values, nil
}

func (s *paymentFulfillmentSettingRepoStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func ensurePaymentAuditOrderActionUniqueIndex(t *testing.T, ctx context.Context, client *dbent.Client) {
	t.Helper()
	_, err := client.ExecContext(ctx, "CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_audit_logs_order_action_uniq ON payment_audit_logs(order_id, action)")
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// resolveRedeemAction — pure idempotency decision logic
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Table-driven comprehensive test
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// redeemAction enum value sanity
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// RedeemCode.IsUsed / CanUse interaction with resolveRedeemAction
// ---------------------------------------------------------------------------

func TestParseLegacyPaymentOrderID(t *testing.T) {
	t.Parallel()

	oid, ok := payment.ParseLegacyPaymentOrderID("sub2_42", dbent.IsNotFound(&dbent.NotFoundError{}))
	assert.True(t, ok)
	assert.EqualValues(t, 42, oid)

	_, ok = payment.ParseLegacyPaymentOrderID("42", dbent.IsNotFound(&dbent.NotFoundError{}))
	assert.False(t, ok)

	_, ok = payment.ParseLegacyPaymentOrderID("sub2_42", dbent.IsNotFound(errors.New("db down")))
	assert.False(t, ok)
}

func TestPaymentSuccessRecoversNonPendingOrdersIdempotently(t *testing.T) {
	statuses := []string{payment.OrderStatusProcessing, payment.OrderStatusExpired, payment.OrderStatusCancelled}
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			client := sqlitetest.NewClient(t)
			ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
			order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, status, time.Now())
			_, err := client.PaymentAuditLog.Create().
				SetOrderID(strconv.FormatInt(order.ID, 10)).
				SetAction("SUBSCRIPTION_ASSIGNED").
				SetDetail(`{"planID":100}`).
				SetOperator("system").
				Save(ctx)
			require.NoError(t, err)

			svc := paymenttestkit.Fulfillment(client, nil, &billing.SubscriptionService{}, nil)
			notification := &payment.PaymentNotification{
				TradeNo: "trade-recovered-" + status,
				OrderID: order.OutTradeNo,
				Amount:  order.PayAmount,
				Status:  payment.NotificationStatusSuccess,
			}
			require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeAlipay))
			require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeAlipay))

			reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
			recoveredCount, err := client.PaymentAuditLog.Query().Where(
				paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
				paymentauditlog.ActionEQ("ORDER_RECOVERED"),
			).Count(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, recoveredCount)
		})
	}
}

func TestPaymentSuccessWinsOverOutOfOrderFailure(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusPending, time.Now())
	order, err := client.PaymentOrder.UpdateOneID(order.ID).SetPaymentType(payment.TypeStripe).Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("SUBSCRIPTION_ASSIGNED").
		SetDetail(`{"planID":100}`).
		SetOperator("system").
		Save(ctx)
	require.NoError(t, err)

	svc := paymenttestkit.Fulfillment(client, nil, &billing.SubscriptionService{}, nil)
	failure := &payment.PaymentNotification{
		TradeNo: "trade-failed-first",
		OrderID: order.OutTradeNo,
		Status:  payment.ProviderStatusFailed,
		Metadata: map[string]string{
			"currency": payment.DefaultPaymentCurrency,
		},
	}
	require.NoError(t, svc.HandlePaymentNotification(ctx, failure, payment.TypeStripe))

	success := &payment.PaymentNotification{
		TradeNo: "trade-paid-late",
		OrderID: order.OutTradeNo,
		Amount:  order.PayAmount,
		Status:  payment.NotificationStatusSuccess,
		Metadata: map[string]string{
			"currency": payment.DefaultPaymentCurrency,
		},
	}
	require.NoError(t, svc.HandlePaymentNotification(ctx, success, payment.TypeStripe))
	require.NoError(t, svc.HandlePaymentNotification(ctx, failure, payment.TypeStripe))

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
	require.Equal(t, "trade-paid-late", reloaded.PaymentTradeNo)
}

func TestNonStripeFailureNotificationKeepsLegacyIgnoreBehavior(t *testing.T) {
	svc := paymenttestkit.Fulfillment(nil, nil, nil, nil)
	err := svc.HandlePaymentNotification(context.Background(), &payment.PaymentNotification{
		OrderID: "unknown-order",
		Status:  payment.ProviderStatusFailed,
	}, payment.TypeAlipay)
	require.NoError(t, err)
}

func TestRetryFulfillmentRejectsFreshRechargingLease(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusRecharging, time.Now())

	svc := paymenttestkit.Fulfillment(client, nil, nil, nil)
	err := svc.RetryFulfillment(ctx, order.ID)
	require.Error(t, err)
	require.Equal(t, "CONFLICT", apperror.Reason(err))

	reloaded, getErr := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, getErr)
	require.Equal(t, payment.OrderStatusRecharging, reloaded.Status)
}

func TestExecuteFulfillmentRecoversStaleRechargingLease(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentSubscriptionOrder(
		t,
		ctx,
		client,
		payment.OrderStatusRecharging,
		time.Now().Add(-payment.FulfillmentLeaseDuration-time.Minute),
	)
	_, err := client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("SUBSCRIPTION_ASSIGNED").
		SetDetail(`{"planID":100}`).
		SetOperator("system").
		Save(ctx)
	require.NoError(t, err)

	svc := paymenttestkit.Fulfillment(client, nil, &billing.SubscriptionService{}, nil)

	require.NoError(t, svc.ExecuteFulfillment(ctx, order.ID))
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
}

func TestFulfillmentLeaseVersionRejectsStaleWorker(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	staleAt := time.Now().Add(-payment.FulfillmentLeaseDuration - time.Minute)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusRecharging, staleAt)
	svc := paymenttestkit.Fulfillment(client, nil, nil, nil)

	firstLease, err := svc.AcquirePaymentFulfillmentLease(ctx, paymentpostgres.OrderFromEntity(order))
	require.NoError(t, err)
	require.NotNil(t, firstLease)

	_, err = client.PaymentOrder.UpdateOneID(order.ID).SetUpdatedAt(staleAt).Save(ctx)
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	staleOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	secondLease, err := svc.AcquirePaymentFulfillmentLease(ctx, paymentpostgres.OrderFromEntity(staleOrder))
	require.NoError(t, err)
	require.NotNil(t, secondLease)
	require.False(t, firstLease.Version.Equal(secondLease.Version))

	err = svc.MarkCompleted(ctx, paymentpostgres.OrderFromEntity(order), firstLease, "SUBSCRIPTION_SUCCESS")
	require.Error(t, err)
	require.Equal(t, "CONFLICT", apperror.Reason(err))
	svc.MarkFailed(ctx, order.ID, firstLease, errors.New("stale worker failure"))

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusRecharging, reloaded.Status)
	require.NoError(t, svc.MarkCompleted(ctx, paymentpostgres.OrderFromEntity(order), secondLease, "SUBSCRIPTION_SUCCESS"))
}

func TestExecuteBalanceFulfillmentRecoversAfterRedeemWithoutCreditingAgain(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	staleAt := time.Now().Add(-payment.FulfillmentLeaseDuration - time.Minute)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusRecharging, staleAt)
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).
		SetAmount(1000).
		SetPayAmount(100).
		ClearPlanID().
		SetUpdatedAt(staleAt).
		Save(ctx)
	require.NoError(t, err)

	redeemRepo := &redeemCodeRepoStub{codesByCode: map[string]*billing.RedeemCode{
		order.RechargeCode: {
			ID:        101,
			Code:      order.RechargeCode,
			Type:      billing.RedeemTypeBalance,
			Value:     order.Amount,
			Status:    billing.StatusUsed,
			MaxUses:   1,
			UsedCount: 1,
		},
	}}
	inviterID := int64(9001)
	affiliateRepo := &paymentFulfillmentAffiliateRepoStub{
		inviteeSummary: &promotion.AffiliateSummary{
			UserID:    order.UserID,
			InviterID: &inviterID,
			CreatedAt: time.Now().Add(-time.Hour),
		},
		inviterSummary: &promotion.AffiliateSummary{
			UserID:    inviterID,
			CreatedAt: time.Now().Add(-time.Hour),
		},
	}
	settingSvc := promotion.NewRuntimeSettings(&paymentFulfillmentSettingRepoStub{values: map[string]string{
		promotion.SettingKeyAffiliateEnabled:    "true",
		promotion.SettingKeyAffiliateRebateRate: "20",
	}})
	svc := paymenttestkit.Fulfillment(client,
		fulfillmentRedeemer(redeemRepo), nil, promotion.NewAffiliateService(affiliateRepo, settingSvc, nil, nil, promotion.Runtime{}))

	require.NoError(t, svc.ExecuteBalanceFulfillment(ctx, order.ID))
	require.Empty(t, redeemRepo.useCalls, "an already-used order code must not be redeemed again")
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
	require.Len(t, affiliateRepo.accrueCalls, 1)
	require.Equal(t, 200.0, affiliateRepo.accrueCalls[0].amount)
	applied, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("AFFILIATE_REBATE_APPLIED")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, applied.Detail, `"baseAmount":1000`)
	require.Contains(t, applied.Detail, `"rebateAmount":200`)
}

func TestDuplicatePaymentNotificationDoesNotReprocessCompletedBalanceOrder(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusCompleted, time.Now())
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).
		ClearPlanID().
		Save(ctx)
	require.NoError(t, err)

	redeemRepo := &redeemCodeRepoStub{codesByCode: map[string]*billing.RedeemCode{
		order.RechargeCode: {
			ID:     102,
			Code:   order.RechargeCode,
			Type:   billing.RedeemTypeBalance,
			Value:  order.Amount,
			Status: billing.StatusUnused,
		},
	}}
	svc := paymenttestkit.Fulfillment(client,
		fulfillmentRedeemer(redeemRepo), nil, nil)

	notification := &payment.PaymentNotification{
		TradeNo: "alipay-trade-replayed",
		OrderID: order.OutTradeNo,
		Amount:  order.PayAmount,
		Status:  payment.NotificationStatusSuccess,
	}
	require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeAlipay))
	require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeAlipay))

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
	require.Empty(t, redeemRepo.useCalls, "a duplicate notification must not redeem the balance code again")
}

func TestPaymentNotificationRejectsAmountMismatchBeforeFulfillment(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusPending, time.Now())
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).
		ClearPlanID().
		Save(ctx)
	require.NoError(t, err)

	svc := paymenttestkit.Fulfillment(client, nil, nil, nil)
	err = svc.HandlePaymentNotification(ctx, &payment.PaymentNotification{
		TradeNo: "alipay-trade-wrong-amount",
		OrderID: order.OutTradeNo,
		Amount:  order.PayAmount - 1,
		Status:  payment.NotificationStatusSuccess,
	}, payment.TypeAlipay)
	require.ErrorContains(t, err, "amount mismatch")

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusPending, reloaded.Status)
}

func TestExecuteSubscriptionFulfillmentRecoversCommittedAssignmentWithoutExtendingAgain(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	staleAt := time.Now().Add(-payment.FulfillmentLeaseDuration - time.Minute)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusRecharging, staleAt)

	expiresAt := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	sourceOrderID := order.ID
	subRepo := billingtestkit.NewSubscriptionRepository()
	subRepo.Seed(&billing.UserSubscription{
		ID:            99,
		UserID:        order.UserID,
		PlanID:        *order.PlanID,
		StartsAt:      time.Now().Add(-time.Hour),
		ExpiresAt:     expiresAt,
		Status:        billing.SubscriptionStatusActive,
		SourceOrderID: &sourceOrderID,
		Notes:         "payment order already assigned",
	})
	svc := paymenttestkit.Fulfillment(client, nil, billing.NewSubscriptionService(nil, subRepo, billingpostgres.NewSubscriptionMutations(nil)), nil)

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	assertPaymentSubscriptionExpiry(t, subRepo, order, expiresAt)

	assignmentAuditCount, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("SUBSCRIPTION_ASSIGNED"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, assignmentAuditCount)

	// 模拟完成后再次恢复过期租约，持久化审计必须保证订阅权益不会重复发放。
	_, err = client.PaymentOrder.UpdateOneID(order.ID).
		SetStatus(payment.OrderStatusRecharging).
		SetUpdatedAt(staleAt).
		ClearCompletedAt().
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	assertPaymentSubscriptionExpiry(t, subRepo, order, expiresAt)

	assignmentAuditCount, err = client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("SUBSCRIPTION_ASSIGNED"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, assignmentAuditCount)
}

func createPaymentFulfillmentSubscriptionOrder(
	t *testing.T,
	ctx context.Context,
	client *dbent.Client,
	status string,
	updatedAt time.Time,
) *dbent.PaymentOrder {
	t.Helper()
	user, err := client.User.Create().
		SetEmail("fulfillment-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com").
		SetPasswordHash("hash").
		SetUsername("payment-fulfillment-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(80).
		SetPayAmount(80).
		SetFeeRate(0).
		SetRechargeCode("PAY-SUB-" + strconv.FormatInt(time.Now().UnixNano(), 10)).
		SetOutTradeNo("sub2_fulfillment_" + strconv.FormatInt(time.Now().UnixNano(), 10)).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-fulfillment").
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(100).
		SetStatus(status).
		SetPaidAt(time.Now().Add(-time.Hour)).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetUpdatedAt(updatedAt).
		Save(ctx)
	require.NoError(t, err)
	return order
}

func assertPaymentSubscriptionExpiry(t *testing.T, repo *billingtestkit.SubscriptionRepository, order *dbent.PaymentOrder, expected time.Time) {
	t.Helper()
	subs, err := repo.ListBySourceOrderID(context.Background(), order.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	sub := subs[0]
	require.True(t, sub.ExpiresAt.Equal(expected), "subscription expiry changed from %s to %s", expected, sub.ExpiresAt)
}

func TestExecuteSubscriptionFulfillmentAppliesAffiliateRebateFromPurchasedPoints(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	user, err := client.User.Create().
		SetEmail("subscription-affiliate@example.com").
		SetPasswordHash("hash").
		SetUsername("subscription-affiliate-user").
		Save(ctx)
	require.NoError(t, err)

	monthlyPoints := 1000.0
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(100).
		SetFeeRate(0).
		SetRechargeCode("PAY-SUB-AFFILIATE").
		SetOutTradeNo("sub2_subscription_affiliate").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-sub-affiliate").
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(99).
		SetPlanSnapshot(billing.SubscriptionPlanSnapshot{
			Name:            "Pro",
			Price:           100,
			ValidityDays:    30,
			MonthlyLimitUSD: &monthlyPoints,
		}).
		SetStatus(payment.OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	inviterID := int64(9001)
	affiliateRepo := &paymentFulfillmentAffiliateRepoStub{
		inviteeSummary: &promotion.AffiliateSummary{
			UserID:    user.ID,
			AffCode:   "INVITEE",
			InviterID: &inviterID,
			CreatedAt: time.Now().Add(-24 * time.Hour),
		},
		inviterSummary: &promotion.AffiliateSummary{
			UserID:    inviterID,
			AffCode:   "INVITER",
			CreatedAt: time.Now().Add(-48 * time.Hour),
		},
	}
	settingSvc := promotion.NewRuntimeSettings(&paymentFulfillmentSettingRepoStub{values: map[string]string{
		promotion.SettingKeyAffiliateEnabled:           "true",
		promotion.SettingKeyAffiliateRebateRate:        "20",
		promotion.SettingKeyAffiliateRebateFreezeHours: "0",
	}})
	subRepo := billingtestkit.NewSubscriptionRepository()
	subscriptionSvc := billing.NewSubscriptionService(nil, subRepo, billingpostgres.NewSubscriptionMutations(nil))
	svc := paymenttestkit.Fulfillment(client, nil, subscriptionSvc,
		promotion.NewAffiliateService(affiliateRepo, settingSvc, nil, nil, promotion.Runtime{}))

	err = svc.ExecuteSubscriptionFulfillment(ctx, order.ID)
	require.NoError(t, err)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
	require.Len(t, affiliateRepo.accrueCalls, 1)
	require.Equal(t, inviterID, affiliateRepo.accrueCalls[0].inviterID)
	require.Equal(t, user.ID, affiliateRepo.accrueCalls[0].inviteeUserID)
	require.Equal(t, 200.0, affiliateRepo.accrueCalls[0].amount)
	require.NotNil(t, affiliateRepo.accrueCalls[0].sourceOrderID)
	require.Equal(t, order.ID, *affiliateRepo.accrueCalls[0].sourceOrderID)
	require.Equal(t, 1, subRepo.CreateCalls)

	sub, err := subRepo.GetLatestByUserIDAndPlanID(ctx, user.ID, 99)
	require.NoError(t, err)
	require.Equal(t, order.ID, *sub.SourceOrderID)

	assigned, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("SUBSCRIPTION_ASSIGNED")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, assigned.Detail, `"planID":99`)

	applied, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("AFFILIATE_REBATE_APPLIED")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, applied.Detail, `"baseAmount":1000`)
	require.Contains(t, applied.Detail, `"rebateAmount":200`)
}

// TestAccrueInviteRebateCapsPurchasedPoints 验证单人上限与返利都使用推理积分单位。
func TestAccrueInviteRebateCapsPurchasedPoints(t *testing.T) {
	ctx := context.Background()
	inviterID := int64(9001)
	inviteeID := int64(9002)
	repo := &paymentFulfillmentAffiliateRepoStub{
		inviteeSummary: &promotion.AffiliateSummary{
			UserID:    inviteeID,
			InviterID: &inviterID,
			CreatedAt: time.Now().Add(-time.Hour),
		},
		inviterSummary: &promotion.AffiliateSummary{
			UserID:    inviterID,
			CreatedAt: time.Now().Add(-time.Hour),
		},
		accruedRebate: 175,
	}
	settingSvc := promotion.NewRuntimeSettings(&paymentFulfillmentSettingRepoStub{values: map[string]string{
		promotion.SettingKeyAffiliateEnabled:             "true",
		promotion.SettingKeyAffiliateRebateRate:          "20",
		promotion.SettingKeyAffiliateRebatePerInviteeCap: "250",
	}})
	svc := promotion.NewAffiliateService(repo, settingSvc, nil, nil, promotion.Runtime{})

	rebate, err := svc.AccrueInviteRebate(ctx, inviteeID, 1000)
	require.NoError(t, err)
	require.Equal(t, 75.0, rebate)
	require.Len(t, repo.accrueCalls, 1)
	require.Equal(t, 75.0, repo.accrueCalls[0].amount)
}

func TestExecuteSubscriptionFulfillmentDoesNotDuplicateWorkAfterLegacySuccessAudit(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	user, err := client.User.Create().
		SetEmail("subscription-affiliate-idempotent@example.com").
		SetPasswordHash("hash").
		SetUsername("subscription-affiliate-idempotent-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(80).
		SetPayAmount(80).
		SetFeeRate(0).
		SetRechargeCode("PAY-SUB-AFFILIATE-IDEMPOTENT").
		SetOutTradeNo("sub2_subscription_affiliate_idempotent").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-sub-affiliate-idempotent").
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(100).
		SetPlanSnapshot(billing.SubscriptionPlanSnapshot{
			Name:         "Legacy",
			Price:        80,
			ValidityDays: 30,
		}).
		SetStatus(payment.OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("SUBSCRIPTION_SUCCESS").
		SetDetail(`{"planID":100}`).
		SetOperator("system").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("AFFILIATE_REBATE_APPLIED").
		SetDetail(`{"baseAmount":80,"rebateAmount":16}`).
		SetOperator("system").
		Save(ctx)
	require.NoError(t, err)

	inviterID := int64(9001)
	affiliateRepo := &paymentFulfillmentAffiliateRepoStub{
		inviteeSummary: &promotion.AffiliateSummary{
			UserID:    user.ID,
			AffCode:   "INVITEE",
			InviterID: &inviterID,
			CreatedAt: time.Now().Add(-24 * time.Hour),
		},
		inviterSummary: &promotion.AffiliateSummary{
			UserID:    inviterID,
			AffCode:   "INVITER",
			CreatedAt: time.Now().Add(-48 * time.Hour),
		},
	}
	settingSvc := promotion.NewRuntimeSettings(&paymentFulfillmentSettingRepoStub{values: map[string]string{
		promotion.SettingKeyAffiliateEnabled:    "true",
		promotion.SettingKeyAffiliateRebateRate: "20",
	}})
	subRepo := billingtestkit.NewSubscriptionRepository()
	subscriptionSvc := billing.NewSubscriptionService(nil, subRepo, billingpostgres.NewSubscriptionMutations(nil))
	svc := paymenttestkit.Fulfillment(client, nil, subscriptionSvc,
		promotion.NewAffiliateService(affiliateRepo, settingSvc, nil, nil, promotion.Runtime{}))

	err = svc.ExecuteSubscriptionFulfillment(ctx, order.ID)
	require.NoError(t, err)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
	require.Empty(t, affiliateRepo.accrueCalls)
	require.Zero(t, subRepo.CreateCalls)
}

var _ promotion.AffiliateRepository = (*paymentFulfillmentAffiliateRepoStub)(nil)
var _ settings.Repository = (*paymentFulfillmentSettingRepoStub)(nil)

// 替身同步执行事务回调；真实行锁行为由 PostgreSQL 集成验证。
func (r *paymentFulfillmentAffiliateRepoStub) WithLockedInviter(ctx context.Context, _ int64, fn func(context.Context) error) error {
	return fn(ctx)
}
