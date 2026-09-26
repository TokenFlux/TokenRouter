package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 测试覆盖生产解析入口，分组映射前允许别名、映射后仍校验原生能力。
func TestImageRequestRoutingAndMultipart(t *testing.T) {
	request, err := ParseImageRequest("/v1/images/generations", "application/json", []byte(`{"model":"my-alias","prompt":"draw"}`), false)
	require.NoError(t, err)
	require.Error(t, request.ValidateRoutingModel("text-model"))
	require.NoError(t, request.ValidateRoutingModel("gpt-image-2"))
	require.Equal(t, ImageCapabilityNative, request.RequiredCapability)
	require.Equal(t, "my-alias", request.Model)
	basic, err := ParseImageRequest("/v1/images/generations", "application/json", []byte(`{"prompt":"draw"}`), true)
	require.NoError(t, err)
	require.Equal(t, ImageCapabilityBasic, basic.RequiredCapability)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.WriteField("prompt", "edit"))
	require.NoError(t, writer.WriteField("size", "1536x1024"))
	part, err := writer.CreateFormFile("image", "source.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("source-image"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	parsed, err := ParseImageRequest("/v1/images/edits", writer.FormDataContentType(), buf.Bytes(), true)
	require.NoError(t, err)
	require.True(t, parsed.IsEdits())
	require.Equal(t, ImageCapabilityNative, parsed.RequiredCapability)
	require.Len(t, parsed.Uploads, 1)
	require.Contains(t, string(parsed.ModerationBody()), "edit")
	require.NotEmpty(t, parsed.StickySessionSeed())
	require.Equal(t, parsed.StickySessionSeed(), NativeImageRequest(parsed).StickySessionSeed())
	encoded, err := json.Marshal(parsed)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "BodyHash")
}

func TestImageOutcomePreservesPartialAndTransportBoundaries(t *testing.T) {
	failure := errors.New("read failure")
	cases := []struct {
		name               string
		stream, oauth, sse bool
		observed           int
		err                error
		count              int
		retain             bool
	}{
		{"nonstream error", false, false, false, 1, failure, 0, false},
		{"stream partial", true, false, true, 2, failure, 2, true},
		{"stream empty error", true, true, true, 0, failure, 0, false},
		{"API SSE no fallback", true, false, true, 0, nil, 0, true},
		{"API JSON fallback", true, false, false, 0, nil, 3, true},
		{"OAuth fallback", true, true, true, 0, nil, 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			count, retain := ImageOutcome(tc.stream, tc.oauth, tc.sse, 3, tc.observed, tc.err)
			require.Equal(t, tc.count, count)
			require.Equal(t, tc.retain, retain)
		})
	}
}

func TestAuxiliaryCompletionRules(t *testing.T) {
	require.Nil(t, RealtimeAudioUsage(time.Minute, false))
	require.Nil(t, RealtimeAudioUsage(0, true))
	require.Equal(t, 1.5, RealtimeAudioUsage(90*time.Second, true).DurationOrUnits)
	require.Equal(t, "", TTSInputText([]byte(`{"input":"  ","text":"fallback"}`)))
	require.Equal(t, "fallback", TTSInputText([]byte(`{"input":null,"text":" fallback "}`)))
	require.Equal(t, "last", TTSInputText([]byte(`{"input":3,"prompt":"last"}`)))
	require.False(t, AlphaAccountErrorSideEffects(401))
	require.False(t, AlphaAccountErrorSideEffects(404))
	require.True(t, AlphaAccountErrorSideEffects(429))
	require.True(t, AlphaEndpointUnsupported(true, 405))
	require.False(t, AlphaEndpointUnsupported(false, 405))
	raw, ok := RequiredModel([]byte(`{"model":" my-model "}`), false)
	require.True(t, ok)
	require.Equal(t, " my-model ", raw)
	trimmed, ok := RequiredModel([]byte(`{"model":" my-model "}`), true)
	require.True(t, ok)
	require.Equal(t, "my-model", trimmed)
	require.True(t, RecordImmediateImages("images_generations", "m", 1))
	require.False(t, RecordImmediateImages("videos_generations", "m", 1))
	require.False(t, RecordImmediateImages("video_status", "m", 1))
	require.False(t, RecordImmediateImages("images_generations", "m", 0))
}
