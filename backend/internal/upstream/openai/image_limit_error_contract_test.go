//go:build unit

package openai

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsOpenAIImageRateLimitError(t *testing.T) {
	imageBody := []byte(`{"error":{"message":"Rate limit reached for gpt-image-2-codex (for limit gpt-image) in organization org on input-images per min: Limit 4000, Used 4000. Please try again in 467ms."}}`)
	textBody := []byte(`{"error":{"message":"Rate limit reached for gpt-5.4 in organization org on tokens per min: Limit 30000, Used 30000. Please try again in 1s."}}`)

	require.True(t, IsImageRateLimitError(http.StatusTooManyRequests, imageBody))
	require.False(t, IsImageRateLimitError(http.StatusTooManyRequests, textBody))
	require.False(t, IsImageRateLimitError(http.StatusBadRequest, imageBody))
}

func TestIsOpenAIImageCapabilityLossError(t *testing.T) {
	capabilityLossBody := []byte(`{"error":{"message":"Tool choice 'image_generation' not found in 'tools' parameter.","param":"tool_choice","type":"invalid_request_error"}}`)
	genericBadRequestBody := []byte(`{"error":{"message":"Invalid type for input[0].arguments"}}`)

	require.True(t, IsImageCapabilityLossError(http.StatusBadRequest, capabilityLossBody))
	require.False(t, IsImageCapabilityLossError(http.StatusBadRequest, genericBadRequestBody))
	require.False(t, IsImageCapabilityLossError(http.StatusTooManyRequests, capabilityLossBody))
}
