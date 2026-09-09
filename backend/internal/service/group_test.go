//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGroupClientProtocolsDoNotRecoverLegacyPolicy 锁定空集合即全部禁用，不读取旧开关恢复协议。
func TestGroupClientProtocolsDoNotRecoverLegacyPolicy(t *testing.T) {
	group := &Group{
		Platform:              PlatformOpenAI,
		AllowMessagesDispatch: true,
	}

	require.NotNil(t, group.EffectiveAllowedClientProtocols())
	require.Empty(t, group.EffectiveAllowedClientProtocols())
	require.False(t, group.AllowsClientProtocol(GroupClientProtocolAnthropicMessages))
}
