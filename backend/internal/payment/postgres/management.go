// 配置存储保持原选择、字段更新与保护计数；业务校验由核心负责。
package postgres

import (
	"context"
	"strconv"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/ent/paymentproviderinstance"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func (s *InstanceStore) IsNotFound(err error) bool { return dbent.IsNotFound(err) }
func (s *InstanceStore) ListInstances(ctx context.Context, f payment.InstanceFilter) ([]*payment.ProviderInstance, error) {
	q := s.client.PaymentProviderInstance.Query()
	if f.EnabledOnly {
		q = q.Where(paymentproviderinstance.EnabledEQ(true))
	}
	if f.ProviderKey != "" {
		q = q.Where(paymentproviderinstance.ProviderKeyEQ(f.ProviderKey))
	}
	if f.ExcludeID > 0 {
		q = q.Where(paymentproviderinstance.IDNEQ(f.ExcludeID))
	}
	if f.SortByOrder {
		q = q.Order(paymentproviderinstance.BySortOrder())
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	var rows []*dbent.PaymentProviderInstance
	var err error
	if f.UserRefundEligible {
		rows, err = q.Where(
			paymentproviderinstance.RefundEnabledEQ(true),
			paymentproviderinstance.AllowUserRefundEQ(true),
		).Select(paymentproviderinstance.FieldID).All(ctx)
	} else {
		rows, err = q.All(ctx)
	}
	if err != nil {
		return nil, err
	}
	out := make([]*payment.ProviderInstance, len(rows))
	for i, row := range rows {
		out[i] = InstanceFromEntity(row)
	}
	return out, nil
}
func (s *InstanceStore) CreateInstance(ctx context.Context, v payment.ProviderInstance) (*payment.ProviderInstance, error) {
	value, err := s.client.PaymentProviderInstance.Create().
		SetProviderKey(v.ProviderKey).
		SetName(v.Name).
		SetConfig(v.Config).
		SetSupportedTypes(v.SupportedTypes).
		SetEnabled(v.Enabled).
		SetPaymentMode(v.PaymentMode).
		SetSortOrder(v.SortOrder).
		SetLimits(v.Limits).
		SetRefundEnabled(v.RefundEnabled).
		SetAllowUserRefund(v.AllowUserRefund).
		Save(ctx)
	return InstanceFromEntity(value), err
}
func (s *InstanceStore) UpdateInstance(ctx context.Context, id int64, v payment.InstancePatch) (*payment.ProviderInstance, error) {
	q := s.client.PaymentProviderInstance.UpdateOneID(id)
	if v.Name != nil {
		q.SetName(*v.Name)
	}
	if v.Config != nil {
		q.SetConfig(*v.Config)
	}
	if v.SupportedTypes != nil {
		q.SetSupportedTypes(*v.SupportedTypes)
	}
	if v.Enabled != nil {
		q.SetEnabled(*v.Enabled)
	}
	if v.SortOrder != nil {
		q.SetSortOrder(*v.SortOrder)
	}
	if v.Limits != nil {
		q.SetLimits(*v.Limits)
	}
	if v.RefundEnabled != nil {
		q.SetRefundEnabled(*v.RefundEnabled)
	}
	if v.AllowUserRefund != nil {
		q.SetAllowUserRefund(*v.AllowUserRefund)
	}
	if v.PaymentMode != nil {
		q.SetPaymentMode(*v.PaymentMode)
	}
	row, err := q.Save(ctx)
	return InstanceFromEntity(row), err
}
func (s *InstanceStore) DeleteInstance(ctx context.Context, id int64) error {
	return s.client.PaymentProviderInstance.DeleteOneID(id).Exec(ctx)
}
func (s *InstanceStore) CountInProgressByProvider(ctx context.Context, id int64) (int, error) {
	return s.client.PaymentOrder.Query().Where(
		paymentorder.ProviderInstanceIDEQ(strconv.FormatInt(id, 10)),
		paymentorder.StatusIn(payment.InProgressOrderStatuses()...),
	).Count(ctx)
}
func (s *InstanceStore) CountInProgressByPlan(ctx context.Context, id int64) (int, error) {
	return s.client.PaymentOrder.Query().Where(
		paymentorder.PlanIDEQ(id),
		paymentorder.StatusIn(payment.InProgressOrderStatuses()...),
	).Count(ctx)
}
func (s *InstanceStore) CountForcedExpiredByProvider(ctx context.Context, id int64) (int, error) {
	ids, err := s.client.PaymentOrder.Query().Where(
		paymentorder.ProviderInstanceIDEQ(strconv.FormatInt(id, 10)),
		paymentorder.StatusEQ(payment.OrderStatusExpired),
	).IDs(ctx)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	auditIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		auditIDs = append(auditIDs, strconv.FormatInt(id, 10))
	}
	return s.client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDIn(auditIDs...),
		paymentauditlog.ActionEQ("ORDER_FORCE_EXPIRED"),
	).Count(ctx)
}
