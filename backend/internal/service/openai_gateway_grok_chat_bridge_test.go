//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardGrokChatViaResponsesNonStreamingCachesAndReturnsChat(t *testing.T) {

	body := []byte(`{"model":"grok","messages":[{"role":"system","content":"be concise"},{"role":"user","content":"hi"}],"stream":false,"prompt_cache_key":"stable-session","tools":[],"functions":null,"tool_choice":"none"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 7101})

	account := grokChatBridgeTestAccount(71)
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &httpUpstreamRecorder{resp: grokChatBridgeCompletedResponse("resp_grok_chat_cache", 9856)}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, xai.GrokChatResponsesEndpoint, result.UpstreamEndpoint)
	require.Equal(t, "grok", result.BillingModel)
	require.Equal(t, "grok-4.5", result.UpstreamModel)
	require.Equal(t, 9908, result.Usage.InputTokens)
	require.Equal(t, 12, result.Usage.OutputTokens)
	require.Equal(t, 9856, result.Usage.CacheReadInputTokens)

	identity := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, identity)
	require.NotEqual(t, "stable-session", identity)
	require.Equal(t, identity, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "web_search", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "x_search", gjson.GetBytes(upstream.lastBody, "tools.1.type").String())
	require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "system", gjson.GetBytes(upstream.lastBody, "input.0.role").String())
	require.Equal(t, "user", gjson.GetBytes(upstream.lastBody, "input.1.role").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "include").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Exists())

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "cached ok", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
	require.Equal(t, int64(9856), gjson.Get(recorder.Body.String(), "usage.prompt_tokens_details.cached_tokens").Int())
	require.NotNil(t, repo.updates[account.Record.ID][grokQuotaSnapshotExtraKey])
}

func TestForwardGrokChatViaResponsesNonStreamingRejectsCompletedResponseWithoutUsage(t *testing.T) {

	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false,"prompt_cache_key":"stable-session"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 7102})

	account := grokChatBridgeTestAccount(72)
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstreamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","sequence_number":0,"delta":"ok"}`,
		"",
		`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_missing_usage","object":"response","model":"grok-4.5","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "Xai-Request-Id": []string{"rid-responses-missing-usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	service := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := service.Text.Chat(context.Background(), c, account, body, "", "")

	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Equal(t, "grok_missing_usage", gjson.GetBytes(failoverErr.ResponseBody, "error.code").String())
	require.Equal(t, "rid-responses-missing-usage", http.Header(failoverErr.ResponseHeaders).Get("x-request-id"))
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestForwardGrokChatImageWithoutCacheIdentityUsesResponses(t *testing.T) {

	body := []byte(`{"model":"grok","messages":[{"role":"user","content":[{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,QQ=="}}]}],"stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))

	account := grokChatBridgeTestAccount(711)
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &httpUpstreamRecorder{resp: grokChatBridgeCompletedResponse("resp_grok_chat_image", 0)}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, xai.GrokChatResponsesEndpoint, result.UpstreamEndpoint)
	require.Equal(t, "input_text", gjson.GetBytes(upstream.lastBody, "input.0.content.0.type").String())
	require.Equal(t, "what is this", gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String())
	require.Equal(t, "input_image", gjson.GetBytes(upstream.lastBody, "input.0.content.1.type").String())
	require.Equal(t, "data:image/png;base64,QQ==", gjson.GetBytes(upstream.lastBody, "input.0.content.1.image_url").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").Exists())
	require.Empty(t, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
}

func TestForwardGrokChatViaResponsesCodeBuddyUsesStableConversationHeader(t *testing.T) {

	const conversationID = "codebuddy-session-42"
	tests := []struct {
		name      string
		requestID string
		messageID string
		body      []byte
	}{
		{
			name:      "first turn",
			requestID: "request-one",
			messageID: "message-one",
			body:      []byte(`{"model":"grok","messages":[{"role":"user","content":"first question"}],"stream":false,"tools":[]}`),
		},
		{
			name:      "later turn with tools",
			requestID: "request-two",
			messageID: "message-two",
			body:      []byte(`{"model":"grok","messages":[{"role":"system","content":"be concise"},{"role":"user","content":"different later question"}],"stream":false,"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup a value","parameters":{"type":"object","properties":{"key":{"type":"string"}}}}}],"tool_choice":"auto"}`),
		},
	}

	var stableIdentity string
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(tt.body))
			c.Request.Header.Set(gatewayhttp.CodeBuddyConversationHeader, conversationID)
			c.Request.Header.Set("X-Conversation-Request-ID", tt.requestID)
			c.Request.Header.Set("X-Conversation-Message-ID", tt.messageID)
			c.Request.Header.Set("X-Request-ID", "generic-"+tt.requestID)
			c.Set("api_key", &apikey.APIKey{ID: 7111})

			identity := gatewayhttp.ResolveGrokCacheIdentity(c, tt.body, "", "grok-4.5")
			require.NotEmpty(t, identity)
			if index == 0 {
				stableIdentity = identity
			} else {
				require.Equal(t, stableIdentity, identity)
			}
			require.NotContains(t, identity, conversationID)

			account := grokChatBridgeTestAccount(int64(711 + index))
			repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
				accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
			}}
			upstream := &httpUpstreamRecorder{resp: grokChatBridgeCompletedResponse("resp_codebuddy_"+strconv.Itoa(index), 4096)}
			svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
				httpUpstream: upstream,

				accountRepo: repo,
			}), newGrokTokenSourceForTest(repo, nil))

			result, err := svc.Text.Chat(context.Background(), c, account, tt.body, "", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
			require.Equal(t, xai.GrokChatResponsesEndpoint, result.UpstreamEndpoint)
			require.Equal(t, identity, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
			require.Equal(t, identity, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
		})
	}
}

func TestForwardGrokChatViaResponsesTraeToolHistoryKeepsCacheRoute(t *testing.T) {

	firstTurnBody := []byte(`{"model":"grok","messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"Find alpha"}],"stream":false,"prompt_cache_key":"trae-session","tools":[{"type":"function","function":{"name":"lookup","description":"Lookup a value","parameters":{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]},"strict":false}}],"tool_choice":"auto","parallel_tool_calls":true}`)
	body := []byte(`{"model":"grok","messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"Find alpha"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"key\":\"alpha\"}"}}]},{"role":"tool","tool_call_id":"call_lookup","content":"{\"value\":\"ok\"}"},{"role":"user","content":"Summarize"}],"stream":false,"prompt_cache_key":"trae-session","tools":[{"type":"function","function":{"name":"lookup","description":"Lookup a value","parameters":{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]},"strict":false}}],"tool_choice":"auto","parallel_tool_calls":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))
	c.Request.Header.Set("X-Sub2API-Grok-Client-Tool-Cache", "prefer-cache")
	c.Set("api_key", &apikey.APIKey{ID: 7151})

	account := grokChatBridgeTestAccount(715)
	account.Record.Credentials["subscription_tier"] = "free"
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &httpUpstreamRecorder{resp: grokChatBridgeCompletedResponse("resp_grok_chat_trae", 8192)}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	firstTurnIdentity := gatewayhttp.ResolveGrokCacheIdentity(c, firstTurnBody, "", "grok-4.5")
	extendedTurnIdentity := gatewayhttp.ResolveGrokCacheIdentity(c, body, "", "grok-4.5")
	require.NotEmpty(t, firstTurnIdentity)
	require.Equal(t, firstTurnIdentity, extendedTurnIdentity)

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, xai.GrokChatResponsesEndpoint, result.UpstreamEndpoint)
	require.Equal(t, extendedTurnIdentity, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, extendedTurnIdentity, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))

	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "Lookup a value", tools[0].Get("description").String())
	require.Equal(t, "string", tools[0].Get("parameters.properties.key.type").String())
	require.True(t, tools[0].Get("strict").Exists())
	require.False(t, tools[0].Get("strict").Bool())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "x_search", tools[2].Get("type").String())
	require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "parallel_tool_calls").Bool())

	require.Equal(t, "function_call", gjson.GetBytes(upstream.lastBody, "input.2.type").String())
	require.Equal(t, "call_lookup", gjson.GetBytes(upstream.lastBody, "input.2.call_id").String())
	require.Equal(t, "lookup", gjson.GetBytes(upstream.lastBody, "input.2.name").String())
	require.Equal(t, `{"key":"alpha"}`, gjson.GetBytes(upstream.lastBody, "input.2.arguments").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.lastBody, "input.3.type").String())
	require.Equal(t, "call_lookup", gjson.GetBytes(upstream.lastBody, "input.3.call_id").String())
	require.Equal(t, `{"value":"ok"}`, gjson.GetBytes(upstream.lastBody, "input.3.output").String())
}

func TestForwardGrokChatViaResponsesTraeCompatibilityFieldsKeepCacheRoute(t *testing.T) {

	firstTurnBody := []byte(`{"model":"grok","messages":[{"role":"user","content":"Find alpha"}],"instructions":"Return concise JSON","stream":false,"response_format":{"type":"json_object"},"service_tier":"fast","stop":null,"reasoning_effort":null,"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup a value","parameters":{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}}}],"tool_choice":"auto","parallel_tool_calls":true}`)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"Find alpha"},{"role":"assistant","content":null,"reasoning_content":"I should use lookup","tool_calls":[{"index":0,"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"key\":\"alpha\"}"}}]},{"role":"tool","tool_call_id":"call_lookup","content":"{\"value\":\"ok\"}"},{"role":"user","content":"Summarize"}],"instructions":"Return concise JSON","stream":false,"response_format":{"type":"json_object"},"service_tier":"fast","stop":null,"reasoning_effort":null,"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup a value","parameters":{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}}}],"tool_choice":"auto","parallel_tool_calls":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 7161})

	account := grokChatBridgeTestAccount(716)
	account.Record.Credentials["subscription_tier"] = "free"
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &httpUpstreamRecorder{resp: grokChatBridgeCompletedResponse("resp_grok_chat_trae_compat", 12288)}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	firstTurnIdentity := gatewayhttp.ResolveGrokCacheIdentity(c, firstTurnBody, "", "grok-4.5")
	extendedTurnIdentity := gatewayhttp.ResolveGrokCacheIdentity(c, body, "", "grok-4.5")
	require.NotEmpty(t, firstTurnIdentity)
	require.Equal(t, firstTurnIdentity, extendedTurnIdentity)

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, xai.GrokChatResponsesEndpoint, result.UpstreamEndpoint)
	require.Equal(t, 12288, result.Usage.CacheReadInputTokens)
	require.Equal(t, extendedTurnIdentity, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, extendedTurnIdentity, upstream.lastReq.Header.Get(gatewayhttp.GrokConversationIDHeader))
	require.Equal(t, "Return concise JSON", gjson.GetBytes(upstream.lastBody, "instructions").String())
	require.Equal(t, "json_object", gjson.GetBytes(upstream.lastBody, "text.format.type").String())
	require.Equal(t, "priority", gjson.GetBytes(upstream.lastBody, "service_tier").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "stop").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning").Exists())
	require.Contains(t, gjson.GetBytes(upstream.lastBody, "input.1.content").String(), "<thinking>I should use lookup</thinking>")
	require.Equal(t, "function_call", gjson.GetBytes(upstream.lastBody, "input.2.type").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(upstream.lastBody, "input.3.type").String())

	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "lookup", tools[0].Get("name").String())
	require.Equal(t, "web_search", tools[1].Get("type").String())
	require.Equal(t, "x_search", tools[2].Get("type").String())
	require.Equal(t, int64(12288), gjson.Get(recorder.Body.String(), "usage.prompt_tokens_details.cached_tokens").Int())
}

func TestForwardGrokChatViaResponsesStreamingPropagatesCachedUsage(t *testing.T) {

	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 7201})

	account := grokChatBridgeTestAccount(72)
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &httpUpstreamRecorder{resp: grokChatBridgeCompletedResponse("resp_grok_chat_stream", 4096)}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, xai.GrokChatResponsesEndpoint, result.UpstreamEndpoint)
	require.Equal(t, 4096, result.Usage.CacheReadInputTokens)
	require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, recorder.Body.String(), `"content":"cached ok"`)
	require.Contains(t, recorder.Body.String(), `"cached_tokens":4096`)
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
}

func TestForwardGrokChatRuntimeGateFallsBackToRaw(t *testing.T) {

	tests := []struct {
		name         string
		setAPIKey    bool
		mappedModel  string
		wantUpstream string
	}{
		{name: "missing cache identity", wantUpstream: "grok-4.5"},
		{name: "non cache capable mapped model", setAPIKey: true, mappedModel: "grok-4.3", wantUpstream: "grok-4.3"},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))
			if tt.setAPIKey {
				c.Set("api_key", &apikey.APIKey{ID: int64(7301 + index)})
			}

			account := grokChatBridgeTestAccount(int64(73 + index))
			if tt.mappedModel != "" {
				account.Record.Credentials["model_mapping"] = map[string]any{"grok": tt.mappedModel}
			}
			repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
				accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
			}}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"chat_raw","object":"chat.completion","model":"` + tt.wantUpstream + `","choices":[{"index":0,"message":{"role":"assistant","content":"raw ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
				)),
			}}
			svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
				httpUpstream: upstream,

				accountRepo: repo,
			}), newGrokTokenSourceForTest(repo, nil))

			result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, xai.DefaultCLIBaseURL+"/chat/completions", upstream.lastReq.URL.String())
			require.Equal(t, grokChatRawEndpoint, result.UpstreamEndpoint)
			require.Equal(t, tt.wantUpstream, result.UpstreamModel)
			require.False(t, gjson.GetBytes(upstream.lastBody, "tools").Exists())
			require.Equal(t, "raw ok", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
		})
	}
}

func TestForwardGrokChatViaResponses429UsesGrokRateLimitPolicy(t *testing.T) {

	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 7501})

	account := grokChatBridgeTestAccount(75)
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Retry-After":  []string{"45"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))
	before := time.Now()

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.Error(t, err)
	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Equal(t, "45", http.Header(failoverErr.ResponseHeaders).Get("Retry-After"))
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, xai.GrokChatResponsesEndpoint, gatewayhttp.GetActualOpenAIUpstreamEndpoint(c))
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Zero(t, repo.tempUnschedCalls)
	require.WithinDuration(t, before.Add(45*time.Second), repo.lastRateLimitResetAt, time.Second)
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestForwardGrokRawChat429PreservesRetryAfter(t *testing.T) {

	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false,"stop":"done"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 7551})

	account := grokChatBridgeTestAccount(755)
	account.Record.Credentials["expires_at"] = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Retry-After":  []string{"45"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	require.Nil(t, result)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Equal(t, "45", http.Header(failoverErr.ResponseHeaders).Get("Retry-After"))
	require.Equal(t, xai.DefaultCLIBaseURL+"/chat/completions", upstream.lastReq.URL.String())
}

func TestForwardGrokRawChatErrorRecordsActualEndpoint(t *testing.T) {

	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":false,"stop":"done"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, grokChatRawEndpoint, bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 7601})

	account := grokChatBridgeTestAccount(76)
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account},
	}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad request"}}`)),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Text.Chat(context.Background(), c, account, body, "", "")
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, xai.DefaultCLIBaseURL+"/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, grokChatRawEndpoint, gatewayhttp.GetActualOpenAIUpstreamEndpoint(c))
}

func grokChatBridgeTestAccount(id int64) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id,
		Name:        "grok-cache-bridge",
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			"base_url":      xai.DefaultCLIBaseURL,
		}},
	}
}

func grokChatBridgeCompletedResponse(responseID string, cachedTokens int) *http.Response {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","sequence_number":0,"delta":"cached ok"}`,
		"",
		`data: {"type":"response.completed","sequence_number":1,"response":{"id":"` + responseID + `","object":"response","model":"grok-4.5","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"cached ok"}]}],"usage":{"input_tokens":9908,"output_tokens":12,"total_tokens":9920,"input_tokens_details":{"cached_tokens":` + strconv.Itoa(cachedTokens) + `}}}}`,
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"Xai-Request-Id":                 []string{responseID + "-request"},
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"9"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}
