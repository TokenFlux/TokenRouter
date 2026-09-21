package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 当前请求计划不能污染共享账号；模型读取仍按每次原匹配时机获得最新配置。
func TestS06RoutePlanRebuildsCandidateAndKeepsModelReadTiming(t *testing.T) {
	group := &routing.Group{ID: 7, Platform: capability.PlatformOpenAI, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}, ProtocolFallbacks: map[protocol.ProtocolID]protocol.ProtocolID{protocol.ProtocolAnthropicMessages: protocol.ProtocolOpenAIResponses}}
	ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), protocol.ProtocolAnthropicMessages)
	mapping := routing.ChannelMappingResult{MappedModel: "channel-model", Mapped: true, ChannelID: 9, BillingModelSource: "requested"}
	plan := routePlanForMapping(ctx, group, &group.ID, "key-model", mapping)
	ctx = requeststate.WithRoutePlan(ctx, plan)
	shared := &Account{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{account.UpstreamProtocolsKey: []string{"openai_responses"}, "model_mapping": map[string]any{"channel-model": "first"}}}
	first, err := accountForProtocolAttempt(ctx, shared)
	require.NoError(t, err)
	require.NotSame(t, shared, first)
	_, captured := shared.attemptRoute.Candidate()
	require.False(t, captured)
	_, captured = first.attemptRoute.Candidate()
	require.True(t, captured)
	require.Equal(t, "first", first.GetMappedModel("channel-model"))
	first.Credentials = map[string]any{account.UpstreamProtocolsKey: []string{"openai_responses"}, "model_mapping": map[string]any{"channel-model": "latest"}}
	require.Equal(t, "latest", first.GetMappedModel("channel-model"))
	require.Equal(t, "first", shared.GetMappedModel("channel-model"))
	first.Credentials = map[string]any{account.UpstreamProtocolsKey: []string{"openai_chat_completions"}}
	_, err = accountForProtocolAttempt(ctx, first)
	require.Error(t, err, "已有尝试副本也须复核 fresh 能力")
}
