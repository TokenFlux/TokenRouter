// 渠道存储保留原 SQL 选择与批量用量，不拥有选择策略。
package postgres

import (
	"context"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/ent/paymentproviderinstance"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

type InstanceStore struct{ client *dbent.Client }

func NewInstanceStore(client *dbent.Client) *InstanceStore { return &InstanceStore{client: client} }
func InstanceFromEntity(v *dbent.PaymentProviderInstance) *payment.ProviderInstance {
	if v == nil {
		return nil
	}
	return &payment.ProviderInstance{
		ID:              v.ID,
		ProviderKey:     v.ProviderKey,
		Name:            v.Name,
		Config:          v.Config,
		SupportedTypes:  v.SupportedTypes,
		Enabled:         v.Enabled,
		PaymentMode:     v.PaymentMode,
		SortOrder:       v.SortOrder,
		Limits:          v.Limits,
		RefundEnabled:   v.RefundEnabled,
		AllowUserRefund: v.AllowUserRefund,
		CreatedAt:       v.CreatedAt,
		UpdatedAt:       v.UpdatedAt,
	}
}
func (s *InstanceStore) EnabledInstances(ctx context.Context, key string) ([]*payment.ProviderInstance, error) {
	query := s.client.PaymentProviderInstance.Query().Where(paymentproviderinstance.Enabled(true))
	if key != "" {
		query = query.Where(paymentproviderinstance.ProviderKey(key))
	}
	rows, err := query.Order(dbent.Asc(paymentproviderinstance.FieldSortOrder)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*payment.ProviderInstance, len(rows))
	for i, v := range rows {
		out[i] = InstanceFromEntity(v)
	}
	return out, nil
}
func (s *InstanceStore) Instance(ctx context.Context, id int64) (*payment.ProviderInstance, error) {
	v, err := s.client.PaymentProviderInstance.Get(ctx, id)
	return InstanceFromEntity(v), err
}
func (s *InstanceStore) DailyUsage(ctx context.Context, ids []string, start time.Time) (map[string]float64, error) {
	var rows []struct {
		InstanceID string  `json:"provider_instance_id"`
		Sum        float64 `json:"sum"`
	}
	err := s.client.PaymentOrder.Query().
		Where(
			paymentorder.ProviderInstanceIDIn(ids...),
			paymentorder.StatusIn(payment.OrderStatusPending, payment.OrderStatusProcessing, payment.OrderStatusPaid, payment.OrderStatusCompleted, payment.OrderStatusRecharging),
			paymentorder.CreatedAtGTE(start),
		).
		GroupBy(paymentorder.FieldProviderInstanceID).
		Aggregate(dbent.Sum(paymentorder.FieldPayAmount)).
		Scan(ctx, &rows)
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		out[r.InstanceID] = r.Sum
	}
	return out, err
}
func (s *InstanceStore) PaidDailyAmount(ctx context.Context, id string, start time.Time) (float64, error) {
	var rows []struct {
		Sum float64 `json:"sum"`
	}
	err := s.client.PaymentOrder.Query().
		Where(
			paymentorder.ProviderInstanceID(id),
			paymentorder.StatusIn(payment.OrderStatusCompleted, payment.OrderStatusPaid, payment.OrderStatusRecharging),
			paymentorder.PaidAtGTE(start),
		).
		Aggregate(dbent.Sum(paymentorder.FieldPayAmount)).
		Scan(ctx, &rows)
	if err != nil {
		return 0, err
	}
	if len(rows) > 0 {
		return rows[0].Sum, nil
	}
	return 0, nil
}

// CountEnabledInstances 保持旧 webhook fallback 的单条 COUNT 查询。
func (s *InstanceStore) CountEnabledInstances(ctx context.Context, key string) (int, error) {
	return s.client.PaymentProviderInstance.Query().Where(paymentproviderinstance.ProviderKeyEQ(key), paymentproviderinstance.EnabledEQ(true)).Count(ctx)
}
func (s *InstanceStore) OrderByTradeNumber(ctx context.Context, no string) (*payment.Order, error) {
	o, e := s.client.PaymentOrder.Query().Where(paymentorder.OutTradeNo(no)).Only(ctx)
	return OrderFromEntity(o), e
}
