// 旧查询入口不保存状态或计算规则，委托原 SQL 适配与 payment 查询用例。
package service

import (
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
)

func (s *PaymentService) paymentQueries() *payment.OrderQueries {
	if s.queries != nil {
		return s.queries
	}
	return payment.NewOrderQueries(paymentpostgres.NewOrderStore(s.entClient), time.Now, s.paymentBindings().GetOrderProvider)
}
func (s *PaymentService) BindOrderQueries(queries *payment.OrderQueries) { s.queries = queries }
func paymentOrderValues(rows []*dbent.PaymentOrder) []*payment.Order {
	if rows == nil {
		return nil
	}
	out := make([]*payment.Order, len(rows))
	for i, v := range rows {
		out[i] = paymentOrderValue(v)
	}
	return out
}
