package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// Usage response conversion selects client model names and derives legacy
// transport flags from effective request categories. These fixtures compare
// user and administrator field visibility, inspect timing values and retain
// absent historical image metadata, so mapping expectations stay tied to
// encoded responses.

// TestUsageLogFromService_AverageSpeedUsesStoredAttempt restores a buffered stream's
// persisted observations. User and administrator responses expose the same rounded
// TPS, while historical rows without timing retain an explicit JSON null.
func TestUsageLogFromService_AverageSpeedUsesStoredAttempt(t *testing.T) {
	var row usage.UsageLog
	require.NoError(t, json.Unmarshal([]byte(`{"Timings":{
		"ttft_mode":"first_event",
		"upstream_stream":true,
		"upstream_complete":true,
		"usage_complete":true,
		"values":{
			"upstream_started_ms":11103,
			"upstream_first_output_ms":26297,
			"upstream_last_output_ms":26297,
			"upstream_output_event_count":1
		}
	}}`), &row))
	row.OutputTokens = 119
	for _, got := range []*UsageLog{FromUsage(&row), &FromUsageAdmin(&row).UsageLog} {
		require.NotNil(t, got.TPS)
		require.Equal(t, 7.8, *got.TPS)
		body, marshalErr := json.Marshal(got)
		require.NoError(t, marshalErr)
		require.Contains(t, string(body), `"tps":7.8`)
	}
	row.Timings = nil
	for _, got := range []*UsageLog{FromUsage(&row), &FromUsageAdmin(&row).UsageLog} {
		require.Nil(t, got.TPS)
		body, marshalErr := json.Marshal(got)
		require.NoError(t, marshalErr)
		require.Contains(t, string(body), `"tps":null`)
	}
}

// TestUsageLogFromService_IncludesOpenAIWSMode supplies WebSocket and
// ordinary HTTP legacy flags. It requires user and administrator mappers to
// expose the corresponding OpenAIWSMode values.
func TestUsageLogFromService_IncludesOpenAIWSMode(t *testing.T) {
	t.Parallel()

	wsLog := &usage.UsageLog{
		RequestID:    "req_1",
		Model:        "gpt-5.3-codex",
		OpenAIWSMode: true,
	}
	httpLog := &usage.UsageLog{
		RequestID:    "resp_1",
		Model:        "gpt-5.3-codex",
		OpenAIWSMode: false,
	}

	require.True(t, FromUsage(wsLog).OpenAIWSMode)
	require.False(t, FromUsage(httpLog).OpenAIWSMode)
	require.True(t, FromUsageAdmin(wsLog).OpenAIWSMode)
	require.False(t, FromUsageAdmin(httpLog).OpenAIWSMode)
}

// TestUsageLogTimingFromService_MapsStages supplies first-byte offset,
// attempt count and connection reuse. It requires those observations to
// appear in the returned timing response.
func TestUsageLogTimingFromService_MapsStages(t *testing.T) {
	t.Parallel()

	var row usage.UsageLog
	require.NoError(t, json.Unmarshal([]byte(`{"Timings":{"values":{"upstream_first_response_byte_ms":1200,"upstream_attempt_count":2,"upstream_connection_reused":1}}}`), &row))
	got := TimingFromUsage(&row)
	require.NotNil(t, got)
	require.Equal(t, int64(1200), *got.UpstreamFirstResponseByteMs)
	require.Equal(t, int64(2), *got.UpstreamAttemptCount)
	require.NotNil(t, got.UpstreamConnectionReused)
	require.True(t, *got.UpstreamConnectionReused)
}

// TestUsageLogFromService_PrefersRequestTypeForLegacyFields supplies explicit
// WS v2 beside false legacy flags. It requires both DTO forms to show ws_v2
// and true transport booleans.
func TestUsageLogFromService_PrefersRequestTypeForLegacyFields(t *testing.T) {
	t.Parallel()

	log := &usage.UsageLog{
		RequestID:    "req_2",
		Model:        "gpt-5.3-codex",
		RequestType:  usage.RequestTypeWSV2,
		Stream:       false,
		OpenAIWSMode: false,
	}

	userDTO := FromUsage(log)
	adminDTO := FromUsageAdmin(log)

	require.Equal(t, "ws_v2", userDTO.RequestType)
	require.True(t, userDTO.Stream)
	require.True(t, userDTO.OpenAIWSMode)
	require.Equal(t, "ws_v2", adminDTO.RequestType)
	require.True(t, adminDTO.Stream)
	require.True(t, adminDTO.OpenAIWSMode)
}

// TestUsageCleanupTaskFromService_RequestTypeMapping supplies a stream
// cleanup category as a stored integer pointer. It requires a nonnil task
// response with the stream text filter.
func TestUsageCleanupTaskFromService_RequestTypeMapping(t *testing.T) {
	t.Parallel()

	requestType := int16(usage.RequestTypeStream)
	task := &usage.UsageCleanupTask{
		ID:     1,
		Status: usage.UsageCleanupStatusPending,
		Filters: usage.UsageCleanupFilters{
			RequestType: &requestType,
		},
	}

	dtoTask := CleanupFromUsage(task)
	require.NotNil(t, dtoTask)
	require.NotNil(t, dtoTask.Filters.RequestType)
	require.Equal(t, "stream", *dtoTask.Filters.RequestType)
}

// TestRequestTypeStringPtrNil calls the request-type display wrapper with
// absent input. It requires a nil result so optional cleanup category text
// can remain absent.
func TestRequestTypeStringPtrNil(t *testing.T) {
	t.Parallel()
	require.Nil(t, requestTypeStringPtr(nil))
}

// TestUsageLogFromService_IncludesServiceTierForUserAndAdmin supplies
// priority tier and both endpoints. It checks shared inbound fields and
// administrator-only outbound endpoint and provider multiplier observations.
func TestUsageLogFromService_IncludesServiceTierForUserAndAdmin(t *testing.T) {
	t.Parallel()

	serviceTier := "priority"
	inboundEndpoint := "/v1/chat/completions"
	upstreamEndpoint := "/v1/responses"
	log := &usage.UsageLog{
		RequestID:              "req_3",
		Model:                  "gpt-5.4",
		ServiceTier:            &serviceTier,
		InboundEndpoint:        &inboundEndpoint,
		UpstreamEndpoint:       &upstreamEndpoint,
		ProviderRateMultiplier: f64Ptr(1.5),
	}

	userDTO := FromUsage(log)
	adminDTO := FromUsageAdmin(log)

	require.NotNil(t, userDTO.ServiceTier)
	require.Equal(t, serviceTier, *userDTO.ServiceTier)
	require.NotNil(t, userDTO.InboundEndpoint)
	require.Equal(t, inboundEndpoint, *userDTO.InboundEndpoint)
	require.Nil(t, userDTO.UpstreamEndpoint)
	require.NotNil(t, adminDTO.ServiceTier)
	require.Equal(t, serviceTier, *adminDTO.ServiceTier)
	require.NotNil(t, adminDTO.InboundEndpoint)
	require.Equal(t, inboundEndpoint, *adminDTO.InboundEndpoint)
	require.NotNil(t, adminDTO.UpstreamEndpoint)
	require.Equal(t, upstreamEndpoint, *adminDTO.UpstreamEndpoint)
	require.NotNil(t, adminDTO.ProviderRateMultiplier)
	// Allow floating-point representation differences within this asserted multiplier tolerance.
	require.InDelta(t, 1.5, *adminDTO.ProviderRateMultiplier, 1e-12)
}

// TestUsageLogFromService_IncludesRequestedEffort supplies distinct client
// and forwarded effort values. It checks both DTO forms and requires the
// requested effort field in user JSON.
func TestUsageLogFromService_IncludesRequestedEffort(t *testing.T) {
	t.Parallel()
	requested := "max"
	forwarded := "xhigh"
	log := &usage.UsageLog{
		RequestID:                "req_reasoning_mapping",
		Model:                    "gpt-5.4",
		RequestedReasoningEffort: &requested,
		ReasoningEffort:          &forwarded,
	}

	userDTO := FromUsage(log)
	adminDTO := FromUsageAdmin(log)
	require.Equal(t, forwarded, *userDTO.ReasoningEffort)
	require.Equal(t, requested, *userDTO.RequestedReasoningEffort)
	require.Equal(t, forwarded, *adminDTO.ReasoningEffort)
	require.Equal(t, requested, *adminDTO.RequestedReasoningEffort)

	userJSON, err := json.Marshal(userDTO)
	require.NoError(t, err)
	require.Contains(t, string(userJSON), "requested_reasoning_effort")
}

// TestUsageLogFromService_PreservesEquivalentRequestedEffort supplies the
// same effort text in both source fields. It requires the administrator
// requested-effort pointer and exact supplied spelling.
func TestUsageLogFromService_PreservesEquivalentRequestedEffort(t *testing.T) {
	t.Parallel()
	effort := "x-high"
	log := &usage.UsageLog{RequestedReasoningEffort: &effort, ReasoningEffort: &effort}
	adminDTO := FromUsageAdmin(log)
	require.NotNil(t, adminDTO.RequestedReasoningEffort)
	require.Equal(t, effort, *adminDTO.RequestedReasoningEffort)
}

// TestUsageLogFromService_UsesRequestedModelAndKeepsUpstreamAdminOnly
// supplies client and outbound model observations. It checks the client
// display name in both forms and administrator-only encoded diagnostics.
func TestUsageLogFromService_UsesRequestedModelAndKeepsUpstreamAdminOnly(t *testing.T) {
	t.Parallel()

	responseModel := "claude-runtime"
	mismatch := true
	upstreamModel := "claude-sonnet-4-20250514"
	log := &usage.UsageLog{
		RequestID:             "req_4",
		Model:                 upstreamModel,
		RequestedModel:        "claude-sonnet-4",
		UpstreamModel:         &upstreamModel,
		UpstreamResponseModel: &responseModel,
		UpstreamModelMismatch: &mismatch,
	}

	userDTO := FromUsage(log)
	adminDTO := FromUsageAdmin(log)

	require.Equal(t, "claude-sonnet-4", userDTO.Model)
	require.Equal(t, "claude-sonnet-4", adminDTO.Model)

	userJSON, err := json.Marshal(userDTO)
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "upstream_model")
	require.NotContains(t, string(userJSON), "upstream_response_model")
	require.NotContains(t, string(userJSON), "upstream_model_mismatch")

	require.Equal(t, &responseModel, adminDTO.UpstreamResponseModel)
	require.Equal(t, &mismatch, adminDTO.UpstreamModelMismatch)
	adminJSON, err := json.Marshal(adminDTO)
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"upstream_model":"claude-sonnet-4-20250514"`)
}

// TestUsageLogFromService_KeepsUserBillingAndIPWithoutAdminCostFields
// supplies charged costs, IP and provider-cost inputs. It checks user billing
// values and requires provider cost keys to remain absent.
func TestUsageLogFromService_KeepsUserBillingAndIPWithoutAdminCostFields(t *testing.T) {
	t.Parallel()

	ipAddress := "203.0.113.10"
	providerRateMultiplier := 1.5
	providerStatsCost := 0.21
	log := &usage.UsageLog{
		RequestID:              "req_user_visible_billing",
		Model:                  "gpt-5.4",
		InputCost:              0.01,
		OutputCost:             0.02,
		CacheCreationCost:      0.03,
		CacheReadCost:          0.04,
		TotalCost:              0.10,
		ActualCost:             0.08,
		RateMultiplier:         0.8,
		IPAddress:              &ipAddress,
		ProviderRateMultiplier: &providerRateMultiplier,
		ProviderStatsCost:      &providerStatsCost,
	}

	userDTO := FromUsage(log)
	require.Equal(t, 0.01, userDTO.InputCost)
	require.Equal(t, 0.02, userDTO.OutputCost)
	require.Equal(t, 0.03, userDTO.CacheCreationCost)
	require.Equal(t, 0.04, userDTO.CacheReadCost)
	require.Equal(t, 0.10, userDTO.TotalCost)
	require.Equal(t, 0.08, userDTO.ActualCost)
	require.Equal(t, 0.8, userDTO.RateMultiplier)
	require.NotNil(t, userDTO.IPAddress)
	require.Equal(t, ipAddress, *userDTO.IPAddress)

	userJSON, err := json.Marshal(userDTO)
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "provider_rate_multiplier")
	require.NotContains(t, string(userJSON), "provider_stats_cost")
	require.NotContains(t, string(userJSON), "provider_cost")
}

// TestUsageLogFromService_FallsBackToLegacyModelWhenRequestedModelMissing
// supplies only the stored model name. It requires both response forms to use
// that name as their displayed model.
func TestUsageLogFromService_FallsBackToLegacyModelWhenRequestedModelMissing(t *testing.T) {
	t.Parallel()

	log := &usage.UsageLog{
		RequestID: "req_legacy",
		Model:     "claude-3",
	}

	userDTO := FromUsage(log)
	adminDTO := FromUsageAdmin(log)

	require.Equal(t, "claude-3", userDTO.Model)
	require.Equal(t, "claude-3", adminDTO.Model)
}

// TestUsageLogFromService_IncludesImageBillingMetadataForUserAndAdmin
// supplies observed image sizes and a size-count map. It requires both
// response forms to carry the fixture's image metadata values.
func TestUsageLogFromService_IncludesImageBillingMetadataForUserAndAdmin(t *testing.T) {
	t.Parallel()

	imageSize := "4K"
	inputSize := "1024x1024"
	outputSize := "3840x2160"
	source := "output"
	log := &usage.UsageLog{
		RequestID:          "req_image_metadata",
		Model:              "gpt-image-2",
		ImageCount:         2,
		ImageSize:          &imageSize,
		ImageInputSize:     &inputSize,
		ImageOutputSize:    &outputSize,
		ImageSizeSource:    &source,
		ImageSizeBreakdown: map[string]int{"4K": 2},
	}

	userDTO := FromUsage(log)
	adminDTO := FromUsageAdmin(log)

	for _, got := range []*UsageLog{userDTO, &adminDTO.UsageLog} {
		require.Equal(t, 2, got.ImageCount)
		require.NotNil(t, got.ImageSize)
		require.Equal(t, imageSize, *got.ImageSize)
		require.NotNil(t, got.ImageInputSize)
		require.Equal(t, inputSize, *got.ImageInputSize)
		require.NotNil(t, got.ImageOutputSize)
		require.Equal(t, outputSize, *got.ImageOutputSize)
		require.NotNil(t, got.ImageSizeSource)
		require.Equal(t, source, *got.ImageSizeSource)
		require.Equal(t, map[string]int{"4K": 2}, got.ImageSizeBreakdown)
	}
}

// TestUsageLogFromService_PreservesHistoricalMissingImageSize supplies a
// historical image count with absent size observations. It requires nil
// metadata and encoded image_size null rather than an invented size.
func TestUsageLogFromService_PreservesHistoricalMissingImageSize(t *testing.T) {
	t.Parallel()

	log := &usage.UsageLog{
		RequestID:  "req_legacy_image_missing_size",
		Model:      "gpt-image-2",
		ImageCount: 1,
		ImageSize:  nil,
	}

	dto := FromUsage(log)
	require.Equal(t, 1, dto.ImageCount)
	require.Nil(t, dto.ImageSize)
	require.Nil(t, dto.ImageInputSize)
	require.Nil(t, dto.ImageOutputSize)
	require.Nil(t, dto.ImageSizeSource)
	require.Nil(t, dto.ImageSizeBreakdown)

	body, err := json.Marshal(dto)
	require.NoError(t, err)
	require.Contains(t, string(body), `"image_size":null`)
	require.NotContains(t, string(body), `"image_size":"2K"`)
}

// f64Ptr returns independent pointer storage for a floating-point mapper
// fixture.
func f64Ptr(value float64) *float64 {
	return &value
}

// requestTypeStringPtr delegates optional category display conversion to
// RequestTypeStringPtr. Nil inputs remain nil, while nonnil inputs use the
// helper's supported lowercase normalization for cleanup filter response
// tests.
func requestTypeStringPtr(v *int16) *string { return RequestTypeStringPtr(v) }
