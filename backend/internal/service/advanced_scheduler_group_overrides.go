package service

import (
	context "context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
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

func validateAdvancedSchedulerEffectiveWeights(weights GatewayAdvancedSchedulerScoreWeightsView) error {
	return policy.ValidateEffectiveWeights(policy.ScoreWeights(weights))
}

func resolveAdvancedSchedulerEffectiveSettings(
	baseTopK int,
	baseWeights GatewayAdvancedSchedulerScoreWeightsView,
	global advancedSchedulerRuntimeSettings,
	overrides routing.GroupAdvancedSchedulerOverrides,
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
	group *routing.Group,
) advancedSchedulerEffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	var overrides routing.GroupAdvancedSchedulerOverrides
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
func (s *OpenAIGatewayService) advancedSchedulerGroupForRequest(ctx context.Context, groupID *int64) *routing.Group {
	if groupID == nil || *groupID <= 0 {
		return nil
	}
	if ctx != nil {
		if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == *groupID {
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
