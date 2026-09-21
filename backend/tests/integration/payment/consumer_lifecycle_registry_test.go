//go:build unit

package payment_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"

	"entgo.io/ent/dialect"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/stretchr/testify/require"
)

// 回归夹具只在驱动边界返回受控结果，不改变生产实现。
type paymentS12Driver struct {
	dialect.Driver
	calls   atomic.Int32
	entered chan context.Context
	release chan struct{}
}

func (d *paymentS12Driver) Dialect() string { return dialect.SQLite }
func (d *paymentS12Driver) Query(ctx context.Context, query string, args, result any) error {
	n := d.calls.Add(1)
	if n == 1 && d.entered != nil {
		d.entered <- ctx
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.release:
		}
	}
	return errors.New("fixture database unavailable")
}

type paymentS12Leader struct {
	calls   atomic.Int32
	entered chan struct{}
}

func (l *paymentS12Leader) TryAcquireLeaderLock(context.Context, string, string, time.Duration) (bool, error) {
	l.calls.Add(1)
	l.entered <- struct{}{}
	return false, nil
}
func (l *paymentS12Leader) ReleaseLeaderLock(context.Context, string, string) error { return nil }
func TestPaymentS12ExpiryRepeatedStart(t *testing.T) {
	l := &paymentS12Leader{entered: make(chan struct{}, 4)}
	s := payment.NewOrderExpiry(paymenttestkit.Lifecycle(nil, nil, nil, nil, nil, false), time.Hour, payment.ExpiryRuntime{Acquire: func(ctx context.Context) (func(), bool) {
		return account.AcquireSingletonLease(ctx, l, nil, payment.OrderExpiryLeaderKey, "fixture", payment.OrderExpiryLeaderTTL)
	}})
	s.Start(context.Background())
	<-l.entered
	s.Start(context.Background())
	select {
	case <-l.entered:
		t.Errorf("重复 Start 又执行首轮；次数=%d", l.calls.Load())
	case <-time.After(80 * time.Millisecond):
	}
	require.NoError(t, s.StopContext(context.Background()))
}
func TestPaymentS12ExpiryStartAfterStop(t *testing.T) {
	l := &paymentS12Leader{entered: make(chan struct{}, 4)}
	s := payment.NewOrderExpiry(paymenttestkit.Lifecycle(nil, nil, nil, nil, nil, false), time.Hour, payment.ExpiryRuntime{Acquire: func(ctx context.Context) (func(), bool) {
		return account.AcquireSingletonLease(ctx, l, nil, payment.OrderExpiryLeaderKey, "fixture", payment.OrderExpiryLeaderTTL)
	}})
	require.NoError(t, s.StopContext(context.Background()))
	s.Start(context.Background())
	select {
	case <-l.entered:
		t.Error("Stop 后 Start 仍领取首轮任务")
	case <-time.After(80 * time.Millisecond):
	}
	require.NoError(t, s.StopContext(context.Background()))
}
func TestPaymentS12ExpiryStopCancelsQuery(t *testing.T) {
	d := &paymentS12Driver{entered: make(chan context.Context, 1), release: make(chan struct{})}
	client := dbent.NewClient(dbent.Driver(d))
	s := payment.NewOrderExpiry(paymenttestkit.Lifecycle(client, nil, nil, nil, nil, false), time.Hour, payment.ExpiryRuntime{})
	s.Start(context.Background())
	runCtx := <-d.entered
	done := make(chan error, 1)
	go func() { done <- s.StopContext(context.Background()); close(done) }()
	select {
	case err := <-done:
		require.NoError(t, err)
		if runCtx.Err() == nil {
			t.Error("停止未取消运行 context")
		}
	case <-time.After(80 * time.Millisecond):
		t.Error("Stop 未取消支持 context 的查询，仍被在途操作阻塞")
	}
	close(d.release)
	<-done
}
func TestPaymentS12ProvidersFailedInitialLoadRetries(t *testing.T) {
	d := &paymentS12Driver{}
	s := payment.NewProviderBindings(paymentpostgres.NewInstanceStore(dbent.NewClient(dbent.Driver(d))), payment.NewRegistry(), nil, payment.BindingRuntime{}, false)
	s.EnsureProviders(context.Background())
	s.EnsureProviders(context.Background())
	if d.calls.Load() != 2 {
		t.Errorf("首次加载失败被标成已加载，后续不重试：query calls=%d", d.calls.Load())
	}
}
func TestPaymentS12ProvidersFailedRefreshPreservesPublished(t *testing.T) {
	d := &paymentS12Driver{}
	reg := payment.NewRegistry()
	reg.Register(paymenttestkit.StaticProvider{Key: payment.TypeStripe, Types: []string{payment.TypeStripe}})
	s := payment.NewProviderBindings(paymentpostgres.NewInstanceStore(dbent.NewClient(dbent.Driver(d))), reg, nil, payment.BindingRuntime{}, false)
	s.RefreshProviders(context.Background())
	if _, err := reg.GetProvider(payment.TypeStripe); err != nil {
		t.Error("刷新读取失败丢失了已发布的 provider")
	}
}
