//go:build unit

package service

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type paymentReferralRewardUserRepo struct {
	*mockUserRepo
	client *dbent.Client
}

func newPaymentReferralRewardUserRepo(client *dbent.Client) *paymentReferralRewardUserRepo {
	return &paymentReferralRewardUserRepo{
		mockUserRepo: &mockUserRepo{},
		client:       client,
	}
}

func (r *paymentReferralRewardUserRepo) entClient(ctx context.Context) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return r.client
}

func (r *paymentReferralRewardUserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	user, err := r.entClient(ctx).User.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &User{
		ID:          user.ID,
		Email:       user.Email,
		Username:    user.Username,
		Balance:     user.Balance,
		Concurrency: user.Concurrency,
	}, nil
}

func (r *paymentReferralRewardUserRepo) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	update := r.entClient(ctx).User.Update().
		Where(dbuser.IDEQ(id)).
		AddBalance(amount)
	if amount > 0 {
		update = update.AddTotalRecharged(amount)
	}
	_, err := update.Save(ctx)
	return err
}

func (r *paymentReferralRewardUserRepo) AddBalance(ctx context.Context, id int64, amount float64) error {
	if amount == 0 {
		return nil
	}
	_, err := r.entClient(ctx).User.Update().
		Where(dbuser.IDEQ(id)).
		AddBalance(amount).
		Save(ctx)
	return err
}

type paymentReferralRewardRedeemRepo struct {
	nextID                int64
	codesByCode           map[string]*RedeemCode
	usageByRedeemCodeID   map[int64]map[int64]*RedeemCodeUsage
	nextUsageID           int64
	referralRewardCreates int
}

func newPaymentReferralRewardRedeemRepo() *paymentReferralRewardRedeemRepo {
	return &paymentReferralRewardRedeemRepo{
		nextID:              1,
		codesByCode:         make(map[string]*RedeemCode),
		usageByRedeemCodeID: make(map[int64]map[int64]*RedeemCodeUsage),
		nextUsageID:         1,
	}
}

func clonePaymentReferralRewardRedeemCode(code *RedeemCode) *RedeemCode {
	if code == nil {
		return nil
	}
	cloned := *code
	return &cloned
}

func (r *paymentReferralRewardRedeemRepo) Create(_ context.Context, code *RedeemCode) error {
	cloned := *code
	if cloned.ID == 0 {
		cloned.ID = r.nextID
		r.nextID++
	}
	cloned.Status = cloned.PersistedStatus()
	code.ID = cloned.ID
	code.Status = cloned.Status
	if cloned.Type == RedeemTypeReferralReward {
		r.referralRewardCreates++
	}
	r.codesByCode[cloned.Code] = &cloned
	return nil
}

func (r *paymentReferralRewardRedeemRepo) CreateBatch(ctx context.Context, codes []RedeemCode) error {
	for i := range codes {
		if err := r.Create(ctx, &codes[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *paymentReferralRewardRedeemRepo) GetByID(_ context.Context, id int64) (*RedeemCode, error) {
	for _, code := range r.codesByCode {
		if code.ID == id {
			return clonePaymentReferralRewardRedeemCode(code), nil
		}
	}
	return nil, ErrRedeemCodeNotFound
}

func (r *paymentReferralRewardRedeemRepo) GetByCode(_ context.Context, code string) (*RedeemCode, error) {
	redeemCode := r.codesByCode[code]
	if redeemCode == nil {
		return nil, ErrRedeemCodeNotFound
	}
	return clonePaymentReferralRewardRedeemCode(redeemCode), nil
}

func (r *paymentReferralRewardRedeemRepo) GetByCodeForUpdate(ctx context.Context, code string) (*RedeemCode, error) {
	return r.GetByCode(ctx, code)
}

func (r *paymentReferralRewardRedeemRepo) Update(_ context.Context, code *RedeemCode) error {
	cloned := *code
	cloned.Status = cloned.PersistedStatus()
	r.codesByCode[cloned.Code] = &cloned
	return nil
}

func (r *paymentReferralRewardRedeemRepo) Delete(_ context.Context, id int64) error {
	for code, redeemCode := range r.codesByCode {
		if redeemCode.ID == id {
			delete(r.codesByCode, code)
			delete(r.usageByRedeemCodeID, id)
			return nil
		}
	}
	return ErrRedeemCodeNotFound
}

func (r *paymentReferralRewardRedeemRepo) Use(_ context.Context, id, userID int64) error {
	for code, redeemCode := range r.codesByCode {
		if redeemCode.ID != id {
			continue
		}
		usedAt := time.Now()
		cloned := *redeemCode
		cloned.UsedBy = &userID
		cloned.UsedAt = &usedAt
		cloned.UsedCount++
		cloned.Status = cloned.PersistedStatus()
		r.codesByCode[code] = &cloned
		return nil
	}
	return ErrRedeemCodeNotFound
}

func (r *paymentReferralRewardRedeemRepo) CreateUsage(_ context.Context, usage *RedeemCodeUsage) error {
	cloned := *usage
	if cloned.ID == 0 {
		cloned.ID = r.nextUsageID
		r.nextUsageID++
	}
	if r.usageByRedeemCodeID[cloned.RedeemCodeID] == nil {
		r.usageByRedeemCodeID[cloned.RedeemCodeID] = make(map[int64]*RedeemCodeUsage)
	}
	r.usageByRedeemCodeID[cloned.RedeemCodeID][cloned.UserID] = &cloned
	return nil
}

func (r *paymentReferralRewardRedeemRepo) GetUsageByRedeemCodeAndUser(_ context.Context, redeemCodeID, userID int64) (*RedeemCodeUsage, error) {
	usagesByUser := r.usageByRedeemCodeID[redeemCodeID]
	if usagesByUser == nil {
		return nil, nil
	}
	usage := usagesByUser[userID]
	if usage == nil {
		return nil, nil
	}
	cloned := *usage
	return &cloned, nil
}

func (r *paymentReferralRewardRedeemRepo) List(_ context.Context, _ pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *paymentReferralRewardRedeemRepo) ListWithFilters(_ context.Context, _ pagination.PaginationParams, _, _, _ string) ([]RedeemCode, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *paymentReferralRewardRedeemRepo) ListByUser(_ context.Context, userID int64, _ int) ([]RedeemCode, error) {
	out := make([]RedeemCode, 0)
	for _, code := range r.codesByCode {
		if code.UsedBy != nil && *code.UsedBy == userID {
			out = append(out, *clonePaymentReferralRewardRedeemCode(code))
			continue
		}
		if r.usageByRedeemCodeID[code.ID][userID] != nil {
			out = append(out, *clonePaymentReferralRewardRedeemCode(code))
		}
	}
	return out, nil
}

func (r *paymentReferralRewardRedeemRepo) ListByUserPaginated(ctx context.Context, userID int64, _ pagination.PaginationParams, codeType string) ([]RedeemCode, *pagination.PaginationResult, error) {
	codes, err := r.ListByUser(ctx, userID, 0)
	if err != nil {
		return nil, nil, err
	}
	if codeType != "" {
		filtered := codes[:0]
		for _, code := range codes {
			if code.Type == codeType {
				filtered = append(filtered, code)
			}
		}
		codes = filtered
	}
	return codes, &pagination.PaginationResult{Total: int64(len(codes))}, nil
}

func (r *paymentReferralRewardRedeemRepo) SumPositiveBalanceByUser(_ context.Context, userID int64) (float64, error) {
	total := 0.0
	for _, code := range r.codesByCode {
		if code.Type != RedeemTypeBalance || code.Value <= 0 {
			continue
		}
		if code.UsedBy != nil && *code.UsedBy == userID {
			total += code.Value
			continue
		}
		if r.usageByRedeemCodeID[code.ID][userID] != nil {
			total += code.Value
		}
	}
	return total, nil
}

func (r *paymentReferralRewardRedeemRepo) countByUserAndType(userID int64, codeType string) int {
	count := 0
	for _, code := range r.codesByCode {
		if code.Type != codeType {
			continue
		}
		if code.UsedBy != nil && *code.UsedBy == userID {
			count++
			continue
		}
		if r.usageByRedeemCodeID[code.ID][userID] != nil {
			count++
		}
	}
	return count
}

func TestPaymentFulfillmentGrantsReferralRewardForFirstBalanceOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	svc, redeemRepo, _ := newPaymentReferralRewardTestService(client)
	inviter, invitee := createPaymentReferralRewardUsers(t, ctx, client, "balance", 10, 2, 6.5)
	order := createPaymentReferralRewardOrder(t, ctx, client, invitee, payment.OrderTypeBalance, OrderStatusPaid)

	err := svc.ExecuteBalanceFulfillment(ctx, order.ID)
	require.NoError(t, err)

	reloadedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloadedOrder.Status)
	require.NotNil(t, reloadedOrder.CompletedAt)

	reloadedInvitee, err := client.User.Get(ctx, invitee.ID)
	require.NoError(t, err)
	require.Equal(t, 2+order.Amount+6.5, reloadedInvitee.Balance)
	require.NotNil(t, reloadedInvitee.ReferralRewardGrantedAt)

	reloadedInviter, err := client.User.Get(ctx, inviter.ID)
	require.NoError(t, err)
	require.Equal(t, 10+6.5, reloadedInviter.Balance)
	require.Equal(t, 1, redeemRepo.countByUserAndType(invitee.ID, RedeemTypeReferralReward))
	require.Equal(t, 1, redeemRepo.countByUserAndType(inviter.ID, RedeemTypeReferralReward))
	require.Equal(t, 1, countPaymentReferralRewardAuditLogs(t, ctx, client, order.ID))

	err = svc.ExecuteBalanceFulfillment(ctx, order.ID)
	require.NoError(t, err)

	afterRetryInvitee, err := client.User.Get(ctx, invitee.ID)
	require.NoError(t, err)
	afterRetryInviter, err := client.User.Get(ctx, inviter.ID)
	require.NoError(t, err)
	require.Equal(t, reloadedInvitee.Balance, afterRetryInvitee.Balance)
	require.Equal(t, reloadedInviter.Balance, afterRetryInviter.Balance)
	require.Equal(t, 1, redeemRepo.countByUserAndType(invitee.ID, RedeemTypeReferralReward))
	require.Equal(t, 1, redeemRepo.countByUserAndType(inviter.ID, RedeemTypeReferralReward))
}

func TestPaymentFulfillmentGrantsReferralRewardForSubscriptionOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	svc, redeemRepo, subRepo := newPaymentReferralRewardTestService(client)
	inviter, invitee := createPaymentReferralRewardUsers(t, ctx, client, "subscription", 12, 3, 7.25)
	order := createPaymentReferralRewardOrder(t, ctx, client, invitee, payment.OrderTypeSubscription, OrderStatusPaid)

	err := svc.ExecuteSubscriptionFulfillment(ctx, order.ID)
	require.NoError(t, err)

	reloadedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloadedOrder.Status)
	require.Equal(t, 1, subRepo.createCalls)

	reloadedInvitee, err := client.User.Get(ctx, invitee.ID)
	require.NoError(t, err)
	require.Equal(t, 3+7.25, reloadedInvitee.Balance)
	require.NotNil(t, reloadedInvitee.ReferralRewardGrantedAt)

	reloadedInviter, err := client.User.Get(ctx, inviter.ID)
	require.NoError(t, err)
	require.Equal(t, 12+7.25, reloadedInviter.Balance)
	require.Equal(t, 1, redeemRepo.countByUserAndType(invitee.ID, RedeemTypeReferralReward))
	require.Equal(t, 1, redeemRepo.countByUserAndType(inviter.ID, RedeemTypeReferralReward))
	require.Equal(t, 1, countPaymentReferralRewardAuditLogs(t, ctx, client, order.ID))
}

func TestPaymentReferralRewardSkipsIneligibleInvitees(t *testing.T) {
	for _, tc := range []struct {
		name           string
		referred       bool
		rewardAmount   float64
		alreadyGranted bool
	}{
		{name: "no referral", rewardAmount: 6},
		{name: "zero reward", referred: true},
		{name: "already granted", referred: true, rewardAmount: 6, alreadyGranted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentOrderLifecycleTestClient(t)
			svc, redeemRepo, _ := newPaymentReferralRewardTestService(client)

			inviter, err := client.User.Create().
				SetEmail(fmt.Sprintf("skip-inviter-%s@example.com", strconv.FormatInt(time.Now().UnixNano(), 10))).
				SetPasswordHash("hash").
				SetUsername("skip-inviter").
				SetBalance(8).
				Save(ctx)
			require.NoError(t, err)

			createInvitee := client.User.Create().
				SetEmail(fmt.Sprintf("skip-invitee-%s@example.com", strconv.FormatInt(time.Now().UnixNano(), 10))).
				SetPasswordHash("hash").
				SetUsername("skip-invitee").
				SetBalance(4).
				SetReferralRewardAmount(tc.rewardAmount)
			if tc.referred {
				createInvitee.SetReferredByUserID(inviter.ID)
			}
			if tc.alreadyGranted {
				createInvitee.SetReferralRewardGrantedAt(time.Now().Add(-time.Hour))
			}
			invitee, err := createInvitee.Save(ctx)
			require.NoError(t, err)

			order := createPaymentReferralRewardOrder(t, ctx, client, invitee, payment.OrderTypeBalance, OrderStatusRecharging)
			err = svc.markCompleted(ctx, order, "RECHARGE_SUCCESS")
			require.NoError(t, err)

			reloadedInvitee, err := client.User.Get(ctx, invitee.ID)
			require.NoError(t, err)
			reloadedInviter, err := client.User.Get(ctx, inviter.ID)
			require.NoError(t, err)
			require.Equal(t, 4.0, reloadedInvitee.Balance)
			require.Equal(t, 8.0, reloadedInviter.Balance)
			require.Equal(t, 0, redeemRepo.countByUserAndType(invitee.ID, RedeemTypeReferralReward))
			require.Equal(t, 0, redeemRepo.countByUserAndType(inviter.ID, RedeemTypeReferralReward))
			require.Equal(t, 0, countPaymentReferralRewardAuditLogs(t, ctx, client, order.ID))
		})
	}
}

func newPaymentReferralRewardTestService(client *dbent.Client) (*PaymentService, *paymentReferralRewardRedeemRepo, *subscriptionUserSubRepoStub) {
	userRepo := newPaymentReferralRewardUserRepo(client)
	redeemRepo := newPaymentReferralRewardRedeemRepo()
	redeemService := NewRedeemService(
		redeemRepo,
		userRepo,
		nil,
		nil,
		nil,
		client,
		nil,
	)
	subRepo := newSubscriptionUserSubRepoStub()
	subscriptionSvc := NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil)
	return &PaymentService{
		entClient:       client,
		redeemService:   redeemService,
		subscriptionSvc: subscriptionSvc,
		userRepo:        userRepo,
	}, redeemRepo, subRepo
}

func createPaymentReferralRewardUsers(t *testing.T, ctx context.Context, client *dbent.Client, name string, inviterBalance, inviteeBalance, rewardAmount float64) (*dbent.User, *dbent.User) {
	t.Helper()

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	inviter, err := client.User.Create().
		SetEmail(fmt.Sprintf("rr-%s-inviter-%s@example.com", name, suffix)).
		SetPasswordHash("hash").
		SetUsername("rr-" + name + "-inviter").
		SetBalance(inviterBalance).
		Save(ctx)
	require.NoError(t, err)

	invitee, err := client.User.Create().
		SetEmail(fmt.Sprintf("rr-%s-invitee-%s@example.com", name, suffix)).
		SetPasswordHash("hash").
		SetUsername("rr-" + name + "-invitee").
		SetBalance(inviteeBalance).
		SetReferredByUserID(inviter.ID).
		SetReferralRewardAmount(rewardAmount).
		Save(ctx)
	require.NoError(t, err)
	return inviter, invitee
}

func createPaymentReferralRewardOrder(t *testing.T, ctx context.Context, client *dbent.Client, user *dbent.User, orderType string, status string) *dbent.PaymentOrder {
	t.Helper()

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	create := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(20).
		SetPayAmount(20).
		SetFeeRate(0).
		SetRechargeCode("RR" + suffix[len(suffix)-16:]).
		SetOutTradeNo("sub2_rr_" + suffix).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-" + suffix).
		SetOrderType(orderType).
		SetStatus(status).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com")
	if orderType == payment.OrderTypeSubscription {
		planID := int64(100)
		create.SetPlanID(planID)
		create.SetPlanSnapshot(domain.SubscriptionPlanSnapshot{ValidityDays: 30})
	}
	order, err := create.Save(ctx)
	require.NoError(t, err)
	return order
}

func countPaymentReferralRewardAuditLogs(t *testing.T, ctx context.Context, client *dbent.Client, orderID int64) int {
	t.Helper()

	count, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
			paymentauditlog.ActionEQ("REFERRAL_REWARD_GRANTED"),
		).
		Count(ctx)
	require.NoError(t, err)
	return count
}
