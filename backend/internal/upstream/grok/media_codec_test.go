package grok

import (
	"bytes"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
)

// New-media qualification includes image generation/editing and video
// generation/editing/extension, while existing video status uses a different
// gate. The table checks those five new-work identities and the status identity
// so callers can keep generation qualification separate from lookup behavior.

// TestGrokMediaGenerationGateCoversImagesAndVideo compares five new-media
// endpoint identities with an existing video-status endpoint.
// IsGenerationRequest must accept generation, editing and extension while
// returning false for the status fixture.
func TestGrokMediaGenerationGateCoversImagesAndVideo(t *testing.T) {
	tests := []struct {
		name     string            // scenario display label
		endpoint GrokMediaEndpoint // media endpoint fixture
		want     bool              // expected generation qualification
	}{
		{name: "image generation", endpoint: GrokMediaEndpointImagesGenerations, want: true},
		{name: "image edit", endpoint: GrokMediaEndpointImagesEdits, want: true},
		{name: "video generation", endpoint: GrokMediaEndpointVideosGenerations, want: true},
		{name: "video edit", endpoint: GrokMediaEndpointVideosEdits, want: true},
		{name: "video extension", endpoint: GrokMediaEndpointVideosExtensions, want: true},
		{name: "video status", endpoint: GrokMediaEndpointVideoStatus, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.endpoint.IsGenerationRequest())
		})
	}
}

// TestValidateGrokMediaRequestRejectsAmbiguousJSONAndMultipart checks JSON and
// multipart Grok media payloads. Duplicate or case-variant scalar controls
// fail validation while multi-file image uploads continue to succeed.
func TestValidateGrokMediaRequestRejectsAmbiguousJSONAndMultipart(t *testing.T) {
	require.NoError(t, ValidateGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-image","prompt":"sunset"}`)))
	require.Error(t, ValidateGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-image","prompt":"sunset","model":"grok-imagine-video"}`)))
	require.Error(t, ValidateGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-image","Model":"grok-imagine-video"}`)))

	buildMultipart := func(fields [][2]string, files [][2]string) ([]byte, string) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		for _, field := range fields {
			require.NoError(t, writer.WriteField(field[0], field[1]))
		}
		for _, file := range files {
			part, err := writer.CreateFormFile(file[0], file[1])
			require.NoError(t, err)
			_, err = part.Write([]byte("IMG"))
			require.NoError(t, err)
		}
		require.NoError(t, writer.Close())
		return buf.Bytes(), writer.FormDataContentType()
	}

	okBody, okType := buildMultipart(
		[][2]string{{"model", "grok-imagine-image"}, {"prompt", "combine"}},
		[][2]string{{"image", "1.png"}, {"image", "2.png"}},
	)
	require.NoError(t, ValidateGrokMediaRequest(okType, okBody))

	for _, fields := range [][][2]string{
		{{"model", "grok-imagine-image"}, {"model", "grok-imagine-video"}},
		{{"model", "grok-imagine-image"}, {"Model", "grok-imagine-video"}},
		{{"model", "grok-imagine-image"}, {"prompt", "a"}, {"prompt", "b"}},
	} {
		badBody, badType := buildMultipart(fields, nil)
		require.Error(t, ValidateGrokMediaRequest(badType, badBody))
	}
}
