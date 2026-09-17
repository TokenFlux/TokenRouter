// ProviderInstance 是渠道配置与订单关联需要的值，不携带数据库客户端。
package payment

import "time"

type ProviderInstance struct {
	ID              int64     `json:"id,omitempty"`
	ProviderKey     string    `json:"provider_key,omitempty"`
	Name            string    `json:"name,omitempty"`
	Config          string    `json:"config,omitempty"`
	SupportedTypes  string    `json:"supported_types,omitempty"`
	Enabled         bool      `json:"enabled,omitempty"`
	PaymentMode     string    `json:"payment_mode,omitempty"`
	SortOrder       int       `json:"sort_order,omitempty"`
	Limits          string    `json:"limits,omitempty"`
	RefundEnabled   bool      `json:"refund_enabled,omitempty"`
	AllowUserRefund bool      `json:"allow_user_refund,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}
