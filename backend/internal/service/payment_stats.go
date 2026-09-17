package service

import (
	"context"
	"log/slog"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
)

// --- Dashboard & Analytics ---

func (s *PaymentService) GetDashboardStats(ctx context.Context, days int) (*DashboardStats, error) {
	return s.paymentQueries().GetDashboardStats(ctx, days)
}

func (s *PaymentService) GetDashboardStatsWithRange(ctx context.Context, start, end time.Time) (*DashboardStats, error) {
	return s.paymentQueries().GetDashboardStatsWithRange(ctx, start, end)
}

// --- Audit Logs ---

func (s *PaymentService) writeAuditLog(ctx context.Context, oid int64, action, op string, detail map[string]any) {
	if err := s.paymentRefundStore().AppendObservation(ctx, oid, action, op, detail); err != nil {
		slog.Error("audit log failed", "orderID", oid, "action", action, "error", err)
	}
}

func (s *PaymentService) GetOrderAuditLogs(ctx context.Context, oid int64) ([]*dbent.PaymentAuditLog, error) {
	rows, err := s.paymentQueries().GetOrderAuditLogs(ctx, oid)
	if err != nil {
		return nil, err
	}
	out := make([]*dbent.PaymentAuditLog, len(rows))
	for i, v := range rows {
		out[i] = &dbent.PaymentAuditLog{ID: v.ID, OrderID: v.OrderID, Action: v.Action, Detail: v.Detail, Operator: v.Operator, CreatedAt: v.CreatedAt}
	}
	return out, nil
}
