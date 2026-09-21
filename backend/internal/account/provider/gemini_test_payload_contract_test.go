//go:build unit

package provider

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
)

func TestCreateGeminiTestPayload_ImageModel(t *testing.T) {
	t.Parallel()

	payload := geminiTestPayload("gemini-2.5-flash-image", "draw a tiny robot")

	var parsed struct {
		Contents []struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		GenerationConfig struct {
			ResponseModalities []string `json:"responseModalities"`
			ImageConfig        struct {
				AspectRatio string `json:"aspectRatio"`
			} `json:"imageConfig"`
		} `json:"generationConfig"`
	}

	require.NoError(t, json.Unmarshal(payload, &parsed))
	require.Len(t, parsed.Contents, 1)
	require.Len(t, parsed.Contents[0].Parts, 1)
	require.Equal(t, "draw a tiny robot", parsed.Contents[0].Parts[0].Text)
	require.Equal(t, []string{"TEXT", "IMAGE"}, parsed.GenerationConfig.ResponseModalities)
	require.Equal(t, "1:1", parsed.GenerationConfig.ImageConfig.AspectRatio)
}

func TestCreateGeminiTestPayload_ExplicitTypeOverridesModelName(t *testing.T) {
	t.Parallel()

	textPayload := geminiTestPayload("gemini-2.5-flash-image", "reply briefly", account.AccountTestTypeText)
	var textParsed map[string]any
	require.NoError(t, json.Unmarshal(textPayload, &textParsed))
	require.NotContains(t, textParsed, "generationConfig")
	content := testassert.MustType[map[string]any](testassert.MustType[[]any](textParsed["contents"])[0])
	parts := testassert.MustType[[]any](content["parts"])
	require.Equal(t, "reply briefly", testassert.MustType[map[string]any](parts[0])["text"])

	imagePayload := geminiTestPayload("gemini-2.5-flash", "draw a tiny robot", account.AccountTestTypeImage)
	var imageParsed map[string]any
	require.NoError(t, json.Unmarshal(imagePayload, &imageParsed))
	config := testassert.MustType[map[string]any](imageParsed["generationConfig"])
	require.Equal(t, []any{"TEXT", "IMAGE"}, config["responseModalities"])
}
