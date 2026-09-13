package routing

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 每个候选独立验证启用集合，旧计划不受之后的配置或返回切片修改影响。
func TestRoutePlanCandidateRecalculationAndIsolation(t *testing.T) {
	group := &Group{ID: 7, Platform: capability.PlatformOpenAI, SchedulerType: GroupSchedulerTypeAdvanced,
		AllowedProtocols:  []capability.ProtocolID{capability.ProtocolAnthropicMessages},
		ProtocolFallbacks: map[capability.ProtocolID]capability.ProtocolID{capability.ProtocolAnthropicMessages: capability.ProtocolOpenAIResponses}}
	plan := Plan(PlanInput{Group: group, ClientProtocol: capability.ProtocolAnthropicMessages})
	responses := account.AccountSnapshot{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, EnabledProtocols: []capability.ProtocolID{capability.ProtocolOpenAIResponses}}
	chat := account.AccountSnapshot{ID: 2, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, EnabledProtocols: []capability.ProtocolID{capability.ProtocolOpenAIChatCompletions}}
	first, ok := plan.ResolveCandidate(responses)
	require.True(t, ok)
	require.Equal(t, int64(1), first.AccountID)
	require.Equal(t, capability.ProtocolOpenAIResponses, first.UpstreamProtocol)
	_, ok = plan.ResolveCandidate(chat)
	require.False(t, ok)
	group.ProtocolFallbacks[capability.ProtocolAnthropicMessages] = capability.ProtocolOpenAIChatCompletions
	plan.AllowedProtocols()[0] = capability.ProtocolLive
	again, ok := plan.ResolveCandidate(responses)
	require.True(t, ok)
	require.Equal(t, first, again)
	fresh := Plan(PlanInput{Group: group, ClientProtocol: capability.ProtocolAnthropicMessages})
	second, ok := fresh.ResolveCandidate(chat)
	require.True(t, ok)
	require.Equal(t, int64(2), second.AccountID)
	require.Equal(t, capability.ProtocolOpenAIChatCompletions, second.UpstreamProtocol)
	require.Equal(t, GroupSchedulerTypeAdvanced, plan.SchedulerType())
	require.Equal(t, capability.PlatformOpenAI, plan.Platform())
	require.Equal(t, int64(7), plan.GroupID())
	require.Equal(t, []capability.ProtocolID{capability.ProtocolAnthropicMessages}, plan.AllowedProtocols())
}

// 模型链的客户端/Key/渠道事实固定，但账号映射在每次匹配时读取独立快照。
func TestRoutePlanModelChainAndAttemptSnapshots(t *testing.T) {
	groupID := int64(7)
	mapping := ChannelMappingResult{MappedModel: "channel-model", ChannelID: 9, Mapped: true, BillingModelSource: "requested", ClientModel: "prefix/client-model", APIKeyRedirected: true}
	plan := Plan(PlanInput{GroupID: &groupID, RequestedModel: "key-model", Channel: mapping, ClientProtocol: capability.ProtocolOpenAIResponses})
	groupID = 8
	require.Equal(t, int64(7), plan.GroupID())
	require.Equal(t, mapping, plan.Mapping())
	require.Equal(t, "prefix/client-model", plan.Models().ClientModel)
	require.Equal(t, "key-model", plan.Models().RequestedModel)
	candidate, ok := plan.ResolveCandidate(account.AccountSnapshot{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, EnabledProtocols: []capability.ProtocolID{capability.ProtocolOpenAIResponses}})
	require.True(t, ok)
	rules := map[string]string{"channel-model": "upstream-one", "upstream-one": "must-not-recurse"}
	snapshot := account.AccountSnapshot{ID: 1, ModelPolicy: account.NewModelRoutingSnapshot(capability.PlatformOpenAI, rules)}
	rules["channel-model"] = "upstream-two"
	first, matched := candidate.ResolveModel(snapshot, "channel-model")
	require.True(t, matched)
	require.Equal(t, "upstream-one", first.Models.AccountMappedModel)
	require.Empty(t, candidate.Models.AccountMappedModel)
	require.Empty(t, plan.Models().AccountMappedModel)
	fresh := account.AccountSnapshot{ID: 1, ModelPolicy: account.NewModelRoutingSnapshot(capability.PlatformOpenAI, rules)}
	second, matched := candidate.ResolveModel(fresh, "channel-model")
	require.True(t, matched)
	require.Equal(t, "upstream-two", second.Models.AccountMappedModel)
}
