// 查询只访问命名读取端口；统计和权限规则不依赖存储查询构造器。
package payment

import (
	"context"
	"time"
)

type OrderQueryStore interface {
	OrderAuditLogs(context.Context, int64) ([]*AuditLog, error)
	Order(context.Context, int64) (*Order, error)
	GetUserOrders(context.Context, int64, OrderListParams) ([]*Order, int, error)
	AdminListOrders(context.Context, int64, OrderListParams) ([]*Order, int, error)
	PaidOrders(context.Context, time.Time, time.Time, []string) ([]*Order, error)
	PendingCount(context.Context) (int, error)
	PlanNames(context.Context, []int64) (map[int64]string, error)
}
type OrderQueries struct {
	provider func(context.Context, *Order) (Provider, error)
	store    OrderQueryStore
	now      func() time.Time
}

func NewOrderQueries(store OrderQueryStore, now func() time.Time, provider func(context.Context, *Order) (Provider, error)) *OrderQueries {
	if now == nil {
		now = time.Now
	}
	return &OrderQueries{store: store, now: now, provider: provider}
}
func QueryPagination(pageSize, page int) (size, pg int) {
	size = pageSize
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	pg = page
	if pg < 1 {
		pg = 1
	}
	return
}
