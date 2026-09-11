package pricing

// TokenCharge 描述旧长上下文入口的一段纯计费输入。
type TokenCharge struct {
	Tokens         UsageTokens
	RateMultiplier float64
}

// LegacyLongContextCharges 保持旧入口的分段规则与缓存优先占用阈值语义。
func LegacyLongContextCharges(tokens UsageTokens, rateMultiplier float64, threshold int, extraMultiplier float64) []TokenCharge {
	if threshold <= 0 || extraMultiplier <= 1 {
		return []TokenCharge{{Tokens: tokens, RateMultiplier: rateMultiplier}}
	}

	// 计算总输入 token（缓存读取 + 新输入）
	total := tokens.CacheReadTokens + tokens.InputTokens
	if total <= threshold {
		return []TokenCharge{{Tokens: tokens, RateMultiplier: rateMultiplier}}
	}

	// 拆分成范围内和范围外
	var inRangeCacheTokens, inRangeInputTokens int
	var outRangeCacheTokens, outRangeInputTokens int

	if tokens.CacheReadTokens >= threshold {
		// 缓存已超过阈值：范围内只有缓存，范围外是超出的缓存+全部输入
		inRangeCacheTokens = threshold
		inRangeInputTokens = 0
		outRangeCacheTokens = tokens.CacheReadTokens - threshold
		outRangeInputTokens = tokens.InputTokens
	} else {
		// 缓存未超过阈值：范围内是全部缓存+部分输入，范围外是剩余输入
		inRangeCacheTokens = tokens.CacheReadTokens
		inRangeInputTokens = threshold - tokens.CacheReadTokens
		outRangeCacheTokens = 0
		outRangeInputTokens = tokens.InputTokens - inRangeInputTokens
	}

	// 范围内部分：正常计费
	inRangeTokens := UsageTokens{
		InputTokens:           inRangeInputTokens,
		OutputTokens:          tokens.OutputTokens, // 输出只算一次
		CacheCreationTokens:   tokens.CacheCreationTokens,
		CacheReadTokens:       inRangeCacheTokens,
		CacheCreation5mTokens: tokens.CacheCreation5mTokens,
		CacheCreation1hTokens: tokens.CacheCreation1hTokens,
		ImageOutputTokens:     tokens.ImageOutputTokens,
	}
	outRangeTokens := UsageTokens{
		InputTokens:     outRangeInputTokens,
		CacheReadTokens: outRangeCacheTokens,
	}

	return []TokenCharge{{Tokens: inRangeTokens, RateMultiplier: rateMultiplier}, {Tokens: outRangeTokens, RateMultiplier: rateMultiplier * extraMultiplier}}
}

// CombineLongContextCosts 保留输出只计算一次与原有加法顺序。
func CombineLongContextCosts(inRangeCost, outRangeCost *CostBreakdown) *CostBreakdown {
	return &CostBreakdown{
		InputCost:                 inRangeCost.InputCost + outRangeCost.InputCost,
		ImageInputCost:            inRangeCost.ImageInputCost + outRangeCost.ImageInputCost,
		OutputCost:                inRangeCost.OutputCost,
		ImageOutputCost:           inRangeCost.ImageOutputCost,
		CacheCreationCost:         inRangeCost.CacheCreationCost,
		CacheReadCost:             inRangeCost.CacheReadCost + outRangeCost.CacheReadCost,
		TotalCost:                 inRangeCost.TotalCost + outRangeCost.TotalCost,
		ActualCost:                inRangeCost.ActualCost + outRangeCost.ActualCost,
		LongContextBillingApplied: outRangeCost.ActualCost > 0,
	}
}
