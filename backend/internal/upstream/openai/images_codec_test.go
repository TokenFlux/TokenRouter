package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// images_codec_test.go verifies explicit image-driver construction and error
// attribution. Image model selection and edit inputs remain independent of the
// Responses text model, and tool denials cannot be relabeled as driver failures.

// TestBuildOpenAIImagesResponsesRequestExplicitDriver builds generation and edit
// requests with a selected text driver. Both operations retain image model and
// client options while the upstream response is always requested as a stream.
func TestBuildOpenAIImagesResponsesRequestExplicitDriver(t *testing.T) {
	for _, endpoint := range []string{upstream.OpenAIImagesGenerationsEndpoint, upstream.OpenAIImagesEditsEndpoint} {
		parsed := &upstream.ImageRequest{
			Endpoint: endpoint, Model: "gpt-image-2", Prompt: "draw a cat", N: 2,
			InputImageURLs: []string{"https://example.com/source.png"},
			MaskImageURL:   "https://example.com/mask.png",
		}
		body, err := BuildOpenAIImagesResponsesRequest(parsed, "gpt-image-2", "gpt-6-luna")
		require.NoError(t, err)
		require.Equal(t, "gpt-6-luna", gjson.GetBytes(body, "model").String())
		require.Equal(t, "gpt-image-2", gjson.GetBytes(body, "tools.0.model").String())
		require.True(t, gjson.GetBytes(body, "stream").Bool())
		require.Equal(t, int64(2), gjson.GetBytes(body, "tools.0.n").Int())
		require.Equal(t, "https://example.com/source.png", gjson.GetBytes(body, "input.0.content.1.image_url").String())
		require.Equal(t, "https://example.com/mask.png", gjson.GetBytes(body, "tools.0.input_image_mask.image_url").String())
		_, err = BuildOpenAIImagesResponsesRequest(parsed, "gpt-image-2", "")
		require.ErrorContains(t, err, "main model is required")
	}
}

// TestOpenAIImagesMainModelError attributes an upstream rejection to the exact
// named text driver. Image model errors and different model names keep their
// original classification so health policy does not confuse their roles.
func TestOpenAIImagesMainModelError(t *testing.T) {
	body := []byte(`{"detail":"The 'gpt-6-luna' model is not supported when using Codex with a ChatGPT account."}`)
	err := OpenAIImagesMainModelError(400, body, "gpt-6-luna", "gpt-image-2")
	require.NotNil(t, err)
	require.Equal(t, "image_main_model_unsupported", err.Code)
	require.Contains(t, err.Message, "gpt-6-luna")
	require.Contains(t, err.Message, "gpt-image-2")
	require.Nil(t, OpenAIImagesMainModelError(400, body, "gpt-6", "gpt-image-2"))
	require.Nil(t, OpenAIImagesMainModelError(403, body, "gpt-6-luna", "gpt-image-2"))
	require.Nil(t, OpenAIImagesMainModelError(400, []byte(`{"detail":"The 'gpt-image-2' model is not supported when using Codex with a ChatGPT account."}`), "gpt-6-luna", "gpt-image-2"))
}

// TestBuildOpenAIImagesResponsesRequestGIF preserves normalized animation controls
// through OAuth conversion. Responses lifecycle streaming remains enabled while
// the image tool carries no partial-image or compression controls.
func TestBuildOpenAIImagesResponsesRequestGIF(t *testing.T) {
	parsed, err := upstream.ParseImageRequest("/v1/images/generations", "application/json", []byte(`{"model":"gpt-image-2.6-flare","prompt":"A looping light","output_format":"gif","frame_count":4,"frame_duration_ms":255}`))
	require.NoError(t, err)
	body, err := BuildOpenAIImagesResponsesRequest(parsed, parsed.Model, "gpt-6-luna")
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(body, "stream").Bool())
	require.Equal(t, "gif", gjson.GetBytes(body, "tools.0.output_format").String())
	require.Equal(t, int64(4), gjson.GetBytes(body, "tools.0.frame_count").Int())
	require.Equal(t, int64(260), gjson.GetBytes(body, "tools.0.frame_duration_ms").Int())
	require.False(t, gjson.GetBytes(body, "tools.0.partial_images").Exists())
}
