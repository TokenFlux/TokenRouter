//go:build integration

package service_test

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/ent/redeemcode"
	"github.com/TokenFlux/TokenRouter/ent/redeemcodeusage"
	"github.com/TokenFlux/TokenRouter/ent/usersubscription"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/repository"
	svc "github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	_ "github.com/lib/pq"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const paymentServicePostgresImageTag = "postgres:18.1-alpine3.23"

var (
	paymentServiceIntegrationDB     *sql.DB
	paymentServiceIntegrationClient *dbent.Client
	paymentServiceTestSeq           uint64
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	if err := timezone.Init("UTC"); err != nil {
		log.Printf("failed to init timezone: %v", err)
		os.Exit(1)
	}

	if !paymentServiceDockerIsAvailable(ctx) {
		if os.Getenv("CI") != "" {
			log.Printf("docker is not available (CI=true); failing integration tests")
			os.Exit(1)
		}
		log.Printf("docker is not available; skipping integration tests (start Docker to enable)")
		os.Exit(0)
	}

	pgContainer, err := tcpostgres.Run(
		ctx,
		paymentServicePostgresImageTag,
		tcpostgres.WithDatabase("sub2api_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Printf("failed to start postgres container: %v", err)
		os.Exit(1)
	}
	defer func() { _ = pgContainer.Terminate(ctx) }()

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	if err != nil {
		log.Printf("failed to get postgres dsn: %v", err)
		os.Exit(1)
	}

	paymentServiceIntegrationDB, err = paymentServiceOpenSQLWithRetry(ctx, dsn, 30*time.Second)
	if err != nil {
		log.Printf("failed to open sql db: %v", err)
		os.Exit(1)
	}

	if err := repository.ApplyMigrations(ctx, paymentServiceIntegrationDB); err != nil {
		log.Printf("failed to apply db migrations: %v", err)
		os.Exit(1)
	}

	drv := entsql.OpenDB(dialect.Postgres, paymentServiceIntegrationDB)
	paymentServiceIntegrationClient = dbent.NewClient(dbent.Driver(drv))

	code := m.Run()

	_ = paymentServiceIntegrationClient.Close()
	_ = paymentServiceIntegrationDB.Close()

	os.Exit(code)
}

func paymentServiceDockerIsAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	cmd.Env = os.Environ()
	return cmd.Run() == nil
}

func paymentServiceOpenSQLWithRetry(ctx context.Context, dsn string, timeout time.Duration) (*sql.DB, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			lastErr = err
			time.Sleep(250 * time.Millisecond)
			continue
		}

		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = db.PingContext(pingCtx)
		cancel()
		if err != nil {
			lastErr = err
			_ = db.Close()
			time.Sleep(250 * time.Millisecond)
			continue
		}

		return db, nil
	}

	return nil, fmt.Errorf("db not ready after %s: %w", timeout, lastErr)
}

type paymentServiceIntegrationEnv struct {
	ctx             context.Context
	client          *dbent.Client
	paymentSvc      *svc.PaymentService
	subscriptionSvc *svc.SubscriptionService
}

func newPaymentServiceIntegrationEnv(t *testing.T) *paymentServiceIntegrationEnv {
	t.Helper()

	require.NotNil(t, paymentServiceIntegrationDB)
	require.NotNil(t, paymentServiceIntegrationClient)

	userRepo := repository.NewUserRepository(paymentServiceIntegrationClient, paymentServiceIntegrationDB)
	groupRepo := repository.NewGroupRepository(paymentServiceIntegrationClient, paymentServiceIntegrationDB)
	userSubRepo := repository.NewUserSubscriptionRepository(paymentServiceIntegrationClient)
	subscriptionSvc := svc.NewSubscriptionService(groupRepo, userSubRepo, nil, paymentServiceIntegrationClient, nil)
	redeemRepo := repository.NewRedeemCodeRepository(paymentServiceIntegrationClient)
	redeemSvc := svc.NewRedeemService(redeemRepo, userRepo, subscriptionSvc, nil, nil, paymentServiceIntegrationClient, nil)
	paymentSvc := svc.NewPaymentService(paymentServiceIntegrationClient, nil, nil, redeemSvc, subscriptionSvc, nil, userRepo, groupRepo)

	return &paymentServiceIntegrationEnv{
		ctx:             context.Background(),
		client:          paymentServiceIntegrationClient,
		paymentSvc:      paymentSvc,
		subscriptionSvc: subscriptionSvc,
	}
}

func TestPaymentService_ExecuteBalanceFulfillment_GrantsReferralRewardOnFirstPaidOrder(t *testing.T) {
	env := newPaymentServiceIntegrationEnv(t)

	inviter := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{})
	invitee := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{
		referredByUserID:     &inviter.ID,
		referralRewardAmount: 8.5,
	})
	order := paymentServiceMustCreateBalanceOrder(t, env.ctx, env.client, invitee, 20)

	err := env.paymentSvc.ExecuteBalanceFulfillment(env.ctx, order.ID)
	require.NoError(t, err)

	updatedOrder, err := env.client.PaymentOrder.Get(env.ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, updatedOrder.Status)
	require.NotNil(t, updatedOrder.CompletedAt)

	updatedInvitee, err := env.client.User.Get(env.ctx, invitee.ID)
	require.NoError(t, err)
	updatedInviter, err := env.client.User.Get(env.ctx, inviter.ID)
	require.NoError(t, err)

	require.InDelta(t, 28.5, updatedInvitee.Balance, 1e-9)
	require.InDelta(t, 8.5, updatedInviter.Balance, 1e-9)
	require.NotNil(t, updatedInvitee.ReferralRewardGrantedAt)

	require.Equal(t, 1, paymentServiceCountReferralRewardCodesForUser(t, env.ctx, env.client, invitee.ID))
	require.Equal(t, 1, paymentServiceCountReferralRewardCodesForUser(t, env.ctx, env.client, inviter.ID))
	require.Equal(t, 1, paymentServiceCountReferralRewardUsagesForUser(t, env.ctx, env.client, invitee.ID))
	require.Equal(t, 1, paymentServiceCountReferralRewardUsagesForUser(t, env.ctx, env.client, inviter.ID))
	require.Equal(t, 1, paymentServiceCountAuditLogs(t, env.ctx, env.client, order.ID, "REFERRAL_REWARD_GRANTED"))
}

func TestPaymentService_ExecuteSubscriptionFulfillment_GrantsReferralRewardOnFirstPaidOrder(t *testing.T) {
	env := newPaymentServiceIntegrationEnv(t)

	inviter := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{})
	invitee := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{
		referredByUserID:     &inviter.ID,
		referralRewardAmount: 8.5,
	})
	plan := paymentServiceMustCreateSubscriptionPlan(t, env.ctx, env.client, 99, 30)
	order := paymentServiceMustCreateSubscriptionOrder(t, env.ctx, env.client, invitee, plan)

	err := env.paymentSvc.ExecuteSubscriptionFulfillment(env.ctx, order.ID)
	require.NoError(t, err)

	updatedOrder, err := env.client.PaymentOrder.Get(env.ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, updatedOrder.Status)
	require.NotNil(t, updatedOrder.CompletedAt)

	updatedInvitee, err := env.client.User.Get(env.ctx, invitee.ID)
	require.NoError(t, err)
	updatedInviter, err := env.client.User.Get(env.ctx, inviter.ID)
	require.NoError(t, err)

	require.InDelta(t, 8.5, updatedInvitee.Balance, 1e-9)
	require.InDelta(t, 8.5, updatedInviter.Balance, 1e-9)
	require.NotNil(t, updatedInvitee.ReferralRewardGrantedAt)

	sub, err := env.client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(invitee.ID),
			usersubscription.SourceOrderIDEQ(order.ID),
		).
		Only(env.ctx)
	require.NoError(t, err)
	require.Equal(t, plan.ID, sub.PlanID)
	require.Equal(t, svc.SubscriptionStatusActive, sub.Status)
	require.True(t, sub.ExpiresAt.After(sub.StartsAt))

	require.Equal(t, 1, paymentServiceCountReferralRewardCodesForUser(t, env.ctx, env.client, invitee.ID))
	require.Equal(t, 1, paymentServiceCountReferralRewardCodesForUser(t, env.ctx, env.client, inviter.ID))
	require.Equal(t, 1, paymentServiceCountReferralRewardUsagesForUser(t, env.ctx, env.client, invitee.ID))
	require.Equal(t, 1, paymentServiceCountReferralRewardUsagesForUser(t, env.ctx, env.client, inviter.ID))
	require.Equal(t, 1, paymentServiceCountAuditLogs(t, env.ctx, env.client, order.ID, "REFERRAL_REWARD_GRANTED"))
}

func TestPaymentService_RetryFulfillment_DoesNotDuplicateReferralReward(t *testing.T) {
	env := newPaymentServiceIntegrationEnv(t)

	inviter := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{})
	invitee := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{
		referredByUserID:     &inviter.ID,
		referralRewardAmount: 8.5,
	})
	order := paymentServiceMustCreateBalanceOrder(t, env.ctx, env.client, invitee, 20)

	err := env.paymentSvc.ExecuteBalanceFulfillment(env.ctx, order.ID)
	require.NoError(t, err)

	firstInvitee, err := env.client.User.Get(env.ctx, invitee.ID)
	require.NoError(t, err)
	firstInviter, err := env.client.User.Get(env.ctx, inviter.ID)
	require.NoError(t, err)
	require.NotNil(t, firstInvitee.ReferralRewardGrantedAt)
	firstGrantedAt := *firstInvitee.ReferralRewardGrantedAt

	_, err = env.client.PaymentOrder.UpdateOneID(order.ID).
		SetStatus(payment.OrderStatusFailed).
		SetFailedAt(time.Now()).
		SetFailedReason("force retry path").
		Save(env.ctx)
	require.NoError(t, err)

	err = env.paymentSvc.RetryFulfillment(env.ctx, order.ID)
	require.NoError(t, err)

	updatedOrder, err := env.client.PaymentOrder.Get(env.ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, updatedOrder.Status)

	updatedInvitee, err := env.client.User.Get(env.ctx, invitee.ID)
	require.NoError(t, err)
	updatedInviter, err := env.client.User.Get(env.ctx, inviter.ID)
	require.NoError(t, err)

	require.InDelta(t, firstInvitee.Balance, updatedInvitee.Balance, 1e-9)
	require.InDelta(t, firstInviter.Balance, updatedInviter.Balance, 1e-9)
	require.NotNil(t, updatedInvitee.ReferralRewardGrantedAt)
	require.Equal(t, firstGrantedAt, *updatedInvitee.ReferralRewardGrantedAt)

	require.Equal(t, 1, paymentServiceCountReferralRewardCodesForUser(t, env.ctx, env.client, invitee.ID))
	require.Equal(t, 1, paymentServiceCountReferralRewardCodesForUser(t, env.ctx, env.client, inviter.ID))
	require.Equal(t, 1, paymentServiceCountReferralRewardUsagesForUser(t, env.ctx, env.client, invitee.ID))
	require.Equal(t, 1, paymentServiceCountReferralRewardUsagesForUser(t, env.ctx, env.client, inviter.ID))
	require.Equal(t, 1, paymentServiceCountAuditLogs(t, env.ctx, env.client, order.ID, "REFERRAL_REWARD_GRANTED"))
}

func TestSubscriptionService_AssignOrExtendSubscription_SerializesSamePlanGrants(t *testing.T) {
	env := newPaymentServiceIntegrationEnv(t)

	user := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{})
	plan := paymentServiceMustCreateSubscriptionPlan(t, env.ctx, env.client, 49, 30)

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := env.subscriptionSvc.AssignOrExtendSubscription(env.ctx, &svc.AssignSubscriptionInput{
				UserID: user.ID,
				PlanID: plan.ID,
				Notes:  "concurrent grant",
			})
			errCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	subs, err := env.client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(user.ID),
			usersubscription.PlanIDEQ(plan.ID),
		).
		Order(
			dbent.Asc(usersubscription.FieldStartsAt),
			dbent.Asc(usersubscription.FieldCreatedAt),
		).
		All(env.ctx)
	require.NoError(t, err)
	require.Len(t, subs, 2)
	require.Equal(t, svc.SubscriptionStatusActive, subs[0].Status)
	require.Equal(t, svc.SubscriptionStatusPending, subs[1].Status)
	require.Equal(t, subs[0].ExpiresAt, subs[1].StartsAt)
}

func TestPaymentService_RetryFulfillment_DoesNotDuplicateSubscriptionGrant(t *testing.T) {
	env := newPaymentServiceIntegrationEnv(t)

	user := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{})
	plan := paymentServiceMustCreateSubscriptionPlan(t, env.ctx, env.client, 88, 30)
	order := paymentServiceMustCreateSubscriptionOrder(t, env.ctx, env.client, user, plan)

	err := env.paymentSvc.ExecuteSubscriptionFulfillment(env.ctx, order.ID)
	require.NoError(t, err)

	_, err = env.client.PaymentOrder.UpdateOneID(order.ID).
		SetStatus(payment.OrderStatusFailed).
		SetFailedAt(time.Now()).
		SetFailedReason("force retry path").
		Save(env.ctx)
	require.NoError(t, err)

	err = env.paymentSvc.RetryFulfillment(env.ctx, order.ID)
	require.NoError(t, err)

	count, err := env.client.UserSubscription.Query().
		Where(usersubscription.SourceOrderIDEQ(order.ID)).
		Count(env.ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, 1, paymentServiceCountAuditLogs(t, env.ctx, env.client, order.ID, "SUBSCRIPTION_SUCCESS"))
}

func TestPaymentService_ExecuteSubscriptionFulfillment_UsesPlanSnapshot(t *testing.T) {
	env := newPaymentServiceIntegrationEnv(t)

	user := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{})
	plan := paymentServiceMustCreateSubscriptionPlan(t, env.ctx, env.client, 66, 30)
	snapshotDays := 10
	snapshotLimit := 3.5
	order := paymentServiceMustCreateSubscriptionOrder(t, env.ctx, env.client, user, plan)
	_, err := env.client.PaymentOrder.UpdateOneID(order.ID).
		SetPlanSnapshot(domain.SubscriptionPlanSnapshot{
			Name:            plan.Name,
			Price:           plan.Price,
			ValidityDays:    snapshotDays,
			DailyLimitUSD:   &snapshotLimit,
			WeeklyLimitUSD:  nil,
			MonthlyLimitUSD: nil,
		}).
		Save(env.ctx)
	require.NoError(t, err)

	err = env.paymentSvc.ExecuteSubscriptionFulfillment(env.ctx, order.ID)
	require.NoError(t, err)

	sub, err := env.client.UserSubscription.Query().
		Where(usersubscription.SourceOrderIDEQ(order.ID)).
		Only(env.ctx)
	require.NoError(t, err)
	require.InDelta(t, snapshotLimit, *sub.DailyLimitUsd, 1e-9)
	require.Nil(t, sub.WeeklyLimitUsd)
	require.Nil(t, sub.MonthlyLimitUsd)
	require.Equal(t, snapshotDays, int(sub.ExpiresAt.Sub(sub.StartsAt).Hours()/24))
}

func TestSubscriptionService_ExtendAndRevoke_ShiftsLaterChain(t *testing.T) {
	env := newPaymentServiceIntegrationEnv(t)

	user := paymentServiceMustCreateUser(t, env.ctx, env.client, paymentServiceUserFixture{})
	plan := paymentServiceMustCreateSubscriptionPlan(t, env.ctx, env.client, 77, 30)
	now := time.Now().UTC().Truncate(time.Second)

	first, err := env.client.UserSubscription.Create().
		SetUserID(user.ID).
		SetPlanID(plan.ID).
		SetStartsAt(now).
		SetExpiresAt(now.AddDate(0, 0, 7)).
		SetStatus(svc.SubscriptionStatusActive).
		SetAssignedAt(now).
		SetNotes("first").
		Save(env.ctx)
	require.NoError(t, err)
	second, err := env.client.UserSubscription.Create().
		SetUserID(user.ID).
		SetPlanID(plan.ID).
		SetStartsAt(first.ExpiresAt).
		SetExpiresAt(first.ExpiresAt.AddDate(0, 0, 7)).
		SetStatus(svc.SubscriptionStatusPending).
		SetAssignedAt(now).
		SetNotes("second").
		Save(env.ctx)
	require.NoError(t, err)
	third, err := env.client.UserSubscription.Create().
		SetUserID(user.ID).
		SetPlanID(plan.ID).
		SetStartsAt(second.ExpiresAt).
		SetExpiresAt(second.ExpiresAt.AddDate(0, 0, 7)).
		SetStatus(svc.SubscriptionStatusPending).
		SetAssignedAt(now).
		SetNotes("third").
		Save(env.ctx)
	require.NoError(t, err)

	extended, err := env.subscriptionSvc.ExtendSubscription(env.ctx, first.ID, 3)
	require.NoError(t, err)
	require.Equal(t, first.ExpiresAt.AddDate(0, 0, 3), extended.ExpiresAt)

	secondAfterExtend, err := env.client.UserSubscription.Get(env.ctx, second.ID)
	require.NoError(t, err)
	thirdAfterExtend, err := env.client.UserSubscription.Get(env.ctx, third.ID)
	require.NoError(t, err)
	require.Equal(t, second.StartsAt.AddDate(0, 0, 3), secondAfterExtend.StartsAt)
	require.Equal(t, third.StartsAt.AddDate(0, 0, 3), thirdAfterExtend.StartsAt)

	err = env.subscriptionSvc.RevokeSubscription(env.ctx, second.ID)
	require.NoError(t, err)

	_, err = env.client.UserSubscription.Get(env.ctx, second.ID)
	require.Error(t, err)

	thirdAfterRevoke, err := env.client.UserSubscription.Get(env.ctx, third.ID)
	require.NoError(t, err)
	require.Equal(t, thirdAfterExtend.StartsAt.AddDate(0, 0, -7), thirdAfterRevoke.StartsAt)
	require.Equal(t, thirdAfterExtend.ExpiresAt.AddDate(0, 0, -7), thirdAfterRevoke.ExpiresAt)
}

type paymentServiceUserFixture struct {
	referredByUserID     *int64
	referralRewardAmount float64
}

func paymentServiceMustCreateUser(t *testing.T, ctx context.Context, client *dbent.Client, fixture paymentServiceUserFixture) *dbent.User {
	t.Helper()

	suffix := paymentServiceUniqueSuffix()
	create := client.User.Create().
		SetEmail("user-" + suffix + "@example.com").
		SetPasswordHash("test-password-hash").
		SetRole(svc.RoleUser).
		SetUsername("user-" + suffix).
		SetStatus(svc.StatusActive)
	if fixture.referredByUserID != nil {
		create.SetReferredByUserID(*fixture.referredByUserID)
	}
	if fixture.referralRewardAmount > 0 {
		create.SetReferralRewardAmount(fixture.referralRewardAmount)
	}

	user, err := create.Save(ctx)
	require.NoError(t, err)
	return user
}

func paymentServiceMustCreateSubscriptionPlan(t *testing.T, ctx context.Context, client *dbent.Client, price float64, validityDays int) *dbent.SubscriptionPlan {
	t.Helper()

	suffix := paymentServiceUniqueSuffix()
	dailyLimit := 25.0
	plan, err := client.SubscriptionPlan.Create().
		SetName("sub-plan-" + suffix).
		SetDescription("integration test subscription plan").
		SetPrice(price).
		SetValidityDays(validityDays).
		SetDailyLimitUsd(dailyLimit).
		SetForSale(true).
		SetSortOrder(0).
		Save(ctx)
	require.NoError(t, err)
	return plan
}

func paymentServiceMustCreateBalanceOrder(t *testing.T, ctx context.Context, client *dbent.Client, user *dbent.User, amount float64) *dbent.PaymentOrder {
	t.Helper()

	now := time.Now()
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(amount).
		SetPayAmount(amount).
		SetFeeRate(0).
		SetRechargeCode("BAL-" + paymentServiceUniqueSuffix()).
		SetOutTradeNo("OUT-" + paymentServiceUniqueSuffix()).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPaid).
		SetExpiresAt(now.Add(time.Hour)).
		SetPaidAt(now).
		SetClientIP("127.0.0.1").
		SetSrcHost("integration.test").
		Save(ctx)
	require.NoError(t, err)
	return order
}

func paymentServiceMustCreateSubscriptionOrder(t *testing.T, ctx context.Context, client *dbent.Client, user *dbent.User, plan *dbent.SubscriptionPlan) *dbent.PaymentOrder {
	t.Helper()

	now := time.Now()
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(plan.Price).
		SetPayAmount(plan.Price).
		SetFeeRate(0).
		SetRechargeCode("SUB-" + paymentServiceUniqueSuffix()).
		SetOutTradeNo("OUT-" + paymentServiceUniqueSuffix()).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(plan.ID).
		SetPlanSnapshot(domain.SubscriptionPlanSnapshot{
			Name:            plan.Name,
			Price:           plan.Price,
			ValidityDays:    plan.ValidityDays,
			DailyLimitUSD:   plan.DailyLimitUsd,
			WeeklyLimitUSD:  plan.WeeklyLimitUsd,
			MonthlyLimitUSD: plan.MonthlyLimitUsd,
		}).
		SetStatus(payment.OrderStatusPaid).
		SetExpiresAt(now.Add(time.Hour)).
		SetPaidAt(now).
		SetClientIP("127.0.0.1").
		SetSrcHost("integration.test").
		Save(ctx)
	require.NoError(t, err)
	return order
}

func paymentServiceCountReferralRewardCodesForUser(t *testing.T, ctx context.Context, client *dbent.Client, userID int64) int {
	t.Helper()

	count, err := client.RedeemCode.Query().
		Where(
			redeemcode.TypeEQ(svc.RedeemTypeReferralReward),
			redeemcode.UsedByEQ(userID),
		).
		Count(ctx)
	require.NoError(t, err)
	return count
}

func paymentServiceCountReferralRewardUsagesForUser(t *testing.T, ctx context.Context, client *dbent.Client, userID int64) int {
	t.Helper()

	count, err := client.RedeemCodeUsage.Query().
		Where(
			redeemcodeusage.UserIDEQ(userID),
			redeemcodeusage.HasRedeemCodeWith(redeemcode.TypeEQ(svc.RedeemTypeReferralReward)),
		).
		Count(ctx)
	require.NoError(t, err)
	return count
}

func paymentServiceCountAuditLogs(t *testing.T, ctx context.Context, client *dbent.Client, orderID int64, action string) int {
	t.Helper()

	count, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
			paymentauditlog.ActionEQ(action),
		).
		Count(ctx)
	require.NoError(t, err)
	return count
}

func paymentServiceUniqueSuffix() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddUint64(&paymentServiceTestSeq, 1))
}
