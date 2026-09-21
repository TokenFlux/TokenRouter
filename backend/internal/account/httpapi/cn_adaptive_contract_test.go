//go:build unit

package httpapi

import (
	"io"
	"net/http"
	"strings"
	"testing"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func adaptiveCNAccountTestAccount(id int64, platform string) *accountcore.Record {
	return &accountcore.Record{
		ID:          id,
		Name:        "adaptive-cn-test",
		Platform:    platform,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-adaptive-test",
			"api_protocol": accountcore.APIProtocolAdaptive,
			"api_base_urls": map[string]any{
				accountcore.APIProtocolChatCompletions: "http://chat.example/v1",
				accountcore.APIProtocolAnthropic:       "http://anthropic.example",
				accountcore.APIProtocolResponses:       "http://responses.example",
			},
		},
	}
}

func adaptiveCNAccountTestService(account *accountcore.Record, responses ...*http.Response) (*accountprovider.OpenAIAccountTest, *openAIProbeTransport) {
	repo := &openAIProbeStore{
		openAIProbeRecords: openAIProbeRecords{
			accountsByID: map[int64]*accountcore.Record{account.ID: account},
		},
	}
	upstream := &openAIProbeTransport{responses: responses}
	return &accountprovider.OpenAIAccountTest{
		Store:       repo,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{AllowInsecureHTTP: true}).Validate,
	}, upstream
}

func adaptiveCNChatTestResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(`data: {"choices":[{"delta":{"content":"chat ok"},"finish_reason":"stop"}]}

data: [DONE]

`)),
	}
}

func adaptiveCNAnthropicTestResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(`data: {"type":"content_block_delta","delta":{"text":"anthropic ok"}}

data: {"type":"message_stop"}

`)),
	}
}

func adaptiveCNResponsesTestResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"responses ok"}

data: {"type":"response.completed"}

`)),
	}
}

func TestAccountTestService_AdaptiveChatOnlyProvidersTestChatAndAnthropicEndpoints(t *testing.T) {
	account := adaptiveCNAccountTestAccount(301, capability.PlatformZhipu)
	svc, upstream := adaptiveCNAccountTestService(
		account,
		adaptiveCNChatTestResponse(),
		adaptiveCNAnthropicTestResponse(),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "glm-4.7", "hello", accountcore.AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "http://chat.example/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, "http://anthropic.example/v1/messages", upstream.requests[1].URL.String())
	require.Equal(t, "Bearer sk-adaptive-test", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "sk-adaptive-test", anthropic.GetHeaderRaw(upstream.requests[1].Header, "x-api-key"))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_start"`))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
	require.Contains(t, recorder.Body.String(), "已通过原生 /v1/messages 验证")
}

func TestAccountTestService_AdaptiveDeepSeekAlsoTestsResponsesEndpoint(t *testing.T) {
	account := adaptiveCNAccountTestAccount(302, capability.PlatformDeepseek)
	svc, upstream := adaptiveCNAccountTestService(
		account,
		adaptiveCNChatTestResponse(),
		adaptiveCNAnthropicTestResponse(),
		adaptiveCNResponsesTestResponse(),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "deepseek-chat", "", accountcore.AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "http://responses.example/responses", upstream.requests[2].URL.String())
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.requests[2].Context()))
	require.Equal(t, "Bearer sk-adaptive-test", upstream.requests[2].Header.Get("Authorization"))
	require.True(t, gjson.GetBytes(upstream.bodies[2], "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "store").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "instructions").Exists())
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
	require.Contains(t, recorder.Body.String(), "已通过原生 /responses 验证")
}

func TestAccountTestService_AdaptiveKimiAlsoTestsResponsesEndpoint(t *testing.T) {
	account := adaptiveCNAccountTestAccount(306, capability.PlatformKimi)
	svc, upstream := adaptiveCNAccountTestService(
		account,
		adaptiveCNChatTestResponse(),
		adaptiveCNAnthropicTestResponse(),
		adaptiveCNResponsesTestResponse(),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "k3-256k", "", accountcore.AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "http://responses.example/v1/responses", upstream.requests[2].URL.String())
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.requests[2].Context()))
	require.Equal(t, "Bearer sk-adaptive-test", upstream.requests[2].Header.Get("Authorization"))
	require.True(t, gjson.GetBytes(upstream.bodies[2], "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "store").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "instructions").Exists())
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
	require.Contains(t, recorder.Body.String(), "已通过原生 /responses 验证")
}

func TestAccountTestService_AdaptiveStopsAndNamesFailingEndpoint(t *testing.T) {
	account := adaptiveCNAccountTestAccount(303, capability.PlatformDeepseek)
	svc, upstream := adaptiveCNAccountTestService(
		account,
		adaptiveCNChatTestResponse(),
		newJSONResponse(http.StatusNotFound, `{"error":{"message":"missing messages route"}}`),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "deepseek-chat", "", accountcore.AccountTestModeDefault)

	require.Error(t, err)
	require.Contains(t, err.Error(), "Adaptive Anthropic endpoint returned 404")
	require.Len(t, upstream.requests, 2)
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.NotContains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestService_AdaptiveRejectsInvalidAnthropicSuccessBody(t *testing.T) {
	account := adaptiveCNAccountTestAccount(305, capability.PlatformKimi)
	svc, upstream := adaptiveCNAccountTestService(
		account,
		adaptiveCNChatTestResponse(),
		newJSONResponse(http.StatusOK, `<html>not an Anthropic stream</html>`),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "kimi-k2.5", "", accountcore.AccountTestModeDefault)

	require.Error(t, err)
	require.Contains(t, err.Error(), "Adaptive Anthropic stream ended before message_stop")
	require.Len(t, upstream.requests, 2)
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.NotContains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestService_FixedCNChatProtocolStillTestsOnlyChatEndpoint(t *testing.T) {
	account := adaptiveCNAccountTestAccount(304, capability.PlatformZhipu)
	account.Credentials["api_protocol"] = accountcore.APIProtocolChatCompletions
	account.Credentials["base_url"] = "http://fixed-chat.example/v1"
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "glm-4.7", "", accountcore.AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://fixed-chat.example/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
}

func TestAccountTestService_FixedCNAnthropicUsesNativeEndpointWithoutBetaQuery(t *testing.T) {
	account := adaptiveCNAccountTestAccount(306, capability.PlatformZhipu)
	account.Credentials["api_protocol"] = accountcore.APIProtocolAnthropic
	account.Credentials["base_url"] = "https://open.bigmodel.cn/api/anthropic"
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNAnthropicTestResponse())
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "glm-4.7", "", accountcore.AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic/v1/messages", upstream.requests[0].URL.String())
	require.Empty(t, upstream.requests[0].URL.RawQuery)
	require.Equal(t, "sk-adaptive-test", anthropic.GetHeaderRaw(upstream.requests[0].Header, "x-api-key"))
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestService_FixedCNAnthropicUsesProviderDefault(t *testing.T) {
	account := adaptiveCNAccountTestAccount(307, capability.PlatformZhipu)
	account.Credentials["api_protocol"] = accountcore.APIProtocolAnthropic
	delete(account.Credentials, "api_base_urls")
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNAnthropicTestResponse())
	c, _ := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "glm-4.7", "", accountcore.AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic/v1/messages", upstream.requests[0].URL.String())
}

func TestAccountTestService_FixedCNAnthropicRejectsOpenAIBaseURLAndMarksAuthErrors(t *testing.T) {
	t.Run("misconfigured base URL", func(t *testing.T) {
		account := adaptiveCNAccountTestAccount(308, capability.PlatformZhipu)
		account.Credentials["api_protocol"] = accountcore.APIProtocolAnthropic
		account.Credentials["base_url"] = "https://open.bigmodel.cn/api/paas/v4"
		svc, upstream := adaptiveCNAccountTestService(account)
		c, recorder := newTestContext()

		err := executeOpenAIProbeRequest(t, svc, c, account.ID, "glm-4.7", "", accountcore.AccountTestModeDefault)

		require.Error(t, err)
		require.Empty(t, upstream.requests)
		require.Contains(t, recorder.Body.String(), "looks like an OpenAI-compatible endpoint")
	})

	t.Run("forbidden marks account error", func(t *testing.T) {
		account := adaptiveCNAccountTestAccount(309, capability.PlatformZhipu)
		account.Credentials["api_protocol"] = accountcore.APIProtocolAnthropic
		account.Credentials["base_url"] = "https://open.bigmodel.cn/api/anthropic"
		svc, _ := adaptiveCNAccountTestService(account, newJSONResponse(http.StatusForbidden, `{"error":{"message":"invalid key"}}`))
		c, _ := newTestContext()

		err := executeOpenAIProbeRequest(t, svc, c, account.ID, "glm-4.7", "", accountcore.AccountTestModeDefault)

		require.Error(t, err)
		repo := testassert.MustType[*openAIProbeStore](svc.Store)
		require.Equal(t, account.ID, repo.setErrorID)
	})
}
