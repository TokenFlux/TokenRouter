package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayService_Forward_APIKeyMissingInstructionsKeepsLargeInputRaw(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"usage":{"input_tokens":1,"output_tokens":2,"input_tokens_details":{"cached_tokens":0}}}`,
			)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"reasoning":{"effort":"minimal"},"input":[{"type":"message","content":[{"type":"input_text","text":"hi","nonce":9007199254740993}]}]}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	expectedBody := `{"model":"gpt-5","stream":false,"reasoning":{"effort":"minimal"},"input":[{"type":"message","content":[{"type":"input_text","text":"hi","nonce":9007199254740993}]}]}`
	require.JSONEq(t, expectedBody, string(upstream.lastBody))
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
	require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.lastBody, "input.0.content.0.nonce").Raw)
}

func TestOpenAIGatewayService_Forward_AstraReasoningEffortUsesGroupMapping(t *testing.T) {

	for _, effort := range []string{"minimal", "none"} {
		t.Run(effort, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
				},
			}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3,
				Name:        "openai-apikey",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Concurrency: 1,
				Credentials: map[string]any{
					"api_key":  "sk-test",
					"base_url": "https://api.openai.com",
					"model_mapping": map[string]any{
						"client-model": "gpt-6-astra",
					},
				},
				Extra: map[string]any{"use_responses_api": true}},
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

			body := []byte(`{"model":"client-model","stream":false,"reasoning":{"effort":"` + effort + `"},"input":"hi"}`)
			mappedBody, changed, err := requeststate.ApplyOpenAIReasoningEffortPolicy(body, "", []routing.ReasoningEffortMapping{{
				From:      effort,
				To:        "low",
				MatchType: "exact",
				Model:     "client-model",
			}}, "")
			require.NoError(t, err)
			require.True(t, changed)

			result, err := svc.Forward(context.Background(), c, account, mappedBody)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, "low", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
		})
	}
}

func TestOpenAIGatewayService_Forward_PassthroughPreservesAstraInputWithoutGroupMapping(t *testing.T) {

	for _, tt := range []struct {
		effort string
		want   string
	}{
		{effort: "none", want: "none"},
		{effort: "minimal", want: "minimal"},
	} {
		t.Run(tt.effort, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
				},
			}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4,
				Name:        "openai-passthrough",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Concurrency: 1,
				Credentials: map[string]any{
					"api_key":  "sk-test",
					"base_url": "https://api.openai.com",
				},
				Extra: map[string]any{"openai_passthrough": true}},
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

			result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6-astra","reasoning":{"effort":"`+tt.effort+`"},"input":"hi"}`))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, tt.want, gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			require.NotEqual(t, "low", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
		})
	}
}

func TestOpenAIGatewayService_Forward_DecodedMutationKeepsLaterFieldDeletes(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.4","stream":false,"max_completion_tokens":12,"tools":[{"type":"image_generation","format":"png"}],"input":[{"type":"message","content":"draw"}]}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_completion_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tools.0.format").Exists())
	require.Equal(t, "png", gjson.GetBytes(upstream.lastBody, "tools.0.output_format").String())
}

// #4417：/v1/responses 原生转发路径需将 Chat-Completions 风格的 max_tokens 归一化为
// max_output_tokens，并移除兼容上游不接受的 prompt_cache_options。
func TestOpenAIGatewayService_Forward_NormalizesMaxTokensAndStripsPromptCacheOptions(t *testing.T) {

	runForward := func(t *testing.T, body []byte) []byte {
		t.Helper()
		upstream := &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
			},
		}
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = false
		svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4,
			Name:        "openai-apikey",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://example.com",
			},
			Extra: map[string]any{}},
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
		gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

		result, err := svc.Forward(context.Background(), c, account, body)
		require.NoError(t, err)
		require.NotNil(t, result)
		return upstream.lastBody
	}

	t.Run("max_tokens 归一化为 max_output_tokens 并移除 prompt_cache_options", func(t *testing.T) {
		out := runForward(t, []byte(`{"model":"gpt-5.4","stream":false,"max_tokens":256,"prompt_cache_options":{"enabled":true},"input":[{"type":"message","content":"hi"}]}`))
		require.Equal(t, int64(256), gjson.GetBytes(out, "max_output_tokens").Int())
		require.False(t, gjson.GetBytes(out, "max_tokens").Exists())
		require.False(t, gjson.GetBytes(out, "prompt_cache_options").Exists())
	})

	t.Run("同时存在时保留 max_output_tokens 丢弃 max_tokens", func(t *testing.T) {
		out := runForward(t, []byte(`{"model":"gpt-5.4","stream":false,"max_tokens":256,"max_output_tokens":512,"input":[{"type":"message","content":"hi"}]}`))
		require.Equal(t, int64(512), gjson.GetBytes(out, "max_output_tokens").Int())
		require.False(t, gjson.GetBytes(out, "max_tokens").Exists())
	})
}

func TestOpenAIGatewayService_Forward_MappedImageModelUsesImageGate(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"base_url":      "https://example.com",
			"model_mapping": map[string]any{"draw-alias": "gpt-image-2"},
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Set("api_key", &apikey.APIKey{Group: &routing.Group{AllowImageGeneration: false}})
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"draw-alias","stream":false,"input":"draw"}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusForbidden, rec.Code)
	cached, known := gatewayhttp.GetOpenAIImageIntentHint(c)
	require.True(t, known)
	require.False(t, cached)

	textAccount := *account
	textAccount.Record.ID = 4
	textAccount.Record.Credentials = map[string]any{
		"api_key":  "sk-test",
		"base_url": "https://example.com",
	}
	result, err = svc.Forward(context.Background(), c, &textAccount, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Len(t, upstream.bodies, 1)
	cached, known = gatewayhttp.GetOpenAIImageIntentHint(c)
	require.True(t, known)
	require.False(t, cached)
}

func TestOpenAIGatewayService_Forward_TextResponsesSetsBillingModelToMappedModel(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_text_mapped_billing"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"resp_text_mapped","object":"response","model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30}}`,
			)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"base_url":      "https://example.com",
			"model_mapping": map[string]any{"gpt-5.4": "gpt-5.5"},
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.4","stream":false,"input":"hello"}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4", result.Model)
	require.Equal(t, "gpt-5.5", result.BillingModel)
	require.Equal(t, "gpt-5.5", result.UpstreamModel)
	require.Equal(t, "gpt-5.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, 0, result.ImageCount)
}

func TestOpenAIGatewayService_Forward_TextResponsesWithoutMappingKeepsRequestedBillingModel(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_text_unmapped_billing"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_text_unmapped","object":"response","model":"gpt-5.4","status":"completed","usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30}}`)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.4","stream":false,"input":"hello"}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4", result.Model)
	require.Equal(t, "gpt-5.4", result.BillingModel)
	require.Equal(t, "gpt-5.4", result.UpstreamModel)
}

func TestOpenAIGatewayService_Forward_TextResponsesBillingModelMatchesChatCompletions(t *testing.T) {

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"base_url":      "https://example.com",
			"model_mapping": map[string]any{"gpt-5.4": "gpt-5.5"},
		},
		Extra: map[string]any{"use_responses_api": true}},
	}

	responsesUpstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_responses_mapped_billing"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"resp_native","object":"response","model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30}}`,
			)),
		},
	}
	responsesSvc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: responsesUpstream})
	responsesRecorder := httptest.NewRecorder()
	responsesCtx, _ := gin.CreateTestContext(responsesRecorder)
	responsesCtx.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(responsesCtx, gatewayhttp.OpenAIClientTransportHTTP)
	responsesResult, err := responsesSvc.Forward(context.Background(), responsesCtx, account, []byte(`{"model":"gpt-5.4","stream":false,"input":"hello"}`))
	require.NoError(t, err)
	require.NotNil(t, responsesResult)

	chatUpstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_mapped_billing"}},
			Body: io.NopCloser(strings.NewReader(
				`data: {"type":"response.completed","response":{"id":"resp_chat","object":"response","model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30}}}` + "\n\n",
			)),
		},
	}
	chatSvc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: chatUpstream})
	chatRecorder := httptest.NewRecorder()
	chatCtx, _ := gin.CreateTestContext(chatRecorder)
	chatCtx.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", nil)
	chatResult, err := chatSvc.Text.Chat(context.Background(), chatCtx, account, []byte(`{"model":"gpt-5.4","stream":false,"messages":[{"role":"user","content":"hello"}]}`), "", "")
	require.NoError(t, err)
	require.NotNil(t, chatResult)

	require.Equal(t, chatResult.BillingModel, responsesResult.BillingModel)
	require.Equal(t, "gpt-5.5", responsesResult.BillingModel)
	require.Equal(t, "gpt-5.5", chatResult.BillingModel)
}

func TestOpenAIGatewayService_Forward_TextDataImageDoesNotForceMapMarshal(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"input":[{"type":"message","content":[{"type":"input_text","text":"literal data:image/png;base64, only","nonce":1e1000000}]}]}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "1e1000000", gjson.GetBytes(upstream.lastBody, "input.0.content.0.nonce").Raw)
}

func TestOpenAIGatewayService_Forward_ImageToolBillingDoesNotForceFullDecode(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"output":[{"id":"ig_1","type":"image_generation_call","result":"final-image"}],"usage":{"input_tokens":1,"output_tokens":2}}`,
			)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2","size":"2048x1152"}],"input":[{"type":"message","content":[{"type":"input_text","text":"draw","nonce":1e1000000}]}]}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "1e1000000", gjson.GetBytes(upstream.lastBody, "input.0.content.0.nonce").Raw)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "gpt-image-2", result.BillingModel)
}

func TestOpenAIGatewayService_Forward_ImageToolWithImageOnlyModelIsNormalized(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 11,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-image-2","stream":false,"tools":[{"type":"image_generation","model":"gpt-image-2"}],"input":"draw"}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, openai.ImagesResponsesMainModel, gjson.GetBytes(upstream.lastBody, "model").String())
}

func TestOpenAIGatewayService_Forward_HTTPRetryRecoveryDoesNotDecodeBeforeError(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		responses: []*http.Response{
			{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"bad encrypted content"}}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
			},
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 10,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"input":[{"type":"reasoning","encrypted_content":"gAAA","summary":[{"type":"summary_text","text":"keep me"}]},{"type":"message","content":[{"type":"input_text","text":"hi","nonce":9007199254740993}]}]}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "gAAA", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(upstream.bodies[0], "input.1.content.0.nonce").Raw)
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
	require.Equal(t, "summary_text", gjson.GetBytes(upstream.bodies[1], "input.0.summary.0.type").String())
}

func TestOpenAIGatewayService_Forward_HTTPRetryRecoveryDropsCompaction(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		responses: []*http.Response{
			{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"bad encrypted content"}}`)),
			},
			{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
			},
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 10,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"input":[{"id":"cmp_stale","type":"compaction","encrypted_content":"gAAA"},{"type":"message","content":[{"type":"input_text","text":"hi"}]}]}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, "compaction", gjson.GetBytes(upstream.bodies[0], "input.0.type").String())
	require.Equal(t, "message", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.1").Exists())
}

func TestOpenAIGatewayService_Forward_CodexSparkRejectsEscapedInputImage(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.3-codex-spark","stream":false,"input":[{"type":"input_` + "\\u0069" + `mage","file_id":"file_1"}]}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOpenAIGatewayService_Forward_CodexBridgeInjectionSetsImageBilling(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"output":[{"id":"ig_1","type":"image_generation_call","result":"final-image","size":"1024x1024"}],"usage":{"input_tokens":1,"output_tokens":2}}`,
			)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.ForceCodexCLI = true
	cfg.Gateway.CodexImageGenerationBridgeEnabled = true
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Set("api_key", &apikey.APIKey{Group: &routing.Group{AllowImageGeneration: true}})
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5","stream":false,"input":"draw if needed"}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "gpt-image-2", result.BillingModel)
}

func TestOpenAIGatewayService_Forward_HTTPPreservesPreviousResponseIDForAPIKey(t *testing.T) {

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 8,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}

	for _, body := range [][]byte{
		[]byte(`{"model":"gpt-5","stream":false,"previous_response_id":"","input":"hi"}`),
		[]byte(`{"model":"gpt-5","stream":false,"previous_response_id":null,"input":"hi"}`),
	} {
		upstream := &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
			},
		}
		svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
		gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

		result, err := svc.Forward(context.Background(), c, account, body)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.True(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
	}
}

func TestOpenAIGatewayService_Forward_StripsImageGenerationToolForSparkAPIKey(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 11,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	// 开启图片生成以复现工具被规范化保留的路径，确保 Spark 剥离逻辑能覆盖该泄漏。
	c.Set("api_key", &apikey.APIKey{Group: &routing.Group{AllowImageGeneration: true}})
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-5.3-codex-spark","stream":false,"input":"hi","tools":[{"type":"function","name":"shell"},{"type":"image_generation","output_format":"png"}]}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
	require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="function")`).Exists())
}

func TestOpenAIRequestBodyMayContainEmptyBase64InputImageSeesEscapedJSON(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","content":[{"type":"input_image","image_` + "\\u0075" + `rl":"data:image/png;base64` + "\\u002c" + `   "}]}]}`)

	require.True(t, openai.OpenAIRequestBodyMayContainEmptyBase64InputImage(body))
}

func TestOpenAIRequestBodyMayContainEmptyBase64InputImageSeesEscapedImageType(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","content":[{"type":"input_` + "\\u0069" + `mage","image_url":"data:image/png;base64,   "}]}]}`)

	require.True(t, openai.OpenAIRequestBodyMayContainEmptyBase64InputImage(body))
}

func TestOpenAIRequestBodyMayContainEmptyBase64InputImageSeesEscapedInputPrefix(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","content":[{"type":"inp` + "\\u0075" + `t_image","image_url":"data:image/png;base64,   "}]}]}`)

	require.True(t, openai.OpenAIRequestBodyMayContainEmptyBase64InputImage(body))
}

func TestOpenAIGatewayService_Forward_ImageOnlyModelKeepsSupportedVerbosity(t *testing.T) {

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":2}}`)),
		},
	}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 6,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://example.com",
		},
		Extra: map[string]any{"use_responses_api": true}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)

	body := []byte(`{"model":"gpt-image-2","stream":false,"text":{"verbosity":"low"},"input":"draw"}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "low", gjson.GetBytes(upstream.lastBody, "text.verbosity").String())
	require.Equal(t, openai.ImagesResponsesMainModel, gjson.GetBytes(upstream.lastBody, "model").String())
}

func TestOpenAIGatewayEntrypointsRejectUltraBeforeUpstream(t *testing.T) {

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type:  capability.AccountTypeAPIKey,
		Extra: map[string]any{"openai_text_route_mode": "force_chat_completions"}},
	}

	t.Run("Responses", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.6-sol","input":"hi","reasoning":{"effort":"ultra"}}`)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))

		result, err := svc.Forward(context.Background(), c, account, body)
		require.ErrorContains(t, err, "not supported")
		require.Nil(t, result)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("Chat Completions 原始透传", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.6-sol","messages":[],"reasoning_effort":"ultra"}`)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))

		result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
		require.ErrorContains(t, err, "not supported")
		require.Nil(t, result)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("Anthropic Messages", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.6-sol","max_tokens":1024,"messages":[],"output_config":{"effort":"ultra"}}`)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))

		result, err := svc.Text.Messages(context.Background(), c, account, body, "", "")
		require.ErrorContains(t, err, "not supported")
		require.Nil(t, result)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestGetOpenAIRequestBodyMap_DoesNotWriteContextCache(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	adapter := openAIForwardTransformAdapter{openAIForwardPreludeAdapter: openAIForwardPreludeAdapter{c: c}}
	got, err := adapter.Decode([]byte(`{"model":"gpt-5","stream":true}`))
	require.NoError(t, err)
	require.Equal(t, "gpt-5", got["model"])
	require.Empty(t, c.Keys)
}

func TestSanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(t *testing.T) {
	var reqBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5.4",
		"input":[
			{"role":"user","content":[
				{"type":"input_text","text":"Describe this"},
				{"type":"input_image","image_url":"data:image/png;base64,   "},
				{"type":"input_image","image_url":"data:image/png;base64,abc123"}
			]},
			{"role":"user","content":[
				{"type":"input_image","image_url":"data:image/png;base64,"}
			]},
			{"type":"input_image","image_url":"data:image/png;base64,"},
			{"type":"input_image","image_url":"data:image/png;base64,top-level-valid"}
		]
	}`), &reqBody))

	require.True(t, openai.SanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(reqBody))

	normalized, err := json.Marshal(reqBody)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"gpt-5.4",
		"input":[
			{"role":"user","content":[
				{"type":"input_text","text":"Describe this"},
				{"type":"input_image","image_url":"data:image/png;base64,abc123"}
			]},
			{"type":"input_image","image_url":"data:image/png;base64,top-level-valid"}
		]
	}`, string(normalized))
}

func TestSanitizeEmptyBase64InputImagesInOpenAIBody(t *testing.T) {
	body, changed, err := openai.SanitizeEmptyBase64InputImagesInOpenAIBody([]byte(`{
		"model":"gpt-5.4",
		"stream":true,
		"input":[
			{"role":"user","content":[
				{"type":"input_text","text":"Describe this"},
				{"type":"input_image","image_url":"data:image/png;base64,"}
			]}
		]
	}`))
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, `{
		"model":"gpt-5.4",
		"stream":true,
		"input":[
			{"role":"user","content":[
				{"type":"input_text","text":"Describe this"}
			]}
		]
	}`, string(body))
}
