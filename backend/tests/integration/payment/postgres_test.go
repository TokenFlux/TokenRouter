//go:build integration

package payment_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
	"github.com/TokenFlux/TokenRouter/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var integrationDB *sql.DB
var integrationEntClient *dbent.Client

// 支付资金契约使用隔离 PostgreSQL 和真实迁移；退出前关闭连接及容器。
func runPostgresTests(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	image := strings.TrimSpace(os.Getenv("TOKENROUTER_TEST_POSTGRES_IMAGE"))
	if image == "" {
		image = "postgres:18.1-alpine3.23"
	}
	container, err := tcpostgres.Run(ctx, image, tcpostgres.WithDatabase("payment_contracts"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if err := container.Terminate(cleanup); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	integrationDB, err = sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	integrationEntClient = dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, integrationDB)))
	defer func() {
		if err := integrationEntClient.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	if err := postgresinfra.ApplyMigrations(ctx, integrationDB, migrations.FS); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

func testEntClient(t *testing.T) *dbent.Client {
	t.Helper()
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "TRUNCATE users RESTART IDENTITY CASCADE")
		require.NoError(t, err)
	})
	return integrationEntClient
}

type refundBalanceParticipant struct {
	payment.RefundRights
	balances *billingpostgres.BalanceStore
}

func (p refundBalanceParticipant) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	return p.balances.DeductRefundBalance(ctx, id, amount)
}

func (p refundBalanceParticipant) CompensateBalance(ctx context.Context, id int64, amount float64) error {
	return p.balances.CompensateRefundBalance(ctx, id, amount)
}

// 订单事务直接绑定 billing 的同连接参与能力，测试不经过旧身份资金写入入口。
func newPostgresRefundWorkflow(client *dbent.Client) *payment.RefundWorkflow {
	instances := paymentpostgres.NewInstanceStore(client)
	bindings := payment.NewProviderBindings(instances, payment.NewRegistry(), payment.NewDefaultLoadBalancer(instances, nil), payment.BindingRuntime{Factory: provider.CreateProvider, RegistryFactory: provider.CreateProvider}, false)
	store := paymentpostgres.NewRefundStore(client, func(tx *dbent.Tx) payment.RefundRights {
		return refundBalanceParticipant{balances: billingpostgres.BalanceInTx(tx)}
	})
	return payment.NewRefundWorkflow(store, payment.RefundRuntime{
		Instance: bindings.GetRefundOrderProviderInstance,
		Provider: bindings.GetRefundProvider,
		User: func(ctx context.Context, id int64) (*payment.RefundUser, error) {
			user, err := client.User.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			return &payment.RefundUser{Balance: user.Balance}, nil
		},
	})
}
