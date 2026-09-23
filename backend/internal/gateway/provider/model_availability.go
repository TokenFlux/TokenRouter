package provider

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// AvailabilityAccounts 只读取持久配置候选，不使用瞬时调度缓存或执行凭据入口。
type AvailabilityAccounts interface {
	ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]account.Record, error)
}

// NewModelAvailability 固定查询和渠道来源；每次诊断仍按原时点读取，无独立缓存。
// @project-doc docs/architecture/account_scheduling_and_cache.md#advanced_scheduler_selection
func NewModelAvailability(source AvailabilityAccounts, channels *routing.ChannelService, simple, compatible bool) *routing.ModelAvailability {
	result := &routing.ModelAvailability{
		Simple:   simple,
		MapModel: channels.ResolveRoutingModel,
	}
	if source == nil {
		return result
	}
	result.Read = func(ctx context.Context, group *int64, platforms []string, grouped bool) ([]routing.AvailabilityAccount, error) {
		values, err := source.ListModelAvailabilityCandidates(ctx, group, platforms, grouped)
		if err != nil {
			return nil, err
		}
		out := make([]routing.AvailabilityAccount, len(values))
		for i := range values {
			record := &values[i]
			out[i] = routing.AvailabilityAccount{
				Platform:        record.Platform,
				MixedScheduling: record.IsMixedSchedulingEnabled(),
				Supports: func(ctx context.Context, model string) bool {
					policy := ModelPolicy{Record: record}
					if compatible {
						return policy.SupportsCompatibleRouting(ctx, model)
					}
					return policy.Supports(ctx, model)
				},
			}
		}
		return out, nil
	}
	return result
}

// SupportsCompatibleRouting 保留 HTTP 自动透传对模型检查的旁路，不改变普通账号一跳规则。
func (p ModelPolicy) SupportsCompatibleRouting(ctx context.Context, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return true
	}
	if p.Record == nil {
		return false
	}
	if requeststate.OpenAIHTTPPassthroughRoutingFromContext(ctx) && p.Record.IsOpenAIPassthroughEnabled() {
		return true
	}
	return p.Record.IsModelSupported(model, accountprovider.ModelDefaults(), accountprovider.ModelRules(p.Record))
}
