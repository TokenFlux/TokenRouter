//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestApplyCodexOAuthTransform_PreservesLiteNamespaceToolChoice(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.6-terra",
		"input": []any{map[string]any{
			"type": "additional_tools",
			"tools": []any{map[string]any{
				"type": "namespace",
				"name": "collaboration",
			}},
		}},
		"tool_choice": map[string]any{"type": "namespace", "name": "collaboration"},
	}

	gatewayprovider.ApplyCodexOAuthTransform(reqBody, true, false)

	require.Equal(t, map[string]any{"type": "namespace", "name": "collaboration"}, reqBody["tool_choice"])
}

func TestOpenAIGatewayServiceForward_NormalizesResponsesLiteToolsForOAuth(t *testing.T) {

	for _, passthrough := range []bool{false, true} {
		name := "managed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
			c.Request.Header.Set(media.ResponsesLiteHeader, "true")
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_lite\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
						"data: [DONE]\n\n",
				)),
			}}
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 501, Name: "responses-lite", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
				Concurrency: 1, Status: billing.StatusActive, Schedulable: true, RateMultiplier: f64p(1),
				Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-account"},
				Extra:       map[string]any{"openai_passthrough": passthrough}},
			}
			body := []byte(`{
				"model":"gpt-5.6-terra","stream":true,"instructions":"test",
				"parallel_tool_calls":true,
				"reasoning":{"effort":"high","context":"current_turn"},
				"tools":[
					{"type":"function","name":"shell","parameters":{"type":"object"}},
					{"type":"custom","name":"exec"},
					{"type":"tool_search"},
					{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent","parameters":{"type":"object"}}]}
				],
				"input":[{"type":"message","role":"user","content":"hello"}],
				"tool_choice":{"type":"namespace","name":"collaboration"}
			}`)

			result, err := svc.Forward(context.Background(), c, account, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "true", upstream.lastReq.Header.Get(media.ResponsesLiteHeader))
			require.True(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Bool())
			require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			require.Equal(t, "all_turns", gjson.GetBytes(upstream.lastBody, "reasoning.context").String())
			require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="namespace")`).Exists())
			require.Equal(t, "shell", gjson.GetBytes(upstream.lastBody, `tools.#(type=="function").name`).String())
			require.Equal(t, "exec", gjson.GetBytes(upstream.lastBody, `tools.#(type=="custom").name`).String())
			require.True(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="tool_search")`).Exists())
			require.Equal(t, "collaboration", gjson.GetBytes(upstream.lastBody, `input.#(type=="additional_tools").tools.0.name`).String())
			require.Equal(t, "namespace", gjson.GetBytes(upstream.lastBody, "tool_choice.type").String())
			require.Equal(t, "collaboration", gjson.GetBytes(upstream.lastBody, "tool_choice.name").String())

			badRec := httptest.NewRecorder()
			badCtx, _ := gin.CreateTestContext(badRec)
			badCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			badCtx.Request.Header.Set(media.ResponsesLiteHeader, "true")
			badUpstream := &httpUpstreamRecorder{}
			svc.httpUpstream = badUpstream
			if svc.Requests != nil {
				svc.Requests.Transport = svc.httpUpstream
			}
			if svc.Grok != nil {
				svc.Grok.Transport = svc.httpUpstream
			}

			result, err = svc.Forward(context.Background(), badCtx, account, []byte(`{"model":"gpt-5.6-terra","tools":[{"type":"function","name":"shell"}],"parallel_tool_calls":"false"}`))

			require.ErrorContains(t, err, "parallel_tool_calls to be a boolean")
			require.Nil(t, result)
			require.Equal(t, http.StatusBadRequest, badRec.Code)
			require.Equal(t, "invalid_request_error", gjson.Get(badRec.Body.String(), "error.type").String())
			require.Equal(t, "parallel_tool_calls", gjson.Get(badRec.Body.String(), "error.param").String())
			require.Contains(t, gjson.Get(badRec.Body.String(), "error.message").String(), "parallel_tool_calls to be a boolean")
			require.Nil(t, badUpstream.lastReq)

			for _, malformed := range []struct {
				body      string
				wantParam string
			}{
				{body: `{"model":"gpt-5.6-terra","tools":{}}`, wantParam: "tools"},
				{body: `{"model":"gpt-5.6-terra","reasoning":[]}`, wantParam: "reasoning"},
			} {
				rec := httptest.NewRecorder()
				requestCtx, _ := gin.CreateTestContext(rec)
				requestCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
				requestCtx.Request.Header.Set(media.ResponsesLiteHeader, "true")

				result, err = svc.Forward(context.Background(), requestCtx, account, []byte(malformed.body))

				require.Error(t, err)
				require.Nil(t, result)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Equal(t, malformed.wantParam, gjson.Get(rec.Body.String(), "error.param").String())
			}
		})
	}
}

func TestOpenAIGatewayServiceForward_DisablesResponsesLiteParallelToolCallsForAPIKey(t *testing.T) {

	for _, passthrough := range []bool{false, true} {
		name := "managed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			c.Request.Header.Set(media.ResponsesLiteHeader, "true")
			gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_lite_apikey\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
						"data: [DONE]\n\n",
				)),
			}}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg, httpUpstream: upstream})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 502, Name: "responses-lite-apikey", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
				Concurrency: 1, Status: billing.StatusActive, Schedulable: true, RateMultiplier: f64p(1),
				Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com"},
				Extra: map[string]any{
					"openai_passthrough": passthrough,
				}},
			}
			body := []byte(`{
				"model":"gpt-5.6-terra","stream":true,
				"parallel_tool_calls":true,
				"reasoning":{"context":"current_turn"},
				"input":"hello"
			}`)

			result, err := svc.Forward(context.Background(), c, account, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "true", upstream.lastReq.Header.Get(media.ResponsesLiteHeader))
			require.True(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Bool())
			require.Equal(t, "current_turn", gjson.GetBytes(upstream.lastBody, "reasoning.context").String())
		})
	}
}
