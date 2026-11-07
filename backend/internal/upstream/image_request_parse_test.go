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
		`"size":"16x16extra"`, `"output_format":"png"`,
	} {
		_, err := ParseImageRequest("/v1/images/generations", "application/json", []byte(`{"output_format":"gif",`+fields+`}`))
		require.Error(t, err, fields)
	}
	err := ValidateGIFJSONControls([]byte(`{"tools":[{"type":"image_generation","output_format":"png"}],"tools":[{"type":"image_generation","output_format":"gif"}]}`))
	require.Error(t, err)
}
