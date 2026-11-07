package openai

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// images_gif_test.go verifies animation delivery from native base64 and URL
// responses. Fixtures retain original bytes and distinguish observed frame units
// from the requested count, while download failures remain terminal result errors.

// TestValidateGIFImagesResponse resolves URL-only animations through the protected
// callback. It preserves signed URLs, original bytes, and the actual two-frame count.
func TestValidateGIFImagesResponse(t *testing.T) {
	encoded := "R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs="
	body := []byte(`{"data":[{"url":"https://images.example/a.gif?signature=secret"}]}`)
	calls := 0
	got, frames, err := ValidateGIFImagesResponse(body, true, func(raw string) (string, error) {
		calls++
		require.Equal(t, "https://images.example/a.gif?signature=secret", raw)
		return encoded, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 2, frames)
	require.Equal(t, encoded, gjson.GetBytes(got, "data.0.b64_json").String())
	require.Equal(t, "https://images.example/a.gif?signature=secret", gjson.GetBytes(got, "data.0.url").String())
	_, _, err = ValidateGIFImagesResponse(body, true, func(string) (string, error) { return "", errors.New("download failed") })
	require.Error(t, err)
	_, _, err = ValidateGIFImagesResponse([]byte(`{"data":[{"b64_json":"aW52YWxpZA=="}]}`), true, nil)
	require.Error(t, err)
}

// TestValidateGIFDataURLMetadata reads actual format and dimensions from bytes.
// A legacy PNG data-URL label must not turn a valid animation into a static
// image, and the corrected URL must retain the original encoded GIF exactly.
func TestValidateGIFDataURLMetadata(t *testing.T) {
	encoded := "R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs="
	body := []byte(`{"data":[{"url":"data:image/png;base64,` + encoded + `"}]}`)
	result, frames, err := ValidateGIFImagesResponse(body, true, nil)
	require.NoError(t, err)
	require.Equal(t, 2, frames)
	require.Equal(t, "gif", gjson.GetBytes(result, "output_format").String())
	require.Equal(t, "1x1", gjson.GetBytes(result, "data.0.size").String())
	require.Equal(t, "data:image/gif;base64,"+encoded, gjson.GetBytes(result, "data.0.url").String())
}
