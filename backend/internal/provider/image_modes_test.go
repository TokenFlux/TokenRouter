package provider

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// image_modes_test.go verifies provider image transport configuration and GIF
// eligibility. The tests distinguish missing settings from explicit overrides
// and reject unsupported combinations before those values can be used by
// native image execution.

// TestImageStreamModeDefaultsAndValidation checks older records and independent
// endpoint selections. Invalid stored values and incompatible provider types
// must fail the shared save normalization used by every management write.
func TestImageStreamModeDefaultsAndValidation(t *testing.T) {
	value := &Record{Platform: PlatformOpenAI, Type: ProviderTypeAPIKey, Credentials: map[string]any{}}
	mode, err := value.ImageStreamMode(protocol.ProtocolImagesGenerations)
	require.NoError(t, err)
	require.Equal(t, protocol.ImageModeBoth, mode)
	for _, key := range []string{ImagesGenerationsModeKey, ImagesEditsModeKey} {
		for _, raw := range []any{"both", "streaming", "non_streaming", "", "auto", nil, true, []string{"streaming"}} {
			t.Run(key, func(t *testing.T) {
				candidate := &Record{Platform: PlatformOpenAI, Type: ProviderTypeAPIKey, Credentials: map[string]any{key: raw, UpstreamProtocolsKey: []string{}}}
				err := NormalizeProviderProtocols(candidate)
				text, ok := raw.(string)
				valid := ok && protocol.ImageStreamMode(text).Valid()
				if valid {
					require.NoError(t, err)
					require.Empty(t, candidate.UpstreamProtocols(), "mode settings cannot enable a protocol")
				} else {
					require.ErrorContains(t, err, key)
				}
			})
		}
	}
	value.Credentials = map[string]any{ImagesGenerationsModeKey: "non_streaming", ImagesEditsModeKey: "streaming"}
	generation, err := value.ImageStreamMode(protocol.ProtocolImagesGenerations)
	require.NoError(t, err)
	edit, err := value.ImageStreamMode(protocol.ProtocolImagesEdits)
	require.NoError(t, err)
	require.False(t, generation.UpstreamStream(true))
	require.True(t, edit.UpstreamStream(false))
	value.Type = ProviderTypeOAuth
	require.ErrorContains(t, NormalizeProviderProtocols(value), "OpenAI API key")
	value.Type = ProviderTypeAPIKey
	value.Platform = PlatformGrok
	require.ErrorContains(t, NormalizeProviderProtocols(value), "OpenAI API key")
}

// TestSupportsImageGIF separates provider eligibility from model format support.
// Missing flags enable OAuth and API keys, but explicit false and streaming-only
// Images endpoints must reject animation selection without affecting ordinary images.
func TestSupportsImageGIF(t *testing.T) {
	for _, kind := range []string{ProviderTypeOAuth, ProviderTypeAPIKey} {
		record := &Record{Platform: PlatformOpenAI, Type: kind, Credentials: map[string]any{}}
		require.True(t, record.SupportsImageGIF(protocol.ProtocolImagesGenerations))
		record.Extra = map[string]any{ImagesGIFEnabledKey: false}
		require.False(t, record.SupportsImageGIF(protocol.ProtocolImagesGenerations))
		require.NoError(t, validateImageStreamModes(record))
		record.Extra[ImagesGIFEnabledKey] = "false"
		require.Error(t, validateImageStreamModes(record))
	}
	record := &Record{Platform: PlatformOpenAI, Type: ProviderTypeAPIKey, Credentials: map[string]any{ImagesGenerationsModeKey: "streaming"}}
	require.False(t, record.SupportsImageGIF(protocol.ProtocolImagesGenerations))
	require.True(t, record.SupportsImageGIF(protocol.ProtocolImagesEdits))
	require.True(t, record.SupportsImageGIF(protocol.ProtocolOpenAIResponses))
}
