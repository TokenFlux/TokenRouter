package pricing

// CalculateCost 在纯输入上统一分发计费模式，保留负倍率及结果模式语义。
func CalculateCost(resolved *ResolvedPricing, input CostInput) (*CostBreakdown, error) {
	// 保存时强制 > 0；若仍有负数泄漏（缓存/迁移残留），按 0 处理避免按 1x 误扣。
	if input.RateMultiplier < 0 {
		input.RateMultiplier = 0
	}

	var breakdown *CostBreakdown
	var err error
	switch resolved.Mode {
	case BillingModePerRequest, BillingModeImage, BillingModeVideo:
		breakdown, err = CalculatePerRequestCost(resolved, input)
	default: // BillingModeToken
		breakdown, err = CalculateTokenCost(resolved, input)
	}
	if err == nil && breakdown != nil {
		breakdown.BillingMode = string(resolved.Mode)
		if breakdown.BillingMode == "" {
			breakdown.BillingMode = string(BillingModeToken)
		}
	}
	return breakdown, err
}
