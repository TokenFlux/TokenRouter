package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupClientProtocolMatrix(t *testing.T) {
	tests := []struct {
		platform  string
		supported []GroupClientProtocol
		defaults  []GroupClientProtocol
	}{
		{PlatformAnthropic, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions}, []GroupClientProtocol{GroupClientProtocolAnthropicMessages}},
		{PlatformOpenAI, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions, ProtocolEmbeddings, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolResponsesWebSocket, ProtocolLive, ProtocolResponsesCompact, ProtocolAlphaSearch}, []GroupClientProtocol{GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions}},
		{PlatformGemini, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions, GroupClientProtocolGeminiGenerateContent, ProtocolImageBatches}, []GroupClientProtocol{GroupClientProtocolGeminiGenerateContent}},
		{PlatformAntigravity, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions, GroupClientProtocolGeminiGenerateContent}, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolGeminiGenerateContent}},
		{PlatformQoder, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions}, []GroupClientProtocol{}},
		{PlatformGrok, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolVideosGenerations, ProtocolVideosEdits, ProtocolVideosExtensions, ProtocolTTS, ProtocolSTT, ProtocolCustomVoices, ProtocolVoiceRealtime, ProtocolResponsesWebSocket, ProtocolResponsesCompact, ProtocolWebSearch, ProtocolXSearch}, []GroupClientProtocol{GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions, "openai_images_generations", "openai_images_edits"}},
		{PlatformKimi, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions}, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions}},
		{PlatformZhipu, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions}, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions}},
		{PlatformDeepseek, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions}, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolOpenAIResponses, GroupClientProtocolOpenAIChatCompletions}},
	}
	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			require.Equal(t, tt.supported, SupportedGroupClientProtocols(tt.platform))
			require.Equal(t, tt.defaults, DefaultGroupClientProtocols(tt.platform))
		})
	}
}

func TestValidateGroupClientProtocols(t *testing.T) {
	validated, err := ValidateGroupClientProtocols(PlatformOpenAI, []GroupClientProtocol{
		GroupClientProtocolOpenAIChatCompletions,
		GroupClientProtocolAnthropicMessages,
		GroupClientProtocolOpenAIResponses,
	})
	require.NoError(t, err)
	require.Equal(t, []GroupClientProtocol{
		GroupClientProtocolAnthropicMessages,
		GroupClientProtocolOpenAIResponses,
		GroupClientProtocolOpenAIChatCompletions,
	}, validated)

	emptyOpenAI, err := ValidateGroupClientProtocols(PlatformOpenAI, []GroupClientProtocol{})
	require.NoError(t, err)
	require.NotNil(t, emptyOpenAI)
	_, err = ValidateGroupClientProtocols(PlatformAnthropic, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolGeminiGenerateContent})
	require.ErrorContains(t, err, "not supported")
	_, err = ValidateGroupClientProtocols(PlatformQoder, []GroupClientProtocol{GroupClientProtocolAnthropicMessages, GroupClientProtocolAnthropicMessages})
	require.ErrorContains(t, err, "duplicated")
	_, err = ValidateGroupClientProtocols(PlatformQoder, []GroupClientProtocol{"unknown"})
	require.ErrorContains(t, err, "unknown protocol")

	empty, err := ValidateGroupClientProtocols(PlatformQoder, []GroupClientProtocol{})
	require.NoError(t, err)
	require.NotNil(t, empty)
}
