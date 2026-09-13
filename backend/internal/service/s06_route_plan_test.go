package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"testing"
)

// 当前请求计划不能污染共享账号；模型读取仍按每次原匹配时机获得最新配置。
func TestS06RoutePlanRebuildsCandidateAndKeepsModelReadTiming(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformOpenAI, AllowedProtocols: []domain.ProtocolID{domain.ProtocolAnthropicMessages}, ProtocolFallbacks: map[domain.ProtocolID]domain.ProtocolID{domain.ProtocolAnthropicMessages: domain.ProtocolOpenAIResponses}}
	ctx := WithClientProtocol(context.WithValue(context.Background(), ctxkey.Group, group), domain.ProtocolAnthropicMessages)
	mapping := ChannelMappingResult{MappedModel: "channel-model", Mapped: true, ChannelID: 9, BillingModelSource: "requested"}
	plan := routePlanForMapping(ctx, group, &group.ID, "key-model", mapping)
	ctx = WithRoutePlan(ctx, plan)
	shared := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{upstreamProtocolsKey: []string{"openai_responses"}, "model_mapping": map[string]any{"channel-model": "first"}}}
	first, err := accountForProtocolAttempt(ctx, shared)
	require.NoError(t, err)
	require.NotSame(t, shared, first)
	require.Nil(t, shared.resolvedCandidate)
	require.NotNil(t, first.resolvedCandidate)
	require.Equal(t, "first", first.GetMappedModel("channel-model"))
	first.Credentials = map[string]any{upstreamProtocolsKey: []string{"openai_responses"}, "model_mapping": map[string]any{"channel-model": "latest"}}
	require.Equal(t, "latest", first.GetMappedModel("channel-model"))
	require.Equal(t, "first", shared.GetMappedModel("channel-model"))
	first.Credentials = map[string]any{upstreamProtocolsKey: []string{"openai_chat_completions"}}
	_, err = accountForProtocolAttempt(ctx, first)
	require.Error(t, err, "已有尝试副本也须复核 fresh 能力")
}
