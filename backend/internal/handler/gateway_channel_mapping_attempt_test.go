package handler

import (
	"context"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareGatewayAttemptRequestUsesCurrentAPIKeyGroupMapping(t *testing.T) {
	sourceGroupID := int64(6101)
	fallbackGroupID := int64(6102)
	channelService := service.NewChannelService(&gatewayModelsChannelRepoStub{
		channels: []service.Channel{
			{
				ID:       701,
				Status:   service.StatusActive,
				GroupIDs: []int64{sourceGroupID},
				ModelMapping: map[string]map[string]string{
					service.PlatformAnthropic: {"client-alias": "source-channel-model"},
				},
			},
			{
				ID:       702,
				Status:   service.StatusActive,
				GroupIDs: []int64{fallbackGroupID},
				ModelMapping: map[string]map[string]string{
					service.PlatformAnthropic: {"client-alias": "fallback-channel-model"},
				},
			},
		},
		groupPlatforms: map[int64]string{
			sourceGroupID:   service.PlatformAnthropic,
			fallbackGroupID: service.PlatformAnthropic,
		},
	}, nil)
	handler := newGatewayModelsHandlerWithChannelForTest(&gatewayModelsAccountRepoStub{}, channelService)
	body := []byte(`{"model":"client-alias","messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := service.ParseGatewayRequest(service.NewRequestBodyRef(body), service.PlatformAnthropic)
	require.NoError(t, err)

	sourceAPIKey := &service.APIKey{GroupID: &sourceGroupID}
	sourceAttempt, sourceMapping, err := handler.prepareGatewayAttemptRequest(
		context.Background(), parsed, body, sourceAPIKey, "client-alias",
	)
	require.NoError(t, err)
	require.Equal(t, sourceGroupID, *sourceAttempt.GroupID)
	require.Equal(t, "source-channel-model", sourceAttempt.Model)
	require.Equal(t, "source-channel-model", gjson.GetBytes(sourceAttempt.Body.Bytes(), "model").String())
	require.Equal(t, int64(701), sourceMapping.ChannelID)

	fallbackAPIKey := &service.APIKey{GroupID: &fallbackGroupID}
	fallbackAttempt, fallbackMapping, err := handler.prepareGatewayAttemptRequest(
		context.Background(), parsed, body, fallbackAPIKey, "client-alias",
	)
	require.NoError(t, err)
	require.Equal(t, fallbackGroupID, *fallbackAttempt.GroupID)
	require.Equal(t, "fallback-channel-model", fallbackAttempt.Model)
	require.Equal(t, "fallback-channel-model", gjson.GetBytes(fallbackAttempt.Body.Bytes(), "model").String())
	require.Equal(t, int64(702), fallbackMapping.ChannelID)

	usageFields := fallbackMapping.ToUsageFields("client-alias", "upstream-model")
	require.Equal(t, int64(702), usageFields.ChannelID)
	require.Equal(t, "fallback-channel-model", usageFields.ChannelMappedModel)
	require.Equal(t, "client-alias", parsed.Model)
	require.Equal(t, "client-alias", gjson.GetBytes(parsed.Body.Bytes(), "model").String())
}

// TestPrepareGatewayAttemptRequestUsesGeminiGroupMapping 验证 Gemini 分组的 Messages 请求体也写入渠道模型 C。
func TestPrepareGatewayAttemptRequestUsesGeminiGroupMapping(t *testing.T) {
	groupID := int64(6103)
	channelService := service.NewChannelService(&gatewayModelsChannelRepoStub{
		channels: []service.Channel{{
			ID:       703,
			Status:   service.StatusActive,
			GroupIDs: []int64{groupID},
			ModelMapping: map[string]map[string]string{
				service.PlatformGemini: {"client-alias": "gemini-channel-model"},
			},
		}},
		groupPlatforms: map[int64]string{groupID: service.PlatformGemini},
	}, nil)
	handler := newGatewayModelsHandlerWithChannelForTest(&gatewayModelsAccountRepoStub{}, channelService)
	body := []byte(`{"model":"client-alias","messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := service.ParseGatewayRequest(service.NewRequestBodyRef(body), service.PlatformAnthropic)
	require.NoError(t, err)

	attempt, mapping, err := handler.prepareGatewayAttemptRequest(
		context.Background(), parsed, body, &service.APIKey{GroupID: &groupID}, "client-alias",
	)
	require.NoError(t, err)
	require.Equal(t, "gemini-channel-model", attempt.Model)
	require.Equal(t, "gemini-channel-model", gjson.GetBytes(attempt.Body.Bytes(), "model").String())
	require.Equal(t, int64(703), mapping.ChannelID)
}
