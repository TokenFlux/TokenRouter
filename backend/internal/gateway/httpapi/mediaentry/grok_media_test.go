package mediaentry

import (
	"bytes"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestRecordGrokMediaUsageIgnoresNilResult(t *testing.T) {
	require.NotPanics(t, func() {
		recordGrokMediaUsage(
			nil, nil, nil, nil, authctx.AuthSubject{}, nil, nil, nil,
			"", routing.GroupMappingResult{}, nil, "",
		)
	})
}

func TestApplyGrokMediaGroupMappingRewritesForwardBody(t *testing.T) {
	jsonBody, contentType, err := applyGrokMediaGroupMapping(
		[]byte(`{"model":"key-target","prompt":"keep key-target in text"}`),
		"application/json",
		routing.GroupMappingResult{Mapped: true, MappedModel: "channel-target"},
	)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.JSONEq(t, `{"model":"channel-target","prompt":"keep key-target in text"}`, string(jsonBody))

	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	require.NoError(t, writer.WriteField("model", "key-target"))
	require.NoError(t, writer.WriteField("prompt", "keep key-target in text"))
	require.NoError(t, writer.Close())
	rewritten, rewrittenType, err := applyGrokMediaGroupMapping(
		multipartBody.Bytes(),
		writer.FormDataContentType(),
		routing.GroupMappingResult{Mapped: true, MappedModel: "channel-target"},
	)
	require.NoError(t, err)
	reader, err := multipart.NewReader(bytes.NewReader(rewritten), multipartBoundaryForTest(t, rewrittenType)).ReadForm(1 << 20)
	require.NoError(t, err)
	require.Equal(t, "channel-target", reader.Value["model"][0])
	require.Equal(t, "keep key-target in text", reader.Value["prompt"][0])
}

func multipartBoundaryForTest(t *testing.T, contentType string) string {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	return params["boundary"]
}

func TestShouldRecordGrokMediaUsage(t *testing.T) {
	tests := []struct {
		name     string
		endpoint grok.GrokMediaEndpoint
		model    string
		want     bool
	}{
		{
			name:     "image generation records usage",
			endpoint: grok.GrokMediaEndpointImagesGenerations,
			model:    "grok-imagine",
			want:     true,
		},
		{
			name:     "image edit records usage",
			endpoint: grok.GrokMediaEndpointImagesEdits,
			model:    "grok-imagine-edit",
			want:     true,
		},
		{
			name:     "video generation defers usage until status",
			endpoint: grok.GrokMediaEndpointVideosGenerations,
			model:    "grok-imagine-video-1.5",
			want:     false,
		},
		{
			name:     "video status skips immediate helper (status path claims separately)",
			endpoint: grok.GrokMediaEndpointVideoStatus,
			model:    "",
			want:     false,
		},
		{
			name:     "video content skips usage",
			endpoint: grok.GrokMediaEndpointVideoContent,
			model:    "",
			want:     false,
		},
		{
			name:     "generation skips usage without model",
			endpoint: grok.GrokMediaEndpointImagesGenerations,
			model:    " ",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 结果为 nil 时绝不能计费。
			require.False(t, shouldRecordGrokMediaUsage(tt.endpoint, tt.model, nil))
			// 即时辅助函数只对图片生成计费，异步视频在状态查询时计费。
			result := &forwardcore.OpenAIResult{ImageCount: 1, VideoCount: 0}
			if tt.endpoint.IsGenerationRequest() && !isGrokVideoCreateEndpoint(tt.endpoint) && strings.TrimSpace(tt.model) != "" {
				require.Equal(t, tt.want, shouldRecordGrokMediaUsage(tt.endpoint, tt.model, result))
			} else {
				require.False(t, shouldRecordGrokMediaUsage(tt.endpoint, tt.model, result))
			}
			// 即使存在生成端点与模型，计费单位为零时也不得计费。
			empty := &forwardcore.OpenAIResult{}
			require.False(t, shouldRecordGrokMediaUsage(tt.endpoint, tt.model, empty))
		})
	}
}

func TestGrokMediaRequiredCapability(t *testing.T) {
	tests := []struct {
		name     string
		endpoint grok.GrokMediaEndpoint
		want     accountcore.OpenAIEndpointCapability
	}{
		{name: "image generation", endpoint: grok.GrokMediaEndpointImagesGenerations, want: accountcore.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "image edit", endpoint: grok.GrokMediaEndpointImagesEdits, want: accountcore.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video generation", endpoint: grok.GrokMediaEndpointVideosGenerations, want: accountcore.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video edit", endpoint: grok.GrokMediaEndpointVideosEdits, want: accountcore.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video extension", endpoint: grok.GrokMediaEndpointVideosExtensions, want: accountcore.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video status preserves lookup", endpoint: grok.GrokMediaEndpointVideoStatus, want: ""},
		{name: "video content preserves lookup", endpoint: grok.GrokMediaEndpointVideoContent, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, grokMediaRequiredCapability(tt.endpoint))
		})
	}
}

func TestGrokMediaScheduleModelUsesNormalizedMappedUpstream(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"grok-imagine-video-1.5": "wrong-raw-model",
					"grok-imagine-video":     "mapped-video-model",
				},
			},
		},
	}

	require.Equal(t, "mapped-video-model", grokMediaScheduleModel(account, "grok-imagine-video", nil))
	require.Equal(t, "actual-upstream-model", grokMediaScheduleModel(account, "grok-imagine-video", &forwardcore.OpenAIResult{
		UpstreamModel: "actual-upstream-model",
	}))
	require.Equal(t, "mapped-video-model", grokMediaScheduleModel(account, "grok-imagine-video", &forwardcore.OpenAIResult{}))
	require.Equal(t, "grok-imagine-video", grokMediaScheduleModel(nil, " grok-imagine-video ", nil))
}
