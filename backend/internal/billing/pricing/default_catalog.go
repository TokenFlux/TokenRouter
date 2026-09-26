package pricing

// DefaultPriceValue 描述一个带明确单位的默认价格或倍率；nil 表示不适用。
type DefaultPriceValue struct {
	Key   string   `json:"key"`
	Value *float64 `json:"value"`
	Unit  string   `json:"unit"`
}

// DefaultModelPrice 是管理员可查询的基础价投影，不包含分组和用户倍率。
type DefaultModelPrice struct {
	Model                         string              `json:"model"`
	Platform                      string              `json:"platform"`
	BillingMode                   string              `json:"billing_mode"`
	PriceStatus                   string              `json:"price_status"`
	Prices                        []DefaultPriceValue `json:"prices"`
	LongContextThreshold          int                 `json:"long_context_threshold,omitempty"`
	LongContextThresholdInclusive bool                `json:"long_context_threshold_inclusive,omitempty"`
}
