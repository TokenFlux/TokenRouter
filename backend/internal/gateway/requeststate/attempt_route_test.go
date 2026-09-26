package requeststate

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 请求快照断言：缺失值不补造，已捕获计划不被后续修改污染。
func TestCapturedCandidatePlanDoesNotResolveMissingOrChangedCandidate(t *testing.T) {
	var attempt AttemptRoute
	_, ok := attempt.Candidate()
	require.False(t, ok)
	attempt = AttemptRoute{candidate: routing.CandidatePlan{AccountID: 3, GroupID: 7}, planned: true}
	captured, ok := attempt.Candidate()
	require.True(t, ok)
	attempt.candidate.GroupID = 8
	attempt = AttemptRoute{}
	_, ok = attempt.Candidate()
	require.False(t, ok)
	require.Equal(t, int64(7), captured.GroupID)
	require.Equal(t, int64(3), captured.AccountID)
}

// 能力每次复核，模型映射仍由实际匹配时传入，不绑定过早的配置副本。
func TestAttemptRouteRechecksCapabilitiesAndReadsCurrentMapping(t *testing.T) {
	group := &routing.Group{ID: 7, Platform: capability.PlatformOpenAI, ProtocolFallbacks: map[protocol.ProtocolID]protocol.ProtocolID{protocol.ProtocolAnthropicMessages: protocol.ProtocolOpenAIResponses}}
	ctx := WithClientProtocol(WithGroup(context.Background(), group), protocol.ProtocolAnthropicMessages)
	ctx = WithRoutePlan(ctx, routing.Plan(routing.PlanInput{Group: group, ClientProtocol: protocol.ProtocolAnthropicMessages}))
	state := RoutingStateFromContext(ctx)
	record := &account.Record{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{account.UpstreamProtocolsKey: []string{"openai_responses"}}}
	attempt, resolved, err := state.ResolveAttempt(record.RoutingSnapshot(), AttemptRoute{})
	require.NoError(t, err)
	require.True(t, resolved)
	require.Equal(t, protocol.ProtocolOpenAIResponses, attempt.Protocol())
	for _, target := range []string{"first", "latest"} {
		model, matched := attempt.ResolveModel(1, record.Platform, map[string]string{"group-model": target}, "group-model")
		require.True(t, matched)
		require.Equal(t, target, model)
	}
	record.Credentials[account.UpstreamProtocolsKey] = []string{"openai_chat_completions"}
	_, _, err = state.ResolveAttempt(record.RoutingSnapshot(), attempt)
	require.Error(t, err)
	retained, resolved, err := (RoutingState{}).ResolveAttempt(record.RoutingSnapshot(), attempt)
	require.NoError(t, err)
	require.False(t, resolved)
	require.Equal(t, attempt, retained)
}
