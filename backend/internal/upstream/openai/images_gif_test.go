package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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
	body := []byte(`{"size":"1024x1024","data":[{"url":"https://images.example/a.gif?signature=secret"}]}`)
	calls := 0
	got, frames, err := ValidateGIFImagesResponse(body, true, func(raw string) (string, error) {
		calls++
		require.Equal(t, "https://images.example/a.gif?signature=secret", raw)
		return encoded, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 2, frames)
	require.Equal(t, "1x1", gjson.GetBytes(got, "size").String())
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
	require.Equal(t, "1x1", gjson.GetBytes(result, "size").String())
	require.Equal(t, "data:image/gif;base64,"+encoded, gjson.GetBytes(result, "data.0.url").String())
}

// TestValidateGIFImagesResponseSize normalizes stale and missing response sizes
// from verified GIF dimensions. Mixed formats and differing canvases omit the
// shared size while preserving each item, usage, and original animation bytes.
func TestValidateGIFImagesResponseSize(t *testing.T) {
	first := gifSizeFixture(t, 438, 390)
	second := gifSizeFixture(t, 400, 400)
	for _, test := range []struct {
		name      string           // scenario display name
		rootSize  any              // supplied response size
		items     []map[string]any // supplied output metadata
		wantSize  string           // expected shared canvas
		wantItems []string         // expected per-item dimensions
		wantUnits int              // expected frame billing units
	}{
		{name: "stale requested size", rootSize: "1024x1024", items: []map[string]any{{"b64_json": first, "size": "1024x1024"}}, wantSize: "438x390", wantItems: []string{"438x390"}, wantUnits: 2},
		{name: "missing root size", items: []map[string]any{{"b64_json": first}}, wantSize: "438x390", wantItems: []string{"438x390"}, wantUnits: 2},
		{name: "same sized GIFs", rootSize: "auto", items: []map[string]any{{"b64_json": first}, {"b64_json": first}}, wantSize: "438x390", wantItems: []string{"438x390", "438x390"}, wantUnits: 4},
		{name: "different GIF sizes", rootSize: "1024x1024", items: []map[string]any{{"b64_json": first}, {"b64_json": second}, {"b64_json": first}}, wantItems: []string{"438x390", "400x400", "438x390"}, wantUnits: 6},
		{name: "GIF then static", rootSize: "1024x1024", items: []map[string]any{{"b64_json": first}, {"url": "https://example.invalid/static.png", "size": "1024x1024"}}, wantItems: []string{"438x390", "1024x1024"}, wantUnits: 3},
		{name: "static then GIF", rootSize: "1024x1024", items: []map[string]any{{"url": "https://example.invalid/static.png", "size": "1024x1024"}, {"b64_json": first}}, wantItems: []string{"1024x1024", "438x390"}, wantUnits: 3},
		{name: "static only", rootSize: "1024x1024", items: []map[string]any{{"url": "https://example.invalid/static.png", "size": "1024x1024"}}, wantSize: "1024x1024", wantItems: []string{"1024x1024"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := map[string]any{"data": test.items, "usage": map[string]int{"input_tokens": 3, "output_tokens": 7}}
			if test.rootSize != nil {
				input["size"] = test.rootSize
			}
			body, err := json.Marshal(input)
			require.NoError(t, err)
			result, frames, err := ValidateGIFImagesResponse(body, false, nil)
			require.NoError(t, err)
			require.Equal(t, test.wantUnits, frames)
			if test.wantSize == "" {
				require.False(t, gjson.GetBytes(result, "size").Exists())
			} else {
				require.Equal(t, test.wantSize, gjson.GetBytes(result, "size").String())
			}
			items := gjson.GetBytes(result, "data").Array()
			require.Len(t, items, len(test.items))
			for index, item := range items {
				require.Equal(t, test.wantItems[index], item.Get("size").String())
				if encoded, ok := test.items[index]["b64_json"].(string); ok {
					require.Equal(t, encoded, item.Get("b64_json").String())
				}
			}
			require.JSONEq(t, gjson.GetBytes(body, "usage").Raw, gjson.GetBytes(result, "usage").Raw)
			if frames == 0 {
				require.Equal(t, body, result)
			}
		})
	}
}

// gifSizeFixture encodes two complete GIF frames with the requested canvas.
// Tests use real encoder output so dimension normalization reads container
// metadata without depending on upstream size labels or private image fixtures.
func gifSizeFixture(t *testing.T, width, height int) string {
	t.Helper()
	frame := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{color.Black, color.White})
	var buffer bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buffer, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{20, 20}}))
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
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
