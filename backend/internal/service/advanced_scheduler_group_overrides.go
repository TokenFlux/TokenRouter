package service

import (
	"context"
	"fmt"
	"math"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
)

// advancedSchedulerEffectiveSettings 是完成全局与分组覆盖合并后的请求级配置。
// 分组字段优先级最高；缺失字段继续继承设置仓库和静态配置的结果。
type advancedSchedulerEffectiveSettings struct {
	stickyWeightedEnabled       bool
	subscriptionPriorityEnabled bool
	topK                        int
	weights                     GatewayAdvancedSchedulerScoreWeightsView
}

// ValidateGroupAdvancedSchedulerOverrides 校验分组稀疏覆盖的单字段边界。
// 基础权重允许全部显式设为零，此时 Top-K 使用稳定并列规则并等权抽样。
func ValidateGroupAdvancedSchedulerOverrides(overrides GroupAdvancedSchedulerOverrides) error {
	if overrides.LBTopK != nil && *overrides.LBTopK <= 0 {
		return fmt.Errorf("lb_top_k must be a positive integer")
	}

	weights := []struct {
		name  string
		value *float64
	}{
		{"weight_priority", overrides.WeightPriority},
		{"weight_load", overrides.WeightLoad},
		{"weight_queue", overrides.WeightQueue},
		{"weight_error_rate", overrides.WeightErrorRate},
		{"weight_ttft", overrides.WeightTTFT},
		{"weight_reset", overrides.WeightReset},
		{"weight_quota_headroom", overrides.WeightQuotaHeadroom},
		{"weight_previous_response", overrides.WeightPreviousResponse},
		{"weight_session_sticky", overrides.WeightSessionSticky},
	}
	for _, item := range weights {
		if item.value == nil {
			continue
		}
		if *item.value < 0 || math.IsNaN(*item.value) || math.IsInf(*item.value, 0) {
			return fmt.Errorf("%s must be a non-negative finite number", item.name)
		}
	}

	return nil
}

// validateAdvancedSchedulerEffectiveWeights 校验合并后的完整权重。
// 基础权重总和可以为零，但基础和完整总和都必须保持有限，避免评分出现 NaN 或 Inf。
func validateAdvancedSchedulerEffectiveWeights(weights GatewayAdvancedSchedulerScoreWeightsView) error {
	values := []struct {
		name  string
		value float64
	}{
		{"weight_priority", weights.Priority},
		{"weight_load", weights.Load},
		{"weight_queue", weights.Queue},
		{"weight_error_rate", weights.ErrorRate},
		{"weight_ttft", weights.TTFT},
		{"weight_reset", weights.Reset},
		{"weight_quota_headroom", weights.QuotaHeadroom},
		{"weight_previous_response", weights.Previous},
		{"weight_session_sticky", weights.SessionSticky},
	}
	for _, item := range values {
		if item.value < 0 || math.IsNaN(item.value) || math.IsInf(item.value, 0) {
			return fmt.Errorf("%s must be a non-negative finite number", item.name)
		}
	}

	resolved := weights.configWeights()
	if baseSum := resolved.BaseWeightSum(); math.IsNaN(baseSum) || math.IsInf(baseSum, 0) {
		return fmt.Errorf("base-weight sum must be finite")
	}
	if totalSum := resolved.TotalWeightSum(); math.IsNaN(totalSum) || math.IsInf(totalSum, 0) {
		return fmt.Errorf("total-weight sum must be finite")
	}
	return nil
}

// hasGroupAdvancedSchedulerWeightOverrides 判断是否需要读取全局权重完成合并校验。
func hasGroupAdvancedSchedulerWeightOverrides(overrides GroupAdvancedSchedulerOverrides) bool {
	return overrides.WeightPriority != nil ||
		overrides.WeightLoad != nil ||
		overrides.WeightQueue != nil ||
		overrides.WeightErrorRate != nil ||
		overrides.WeightTTFT != nil ||
		overrides.WeightReset != nil ||
		overrides.WeightQuotaHeadroom != nil ||
		overrides.WeightPreviousResponse != nil ||
		overrides.WeightSessionSticky != nil
}

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

// applyGroupAdvancedSchedulerWeightOverrides 只替换分组显式提供的权重字段。
func applyGroupAdvancedSchedulerWeightOverrides(
	weights GatewayAdvancedSchedulerScoreWeightsView,
	overrides GroupAdvancedSchedulerOverrides,
) GatewayAdvancedSchedulerScoreWeightsView {
	if overrides.WeightPriority != nil {
		weights.Priority = *overrides.WeightPriority
	}
	if overrides.WeightLoad != nil {
		weights.Load = *overrides.WeightLoad
	}
	if overrides.WeightQueue != nil {
		weights.Queue = *overrides.WeightQueue
	}
	if overrides.WeightErrorRate != nil {
		weights.ErrorRate = *overrides.WeightErrorRate
	}
	if overrides.WeightTTFT != nil {
		weights.TTFT = *overrides.WeightTTFT
	}
	if overrides.WeightReset != nil {
		weights.Reset = *overrides.WeightReset
	}
	if overrides.WeightQuotaHeadroom != nil {
		weights.QuotaHeadroom = *overrides.WeightQuotaHeadroom
	}
	if overrides.WeightPreviousResponse != nil {
		weights.Previous = *overrides.WeightPreviousResponse
	}
	if overrides.WeightSessionSticky != nil {
		weights.SessionSticky = *overrides.WeightSessionSticky
	}
	return weights
}

// validateGroupAdvancedSchedulerOverridesForWrite 使用当前全局权重校验写入后的完整结果。
func (s *adminServiceImpl) validateGroupAdvancedSchedulerOverridesForWrite(
	ctx context.Context,
	overrides GroupAdvancedSchedulerOverrides,
) error {
	if err := ValidateGroupAdvancedSchedulerOverrides(overrides); err != nil {
		return err
	}
	if !hasGroupAdvancedSchedulerWeightOverrides(overrides) {
		return nil
	}

	globalWeights, err := s.advancedSchedulerGlobalWeightsForValidation(ctx)
	if err != nil {
		return err
	}
	return validateAdvancedSchedulerEffectiveWeights(applyGroupAdvancedSchedulerWeightOverrides(globalWeights, overrides))
}

// advancedSchedulerGlobalWeightsForValidation 直接读取设置仓库，避免写入校验依赖短 TTL 热路径缓存。
func (s *adminServiceImpl) advancedSchedulerGlobalWeightsForValidation(
	ctx context.Context,
) (GatewayAdvancedSchedulerScoreWeightsView, error) {
	gateway := &OpenAIGatewayService{}
	if s != nil && s.settingService != nil {
		gateway.cfg = s.settingService.cfg
	}
	baseWeights := gateway.openAIWSSchedulerWeights()
	if !baseWeights.configWeights().IsValid() {
		baseWeights = (&OpenAIGatewayService{}).openAIWSSchedulerWeights()
	}
	if s == nil || s.settingService == nil || s.settingService.settingRepo == nil {
		return baseWeights, nil
	}

	values, err := s.settingService.settingRepo.GetMultiple(ctx, advancedSchedulerRuntimeSettingKeys())
	if err != nil {
		return GatewayAdvancedSchedulerScoreWeightsView{}, fmt.Errorf("load advanced scheduler settings: %w", err)
	}
	globalWeights := applyAdvancedSchedulerWeightOverrides(baseWeights, parseAdvancedSchedulerWeightOverrides(values))
	if !globalWeights.configWeights().IsValid() {
		return baseWeights, nil
	}
	return globalWeights, nil
}

// resolveAdvancedSchedulerEffectiveSettings 以全局生效配置为基线合并分组覆盖。
// 分组显式零值保持生效，不因最终基础权重全零而静默恢复全局参数。
func resolveAdvancedSchedulerEffectiveSettings(
	baseTopK int,
	baseWeights GatewayAdvancedSchedulerScoreWeightsView,
	global advancedSchedulerRuntimeSettings,
	overrides GroupAdvancedSchedulerOverrides,
) advancedSchedulerEffectiveSettings {
	if baseTopK <= 0 {
		baseTopK = 7
	}
	globalTopK := baseTopK
	if global.lbTopKOverride > 0 {
		globalTopK = global.lbTopKOverride
	}
	globalWeights := applyAdvancedSchedulerWeightOverrides(baseWeights, global.weightOverrides)
	if !globalWeights.configWeights().IsValid() {
		globalWeights = baseWeights
	}

	effective := advancedSchedulerEffectiveSettings{
		stickyWeightedEnabled:       global.stickyWeightedEnabled,
		subscriptionPriorityEnabled: global.subscriptionPriorityEnabled,
		topK:                        globalTopK,
		weights:                     globalWeights,
	}
	if overrides.StickyWeightedEnabled != nil {
		effective.stickyWeightedEnabled = *overrides.StickyWeightedEnabled
	}
	if overrides.SubscriptionPriorityEnabled != nil {
		effective.subscriptionPriorityEnabled = *overrides.SubscriptionPriorityEnabled
	}
	if overrides.LBTopK != nil && *overrides.LBTopK > 0 {
		effective.topK = *overrides.LBTopK
	}
	effective.weights = applyGroupAdvancedSchedulerWeightOverrides(effective.weights, overrides)
	if validateErr := validateAdvancedSchedulerEffectiveWeights(effective.weights); validateErr != nil {
		// 历史异常数据不能进入评分；仅回退权重，保留分组其它有效覆盖。
		effective.weights = globalWeights
	}
	return effective
}

// advancedSchedulerEffectiveSettingsForGroup 将分组覆盖置于运行时全局设置之上。
func (s *OpenAIGatewayService) advancedSchedulerEffectiveSettingsForGroup(
	ctx context.Context,
	group *Group,
) advancedSchedulerEffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	var overrides GroupAdvancedSchedulerOverrides
	if group != nil && group.UsesAdvancedScheduler() {
		overrides = group.AdvancedSchedulerOverrides
	}
	return resolveAdvancedSchedulerEffectiveSettings(
		s.openAIWSLBTopK(),
		s.openAIWSSchedulerWeights(),
		s.advancedSchedulerRuntimeSettings(ctx),
		overrides,
	)
}

// advancedSchedulerEffectiveSettingsForRequest 读取最终目标分组并生成请求级有效配置。
// 分组不存在或未被加载时只使用全局配置，保持无分组路径的历史行为。
func (s *OpenAIGatewayService) advancedSchedulerEffectiveSettingsForRequest(
	ctx context.Context,
	groupID *int64,
) advancedSchedulerEffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	return s.advancedSchedulerEffectiveSettingsForGroup(ctx, s.advancedSchedulerGroupForRequest(ctx, groupID))
}

// advancedSchedulerGroupForRequest 优先复用请求上下文的最终分组，必要时再读取调度快照。
func (s *OpenAIGatewayService) advancedSchedulerGroupForRequest(ctx context.Context, groupID *int64) *Group {
	if groupID == nil || *groupID <= 0 {
		return nil
	}
	if ctx != nil {
		if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) && group.ID == *groupID {
			return group
		}
	}
	if s == nil || s.schedulerSnapshot == nil {
		return nil
	}
	group, err := s.schedulerSnapshot.GetGroupByID(ctx, *groupID)
	if err != nil {
		return nil
	}
	return group
}
