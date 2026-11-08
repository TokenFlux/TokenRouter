package openai

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
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

// TestValidateGIFImagesResponseAcceptsActualFrameCounts accepts complete GIFs
// independently of request-only frame bounds. Incorrect upstream frame metadata
// is replaced by the encoded count without changing the returned image bytes.
func TestValidateGIFImagesResponseAcceptsActualFrameCounts(t *testing.T) {
	for _, count := range []int{1, 3, 9, 17} {
		animation := &gif.GIF{}
		for range count {
			animation.Image = append(animation.Image, image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White}))
			// GIF stores delays in centiseconds; twenty represents 200 milliseconds.
			animation.Delay = append(animation.Delay, 20)
		}
		var buffer bytes.Buffer
		require.NoError(t, gif.EncodeAll(&buffer, animation))
		encoded := base64.StdEncoding.EncodeToString(buffer.Bytes())
		body := []byte(`{"data":[{"b64_json":"` + encoded + `","frame_count":3}]}`)
		result, frames, err := ValidateGIFImagesResponse(body, true, nil)
		require.NoError(t, err)
		require.Equal(t, count, frames)
		require.Equal(t, int64(count), gjson.GetBytes(result, "data.0.frame_count").Int())
		require.Equal(t, encoded, gjson.GetBytes(result, "data.0.b64_json").String())
	}
}
