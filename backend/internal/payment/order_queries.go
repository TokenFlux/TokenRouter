// 订单权限与查询错误形状保留原入口契约。
package payment

import (
	"context"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func (s *OrderQueries) GetOrder(ctx context.Context, orderID, userID int64) (*Order, error) {
	o, err := s.store.Order(ctx, orderID)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	return o, nil
}
func (s *OrderQueries) GetOrderByID(ctx context.Context, orderID int64) (*Order, error) {
	o, err := s.store.Order(ctx, orderID)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	return o, nil
}
func (s *OrderQueries) GetUserOrders(ctx context.Context, userID int64, p OrderListParams) ([]*Order, int, error) {
	return s.store.GetUserOrders(ctx, userID, p)
}
func (s *OrderQueries) AdminListOrders(ctx context.Context, userID int64, p OrderListParams) ([]*Order, int, error) {
	return s.store.AdminListOrders(ctx, userID, p)
}
