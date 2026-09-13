package service

import (
	context "context"
	fmt "fmt"
	ctxkey "github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// advancedSchedulerEffectiveSettings 是完成全局与分组覆盖合并后的请求级配置。
// 分组字段优先级最高；缺失字段继续继承设置仓库和静态配置的结果。
type advancedSchedulerEffectiveSettings struct {
	stickyWeightedEnabled       bool
	subscriptionPriorityEnabled bool
	topK                        int
	weights                     GatewayAdvancedSchedulerScoreWeightsView
	feedback                    advancedSchedulerFeedbackConfig
	stickyEscape                advancedStickyEscapeConfig
}

func ValidateGroupAdvancedSchedulerOverrides(overrides GroupAdvancedSchedulerOverrides) error {
	return policy.ValidateGroupOverrides(overrides)
}

func validateAdvancedSchedulerEffectiveWeights(weights GatewayAdvancedSchedulerScoreWeightsView) error {
	return policy.ValidateEffectiveWeights(policy.ScoreWeights(weights))
}

func CloneGroupAdvancedSchedulerOverrides(overrides GroupAdvancedSchedulerOverrides) GroupAdvancedSchedulerOverrides {
	return accessview.CloneGroupAdvancedSchedulerOverrides(overrides)
}

// advancedSchedulerGlobalWeightsForValidation 直接读取设置仓库，避免写入校验依赖短 TTL 热路径缓存。
func GroupValidationWeights(
	ctx context.Context, settings *SettingService,
) (GatewayAdvancedSchedulerScoreWeightsView, error) {
	gateway := &OpenAIGatewayService{}
	if settings != nil {
		gateway.cfg = settings.cfg
	}
	baseWeights := gateway.openAIWSSchedulerWeights()
	if !baseWeights.configWeights().IsValid() {
		baseWeights = (&OpenAIGatewayService{}).openAIWSSchedulerWeights()
	}
	if settings == nil || settings.settingRepo == nil {
		return baseWeights, nil
	}

	values, err := settings.settingRepo.GetMultiple(ctx, advancedSchedulerRuntimeSettingKeys())
	if err != nil {
		return GatewayAdvancedSchedulerScoreWeightsView{}, fmt.Errorf("load advanced scheduler settings: %w", err)
	}
	globalWeights := applyAdvancedSchedulerWeightOverrides(baseWeights, parseAdvancedSchedulerWeightOverrides(values))
	if !globalWeights.configWeights().IsValid() {
		return baseWeights, nil
	}
	return globalWeights, nil
}

func resolveAdvancedSchedulerEffectiveSettings(
	baseTopK int,
	baseWeights GatewayAdvancedSchedulerScoreWeightsView,
	global advancedSchedulerRuntimeSettings,
	overrides GroupAdvancedSchedulerOverrides,
) advancedSchedulerEffectiveSettings {
	v := policy.ResolveEffective(baseTopK, policy.ScoreWeights(baseWeights), policy.RuntimeSettings{
		StickyWeightedEnabled: global.stickyWeightedEnabled, SubscriptionPriorityEnabled: global.subscriptionPriorityEnabled, LbTopKOverride: global.lbTopKOverride, WeightOverrides: global.weightOverrides,
		EwmaErrorRateAlpha: global.ewmaErrorRateAlpha, EwmaTTFTAlpha: global.ewmaTTFTAlpha,
		StickyEscapeEnabled: global.stickyEscapeEnabled, StickyEscapeTTFTMs: global.stickyEscapeTTFTMs, StickyEscapeErrorRate: global.stickyEscapeErrorRate,
	}, overrides)
	return advancedSchedulerEffectiveSettings{
		stickyWeightedEnabled: v.StickyWeightedEnabled, subscriptionPriorityEnabled: v.SubscriptionPriorityEnabled, topK: v.TopK, weights: GatewayAdvancedSchedulerScoreWeightsView(v.Weights),
		feedback: advancedSchedulerFeedbackConfig{errorRateAlpha: v.Feedback.ErrorRateAlpha, ttftAlpha: v.Feedback.TtftAlpha}, stickyEscape: advancedStickyEscapeConfig{enabled: v.StickyEscape.Enabled, ttftMs: v.StickyEscape.TtftMs, errorRate: v.StickyEscape.ErrorRate},
	}
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
