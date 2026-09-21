//go:build unit

package payment_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"
	sqlitetest "github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"entgo.io/ent/dialect"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/enttest"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"

	_ "modernc.org/sqlite"
)

type paymentOrderLifecycleQueryProvider struct {
	key               string
	lastQueryTradeNo  string
	queryTradeNos     []string
	lastCancelTradeNo string
	queryCalls        int
	cancelCalls       int
	responses         []*payment.QueryOrderResponse
	resp              *payment.QueryOrderResponse
	queryErr          error
	cancelErr         error
}

type paymentOrderLifecycleRedeemRepo struct {
	codesByCode map[string]*billing.RedeemCode
	// 按兑换码和用户记录使用轨迹，模拟新仓储接口的去重查询。
	usageByRedeemCodeID map[int64]map[int64]*billing.RedeemCodeUsage
	useCalls            []struct {
		id     int64
		userID int64
	}
}

func (p *paymentOrderLifecycleQueryProvider) Name() string {
	return "payment-order-lifecycle-query-provider"
}

func (p *paymentOrderLifecycleQueryProvider) ProviderKey() string {
	if p.key != "" {
		return p.key
	}
	return payment.TypeAlipay
}

func (p *paymentOrderLifecycleQueryProvider) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{p.ProviderKey()}
}

func (p *paymentOrderLifecycleQueryProvider) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	panic("unexpected call")
}

func (p *paymentOrderLifecycleQueryProvider) QueryOrder(_ context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	p.lastQueryTradeNo = tradeNo
	p.queryTradeNos = append(p.queryTradeNos, tradeNo)
	p.queryCalls++
	if p.queryErr != nil {
		return nil, p.queryErr
	}
	if len(p.responses) > 0 {
		resp := p.responses[0]
		if len(p.responses) > 1 {
			p.responses = p.responses[1:]
		}
		return resp, nil
	}
	return p.resp, nil
}

func (p *paymentOrderLifecycleQueryProvider) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	panic("unexpected call")
}

func (p *paymentOrderLifecycleQueryProvider) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	panic("unexpected call")
}

func (p *paymentOrderLifecycleQueryProvider) CancelPayment(_ context.Context, tradeNo string) error {
	p.lastCancelTradeNo = tradeNo
	p.cancelCalls++
	return p.cancelErr
}

func (r *paymentOrderLifecycleRedeemRepo) Create(context.Context, *billing.RedeemCode) error {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) CreateBatch(context.Context, []billing.RedeemCode) error {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) GetByID(_ context.Context, id int64) (*billing.RedeemCode, error) {
	for _, code := range r.codesByCode {
		if code.ID != id {
			continue
		}
		cloned := *code
		return &cloned, nil
	}
	return nil, billing.ErrRedeemCodeNotFound
}

func (r *paymentOrderLifecycleRedeemRepo) GetByIDForUpdate(ctx context.Context, id int64) (*billing.RedeemCode, error) {
	return r.GetByID(ctx, id)
}

func (r *paymentOrderLifecycleRedeemRepo) GetByCode(_ context.Context, code string) (*billing.RedeemCode, error) {
	redeemCode, ok := r.codesByCode[code]
	if !ok {
		return nil, billing.ErrRedeemCodeNotFound
	}
	cloned := *redeemCode
	return &cloned, nil
}

func (r *paymentOrderLifecycleRedeemRepo) GetByCodeForUpdate(ctx context.Context, code string) (*billing.RedeemCode, error) {
	return r.GetByCode(ctx, code)
}

func (r *paymentOrderLifecycleRedeemRepo) Update(_ context.Context, code *billing.RedeemCode) error {
	if code == nil {
		return nil
	}
	cloned := *code
	if r.codesByCode == nil {
		r.codesByCode = make(map[string]*billing.RedeemCode)
	}
	r.codesByCode[cloned.Code] = &cloned
	return nil
}

func (r *paymentOrderLifecycleRedeemRepo) BatchUpdate(context.Context, []int64, billing.RedeemCodeBatchUpdateFields) (int64, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) Delete(context.Context, int64) error {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) Use(_ context.Context, id, userID int64) error {
	for code, redeemCode := range r.codesByCode {
		if redeemCode.ID != id {
			continue
		}
		now := time.Now().UTC()
		redeemCode.Status = billing.StatusUsed
		redeemCode.UsedBy = &userID
		redeemCode.UsedAt = &now
		r.codesByCode[code] = redeemCode
		r.useCalls = append(r.useCalls, struct {
			id     int64
			userID int64
		}{id: id, userID: userID})
		return nil
	}
	return billing.ErrRedeemCodeNotFound
}

func (r *paymentOrderLifecycleRedeemRepo) CreateUsage(_ context.Context, usage *billing.RedeemCodeUsage) error {
	if usage == nil {
		return nil
	}
	cloned := *usage
	if r.usageByRedeemCodeID == nil {
		r.usageByRedeemCodeID = make(map[int64]map[int64]*billing.RedeemCodeUsage)
	}
	if r.usageByRedeemCodeID[cloned.RedeemCodeID] == nil {
		r.usageByRedeemCodeID[cloned.RedeemCodeID] = make(map[int64]*billing.RedeemCodeUsage)
	}
	r.usageByRedeemCodeID[cloned.RedeemCodeID][cloned.UserID] = &cloned
	r.useCalls = append(r.useCalls, struct {
		id     int64
		userID int64
	}{
		id:     cloned.RedeemCodeID,
		userID: cloned.UserID,
	})
	return nil
}

func (r *paymentOrderLifecycleRedeemRepo) GetUsageByRedeemCodeAndUser(_ context.Context, redeemCodeID, userID int64) (*billing.RedeemCodeUsage, error) {
	if r.usageByRedeemCodeID == nil {
		return nil, nil
	}
	usagesByUser, ok := r.usageByRedeemCodeID[redeemCodeID]
	if !ok {
		return nil, nil
	}
	usage, ok := usagesByUser[userID]
	if !ok {
		return nil, nil
	}
	cloned := *usage
	return &cloned, nil
}

func (r *paymentOrderLifecycleRedeemRepo) List(context.Context, pagination.PaginationParams) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) ListByUser(context.Context, int64, int) ([]billing.RedeemCode, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) ListByUserPaginated(context.Context, int64, pagination.PaginationParams, string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected call")
}

func (r *paymentOrderLifecycleRedeemRepo) SumPositiveBalanceByUser(context.Context, int64) (float64, error) {
	panic("unexpected call")
}

func TestVerifyOrderByOutTradeNoBackfillsTradeNoFromPaidQuery(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-UPSTREAM-TRADE-NO").
		SetOutTradeNo("sub2_checkpaid_trade_no_missing").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &fulfillmentBalance{
		getByIDUser: &billing.UserSummary{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		if userRepo.getByIDUser != nil {
			userRepo.getByIDUser.Balance += amount
		}
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*billing.RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   billing.RedeemTypeBalance,
				Value:  order.Amount,
				Status: billing.StatusUnused,
			},
		},
	}
	redeemService := newFulfillmentRedeemService(
		redeemRepo,
		userRepo,

		client,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: "upstream-trade-123",
			Status:  payment.ProviderStatusPaid,
			Amount:  88,
		},
	}
	registry.Register(provider)

	svc := paymenttestkit.Lifecycle(client,
		registry,
		redeemService, nil, nil, true)

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Equal(t, payment.OrderStatusCompleted, got.Status)
	require.Equal(t, "upstream-trade-123", got.PaymentTradeNo)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
	require.Equal(t, "upstream-trade-123", reloaded.PaymentTradeNo)

	require.Equal(t, 88.0, userRepo.getByIDUser.Balance)
	require.Len(t, redeemRepo.useCalls, 1)
	require.Equal(t, int64(1), redeemRepo.useCalls[0].id)
	require.Equal(t, user.ID, redeemRepo.useCalls[0].userID)
}

func TestVerifyOrderByOutTradeNoRetriesZeroAmountPaidQueryOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid-retry@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-retry-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-UPSTREAM-RETRY").
		SetOutTradeNo("sub2_checkpaid_retry_zero_amount").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &fulfillmentBalance{
		getByIDUser: &billing.UserSummary{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		if userRepo.getByIDUser != nil {
			userRepo.getByIDUser.Balance += amount
		}
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*billing.RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   billing.RedeemTypeBalance,
				Value:  order.Amount,
				Status: billing.StatusUnused,
			},
		},
	}
	redeemService := newFulfillmentRedeemService(
		redeemRepo,
		userRepo,

		client,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		responses: []*payment.QueryOrderResponse{
			{
				TradeNo: "upstream-trade-zero",
				Status:  payment.ProviderStatusPaid,
				Amount:  0,
			},
			{
				TradeNo: "upstream-trade-retry",
				Status:  payment.ProviderStatusPaid,
				Amount:  88,
			},
		},
	}
	registry.Register(provider)

	svc := paymenttestkit.Lifecycle(client,
		registry,
		redeemService, nil, nil, true)

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, 2, provider.queryCalls)
	require.Equal(t, payment.OrderStatusCompleted, got.Status)
	require.Equal(t, "upstream-trade-retry", got.PaymentTradeNo)
}

func TestVerifyOrderByOutTradeNoRejectsPaidQueryWithZeroAmount(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid-zero-amount@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-zero-amount-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-ZERO-AMOUNT").
		SetOutTradeNo("sub2_checkpaid_zero_amount").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &fulfillmentBalance{
		getByIDUser: &billing.UserSummary{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*billing.RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   billing.RedeemTypeBalance,
				Value:  order.Amount,
				Status: billing.StatusUnused,
			},
		},
	}
	redeemService := newFulfillmentRedeemService(
		redeemRepo,
		userRepo,

		client,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: "upstream-trade-zero",
			Status:  payment.ProviderStatusPaid,
			Amount:  0,
		},
	}
	registry.Register(provider)

	svc := paymenttestkit.Lifecycle(client,
		registry,
		redeemService, nil, nil, true)

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Equal(t, payment.OrderStatusPending, got.Status)
	require.Empty(t, got.PaymentTradeNo)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusPending, reloaded.Status)
	require.Empty(t, reloaded.PaymentTradeNo)

	require.Equal(t, 0.0, userRepo.getByIDUser.Balance)
	require.Empty(t, redeemRepo.useCalls)
}

func TestVerifyOrderByOutTradeNoUsesOutTradeNoWhenPaymentTradeNoAlreadyExistsForAlipay(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid-existing-trade@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-existing-trade-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-EXISTING-TRADE-NO").
		SetOutTradeNo("sub2_checkpaid_use_out_trade_no").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("upstream-trade-existing").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &fulfillmentBalance{
		getByIDUser: &billing.UserSummary{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		if userRepo.getByIDUser != nil {
			userRepo.getByIDUser.Balance += amount
		}
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*billing.RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   billing.RedeemTypeBalance,
				Value:  order.Amount,
				Status: billing.StatusUnused,
			},
		},
	}
	redeemService := newFulfillmentRedeemService(
		redeemRepo,
		userRepo,

		client,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: "upstream-trade-existing",
			Status:  payment.ProviderStatusPaid,
			Amount:  88,
		},
	}
	registry.Register(provider)

	svc := paymenttestkit.Lifecycle(client,
		registry,
		redeemService, nil, nil, true)

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Equal(t, "upstream-trade-existing", got.PaymentTradeNo)
}

func TestVerifyOrderByOutTradeNoDoesNotCancelPendingUpstreamOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid-pending@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-pending-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-PENDING").
		SetOutTradeNo("sub2_checkpaid_pending").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: "upstream-trade-pending",
			Status:  payment.ProviderStatusPending,
			Amount:  88,
		},
	}
	registry.Register(provider)

	svc := paymenttestkit.Lifecycle(client,
		registry, nil, nil, nil, true)

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusPending, got.Status)
	require.Equal(t, 1, provider.queryCalls)
	require.Equal(t, 0, provider.cancelCalls)
}

func TestCancelOrderStillClosesPendingUpstreamOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("cancel-pending@example.com").
		SetPasswordHash("hash").
		SetUsername("cancel-pending-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CANCEL-PENDING").
		SetOutTradeNo("sub2_cancel_pending").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: order.OutTradeNo,
			Status:  payment.ProviderStatusPending,
			Amount:  0,
		},
	}
	registry.Register(provider)

	svc := paymenttestkit.Lifecycle(client,
		registry, nil, nil, nil, true)

	outcome, err := svc.CancelOrder(ctx, order.ID, user.ID)
	require.NoError(t, err)
	require.Equal(t, payment.LifecycleCheckPaidResultCancelled, outcome)
	require.Equal(t, order.OutTradeNo, provider.lastCancelTradeNo)
	require.Equal(t, 1, provider.cancelCalls)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCancelled, reloaded.Status)
}

func TestForceExpireOrderRecordsAuditAndRejectsRepeatedTransition(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusPending, time.Now().Add(time.Hour))
	svc := paymenttestkit.Lifecycle(client, nil, nil, nil, nil, false)

	require.NoError(t, svc.ForceExpireOrder(ctx, order.ID, "provider endpoint returned HTML"))
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusExpired, reloaded.Status)

	audit, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
		paymentauditlog.ActionEQ("ORDER_FORCE_EXPIRED"),
	).Only(ctx)
	require.NoError(t, err)
	require.Contains(t, audit.Detail, "provider endpoint returned HTML")

	err = svc.ForceExpireOrder(ctx, order.ID, "repeat")
	require.Error(t, err)
	require.Equal(t, 409, s15httpx.ErrorCode(err))
	require.Equal(t, "ORDER_STATUS_CHANGED", apperror.Reason(err))
}

func TestForceExpireOrderRollsBackWhenAuditWriteFails(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusPending, time.Now().Add(time.Hour))
	_, err := client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("ORDER_FORCE_EXPIRED").
		SetDetail(`{"reason":"existing audit record"}`).
		SetOperator("admin").
		Save(ctx)
	require.NoError(t, err)

	svc := paymenttestkit.Lifecycle(client, nil, nil, nil, nil, false)
	err = svc.ForceExpireOrder(ctx, order.ID, "provider endpoint returned HTML")
	require.Error(t, err)

	reloaded, getErr := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, getErr)
	require.Equal(t, payment.OrderStatusPending, reloaded.Status)
}

func TestCancelOrderReturnsStatusUnavailableWhenProviderQueryFails(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusPending, time.Now().Add(time.Hour))
	provider := &paymentOrderLifecycleQueryProvider{queryErr: errors.New("upstream unavailable")}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := paymenttestkit.Lifecycle(client, registry, nil, nil, nil, true)

	_, err := svc.CancelOrder(ctx, order.ID, order.UserID)
	require.Error(t, err)
	require.Equal(t, 503, s15httpx.ErrorCode(err))
	require.Equal(t, "PAYMENT_STATUS_UNAVAILABLE", apperror.Reason(err))

	reloaded, getErr := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, getErr)
	require.Equal(t, payment.OrderStatusPending, reloaded.Status)
	require.True(t, svc.HasAuditLog(ctx, order.ID, "PAYMENT_CANCEL_FAILED"))
}

func TestCancelOrderMovesToProcessingWhenCloseRacesWithCheckoutCompletion(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusPending, time.Now().Add(time.Hour))

	provider := &paymentOrderLifecycleQueryProvider{
		responses: []*payment.QueryOrderResponse{
			{TradeNo: order.OutTradeNo, Status: payment.ProviderStatusPending},
			{TradeNo: "pi_processing", Status: payment.ProviderStatusProcessing, Amount: order.PayAmount},
		},
		cancelErr: errors.New("checkout session can no longer be expired"),
	}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := paymenttestkit.Lifecycle(client, registry, nil, nil, nil, true)

	outcome, err := svc.CancelOrder(ctx, order.ID, order.UserID)
	require.Error(t, err)
	require.Equal(t, payment.LifecycleCheckPaidResultProcessing, outcome)
	require.Equal(t, 2, provider.queryCalls)
	require.Equal(t, 1, provider.cancelCalls)

	reloaded, getErr := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, getErr)
	require.Equal(t, payment.OrderStatusProcessing, reloaded.Status)
}

func TestExpireTimedOutOrdersKeepsPendingWhenCloseAndRequeryRemainUncertain(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	order := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusPending, time.Now().Add(-time.Minute))

	provider := &paymentOrderLifecycleQueryProvider{
		responses: []*payment.QueryOrderResponse{
			{TradeNo: order.OutTradeNo, Status: payment.ProviderStatusPending},
			{TradeNo: order.OutTradeNo, Status: payment.ProviderStatusPending},
		},
		cancelErr: errors.New("temporary stripe error"),
	}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := paymenttestkit.Lifecycle(client, registry, nil, nil, nil, true)

	expired, err := svc.ExpireTimedOutOrders(ctx)
	require.NoError(t, err)
	require.Zero(t, expired)
	require.Equal(t, 2, provider.queryCalls)
	require.Equal(t, 1, provider.cancelCalls)

	reloaded, getErr := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, getErr)
	require.Equal(t, payment.OrderStatusPending, reloaded.Status)
	require.True(t, svc.HasAuditLog(ctx, order.ID, "PAYMENT_CANCEL_FAILED"))
}

func TestExpireTimedOutOrdersBacksOffAfterProviderFailure(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	now := time.Now()
	order := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusPending, now.Add(-time.Minute))
	provider := &paymentOrderLifecycleQueryProvider{queryErr: errors.New("upstream unavailable")}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := paymenttestkit.Lifecycle(client, registry, nil, nil, nil, true)

	expired, err := svc.ExpireTimedOutOrdersAt(ctx, now)
	require.NoError(t, err)
	require.Zero(t, expired)
	require.Equal(t, 1, provider.queryCalls)

	expired, err = svc.ExpireTimedOutOrdersAt(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Zero(t, expired)
	require.Equal(t, 1, provider.queryCalls)

	expired, err = svc.ExpireTimedOutOrdersAt(ctx, now.Add(payment.LifecyclePaymentExpiryRetryDelay+time.Minute))
	require.NoError(t, err)
	require.Zero(t, expired)
	require.Equal(t, 2, provider.queryCalls)

	reloaded, getErr := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, getErr)
	require.Equal(t, payment.OrderStatusPending, reloaded.Status)
}

func TestReconcileProcessingOrdersFinalizesFailureAndAuditsStaleOnlyOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	staleOrder := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusProcessing, time.Now().Add(time.Hour))
	staleOrder, err := client.PaymentOrder.UpdateOneID(staleOrder.ID).
		SetUpdatedAt(time.Now().Add(-payment.LifecycleProcessingStaleAfter - time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{TradeNo: "pi_stale", Status: payment.ProviderStatusProcessing, Amount: staleOrder.PayAmount},
	}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := paymenttestkit.Lifecycle(client, registry, nil, nil, nil, true)

	for range 2 {
		recovered, err := svc.ReconcileProcessingOrders(ctx)
		require.NoError(t, err)
		require.Zero(t, recovered)
	}
	staleAuditCount, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(staleOrder.ID, 10)),
		paymentauditlog.ActionEQ("PAYMENT_PROCESSING_STALE"),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, staleAuditCount)

	provider.resp = &payment.QueryOrderResponse{TradeNo: "pi_failed", Status: payment.ProviderStatusFailed}
	recovered, err := svc.ReconcileProcessingOrders(ctx)
	require.NoError(t, err)
	require.Zero(t, recovered)
	reloaded, err := client.PaymentOrder.Get(ctx, staleOrder.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusExpired, reloaded.Status)
	require.True(t, svc.HasAuditLog(ctx, staleOrder.ID, "PAYMENT_FAILED"))
}

func TestReconcileProcessingOrdersRecoversPaidOrderAfterServiceRestart(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusProcessing, time.Now())
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetPaymentType(payment.TypeStripe).
		SetPaymentTradeNo("cs_restart_recovery").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("SUBSCRIPTION_ASSIGNED").
		SetDetail(`{"planID":100}`).
		SetOperator("system").
		Save(ctx)
	require.NoError(t, err)

	provider := &paymentOrderLifecycleQueryProvider{
		key: payment.TypeStripe,
		resp: &payment.QueryOrderResponse{
			TradeNo: "pi_restart_recovery",
			Status:  payment.ProviderStatusPaid,
			Amount:  order.PayAmount,
			Metadata: map[string]string{
				"currency": payment.DefaultPaymentCurrency,
			},
		},
	}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := paymenttestkit.Lifecycle(client, registry, nil, &billing.SubscriptionService{}, nil, true)

	recovered, err := svc.ReconcileProcessingOrders(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Equal(t, "cs_restart_recovery", provider.lastQueryTradeNo)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
	require.Equal(t, "pi_restart_recovery", reloaded.PaymentTradeNo)
}

func TestReconcilePageIDsCyclesAcrossEntireBacklog(t *testing.T) {
	t.Parallel()

	ids := make([]int64, 45)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	seen := make(map[int64]bool, len(ids))
	for cursor := range uint64(3) {
		page := payment.ReconcilePageIDs(ids, cursor, payment.LifecycleProcessingReconcileLimit)
		require.LessOrEqual(t, len(page), payment.LifecycleProcessingReconcileLimit)
		for _, id := range page {
			seen[id] = true
		}
	}
	require.Len(t, seen, len(ids))
}

func TestReconcileProcessingOrdersCyclesPastFirstBatch(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{Status: payment.ProviderStatusProcessing},
	}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := paymenttestkit.Lifecycle(client, registry, nil, nil, nil, true)

	for range payment.LifecycleProcessingReconcileLimit + 1 {
		order := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusProcessing, time.Now().Add(time.Hour))
		_, err := client.PaymentOrder.UpdateOneID(order.ID).
			SetPaymentTradeNo(order.OutTradeNo).
			Save(ctx)
		require.NoError(t, err)
	}

	for range 2 {
		recovered, err := svc.ReconcileProcessingOrdersAt(ctx, time.Now())
		require.NoError(t, err)
		require.Zero(t, recovered)
	}
	uniqueTradeNos := make(map[string]bool, len(provider.queryTradeNos))
	for _, tradeNo := range provider.queryTradeNos {
		uniqueTradeNos[tradeNo] = true
	}
	require.Len(t, uniqueTradeNos, payment.LifecycleProcessingReconcileLimit+1)
}

func TestReconcilePaidFulfillmentOrdersRetriesAfterQueryRecoveryFailure(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusProcessing, time.Now())
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetPaymentType(payment.TypeStripe).
		SetPaymentTradeNo("cs_fulfillment_retry").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("SUBSCRIPTION_ASSIGNED").
		SetDetail(`{"planID":100}`).
		SetOperator("system").
		Save(ctx)
	require.NoError(t, err)

	provider := &paymentOrderLifecycleQueryProvider{
		key: payment.TypeStripe,
		resp: &payment.QueryOrderResponse{
			TradeNo: "pi_fulfillment_retry",
			Status:  payment.ProviderStatusPaid,
			Amount:  order.PayAmount,
			Metadata: map[string]string{
				"currency": payment.DefaultPaymentCurrency,
			},
		},
	}
	registry := payment.NewRegistry()
	registry.Register(provider)
	svc := paymenttestkit.Lifecycle(client, registry, nil, nil, nil, true)

	recovered, err := svc.ReconcileProcessingOrdersAt(ctx, time.Now())
	require.NoError(t, err)
	require.Zero(t, recovered)
	failed, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusFailed, failed.Status)
	require.NotNil(t, failed.PaidAt)

	// 新运行图的依赖在构造时固定；恢复夹具重新装配，持久订单保持不变。
	svc = paymenttestkit.Lifecycle(client, registry, nil, &billing.SubscriptionService{}, nil, true)
	recovered, err = svc.ReconcilePaidFulfillmentOrdersAt(ctx, time.Now().Add(payment.LifecycleFulfillmentRetryDelay+time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
}

func TestReconcilePaidFulfillmentOrdersRecoversOnlyExpiredLease(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	now := time.Now()
	staleOrder := createPaymentFulfillmentSubscriptionOrder(
		t,
		ctx,
		client,
		payment.OrderStatusRecharging,
		now.Add(-payment.FulfillmentLeaseDuration-time.Minute),
	)
	freshOrder := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, payment.OrderStatusRecharging, now)
	for _, order := range []*dbent.PaymentOrder{staleOrder, freshOrder} {
		_, err := client.PaymentAuditLog.Create().
			SetOrderID(strconv.FormatInt(order.ID, 10)).
			SetAction("SUBSCRIPTION_ASSIGNED").
			SetDetail(`{"planID":100}`).
			SetOperator("system").
			Save(ctx)
		require.NoError(t, err)
	}

	svc := paymenttestkit.Lifecycle(client, nil, nil, &billing.SubscriptionService{}, nil, false)
	recovered, err := svc.ReconcilePaidFulfillmentOrdersAt(ctx, now)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)

	reloadedStale, err := client.PaymentOrder.Get(ctx, staleOrder.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloadedStale.Status)
	reloadedFresh, err := client.PaymentOrder.Get(ctx, freshOrder.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusRecharging, reloadedFresh.Status)
}

func TestReconcilePendingPaymentOrdersBackfillsPaidOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("wxpay-reconcile@example.com").
		SetPasswordHash("hash").
		SetUsername("wxpay-reconcile-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(50).
		SetPayAmount(50).
		SetFeeRate(0).
		SetRechargeCode("WXPAY-RECONCILE").
		SetOutTradeNo("sub2_wxpay_reconcile").
		SetPaymentType(payment.TypeWxpay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &fulfillmentBalance{
		getByIDUser: &billing.UserSummary{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		if userRepo.getByIDUser != nil {
			userRepo.getByIDUser.Balance += amount
		}
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*billing.RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   billing.RedeemTypeBalance,
				Value:  order.Amount,
				Status: billing.StatusUnused,
			},
		},
	}
	redeemService := newFulfillmentRedeemService(
		redeemRepo,
		userRepo,

		client,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		key: payment.TypeWxpay,
		resp: &payment.QueryOrderResponse{
			TradeNo: "wxpay-upstream-trade-123",
			Status:  payment.ProviderStatusPaid,
			Amount:  50,
			Metadata: map[string]string{
				"trade_state": "SUCCESS",
			},
		},
	}
	registry.Register(provider)

	svc := paymenttestkit.Lifecycle(client,
		registry,
		redeemService, nil, nil, true)

	recovered, err := svc.ReconcilePendingPaymentOrders(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Zero(t, provider.cancelCalls)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, reloaded.Status)
	require.Equal(t, "wxpay-upstream-trade-123", reloaded.PaymentTradeNo)
	require.Equal(t, 50.0, userRepo.getByIDUser.Balance)
	require.Len(t, redeemRepo.useCalls, 1)
}

func TestReconcilePendingPaymentOrdersQueriesAlipayOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("alipay-reconcile@example.com").
		SetPasswordHash("hash").
		SetUsername("alipay-reconcile-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(50).
		SetPayAmount(50).
		SetFeeRate(0).
		SetRechargeCode("ALIPAY-RECONCILE").
		SetOutTradeNo("sub2_alipay_reconcile").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		key: payment.TypeAlipay,
		resp: &payment.QueryOrderResponse{
			TradeNo: order.OutTradeNo,
			Status:  payment.ProviderStatusPending,
		},
	}
	registry.Register(provider)

	svc := paymenttestkit.Lifecycle(client,
		registry, nil, nil, nil, true)

	recovered, err := svc.ReconcilePendingPaymentOrders(ctx)
	require.NoError(t, err)
	require.Zero(t, recovered)
	require.Equal(t, 1, provider.queryCalls)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
}

func TestVerifyOrderByOutTradeNoUsesOutTradeNoWhenPaymentTradeNoAlreadyExistsForAlipayUpstreamRegression(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("checkpaid-existing-trade@example.com").
		SetPasswordHash("hash").
		SetUsername("checkpaid-existing-trade-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("CHECKPAID-EXISTING-TRADE-NO").
		SetOutTradeNo("sub2_checkpaid_use_out_trade_no").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("upstream-trade-existing").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	userRepo := &fulfillmentBalance{
		getByIDUser: &billing.UserSummary{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Balance:  0,
		},
	}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		if userRepo.getByIDUser != nil {
			userRepo.getByIDUser.Balance += amount
		}
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*billing.RedeemCode{
			order.RechargeCode: {
				ID:     1,
				Code:   order.RechargeCode,
				Type:   billing.RedeemTypeBalance,
				Value:  order.Amount,
				Status: billing.StatusUnused,
			},
		},
	}
	redeemService := newFulfillmentRedeemService(
		redeemRepo,
		userRepo,

		client,
	)
	registry := payment.NewRegistry()
	provider := &paymentOrderLifecycleQueryProvider{
		resp: &payment.QueryOrderResponse{
			TradeNo: "upstream-trade-existing",
			Status:  payment.ProviderStatusPaid,
			Amount:  88,
		},
	}
	registry.Register(provider)

	svc := paymenttestkit.Lifecycle(client,
		registry,
		redeemService, nil, nil, true)

	got, err := svc.VerifyOrderByOutTradeNo(ctx, order.OutTradeNo, user.ID)
	require.NoError(t, err)
	require.Equal(t, order.OutTradeNo, provider.lastQueryTradeNo)
	require.Equal(t, "upstream-trade-existing", got.PaymentTradeNo)
}

func TestPaymentOrderAllowsRegistryFallbackOnlyForLegacyOrdersWithoutPinnedProviderState(t *testing.T) {
	t.Parallel()

	require.True(t, payment.PaymentOrderAllowsRegistryFallback(paymentpostgres.OrderFromEntity(&dbent.PaymentOrder{
		PaymentType: payment.TypeAlipay,
	})))

	instanceID := "12"
	require.False(t, payment.PaymentOrderAllowsRegistryFallback(paymentpostgres.OrderFromEntity(&dbent.PaymentOrder{
		PaymentType:        payment.TypeAlipay,
		ProviderInstanceID: &instanceID,
	})))

	require.False(t, payment.PaymentOrderAllowsRegistryFallback(paymentpostgres.OrderFromEntity(&dbent.PaymentOrder{
		PaymentType: payment.TypeAlipay,
		ProviderSnapshot: map[string]any{
			"schema_version":       2,
			"provider_instance_id": "12",
		},
	})))
}

func TestPaymentOrderQueryReferenceUsesOutTradeNoForOfficialProviders(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType:    payment.TypeWxpay,
		OutTradeNo:     "sub2_out_trade_no",
		PaymentTradeNo: "wx-transaction-id",
	}

	require.Equal(t, "sub2_out_trade_no", payment.PaymentOrderQueryReference(paymentpostgres.OrderFromEntity(order), &paymentOrderLifecycleQueryProvider{}))
	require.Equal(t, "sub2_out_trade_no", payment.PaymentOrderQueryReference(paymentpostgres.OrderFromEntity(order), paymenttestkit.StaticProvider{
		Key: payment.TypeWxpay,
	}))
}

func TestPaymentOrderQueryReferenceUsesInvoiceIDForStripeOrders(t *testing.T) {
	t.Parallel()

	invoiceID := "in_123"
	providerKey := payment.TypeStripe
	order := &dbent.PaymentOrder{
		PaymentType:      payment.TypeStripe,
		OutTradeNo:       "sub2_out_trade_no",
		PaymentTradeNo:   "pi_123",
		PaymentInvoiceID: &invoiceID,
		ProviderKey:      &providerKey,
	}

	require.Equal(t, "in_123", payment.PaymentOrderQueryReference(paymentpostgres.OrderFromEntity(order), paymenttestkit.StaticProvider{
		Key: payment.TypeStripe,
	}))
	require.Equal(t, "in_123", payment.PaymentOrderQueryReference(paymentpostgres.OrderFromEntity(order), nil))
}

func TestPaymentOrderQueryReferenceKeepsCheckoutSessionForProcessingStripeOrders(t *testing.T) {
	t.Parallel()

	invoiceID := "in_123"
	providerKey := payment.TypeStripe
	order := &dbent.PaymentOrder{
		PaymentType:      payment.TypeStripe,
		OutTradeNo:       "sub2_out_trade_no",
		PaymentTradeNo:   "cs_123",
		PaymentInvoiceID: &invoiceID,
		ProviderKey:      &providerKey,
	}

	require.Equal(t, "cs_123", payment.PaymentOrderQueryReference(paymentpostgres.OrderFromEntity(order), paymenttestkit.StaticProvider{
		Key: payment.TypeStripe,
	}))
}

func TestPaymentOrderQueryReferenceFallsBackToTradeNoForLegacyStripeOrders(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		PaymentType:    payment.TypeStripe,
		OutTradeNo:     "sub2_out_trade_no",
		PaymentTradeNo: "pi_legacy",
	}

	require.Equal(t, "pi_legacy", payment.PaymentOrderQueryReference(paymentpostgres.OrderFromEntity(order), paymenttestkit.StaticProvider{
		Key: payment.TypeStripe,
	}))
}

func newPaymentOrderLifecycleTestClient(t *testing.T) *dbent.Client {
	t.Helper()

	db, err := sql.Open("sqlite", "file:payment_order_lifecycle?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func createPaymentOrderLifecycleOrder(t *testing.T, ctx context.Context, client *dbent.Client, status string, expiresAt time.Time) *dbent.PaymentOrder {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	user, err := client.User.Create().
		SetEmail("payment-lifecycle-" + suffix + "@example.com").
		SetPasswordHash("hash").
		SetUsername("payment-lifecycle-" + suffix).
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("LIFECYCLE-" + suffix).
		SetOutTradeNo("sub2_lifecycle_" + suffix).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(status).
		SetExpiresAt(expiresAt).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)
	return order
}
