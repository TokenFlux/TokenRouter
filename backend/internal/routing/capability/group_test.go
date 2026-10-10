package capability

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGroupClientProtocolMatrix 验证分组协议集合独立于提供商平台，默认只开放三个文本入口。
func TestGroupClientProtocolMatrix(t *testing.T) {
	supported := SupportedGroupClientProtocols("")
	require.Len(t, supported, 22)
	require.Contains(t, supported, ProtocolImageBatches)
	require.Contains(t, supported, ProtocolVoiceRealtime)
	require.NotContains(t, supported, ProtocolQoderChat)
	require.Equal(t, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, DefaultGroupClientProtocols(""))
	require.Empty(t, DefaultProtocolFallbacks(""))
	supported[0] = ProtocolLive
	require.Equal(t, ProtocolAnthropicMessages, SupportedGroupClientProtocols("")[0])
}

func TestValidateGroupClientProtocols(t *testing.T) {
	validated, err := ValidateGroupClientProtocols(PlatformOpenAI, []ProtocolID{
		ProtocolOpenAIChatCompletions,
		ProtocolAnthropicMessages,
		ProtocolOpenAIResponses,
	})
	require.NoError(t, err)
	require.Equal(t, []ProtocolID{
		ProtocolAnthropicMessages,
		ProtocolOpenAIResponses,
		ProtocolOpenAIChatCompletions,
	}, validated)

	emptyOpenAI, err := ValidateGroupClientProtocols(PlatformOpenAI, []ProtocolID{})
	require.NoError(t, err)
	require.NotNil(t, emptyOpenAI)
	_, err = ValidateGroupClientProtocols(PlatformAnthropic, []ProtocolID{ProtocolAnthropicMessages, ProtocolGeminiGenerateContent})
	require.NoError(t, err)
	_, err = ValidateGroupClientProtocols(PlatformQoder, []ProtocolID{ProtocolAnthropicMessages, ProtocolAnthropicMessages})
	require.ErrorContains(t, err, "duplicated")
	_, err = ValidateGroupClientProtocols(PlatformQoder, []ProtocolID{"unknown"})
	require.ErrorContains(t, err, "unknown protocol")

	empty, err := ValidateGroupClientProtocols(PlatformQoder, []ProtocolID{})
	require.NoError(t, err)
	require.NotNil(t, empty)
}
