package handler

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRecordGrokMediaUsageIgnoresNilResult(t *testing.T) {
	require.NotPanics(t, func() {
		recordGrokMediaUsage(
			nil, nil, nil, nil, middleware2.AuthSubject{}, nil, nil, nil,
			"", service.ChannelMappingResult{}, nil, "",
		)
	})
}

func TestApplyGrokMediaChannelMappingRewritesForwardBody(t *testing.T) {
	jsonBody, contentType, err := applyGrokMediaChannelMapping(
		[]byte(`{"model":"key-target","prompt":"keep key-target in text"}`),
		"application/json",
		service.ChannelMappingResult{Mapped: true, MappedModel: "channel-target"},
	)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.JSONEq(t, `{"model":"channel-target","prompt":"keep key-target in text"}`, string(jsonBody))

	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	require.NoError(t, writer.WriteField("model", "key-target"))
	require.NoError(t, writer.WriteField("prompt", "keep key-target in text"))
	require.NoError(t, writer.Close())
	rewritten, rewrittenType, err := applyGrokMediaChannelMapping(
		multipartBody.Bytes(),
		writer.FormDataContentType(),
		service.ChannelMappingResult{Mapped: true, MappedModel: "channel-target"},
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

type grokMediaEligibilityProberStub struct {
	eligible bool
	reason   string
	err      error
	calls    int
}

func (s *grokMediaEligibilityProberStub) ProbeMediaEligibility(context.Context, int64) (bool, string, error) {
	s.calls++
	return s.eligible, s.reason, s.err
}

func TestShouldRecordGrokMediaUsage(t *testing.T) {
	tests := []struct {
		name     string
		endpoint service.GrokMediaEndpoint
		model    string
		want     bool
	}{
		{
			name:     "image generation records usage",
			endpoint: service.GrokMediaEndpointImagesGenerations,
			model:    "grok-imagine",
			want:     true,
		},
		{
			name:     "image edit records usage",
			endpoint: service.GrokMediaEndpointImagesEdits,
			model:    "grok-imagine-edit",
			want:     true,
		},
		{
			name:     "video generation defers usage until status",
			endpoint: service.GrokMediaEndpointVideosGenerations,
			model:    "grok-imagine-video-1.5",
			want:     false,
		},
		{
			name:     "video status skips immediate helper (status path claims separately)",
			endpoint: service.GrokMediaEndpointVideoStatus,
			model:    "",
			want:     false,
		},
		{
			name:     "video content skips usage",
			endpoint: service.GrokMediaEndpointVideoContent,
			model:    "",
			want:     false,
		},
		{
			name:     "generation skips usage without model",
			endpoint: service.GrokMediaEndpointImagesGenerations,
			model:    " ",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 结果为 nil 时绝不能计费。
			require.False(t, shouldRecordGrokMediaUsage(tt.endpoint, tt.model, nil))
			// 即时辅助函数只对图片生成计费，异步视频在状态查询时计费。
			result := &service.OpenAIForwardResult{ImageCount: 1, VideoCount: 0}
			if tt.endpoint.IsGenerationRequest() && !isGrokVideoCreateEndpoint(tt.endpoint) && strings.TrimSpace(tt.model) != "" {
				require.Equal(t, tt.want, shouldRecordGrokMediaUsage(tt.endpoint, tt.model, result))
			} else {
				require.False(t, shouldRecordGrokMediaUsage(tt.endpoint, tt.model, result))
			}
			// 即使存在生成端点与模型，计费单位为零时也不得计费。
			empty := &service.OpenAIForwardResult{}
			require.False(t, shouldRecordGrokMediaUsage(tt.endpoint, tt.model, empty))
		})
	}
}

func TestGrokMediaRequiredCapability(t *testing.T) {
	tests := []struct {
		name     string
		endpoint service.GrokMediaEndpoint
		want     service.OpenAIEndpointCapability
	}{
		{name: "image generation", endpoint: service.GrokMediaEndpointImagesGenerations, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "image edit", endpoint: service.GrokMediaEndpointImagesEdits, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video generation", endpoint: service.GrokMediaEndpointVideosGenerations, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video edit", endpoint: service.GrokMediaEndpointVideosEdits, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video extension", endpoint: service.GrokMediaEndpointVideosExtensions, want: service.OpenAIEndpointCapabilityGrokMediaGeneration},
		{name: "video status preserves lookup", endpoint: service.GrokMediaEndpointVideoStatus, want: ""},
		{name: "video content preserves lookup", endpoint: service.GrokMediaEndpointVideoContent, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, grokMediaRequiredCapability(tt.endpoint))
		})
	}
}

func TestEnsureGrokMediaAccountEligibility(t *testing.T) {
	t.Run("non oauth account does not probe", func(t *testing.T) {
		prober := &grokMediaEligibilityProberStub{}
		h := &OpenAIGatewayHandler{grokMediaEligibilityProber: prober}
		account := &service.Account{Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey}

		eligible, reason, err := h.ensureGrokMediaAccountEligibility(context.Background(), account)

		require.NoError(t, err)
		require.True(t, eligible)
		require.Equal(t, "non_oauth", reason)
		require.Zero(t, prober.calls)
	})

	t.Run("unobserved oauth is probed before forwarding", func(t *testing.T) {
		prober := &grokMediaEligibilityProberStub{eligible: true, reason: "eligible"}
		h := &OpenAIGatewayHandler{grokMediaEligibilityProber: prober}
		account := &service.Account{ID: 7, Platform: service.PlatformGrok, Type: service.AccountTypeOAuth}

		eligible, reason, err := h.ensureGrokMediaAccountEligibility(context.Background(), account)

		require.NoError(t, err)
		require.True(t, eligible)
		require.Equal(t, "eligible", reason)
		require.Equal(t, 1, prober.calls)
	})

	t.Run("missing prober fails closed", func(t *testing.T) {
		h := &OpenAIGatewayHandler{}
		account := &service.Account{ID: 8, Platform: service.PlatformGrok, Type: service.AccountTypeOAuth}

		eligible, reason, err := h.ensureGrokMediaAccountEligibility(context.Background(), account)

		require.Error(t, err)
		require.False(t, eligible)
		require.Equal(t, "billing_probe_unavailable", reason)
	})

	t.Run("probe failure fails closed", func(t *testing.T) {
		probeErr := errors.New("probe failed")
		prober := &grokMediaEligibilityProberStub{reason: "billing_unobserved", err: probeErr}
		h := &OpenAIGatewayHandler{grokMediaEligibilityProber: prober}
		account := &service.Account{ID: 9, Platform: service.PlatformGrok, Type: service.AccountTypeOAuth}

		eligible, reason, err := h.ensureGrokMediaAccountEligibility(context.Background(), account)

		require.ErrorIs(t, err, probeErr)
		require.False(t, eligible)
		require.Equal(t, "billing_unobserved", reason)
	})
}

func TestGrokMediaScheduleModelUsesNormalizedMappedUpstream(t *testing.T) {
	account := &service.Account{
		Platform: service.PlatformGrok,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"grok-imagine-video-1.5": "wrong-raw-model",
				"grok-imagine-video":     "mapped-video-model",
			},
		},
	}

	require.Equal(t, "mapped-video-model", grokMediaScheduleModel(account, "grok-imagine-video", nil))
	require.Equal(t, "actual-upstream-model", grokMediaScheduleModel(account, "grok-imagine-video", &service.OpenAIForwardResult{
		UpstreamModel: "actual-upstream-model",
	}))
	require.Equal(t, "mapped-video-model", grokMediaScheduleModel(account, "grok-imagine-video", &service.OpenAIForwardResult{}))
	require.Equal(t, "grok-imagine-video", grokMediaScheduleModel(nil, " grok-imagine-video ", nil))
}
