// 一个运行实例持有对账游标；停止由外层 OrderExpiry 取消运行 context。
package payment

import (
	"context"
	"sync"
	"time"
)

type OrderLifecycle struct {
	*Fulfillment
	resume                     *PaymentResumeService
	observe                    func(context.Context) func()
	reconcileCursorMu          sync.Mutex
	processingReconcileCursor  uint64
	fulfillmentReconcileCursor uint64
}

func NewOrderLifecycle(core *Fulfillment, resume *PaymentResumeService, observe func(context.Context) func()) *OrderLifecycle {
	if observe == nil {
		observe = func(context.Context) func() { return func() {} }
	}
	return &OrderLifecycle{Fulfillment: core, resume: resume, observe: observe}
}

// LifeStore 提供订单闭合状态操作及后台处理所需的批量查询。
type LifeStore interface {
	ForceExpire(context.Context, int64, string) error
	TouchPending(context.Context, int64, time.Time) error
	SaveUpstreamTradeNumber(context.Context, int64, string) error
	PendingReconciliation(context.Context, time.Time, int) ([]*Order, error)
	ExpiredPending(context.Context, time.Time) ([]*Order, error)
	ProcessingIDs(context.Context) ([]int64, error)
	ProcessingOrders(context.Context, []int64) ([]*Order, error)
	RecoverableFulfillmentIDs(context.Context, time.Time, time.Duration, time.Duration) ([]int64, error)
}
