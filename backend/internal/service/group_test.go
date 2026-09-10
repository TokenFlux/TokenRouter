//go:build unit

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGroupClientProtocolsDoNotRecoverLegacyPolicy 锁定空集合即全部禁用，不读取旧开关恢复协议。
func TestGroupClientProtocolsDoNotRecoverLegacyPolicy(t *testing.T) {
	group := &Group{
		Platform:              PlatformOpenAI,
		AllowMessagesDispatch: true,
	}

	require.NotNil(t, group.EffectiveAllowedProtocols())
	require.Empty(t, group.EffectiveAllowedProtocols())
	require.False(t, group.AllowsClientProtocol(domain.ProtocolAnthropicMessages))
}

// 查询直接读取集合，响应快照仍隔离可变数据。
func TestGroupProtocolMembershipAndSnapshotIsolation(t *testing.T) {
	var missing *Group
	require.False(t, missing.AllowsClientProtocol(domain.ProtocolOpenAIResponses))
	group := &Group{AllowedProtocols: []domain.ProtocolID{domain.ProtocolOpenAIResponses}}
	require.True(t, group.AllowsClientProtocol(domain.ProtocolOpenAIResponses))
	require.False(t, group.AllowsClientProtocol(domain.ProtocolOpenAIChatCompletions))
	copy := group.EffectiveAllowedProtocols()
	copy[0] = domain.ProtocolOpenAIChatCompletions
	require.True(t, group.AllowsClientProtocol(domain.ProtocolOpenAIResponses))
	require.Zero(t, testing.AllocsPerRun(100, func() {
		if !group.AllowsClientProtocol(domain.ProtocolOpenAIResponses) {
			panic("unexpected protocol membership")
		}
	}))
}
