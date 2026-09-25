package billing

import (
	bytes "bytes"
	json "encoding/json"
)

type NullableFloat64Patch struct {
	Present bool     `json:"-"`
	Value   *float64 `json:"-"`
}

func (p *NullableFloat64Patch) UnmarshalJSON(data []byte) error {
	p.Present = true
	p.Value = nil
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}

	var value float64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	p.Value = &value
	return nil
}

type CreatePlanRequest struct {
	GroupID              int64             `json:"group_id"`
	GroupIDs             []int64           `json:"group_ids"`
	GroupRateMultipliers map[int64]float64 `json:"group_rate_multipliers"`
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	Price                float64           `json:"price"`
	OriginalPrice        *float64          `json:"original_price"`
	Currency             string            `json:"currency"`
	ValidityDays         int               `json:"validity_days"`
	ValidityUnit         string            `json:"validity_unit"`
	DailyLimitUSD        *float64          `json:"daily_limit_usd"`
	WeeklyLimitUSD       *float64          `json:"weekly_limit_usd"`
	MonthlyLimitUSD      *float64          `json:"monthly_limit_usd"`
	Features             string            `json:"features"`
	ProductName          string            `json:"product_name"`
	ForSale              bool              `json:"for_sale"`
	SortOrder            int               `json:"sort_order"`
}

type UpdatePlanRequest struct {
	GroupID              *int64               `json:"group_id"`
	GroupIDs             *[]int64             `json:"group_ids"`
	GroupRateMultipliers *map[int64]float64   `json:"group_rate_multipliers"`
	Name                 *string              `json:"name"`
	Description          *string              `json:"description"`
	Price                *float64             `json:"price"`
	OriginalPrice        NullableFloat64Patch `json:"original_price"`
	Currency             *string              `json:"currency"`
	ValidityDays         *int                 `json:"validity_days"`
	ValidityUnit         *string              `json:"validity_unit"`
	DailyLimitUSD        NullableFloat64Patch `json:"daily_limit_usd"`
	WeeklyLimitUSD       NullableFloat64Patch `json:"weekly_limit_usd"`
	MonthlyLimitUSD      NullableFloat64Patch `json:"monthly_limit_usd"`
	Features             *string              `json:"features"`
	ProductName          *string              `json:"product_name"`
	ForSale              *bool                `json:"for_sale"`
	SortOrder            *int                 `json:"sort_order"`
}
