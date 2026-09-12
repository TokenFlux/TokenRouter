// 本文件维护 accessview 的所属能力；兼容入口复用唯一实现。
package accessview

// CloneGroupAdvancedSchedulerOverrides 返回覆盖对象及其指针字段的独立副本。
func CloneGroupAdvancedSchedulerOverrides(overrides GroupAdvancedSchedulerOverrides) GroupAdvancedSchedulerOverrides {
	cloneBool := func(value *bool) *bool {
		if value == nil {
			return nil
		}
		cloned := *value
		return &cloned
	}
	cloneInt := func(value *int) *int {
		if value == nil {
			return nil
		}
		cloned := *value
		return &cloned
	}
	cloneFloat := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		cloned := *value
		return &cloned
	}
	return GroupAdvancedSchedulerOverrides{
		StickyWeightedEnabled:       cloneBool(overrides.StickyWeightedEnabled),
		SubscriptionPriorityEnabled: cloneBool(overrides.SubscriptionPriorityEnabled),
		EWMAErrorRateAlpha:          cloneFloat(overrides.EWMAErrorRateAlpha),
		EWMATTFTAlpha:               cloneFloat(overrides.EWMATTFTAlpha),
		StickyEscapeEnabled:         cloneBool(overrides.StickyEscapeEnabled),
		StickyEscapeTTFTMs:          cloneInt(overrides.StickyEscapeTTFTMs),
		StickyEscapeErrorRate:       cloneFloat(overrides.StickyEscapeErrorRate),
		LBTopK:                      cloneInt(overrides.LBTopK),
		WeightPriority:              cloneFloat(overrides.WeightPriority),
		WeightLoad:                  cloneFloat(overrides.WeightLoad),
		WeightQueue:                 cloneFloat(overrides.WeightQueue),
		WeightErrorRate:             cloneFloat(overrides.WeightErrorRate),
		WeightTTFT:                  cloneFloat(overrides.WeightTTFT),
		WeightReset:                 cloneFloat(overrides.WeightReset),
		WeightQuotaHeadroom:         cloneFloat(overrides.WeightQuotaHeadroom),
		WeightPreviousResponse:      cloneFloat(overrides.WeightPreviousResponse),
		WeightSessionSticky:         cloneFloat(overrides.WeightSessionSticky),
	}
}
