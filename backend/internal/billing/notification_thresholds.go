// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

// BalanceThreshold 保留固定值与累计充值比例的现有计算顺序。
func BalanceThreshold(threshold float64, kind string, recharged float64) float64 {
	if kind == "percentage" && recharged > 0 {
		return recharged * threshold / 100
	}
	return threshold
}
func CrossedDownward(oldValue, newValue, threshold float64) bool {
	return oldValue >= threshold && newValue < threshold
}

// EffectiveBalanceThreshold 只处理额度规则，配置读取与通知发送由外层投影。
func EffectiveBalanceThreshold(globalEnabled bool, globalThreshold float64, userThreshold *float64, kind string, recharged float64) (float64, bool) {
	if !globalEnabled {
		return 0, false
	}
	threshold := globalThreshold
	if userThreshold != nil {
		threshold = *userThreshold
	}
	if threshold <= 0 {
		return 0, false
	}
	effective := BalanceThreshold(threshold, kind, recharged)
	if effective <= 0 {
		return 0, false
	}
	return effective, true
}

// QuotaNotifyDimension 表示一个额度窗口的已提交用量与通知配置。
type QuotaNotifyDimension struct {
	Name               string
	Enabled            bool
	Threshold          float64
	ThresholdType      string
	CurrentUsed, Limit float64
}

func (d QuotaNotifyDimension) UsageThreshold() float64 {
	if d.Limit <= 0 {
		return 0
	}
	if d.ThresholdType == "percentage" {
		return d.Limit * (1 - d.Threshold/100)
	}
	return d.Limit - d.Threshold
}
func (d QuotaNotifyDimension) Crossing(cost float64) (float64, bool) {
	if !d.Enabled || d.Threshold <= 0 {
		return 0, false
	}
	threshold := d.UsageThreshold()
	if threshold <= 0 {
		return 0, false
	}
	return threshold, d.CurrentUsed-cost < threshold && d.CurrentUsed >= threshold
}
