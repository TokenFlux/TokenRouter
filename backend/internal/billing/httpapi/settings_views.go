package httpapi

// DefaultSubscriptionSetting 保留现有 HTTP JSON 字段与省略语义。
type DefaultSubscriptionSetting struct {
	PlanID int64 `json:"plan_id"`
}
