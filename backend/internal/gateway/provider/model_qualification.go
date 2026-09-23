package provider

import (
	"context"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// CandidateSnapshot 只返回原候选字段，当次协议覆盖不写回账号记录。
func (p ModelPolicy) CandidateSnapshot() account.AccountSnapshot {
	if p.Record == nil {
		return account.AccountSnapshot{}
	}
	snapshot := p.Record.RoutingSnapshot()
	snapshot.EnabledProtocols = slices.Clone(p.Record.UpstreamProtocolsForLegacy(p.protocolTarget().GetAPIProtocol()))
	return snapshot
}

// ProtocolRoute 保留单次候选资格复核及原分组投影。
func (p ModelPolicy) ProtocolRoute(group *routing.Group, source protocol.ProtocolID) (protocol.ProtocolID, bool) {
	if p.Record == nil {
		return "", false
	}
	var projected *routing.Group
	if group != nil {
		projected = &routing.Group{ID: group.ID, Platform: group.Platform, SchedulerType: group.SchedulerType, AllowedProtocols: group.AllowedProtocols, ProtocolFallbacks: group.ProtocolFallbacks}
	}
	plan := routing.Plan(routing.PlanInput{Group: projected, ClientProtocol: source})
	candidate, ok := plan.ResolveCandidate(p.CandidateSnapshot())
	return candidate.UpstreamProtocol, ok
}

// AllowsProtocol 在原调用点读取请求路线，不提前读取模型目录。
func (p ModelPolicy) AllowsProtocol(ctx context.Context) bool {
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	if source == "" {
		return true
	}
	group, _ := requeststate.GroupFromContext(ctx)
	_, ok := p.ProtocolRoute(group, source)
	return ok
}

// Schedulable 保留协议、账号状态、模型窗口的原检查顺序。
func (p ModelPolicy) Schedulable(ctx context.Context, model string) bool {
	if p.Record == nil {
		return false
	}
	if !p.AllowsProtocol(ctx) || !p.Record.IsSchedulable() {
		return false
	}
	return p.AllowsModel(ctx, model)
}

// Limited 只报告模型窗口，不把超额消费许可混入原只读窗口判断。
func (p ModelPolicy) Limited(ctx context.Context, model string) bool {
	for _, key := range p.LimitKeys(ctx, model) {
		if p.Record.ModelRateLimitActive(key) {
			return true
		}
	}
	return false
}

// LimitRemaining 保留多范围最大剩余时间和每次窗口读取的原时钟。
func (p ModelPolicy) LimitRemaining(ctx context.Context, model string) time.Duration {
	remaining := time.Duration(0)
	for _, key := range p.LimitKeys(ctx, model) {
		if value := p.Record.ModelRateLimitRemaining(key); value > remaining {
			remaining = value
		}
	}
	return remaining
}

// FinalAntigravityModel 只提供显式模型和本次 thinking，规则复用账号平台适配。
func (p ModelPolicy) FinalAntigravityModel(ctx context.Context, model string) string {
	return accountprovider.FinalAntigravityModel(p.Record, model, modelThinking(ctx))
}
