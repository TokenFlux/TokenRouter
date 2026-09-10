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
		{PlatformAnthropic, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []GroupClientProtocol{ProtocolAnthropicMessages}},
		{PlatformOpenAI, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolEmbeddings, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolResponsesWebSocket, ProtocolLive, ProtocolResponsesCompact, ProtocolAlphaSearch}, []GroupClientProtocol{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}},
		{PlatformGemini, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolGeminiGenerateContent, ProtocolImageBatches}, []GroupClientProtocol{ProtocolGeminiGenerateContent}},
		{PlatformAntigravity, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolGeminiGenerateContent}, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolGeminiGenerateContent}},
		{PlatformQoder, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []GroupClientProtocol{}},
		{PlatformGrok, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolVideosGenerations, ProtocolVideosEdits, ProtocolVideosExtensions, ProtocolTTS, ProtocolSTT, ProtocolCustomVoices, ProtocolVoiceRealtime, ProtocolResponsesWebSocket, ProtocolResponsesCompact, ProtocolWebSearch, ProtocolXSearch}, []GroupClientProtocol{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, "openai_images_generations", "openai_images_edits"}},
		{PlatformKimi, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}},
		{PlatformZhipu, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}},
		{PlatformDeepseek, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}},
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
		ProtocolOpenAIChatCompletions,
		ProtocolAnthropicMessages,
		ProtocolOpenAIResponses,
	})
	require.NoError(t, err)
	require.Equal(t, []GroupClientProtocol{
		ProtocolAnthropicMessages,
		ProtocolOpenAIResponses,
		ProtocolOpenAIChatCompletions,
	}, validated)

	emptyOpenAI, err := ValidateGroupClientProtocols(PlatformOpenAI, []GroupClientProtocol{})
	require.NoError(t, err)
	require.NotNil(t, emptyOpenAI)
	_, err = ValidateGroupClientProtocols(PlatformAnthropic, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolGeminiGenerateContent})
	require.ErrorContains(t, err, "not supported")
	_, err = ValidateGroupClientProtocols(PlatformQoder, []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolAnthropicMessages})
	require.ErrorContains(t, err, "duplicated")
	_, err = ValidateGroupClientProtocols(PlatformQoder, []GroupClientProtocol{"unknown"})
	require.ErrorContains(t, err, "unknown protocol")

	empty, err := ValidateGroupClientProtocols(PlatformQoder, []GroupClientProtocol{})
	require.NoError(t, err)
	require.NotNil(t, empty)
}
