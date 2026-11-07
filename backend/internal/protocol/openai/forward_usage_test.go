package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCopyOpenAIUsageFromResponsesUsageTrustsCanonicalCacheCreationValue
// supplies a zero canonical cache-write count beside nonzero nested detail.
// Copying must retain that canonical zero and the supplied input/cache-read
// counts, including the nested cached-token observation.
func TestCopyOpenAIUsageFromResponsesUsageTrustsCanonicalCacheCreationValue(t *testing.T) {
	usage := &ResponsesUsage{
		InputTokens:              20,
		OutputTokens:             2,
		CacheCreationInputTokens: 0,
		InputTokensDetails: &ResponsesInputTokensDetails{
			CachedTokens:     3,
			CacheWriteTokens: 19,
		},
	}

	got := CopyForwardUsage(usage)

	require.Equal(t, 20, got.InputTokens)
	require.Equal(t, 3, got.CacheReadInputTokens)
	require.Zero(t, got.CacheCreationInputTokens)
}

// TestExtractNativeImageUsage uses the Image API's optional output-details shape.
// Missing details price all reported output as image tokens, while explicit zero
// remains zero and ordinary Responses extraction never performs that inference.
func TestExtractNativeImageUsage(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":5,"output_tokens":100}}`)
	usage, ok := ExtractNativeImageUsage(body)
	require.True(t, ok)
	require.Equal(t, 100, usage.ImageOutputTokens)
	ordinary, _ := ExtractOpenAIUsageFromJSONBytes(body)
	require.Zero(t, ordinary.ImageOutputTokens)
	usage, ok = ExtractNativeImageUsage([]byte(`{"usage":{"output_tokens":100,"output_tokens_details":{"image_tokens":0,"text_tokens":100}}}`))
	require.True(t, ok)
	require.Zero(t, usage.ImageOutputTokens)
}
