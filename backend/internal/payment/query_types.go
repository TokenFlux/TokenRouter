// 支付查询独立展示类型，不携带 ORM 关系和凭据。
package payment

type OrderListParams struct {
	Page        int
	PageSize    int
	Status      string
	OrderType   string
	PaymentType string
	Keyword     string
}
type DashboardStats struct {
	TodayAmount                        CurrencyAmounts `json:"today_amount"`
	TotalAmount                        CurrencyAmounts `json:"total_amount"`
	TodayCount                         int             `json:"today_count"`
	TotalCount                         int             `json:"total_count"`
	AvgAmount                          CurrencyAmounts `json:"avg_amount"`
	AvgReasoningPointPurchaseUnitPrice float64         `json:"avg_reasoning_point_purchase_unit_price"`
	ReasoningPointPurchaseOrderCount   int             `json:"reasoning_point_purchase_order_count"`
	PendingOrders                      int             `json:"pending_orders"`

	DailySeries          []DailyStats               `json:"daily_series"`
	PaymentMethods       []PaymentMethodStat        `json:"payment_methods"`
	PurchaseDistribution []PurchaseDistributionStat `json:"purchase_distribution"`
	TopUsers             TopUsersByCurrency         `json:"top_users"`
}

// CurrencyAmounts 按 ISO 4217 币种保存金额，禁止把不同币种相加。
type CurrencyAmounts map[string]float64
type DailyStats struct {
	Date   string          `json:"date"`
	Amount CurrencyAmounts `json:"amount"`
	Count  int             `json:"count"`
}
type PaymentMethodStat struct {
	Type   string          `json:"type"`
	Amount CurrencyAmounts `json:"amount"`
	Count  int             `json:"count"`
}
type PurchaseDistributionStat struct {
	Type   string  `json:"type"`
	Label  string  `json:"label"`
	PlanID *int64  `json:"plan_id,omitempty"`
	Amount float64 `json:"amount"`
	Count  int     `json:"count"`
}
type TopUserStat struct {
	UserID int64   `json:"user_id"`
	Email  string  `json:"email"`
	Amount float64 `json:"amount"`
}

// TopUsersByCurrency 为每种币种维护独立排行，避免生成误导性的跨币种榜单。
type TopUsersByCurrency map[string][]TopUserStat
