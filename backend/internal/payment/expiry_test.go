package payment

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// 运行探针提供可控在途任务，验证取消、后续阶段与预算，不依赖外部服务。
type expiryProbe struct {
	entered      chan struct{}
	release      chan struct{}
	ignoreCancel bool
	calls        atomic.Int32
}

func (p *expiryProbe) ReconcilePendingPaymentOrders(ctx context.Context) (int, error) {
	p.calls.Add(1)
	close(p.entered)
	if p.ignoreCancel {
		<-p.release
		return 0, nil
	}
	<-ctx.Done()
	return 0, ctx.Err()
}
func (p *expiryProbe) ReconcileProcessingOrders(context.Context) (int, error) {
	p.calls.Add(1)
	return 0, nil
}
func (p *expiryProbe) ReconcilePaidFulfillmentOrders(context.Context) (int, error) {
	p.calls.Add(1)
	return 0, nil
}
func (p *expiryProbe) ExpireTimedOutOrders(context.Context) (int, error) {
	p.calls.Add(1)
	return 0, nil
}
func TestOrderExpiryConstructionAndCancellation(t *testing.T) {
	p := &expiryProbe{entered: make(chan struct{})}
	runner := NewOrderExpiry(p, time.Hour, ExpiryRuntime{})
	if p.calls.Load() != 0 {
		t.Fatal("构造不得启动")
	}
	runner.Start(context.Background())
	<-p.entered
	runner.Start(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.StopContext(ctx); err != nil {
		t.Fatal(err)
	}
	runner.Start(context.Background())
	if err := runner.StopContext(ctx); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 1 {
		t.Fatalf("停止后不得进入后续阶段，calls=%d", p.calls.Load())
	}
}
func TestOrderExpiryStopBudgetReportsUnfinished(t *testing.T) {
	p := &expiryProbe{entered: make(chan struct{}), release: make(chan struct{}), ignoreCancel: true}
	runner := NewOrderExpiry(p, time.Hour, ExpiryRuntime{})
	runner.Start(context.Background())
	<-p.entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := runner.StopContext(ctx)
	close(p.release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("超时必须报告未完成：%v", err)
	}
	if err := runner.StopContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 1 {
		t.Fatal("停止后不进入下一阶段")
	}
}
