//go:build unit

package openaiforward_test

import (
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestBuildOpenAIResponsesURLForPlatform deepseek 官方端点为 /responses（无 /v1）。
func TestBuildOpenAIResponsesURLForPlatform(t *testing.T) {
	t.Parallel()
	require.Equal(t, "https://api.deepseek.com/responses", forward.ResponsesEndpoint(capability.PlatformDeepseek, "https://api.deepseek.com"))
	require.Equal(t, "https://relay.example.com/responses", forward.ResponsesEndpoint(capability.PlatformDeepseek, "https://relay.example.com"))
	require.Equal(t, "https://relay.example.com/v1/responses", forward.ResponsesEndpoint(capability.PlatformDeepseek, "https://relay.example.com/v1"))
	require.Equal(t, "https://api.openai.com/v1/responses", forward.ResponsesEndpoint(capability.PlatformOpenAI, "https://api.openai.com"))
	require.Equal(t, "https://open.bigmodel.cn/api/paas/v4/responses", forward.ResponsesEndpoint(capability.PlatformZhipu, "https://open.bigmodel.cn/api/paas/v4"))
	require.Equal(t, "https://api.moonshot.cn/v1/responses", forward.ResponsesEndpoint(capability.PlatformKimi, "https://api.moonshot.cn/v1"))
	require.Equal(t, "https://api.kimi.com/coding/v1/responses", forward.ResponsesEndpoint(capability.PlatformKimi, "https://api.kimi.com/coding/v1"))
}

// TestNormalizeDeepSeekResponsesRequestBody 无状态适配：强制 store=false、
// 清除 previous_response_id；非原生 CN Responses 协议原样返回。
func TestNormalizeDeepSeekResponsesRequestBody(t *testing.T) {
	t.Parallel()

	deepseekResponses := &accountcore.Record{
		Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": accountcore.APIProtocolResponses},
	}
	body := []byte(`{"model":"deepseek-v4-pro","store":true,"previous_response_id":"resp_123","input":"hi"}`)
	normalized := forward.NormalizeCNResponsesBody((accountcore.ProtocolTarget{Record: deepseekResponses}).UsesNativeCNResponses(), body)
	require.False(t, gjson.GetBytes(normalized, "store").Bool())
	require.False(t, gjson.GetBytes(normalized, "previous_response_id").Exists())
	require.Equal(t, "deepseek-v4-pro", gjson.GetBytes(normalized, "model").String())

	deepseekAdaptive := &accountcore.Record{
		Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": accountcore.APIProtocolAdaptive},
	}
	adaptiveNormalized := forward.NormalizeCNResponsesBody((accountcore.ProtocolTarget{Record: deepseekAdaptive}).UsesNativeCNResponses(), body)
	require.False(t, gjson.GetBytes(adaptiveNormalized, "store").Bool())
	require.False(t, gjson.GetBytes(adaptiveNormalized, "previous_response_id").Exists())

	// 非 responses 协议（deepseek CC 账号）原样返回
	deepseekCC := &accountcore.Record{Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey}
	require.Equal(t, string(body), string(forward.NormalizeCNResponsesBody((accountcore.ProtocolTarget{Record: deepseekCC}).UsesNativeCNResponses(), body)))

	kimiResponses := &accountcore.Record{
		Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": accountcore.APIProtocolResponses},
	}
	kimiNormalized := forward.NormalizeCNResponsesBody((accountcore.ProtocolTarget{Record: kimiResponses}).UsesNativeCNResponses(), body)
	require.False(t, gjson.GetBytes(kimiNormalized, "store").Bool())
	require.False(t, gjson.GetBytes(kimiNormalized, "previous_response_id").Exists())

	kimiCodingAdaptive := &accountcore.Record{
		Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": accountcore.APIProtocolAdaptive, "account_mode": accountcore.AccountModeCoding},
	}
	kimiCodingNormalized := forward.NormalizeCNResponsesBody((accountcore.ProtocolTarget{Record: kimiCodingAdaptive}).UsesNativeCNResponses(), body)
	require.False(t, gjson.GetBytes(kimiCodingNormalized, "store").Bool())
	require.False(t, gjson.GetBytes(kimiCodingNormalized, "previous_response_id").Exists())

	// openai 账号原样返回
	openai := &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}
	require.Equal(t, string(body), string(forward.NormalizeCNResponsesBody((accountcore.ProtocolTarget{Record: openai}).UsesNativeCNResponses(), body)))
}
