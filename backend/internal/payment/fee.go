package payment

import (
	"github.com/shopspring/decimal"
)

// FeeConfig 表示某个支付渠道实际生效的手续费配置。
type FeeConfig struct {
	FixedFee float64 `json:"fixed_fee"`
	FeeRate  float64 `json:"fee_rate"`
}

// FeeBreakdown 表示一次下单的手续费拆分结果。
type FeeBreakdown struct {
	BaseAmount    float64 `json:"base_amount"`
	FixedFee      float64 `json:"fee_fixed"`
	FeeRate       float64 `json:"fee_rate"`
	FeeRateAmount float64 `json:"fee_rate_amount"`
	FeeAmount     float64 `json:"fee_amount"`
	PayAmount     float64 `json:"pay_amount"`
}

// CalculatePayAmount computes the total pay amount given a recharge amount and
// fee rate (percentage). Fee = amount * feeRate / 100, rounded UP (away from zero)
// to 2 decimal places. The returned string is formatted to exactly 2 decimal places.
// If feeRate <= 0, the amount is returned as-is (formatted to 2 decimal places).
func CalculatePayAmount(rechargeAmount float64, feeRate float64) string {
	return CalculatePayAmountWithFee(rechargeAmount, FeeConfig{FeeRate: feeRate}).PayAmountString()
}

// CalculatePayAmountWithFee 计算固定手续费和比例手续费拆分后的实付金额。
func CalculatePayAmountWithFee(baseAmount float64, cfg FeeConfig) FeeBreakdown {
	amount := decimal.NewFromFloat(baseAmount).Round(2)
	fixedFee := decimal.Zero
	if cfg.FixedFee > 0 {
		fixedFee = decimal.NewFromFloat(cfg.FixedFee).Round(2)
	}

	rateAmount := decimal.Zero
	if cfg.FeeRate > 0 {
		rate := decimal.NewFromFloat(cfg.FeeRate)
		rateAmount = amount.Mul(rate).Div(decimal.NewFromInt(100)).RoundUp(2)
	}

	feeAmount := fixedFee.Add(rateAmount).Round(2)
	payAmount := amount.Add(feeAmount).Round(2)
	return FeeBreakdown{
		BaseAmount:    decimalToFloat(amount),
		FixedFee:      decimalToFloat(fixedFee),
		FeeRate:       cfg.FeeRate,
		FeeRateAmount: decimalToFloat(rateAmount),
		FeeAmount:     decimalToFloat(feeAmount),
		PayAmount:     decimalToFloat(payAmount),
	}
}

// PayAmountString 返回支付网关需要的两位小数字符串金额。
func (b FeeBreakdown) PayAmountString() string {
	return decimal.NewFromFloat(b.PayAmount).StringFixed(2)
}

func decimalToFloat(v decimal.Decimal) float64 {
	out, _ := v.Float64()
	return out
}
