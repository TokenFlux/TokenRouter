//go:build unit

package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestGroupClientProtocolsDoNotRecoverLegacyPolicy 锁定空集合即全部禁用，不读取旧开关恢复协议。
func TestGroupClientProtocolsDoNotRecoverLegacyPolicy(t *testing.T) {
	group := &routing.Group{
		Platform:              capability.PlatformOpenAI,
		AllowMessagesDispatch: true,
	}

	require.NotNil(t, group.EffectiveAllowedProtocols())
	require.Empty(t, group.EffectiveAllowedProtocols())
	require.False(t, group.AllowsClientProtocol(protocol.ProtocolAnthropicMessages))
}

// 查询直接读取集合，响应快照仍隔离可变数据。
func TestGroupProtocolMembershipAndSnapshotIsolation(t *testing.T) {
	var missing *routing.Group
	require.False(t, missing.AllowsClientProtocol(protocol.ProtocolOpenAIResponses))
	group := &routing.Group{AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}}
	require.True(t, group.AllowsClientProtocol(protocol.ProtocolOpenAIResponses))
	require.False(t, group.AllowsClientProtocol(protocol.ProtocolOpenAIChatCompletions))
	copy := group.EffectiveAllowedProtocols()
	copy[0] = protocol.ProtocolOpenAIChatCompletions
	require.True(t, group.AllowsClientProtocol(protocol.ProtocolOpenAIResponses))
	require.Zero(t, testing.AllocsPerRun(100, func() {
		if !group.AllowsClientProtocol(protocol.ProtocolOpenAIResponses) {
			panic("unexpected protocol membership")
		}
	}))
}
