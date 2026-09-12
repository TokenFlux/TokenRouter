package apikey

import (
	"maps"
	"slices"
)

// clonePointer 隔离请求对标量与时间指针的修改，保留未设置状态。
func clonePointer[T any](v *T) *T {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// cloneModelRouting 同时复制映射和候选账号切片，不折叠 nil/空集合。
func cloneModelRouting(v map[string][]int64) map[string][]int64 {
	if v == nil {
		return nil
	}
	out := make(map[string][]int64, len(v))
	for k, ids := range v {
		out[k] = slices.Clone(ids)
	}
	return out
}
func cloneMessagesDispatch(v OpenAIMessagesDispatchModelConfig) OpenAIMessagesDispatchModelConfig {
	v.ExactModelMappings = maps.Clone(v.ExactModelMappings)
	return v
}
func cloneModelsList(v GroupModelsListConfig) GroupModelsListConfig {
	v.Models = slices.Clone(v.Models)
	return v
}

// cloneMembership 不允许请求中的窗口或成员修改影响认证缓存。
func cloneMembership(v *TeamMembership) *TeamMembership {
	if v == nil {
		return nil
	}
	out := *v
	out.DailyWindowStart = clonePointer(v.DailyWindowStart)
	out.WeeklyWindowStart = clonePointer(v.WeeklyWindowStart)
	out.MonthlyWindowStart = clonePointer(v.MonthlyWindowStart)
	out.LastActiveAt = clonePointer(v.LastActiveAt)
	return &out
}
