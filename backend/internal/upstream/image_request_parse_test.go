package upstream

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// image_request_parse_test.go checks native image request validation before
// provider selection. Animation controls are shared by JSON and multipart input,
// while malformed or ambiguous controls must not reach an authenticated upstream.

// TestParseImageRequestGIF rejects incompatible and ambiguous animation controls.
// These failures occur before provider selection, including duplicate fields whose
// effective value could otherwise differ between the gateway and upstream parser.
func TestParseImageRequestGIF(t *testing.T) {
	for _, fields := range []string{
		`"stream":true`, `"partial_images":0`, `"mask":{}`, `"output_compression":80`,
		`"frame_count":1`, `"frame_count":2.5`, `"frame_duration_ms":2001`,
		`"frame_count":"null"`, `"frame_duration_ms":"null"`,
		`"size":"16x16extra"`, `"output_format":"png"`,
	} {
		_, err := ParseImageRequest("/v1/images/generations", "application/json", []byte(`{"output_format":"gif",`+fields+`}`))
		require.Error(t, err, fields)
	}
	err := ValidateGIFJSONControls([]byte(`{"tools":[{"type":"image_generation","output_format":"png"}],"tools":[{"type":"image_generation","output_format":"gif"}]}`))
	require.Error(t, err)
}

// TestParseImageRequestNullableAnimationControls compares omitted and null JSON
// controls for generation and editing. GIF defaults preserve explicit companion
// values, while static requests keep their animation settings absent.
func TestParseImageRequestNullableAnimationControls(t *testing.T) {
	for _, endpoint := range []string{OpenAIImagesGenerationsEndpoint, OpenAIImagesEditsEndpoint} {
		t.Run(endpoint, func(t *testing.T) {
			for _, test := range []struct {
				name     string // request case label
				fields   string // supplied JSON options
				frames   int    // expected frame count
				duration int    // milliseconds per frame
			}{
				{"omitted", `"output_format":"gif"`, 9, 200},
				{"null_count", `"output_format":"gif","frame_count":null`, 9, 200},
				{"null_duration", `"output_format":"gif","frame_duration_ms":null`, 9, 200},
				{"both_null", `"output_format":"gif","frame_count":null,"frame_duration_ms":null`, 9, 200},
				{"explicit_duration", `"output_format":"gif","frame_count":null,"frame_duration_ms":500`, 9, 500},
				{"explicit_count", `"output_format":"gif","frame_count":3,"frame_duration_ms":null`, 3, 200},
				{"static", `"output_format":"png","frame_count":null,"frame_duration_ms":null`, 0, 0},
			} {
				t.Run(test.name, func(t *testing.T) {
					body := `{"model":"gpt-image-2.6-flare","prompt":"fixture",` +
						`"images":[{"image_url":"https://example.com/reference.png"}],` + test.fields + `}`
					req, err := ParseImageRequest(endpoint, "application/json", []byte(body))
					require.NoError(t, err)
					if test.frames == 0 {
						require.Nil(t, req.FrameCount)
						require.Nil(t, req.FrameDurationMS)
						return
					}
					require.NotNil(t, req.FrameCount)
					require.NotNil(t, req.FrameDurationMS)
					require.Equal(t, test.frames, *req.FrameCount)
					require.Equal(t, test.duration, *req.FrameDurationMS)
				})
			}
		})
	}
}
