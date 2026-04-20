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
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/ent/redeemcode"
	"github.com/TokenFlux/TokenRouter/ent/redeemcodeusage"
	"github.com/TokenFlux/TokenRouter/ent/usersubscription"
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
	ctx        context.Context
	client     *dbent.Client
	paymentSvc *svc.PaymentService
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
		ctx:        context.Background(),
		client:     paymentServiceIntegrationClient,
		paymentSvc: paymentSvc,
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
	group := paymentServiceMustCreateSubscriptionGroup(t, env.ctx, env.client)
	order := paymentServiceMustCreateSubscriptionOrder(t, env.ctx, env.client, invitee, group.ID, 30, 99)

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
			usersubscription.GroupIDEQ(group.ID),
		).
		Only(env.ctx)
	require.NoError(t, err)
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

func paymentServiceMustCreateSubscriptionGroup(t *testing.T, ctx context.Context, client *dbent.Client) *dbent.Group {
	t.Helper()

	suffix := paymentServiceUniqueSuffix()
	group, err := client.Group.Create().
		SetName("sub-group-" + suffix).
		SetDescription("integration test subscription group").
		SetPlatform(svc.PlatformAnthropic).
		SetRateMultiplier(1).
		SetStatus(svc.StatusActive).
		SetSubscriptionType(svc.SubscriptionTypeSubscription).
		SetSortOrder(0).
		Save(ctx)
	require.NoError(t, err)
	return group
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

func paymentServiceMustCreateSubscriptionOrder(t *testing.T, ctx context.Context, client *dbent.Client, user *dbent.User, groupID int64, days int, amount float64) *dbent.PaymentOrder {
	t.Helper()

	now := time.Now()
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(amount).
		SetPayAmount(amount).
		SetFeeRate(0).
		SetRechargeCode("SUB-" + paymentServiceUniqueSuffix()).
		SetOutTradeNo("OUT-" + paymentServiceUniqueSuffix()).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeSubscription).
		SetSubscriptionGroupID(groupID).
		SetSubscriptionDays(days).
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
