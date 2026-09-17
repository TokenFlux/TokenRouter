// 支付过程审计的展示值不等同于资金提交证明；退款必要事实由闭合操作负责。
package payment

import (
	"context"
	"time"
)

type AuditLog struct {
	ID        int64     `json:"id,omitempty"`
	OrderID   string    `json:"order_id,omitempty"`
	Action    string    `json:"action,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Operator  string    `json:"operator,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

func (s *OrderQueries) GetOrderAuditLogs(ctx context.Context, id int64) ([]*AuditLog, error) {
	return s.store.OrderAuditLogs(ctx, id)
}
