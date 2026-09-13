package routing

import (
	"maps"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// PlanInput 由最终分组确定后提供，不提前绑定账号或最终上游协议。
type PlanInput struct {
	GroupID        *int64
	RequestedModel string
	Channel        ChannelMappingResult
	Group          *Group
	ClientProtocol capability.ProtocolID
}

// RoutePlan 固化本次分组与协议策略；账号、模型重写及执行重试继续按原时序提供。
// 字段保持私有，调用方不能修改一次尝试使用的 fallback 映射。
type RoutePlan struct {
	models         ModelChain
	groupID        int64
	platform       string
	schedulerType  GroupSchedulerType
	clientProtocol capability.ProtocolID
	allowed        []capability.ProtocolID
	fallbacks      map[capability.ProtocolID]capability.ProtocolID
}

// Plan 复制已完成入口准入的分组投影，不改变原权限、模型或资金检查顺序。
func Plan(input PlanInput) RoutePlan {
	plan := RoutePlan{clientProtocol: input.ClientProtocol, models: ModelChain{RequestedModel: input.RequestedModel, ClientModel: input.Channel.ClientModel, APIKeyRedirected: input.Channel.APIKeyRedirected, ChannelModel: input.Channel.MappedModel, ChannelMapped: input.Channel.Mapped, ChannelID: input.Channel.ChannelID, BillingModelSource: input.Channel.BillingModelSource}}
	if input.Group != nil {
		plan.groupID = input.Group.ID
		plan.platform = input.Group.Platform
		plan.schedulerType = input.Group.SchedulerType
		plan.allowed = slices.Clone(input.Group.AllowedProtocols)
		plan.fallbacks = maps.Clone(input.Group.ProtocolFallbacks)
	}
	if input.GroupID != nil {
		plan.groupID = *input.GroupID
	}
	return plan
}

// CandidatePlan 是单个候选的当次结果，不写入账号或调度缓存。
type CandidatePlan struct {
	Models           ModelChain
	AccountID        int64
	GroupID          int64
	ClientProtocol   capability.ProtocolID
	UpstreamProtocol capability.ProtocolID
}

// ResolveCandidate 保留原生优先和单步转换，每次 fresh/数据库复核重新调用。
func (p RoutePlan) ResolveCandidate(candidate account.AccountSnapshot) (CandidatePlan, bool) {
	target, ok := capability.ResolveRoute(candidate.Protocols(), p.clientProtocol, p.fallbacks)
	if !ok {
		return CandidatePlan{}, false
	}
	return CandidatePlan{Models: p.models, AccountID: candidate.ID, GroupID: p.groupID, ClientProtocol: p.clientProtocol, UpstreamProtocol: target}, true
}

// GroupID、Platform 和 SchedulerType 返回本次最终分组值，不重新读取共享配置。
func (p RoutePlan) GroupID() int64                            { return p.groupID }
func (p RoutePlan) Platform() string                          { return p.platform }
func (p RoutePlan) SchedulerType() GroupSchedulerType         { return p.schedulerType }
func (p RoutePlan) AllowedProtocols() []capability.ProtocolID { return slices.Clone(p.allowed) }

// WithClientProtocol 使用执行层已确定的业务入口，保留计划其它不可变值。
func (p RoutePlan) WithClientProtocol(source capability.ProtocolID) RoutePlan {
	p.clientProtocol = source
	return p
}
