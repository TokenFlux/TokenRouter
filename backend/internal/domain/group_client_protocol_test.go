package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupClientProtocolMatrix(t *testing.T) {
	tests := []struct {
		platform  string
		supported []ProtocolID
		defaults  []ProtocolID
	}{
		{PlatformAnthropic, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []ProtocolID{ProtocolAnthropicMessages}},
		{PlatformOpenAI, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolEmbeddings, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolResponsesWebSocket, ProtocolLive, ProtocolResponsesCompact, ProtocolAlphaSearch}, []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}},
		{PlatformGemini, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolGeminiGenerateContent, ProtocolImageBatches}, []ProtocolID{ProtocolGeminiGenerateContent}},
		{PlatformAntigravity, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolGeminiGenerateContent}, []ProtocolID{ProtocolAnthropicMessages, ProtocolGeminiGenerateContent}},
		{PlatformQoder, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []ProtocolID{}},
		{PlatformGrok, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolImagesGenerations, ProtocolImagesEdits, ProtocolVideosGenerations, ProtocolVideosEdits, ProtocolVideosExtensions, ProtocolTTS, ProtocolSTT, ProtocolCustomVoices, ProtocolVoiceRealtime, ProtocolResponsesWebSocket, ProtocolResponsesCompact, ProtocolWebSearch, ProtocolXSearch}, []ProtocolID{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, "openai_images_generations", "openai_images_edits"}},
		{PlatformKimi, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}},
		{PlatformZhipu, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}},
		{PlatformDeepseek, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}, []ProtocolID{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}},
	}
	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			require.Equal(t, tt.supported, SupportedGroupClientProtocols(tt.platform))
			require.Equal(t, tt.defaults, DefaultGroupClientProtocols(tt.platform))
		})
	}
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
	require.ErrorContains(t, err, "not supported")
	_, err = ValidateGroupClientProtocols(PlatformQoder, []ProtocolID{ProtocolAnthropicMessages, ProtocolAnthropicMessages})
	require.ErrorContains(t, err, "duplicated")
	_, err = ValidateGroupClientProtocols(PlatformQoder, []ProtocolID{"unknown"})
	require.ErrorContains(t, err, "unknown protocol")

	empty, err := ValidateGroupClientProtocols(PlatformQoder, []ProtocolID{})
	require.NoError(t, err)
	require.NotNil(t, empty)
}
