//go:build unit

package postgres_test

import (
	"context"
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
	"github.com/stretchr/testify/require"
)

// 退款测试仅替换权益端口；订单认领、恢复记录和事务由真实 Adapter 执行。
type refundBalanceFixture struct {
	payment.RefundRights
	user                  *payment.RefundUser
	updateBalanceFn       func(context.Context, int64, float64) error
	deductBalanceFn       func(context.Context, int64, float64) error
	deductBalanceResultFn func(context.Context, int64, float64) (float64, error)
}

func (f *refundBalanceFixture) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	if f.deductBalanceResultFn != nil {
		return f.deductBalanceResultFn(ctx, id, amount)
	}
	if f.deductBalanceFn != nil {
		return 0, f.deductBalanceFn(ctx, id, amount)
	}
	return amount, nil
}

func (f *refundBalanceFixture) CompensateBalance(ctx context.Context, id int64, amount float64) error {
	if f.updateBalanceFn != nil {
		return f.updateBalanceFn(ctx, id, amount)
	}
	return nil
}

func newRefundWorkflowFixture(client *dbent.Client, balances *refundBalanceFixture, balancer payment.LoadBalancer, factory func(string, string, map[string]string) (payment.Provider, error)) *payment.RefundWorkflow {
	if factory == nil {
		factory = provider.CreateProvider
	}
	bindings := payment.NewProviderBindings(paymentpostgres.NewInstanceStore(client), nil, balancer, payment.BindingRuntime{Factory: factory}, false)
	store := paymentpostgres.NewRefundStore(client, func(*dbent.Tx) payment.RefundRights { return balances })
	return payment.NewRefundWorkflow(store, payment.RefundRuntime{
		Instance: bindings.GetRefundOrderProviderInstance,
		Provider: bindings.GetRefundProvider,
		User: func(context.Context, int64) (*payment.RefundUser, error) {
			if balances.user == nil {
				return &payment.RefundUser{}, nil
			}
			return balances.user, nil
		},
	})
}

// 收尾前显式读取已提交准备事实，不在夹具里复制恢复判定规则。
func preparedReceiptForTest(t *testing.T, ctx context.Context, client *dbent.Client, id int64) *payment.RefundReceipt {
	t.Helper()
	store := paymentpostgres.NewRefundStore(client, nil)
	order, err := store.Order(ctx, id)
	require.NoError(t, err)
	receipt, err := store.RefundRecovery(ctx, order)
	require.NoError(t, err)
	return receipt
}

func pendingDetailForTest(t *testing.T, ctx context.Context, client *dbent.Client, id int64) payment.RefundPendingDetail {
	t.Helper()
	detail, err := paymentpostgres.NewRefundStore(client, nil).PendingDetail(ctx, id)
	require.NoError(t, err)
	return detail
}
