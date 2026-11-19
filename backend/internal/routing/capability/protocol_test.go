package capability

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// protocol_test.go verifies platform fallback target sets and deterministic
// automatic conversion ordering across client entry protocols.

// TestProtocolFallbackTargetsExcludesUnimplementedAnthropicGemini checks text
// fallback choices for Anthropic and Gemini platforms. Anthropic never lists
// Gemini GenerateContent as an implemented fallback target, while mixed-group
// automatic fallback ordering retains Gemini after OpenAI and Anthropic.
func TestProtocolFallbackTargetsExcludesUnimplementedAnthropicGemini(t *testing.T) {
	require.Empty(t, ProtocolFallbackTargets(PlatformAnthropic, ProtocolAnthropicMessages))
	require.Equal(t, []ProtocolID{ProtocolAnthropicMessages}, ProtocolFallbackTargets(PlatformAnthropic, ProtocolOpenAIResponses))
	require.Equal(t, []ProtocolID{ProtocolAnthropicMessages}, ProtocolFallbackTargets(PlatformAnthropic, ProtocolOpenAIChatCompletions))
	require.False(t, SupportsProtocolConversion(PlatformAnthropic, ProviderTypeAPIKey, "", ProtocolOpenAIResponses, ProtocolGeminiGenerateContent))

	require.Equal(t, []ProtocolID{
		ProtocolOpenAIResponses,
		ProtocolOpenAIChatCompletions,
		ProtocolGeminiGenerateContent,
		ProtocolQoderChat,
	}, AutomaticProtocolFallbackTargets(ProtocolAnthropicMessages))
	require.Equal(t, []ProtocolID{
		ProtocolOpenAIChatCompletions,
		ProtocolAnthropicMessages,
		ProtocolGeminiGenerateContent,
		ProtocolQoderChat,
	}, AutomaticProtocolFallbackTargets(ProtocolOpenAIResponses))
}
