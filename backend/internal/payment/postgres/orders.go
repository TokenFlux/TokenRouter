// 订单读取保留现有 SQL 数量、时间区间和套餐名称批量查询。
package postgres

import (
	"context"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

type OrderRebates interface {
	AccrueInviteRebateForOrder(context.Context, int64, float64, *int64) (float64, error)
}
type OrderStoreRuntime struct {
	Rebates func(*dbent.Tx) OrderRebates
	Audit   func(context.Context, int64, string, string, map[string]any)
}
type OrderStore struct {
	client  *dbent.Client
	runtime OrderStoreRuntime
}

func NewOrderStore(client *dbent.Client, options ...OrderStoreRuntime) *OrderStore {
	var runtime OrderStoreRuntime
	if len(options) > 0 {
		runtime = options[0]
	}
	if runtime.Audit == nil {
		runtime.Audit = func(context.Context, int64, string, string, map[string]any) {}
	}
	return &OrderStore{client: client, runtime: runtime}
}
func (s *OrderStore) Order(ctx context.Context, id int64) (*payment.Order, error) {
	o, e := s.client.PaymentOrder.Get(ctx, id)
	return OrderFromEntity(o), e
}
func orderValues(rows []*dbent.PaymentOrder) []*payment.Order {
	if rows == nil {
		return nil
	}
	out := make([]*payment.Order, len(rows))
	for i, v := range rows {
		out[i] = OrderFromEntity(v)
	}
	return out
}
func (s *OrderStore) PaidOrders(ctx context.Context, start, end time.Time, statuses []string) ([]*payment.Order, error) {
	rows, e := s.client.PaymentOrder.Query().Where(paymentorder.StatusIn(statuses...), paymentorder.PaidAtGTE(start), paymentorder.PaidAtLT(end)).All(ctx)
	return orderValues(rows), e
}
func (s *OrderStore) PendingCount(ctx context.Context) (int, error) {
	return s.client.PaymentOrder.Query().Where(paymentorder.StatusIn(payment.OrderStatusPending, payment.OrderStatusProcessing)).Count(ctx)
}
func (s *OrderStore) PlanNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	rows, e := s.client.SubscriptionPlan.Query().Where(subscriptionplan.IDIn(ids...)).All(ctx)
	if e != nil {
		return nil, e
	}
	names := make(map[int64]string, len(rows))
	for _, v := range rows {
		names[v.ID] = v.Name
	}
	return names, nil
}

func (s *OrderStore) OrderAuditLogs(ctx context.Context, id int64) ([]*payment.AuditLog, error) {
	rows, err := s.client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10))).Order(paymentauditlog.ByCreatedAt()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*payment.AuditLog, len(rows))
	for i, v := range rows {
		out[i] = &payment.AuditLog{ID: v.ID, OrderID: v.OrderID, Action: v.Action, Detail: v.Detail, Operator: v.Operator, CreatedAt: v.CreatedAt}
	}
	return out, nil
}
