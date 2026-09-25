package selection

import (
	"context"

	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// advancedSchedulerEffectiveSettingsForGroup 将分组覆盖置于运行时全局设置之上。
func (s *Compatible) advancedSchedulerEffectiveSettingsForGroup(ctx context.Context, group *routing.Group) policy.EffectiveSettings {
	var parameters *schedulercore.Parameters
	if s != nil {
		parameters = s.schedulerParameters
	}
	return parameters.Effective(ctx, schedulerGroupOverrides(group))
}

// advancedSchedulerEffectiveSettingsForRequest 读取最终目标分组并生成请求级有效配置。
// 分组不存在或未被加载时只使用全局配置，保持无分组路径的历史行为。
func (s *Compatible) advancedSchedulerEffectiveSettingsForRequest(
	ctx context.Context,
	groupID *int64,
) policy.EffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	return s.advancedSchedulerEffectiveSettingsForGroup(ctx, s.advancedSchedulerGroupForRequest(ctx, groupID))
}

// advancedSchedulerGroupForRequest 优先复用请求上下文的最终分组，必要时再读取调度快照。
func (s *Compatible) advancedSchedulerGroupForRequest(ctx context.Context, id *int64) *routing.Group {
	return schedulerRequestGroup(ctx, id, s != nil && s.schedulerSnapshot != nil, s.readSchedulingGroup)
}

// schedulerGroupOverrides 只投影最终高级分组，基础分组忽略其配置残留。
func schedulerGroupOverrides(group *routing.Group) policy.GroupAdvancedSchedulerOverrides {
	if group != nil && group.UsesAdvancedScheduler() {
		return group.AdvancedSchedulerOverrides
	}
	return policy.GroupAdvancedSchedulerOverrides{}
}

// schedulerRequestGroup 保留请求优先和仅有快照时回源的顺序。
func schedulerRequestGroup(ctx context.Context, id *int64, snapshotAvailable bool, read func(context.Context, int64) (*routing.Group, error)) *routing.Group {
	if id == nil || *id <= 0 {
		return nil
	}
	if ctx != nil {
		if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == *id {
			return group
		}
	}
	if !snapshotAvailable {
		return nil
	}
	group, err := read(ctx, *id)
	if err != nil {
		return nil
	}
	return group
}
