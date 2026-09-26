package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const qoderCachedUsageSSEForTest = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":66637,\\\"completion_tokens\\\":6,\\\"total_tokens\\\":66643,\\\"prompt_tokens_details\\\":{\\\"cached_tokens\\\":66612,\\\"cacheable_tokens\\\":19},\\\"completion_tokens_details\\\":{\\\"reasoning_tokens\\\":0}}}\"}\n\n"

func TestQoderGatewayAllowsExplicitPreviewCompatibilityMapping(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	account := &accountcore.Record{
		ID:       88,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"qwen3.8-max-preview": "qmodel_38max",
			},
			"model_whitelist": []any{},
		},
	}
	client := &gatewaytestkit.QoderClient{
		Body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := gatewaytestkit.NewQoderFixture(accountprovider.NewQoderTokenProvider(qoder.SessionBuilder{}),
		client, nil)

	svc.Tokens.Core.Sessions = map[int64]accountcore.QoderSessionCacheEntry[*qoder.SessionContext]{
		account.ID: {
			CredentialsHash: accountcore.QoderCredentialsHash(account.Credentials),
			Session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
	}
	body := []byte(`{"model":"qwen3.8-max-preview","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, account, body, protocolcore.ProtocolOpenAIChatCompletions)

	require.NoError(t, err)
	require.Equal(t, "qwen3.8-max-preview", result.Model)
	require.Equal(t, "qmodel_38max", result.UpstreamModel)
	require.Equal(t, "qmodel_38max", client.Headers["x-model-key"])
	require.Contains(t, rec.Body.String(), `"model":"qwen3.8-max-preview"`)
	assertQoderContextCapabilityForTest(t, qoderLastUpstreamPayloadForTest(t, client), 1000000, true)
}

func TestQoderGatewayForwardUsesOriginalModelAfterGroupMapping(t *testing.T) {
	tests := []struct {
		name string
		path string
		body []byte
		call func(context.Context, *gatewaytestkit.QoderFixture, *gin.Context, *accountcore.Record, []byte, string) (*forwardcore.MessagesResult, error)
	}{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"qmodel","messages":[{"role":"user","content":"hi"}],"stream":false}`),
			call: func(ctx context.Context, svc *gatewaytestkit.QoderFixture, c *gin.Context, account *accountcore.Record, body []byte, responseModel string) (*forwardcore.MessagesResult, error) {
				return ForwardQoderAttempt(ctx, c, svc.Runtime, account, body, protocolcore.ProtocolOpenAIChatCompletions, responseModel)
			},
		},
		{
			name: "responses",
			path: "/v1/responses",
			body: []byte(`{"model":"qmodel","input":"hi","stream":false}`),
			call: func(ctx context.Context, svc *gatewaytestkit.QoderFixture, c *gin.Context, account *accountcore.Record, body []byte, responseModel string) (*forwardcore.MessagesResult, error) {
				return ForwardQoderAttempt(ctx, c, svc.Runtime, account, body, protocolcore.ProtocolOpenAIResponses, responseModel)
			},
		},
		{
			name: "messages",
			path: "/v1/messages",
			body: []byte(`{"model":"qmodel","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":false}`),
			call: func(ctx context.Context, svc *gatewaytestkit.QoderFixture, c *gin.Context, account *accountcore.Record, body []byte, responseModel string) (*forwardcore.MessagesResult, error) {
				return ForwardQoderAttempt(ctx, c, svc.Runtime, account, body, protocolcore.ProtocolAnthropicMessages, responseModel)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, svc, client := gatewaytestkit.NewDefaultQoderFixture()

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader(tt.body))

			result, err := tt.call(context.Background(), svc, c, account, tt.body, "qwen3.7-plus")

			require.NoError(t, err)
			require.Equal(t, "qwen3.7-plus", result.Model)
			require.Equal(t, "qmodel", result.UpstreamModel)
			require.Equal(t, "qmodel", client.Headers["x-model-key"])
			require.Equal(t, "qwen3.7-plus", gjson.Get(rec.Body.String(), "model").String())
		})
	}
}

func TestQoderGatewayChatCompletionsReusesSessionAndSendsFullReplay(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()

	first := qoderForwardChatCompletionsForTest(t, svc, account, "stable-chat-session", []byte(`{
		"model":"auto",
		"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hello"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "stable-chat-session", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"be terse"},
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"next"}
		],
		"stream":false
	}`))

	require.Len(t, client.Bodies, 2)
	require.Equal(t, first["session_id"], second["session_id"])
	firstMessages := qoderFixtureValue[[]any](t, first["messages"])
	require.Len(t, firstMessages, 2)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, firstMessages[0])["role"])

	secondMessages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, secondMessages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, secondMessages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, secondMessages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, secondMessages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, secondMessages[3])["role"])
	require.Equal(t, "next", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, second["chat_context"])["text"])["text"])
}

func TestQoderGatewayChatCompletionsWithoutSessionDoesNotReuseByFirstText(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()

	first := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"how many turns?"}
		],
		"stream":false
	}`))

	require.NotEqual(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 3)
	require.Equal(t, "hello", qoderPayloadMessageTextForTest(qoderFixtureValue[map[string]any](t, messages[0])))
}

func TestQoderGatewayChatCompletionsMapsUpstreamToolNameToDeclaredOpenAITool(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}],
		"stream":false
	}`)

	result, response := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", body)

	require.False(t, result.Stream)
	require.Equal(t, "tool_calls", gjson.Get(response, "choices.0.finish_reason").String())
	require.Equal(t, "bash", gjson.Get(response, "choices.0.message.tool_calls.0.function.name").String())
	require.NotContains(t, response, `"name":"Bash"`)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Equal(t, "bash", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])["name"])
}

func TestQoderGatewayChatCompletionsConvertsLegacyFunctions(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"weather"}],
		"functions":[{
			"name":"get_weather",
			"description":"Get weather",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}}}
		}],
		"function_call":{"name":"get_weather"},
		"stream":false
	}`)

	result, _ := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", body)

	require.False(t, result.Stream)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Len(t, tools, 1)
	// 兼容旧版 OpenAI Chat Completions 客户端传入的 functions/function_call。
	function := qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])
	require.Equal(t, "get_weather", function["name"])
	require.Equal(t, "Get weather", function["description"])
	require.Equal(t, "object", qoderFixtureValue[map[string]any](t, function["parameters"])["type"])
}

func TestQoderGatewayChatCompletionsLegacyFunctionCallNoneClearsTools(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"plain answer"}],
		"functions":[{"name":"get_weather","parameters":{"type":"object"}}],
		"function_call":"none",
		"stream":false
	}`)

	result, _ := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", body)

	require.False(t, result.Stream)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Empty(t, tools)
}

func TestQoderGatewayMessagesMapsUpstreamToolNameToDeclaredAnthropicTool(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":[{"name":"bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"stream":false
	}`)

	result, response := qoderForwardMessagesResultAndBodyForTest(t, svc, account, body)

	require.False(t, result.Stream)
	require.Equal(t, "tool_use", gjson.Get(response, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.Get(response, "content.0.type").String())
	require.Equal(t, "bash", gjson.Get(response, "content.0.name").String())
	require.NotContains(t, response, `"name":"Bash"`)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Equal(t, "bash", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])["name"])
}

func TestQoderGatewayResponsesMapsUpstreamToolNameToDeclaredFunctionCall(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"stream":false
	}`)

	result, response := qoderForwardResponsesResultAndBodyForTest(t, svc, account, body)

	require.False(t, result.Stream)
	require.Equal(t, "response", gjson.Get(response, "object").String())
	require.Equal(t, "function_call", gjson.Get(response, "output.0.type").String())
	require.Equal(t, "bash", gjson.Get(response, "output.0.name").String())
	require.JSONEq(t, `{"command":"pwd"}`, gjson.Get(response, "output.0.arguments").String())
	require.NotContains(t, response, `"name":"Bash"`)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := qoderFixtureValue[[]any](t, upstream["tools"])
	require.Equal(t, "bash", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])["name"])
}

func TestQoderGatewayResponsesPreviousResponseIDReusesQoderSession(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()

	_, firstResponse := qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"instructions":"be terse",
		"input":"hello",
		"stream":false
	}`))
	firstID := gjson.Get(firstResponse, "id").String()
	require.NotEmpty(t, firstID)
	firstPayload := qoderPayloadAtForTest(t, client, 0)

	_, secondResponse := qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"previous_response_id":`+strconv.Quote(firstID)+`,
		"input":"next",
		"stream":false
	}`))
	secondID := gjson.Get(secondResponse, "id").String()
	require.NotEmpty(t, secondID)
	require.NotEqual(t, firstID, secondID)
	secondPayload := qoderPayloadAtForTest(t, client, 1)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.Equal(t, "next", qoderPayloadPromptForTest(t, secondPayload))

	qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"previous_response_id":`+strconv.Quote(secondID)+`,
		"input":"third",
		"stream":false
	}`))
	thirdPayload := qoderPayloadAtForTest(t, client, 2)
	require.Equal(t, firstPayload["session_id"], thirdPayload["session_id"])
	require.Equal(t, "third", qoderPayloadPromptForTest(t, thirdPayload))
}

func TestQoderGatewayResponsesPreviousResponseIDIsScopedByAccount(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	account2 := *account
	account2.ID = account.ID + 1
	svc.Tokens.Core.Sessions[account2.ID] = accountcore.QoderSessionCacheEntry[*qoder.SessionContext]{
		CredentialsHash: accountcore.QoderCredentialsHash(account2.Credentials),
		Session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token-2"}},
	}

	_, firstResponse := qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"input":"hello",
		"stream":false
	}`))
	firstID := gjson.Get(firstResponse, "id").String()
	require.NotEmpty(t, firstID)
	firstPayload := qoderPayloadAtForTest(t, client, 0)

	qoderForwardResponsesResultAndBodyForTest(t, svc, &account2, []byte(`{
		"model":"deepseek-v4-pro",
		"previous_response_id":`+strconv.Quote(firstID)+`,
		"input":"next",
		"stream":false
	}`))
	secondPayload := qoderPayloadAtForTest(t, client, 1)
	require.NotEqual(t, firstPayload["session_id"], secondPayload["session_id"], "Qoder upstream sessions are account-scoped; a response id from one account must not alias another account's session")
}

func TestQoderGatewayResponsesPreviousResponseIDWithExplicitSessionAppendsToExistingSession(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()

	_, firstResponse := qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"session_id":"responses-explicit-session",
		"input":"hello",
		"stream":false
	}`))
	firstID := gjson.Get(firstResponse, "id").String()
	require.NotEmpty(t, firstID)
	firstPayload := qoderPayloadAtForTest(t, client, 0)

	qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"session_id":"responses-explicit-session",
		"previous_response_id":`+strconv.Quote(firstID)+`,
		"input":"next",
		"stream":false
	}`))
	secondPayload := qoderPayloadAtForTest(t, client, 1)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.Equal(t, "next", qoderPayloadPromptForTest(t, secondPayload))
}

func TestQoderGatewayResponsesGeneratedResponseIDDoesNotMaskPromptCacheKey(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()

	_, firstResponse := qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"responses-cache-session",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"stream":false
	}`))
	firstID := gjson.Get(firstResponse, "id").String()
	require.NotEmpty(t, firstID)
	firstPayload := qoderPayloadAtForTest(t, client, 0)

	qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"responses-cache-session",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
		],
		"stream":false
	}`))
	secondPayload := qoderPayloadAtForTest(t, client, 1)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])

	qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"previous_response_id":`+strconv.Quote(firstID)+`,
		"input":"branch from first id",
		"stream":false
	}`))
	thirdPayload := qoderPayloadAtForTest(t, client, 2)
	require.Equal(t, firstPayload["session_id"], thirdPayload["session_id"])
	require.Equal(t, "branch from first id", qoderPayloadPromptForTest(t, thirdPayload))
}

func TestQoderGatewayResponsesStreamEmptyOutputAliasesResponseIDToStableSession(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = "data: {\"body\":\"[DONE]\"}\n\n"

	_, firstStream := qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"responses-empty-stream-session",
		"input":"hello",
		"stream":true
	}`))
	firstID := qoderResponsesCompletedEventForTest(t, firstStream).Get("response.id").String()
	require.NotEmpty(t, firstID)
	firstPayload := qoderPayloadAtForTest(t, client, 0)

	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"content": "OK"}},
	}}) + "data: {\"body\":\"[DONE]\"}\n\n"
	qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"previous_response_id":`+strconv.Quote(firstID)+`,
		"input":"next",
		"stream":false
	}`))
	secondPayload := qoderPayloadAtForTest(t, client, 1)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.Equal(t, "next", qoderPayloadPromptForTest(t, secondPayload))
}

func TestQoderGatewayResponsesStreamPreviousResponseIDReusesQoderSession(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()

	_, firstStream := qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"instructions":"be terse",
		"input":"hello",
		"stream":true
	}`))
	firstCreated := qoderResponsesCreatedEventForTest(t, firstStream)
	firstCompleted := qoderResponsesCompletedEventForTest(t, firstStream)
	firstID := firstCreated.Get("response.id").String()
	require.NotEmpty(t, firstID)
	require.Equal(t, firstID, firstCompleted.Get("response.id").String())
	firstPayload := qoderPayloadAtForTest(t, client, 0)

	_, secondStream := qoderForwardResponsesResultAndBodyForTest(t, svc, account, []byte(`{
		"model":"deepseek-v4-pro",
		"previous_response_id":`+strconv.Quote(firstID)+`,
		"input":"next",
		"stream":true
	}`))
	secondCreated := qoderResponsesCreatedEventForTest(t, secondStream)
	secondCompleted := qoderResponsesCompletedEventForTest(t, secondStream)
	secondID := secondCreated.Get("response.id").String()
	require.NotEmpty(t, secondID)
	require.NotEqual(t, firstID, secondID)
	require.Equal(t, secondID, secondCompleted.Get("response.id").String())
	secondPayload := qoderPayloadAtForTest(t, client, 1)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.Equal(t, "next", qoderPayloadPromptForTest(t, secondPayload))
}

func TestQoderGatewayResponsesStreamsDeclaredFunctionCallEvents(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"stream":true
	}`)

	result, response := qoderForwardResponsesResultAndBodyForTest(t, svc, account, body)

	require.True(t, result.Stream)
	events := qoderResponsesStreamEventsForTest(t, response)
	require.Equal(t, "response.created", events[0].Get("type").String())
	var added gjson.Result
	var argsDone gjson.Result
	for _, event := range events {
		switch event.Get("type").String() {
		case "response.output_item.added":
			if event.Get("item.type").String() == "function_call" {
				added = event
			}
		case "response.function_call_arguments.done":
			argsDone = event
		}
	}
	require.Equal(t, "bash", added.Get("item.name").String())
	require.JSONEq(t, `{"command":"pwd"}`, argsDone.Get("arguments").String())
	require.NotContains(t, response, `"name":"Bash"`)
}

func TestQoderGatewayResponsesToolContinuationUsesToolResultsAsPrompt(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	tools := `[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]`
	firstBody := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[{"role":"user","content":"run two commands"}],
		"tools":` + tools + `,
		"stream":true
	}`)
	secondBody := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[
			{"role":"user","content":"run two commands"},
			{"type":"function_call","call_id":"call_a","name":"bash","arguments":"{\"command\":\"printf A\"}"},
			{"type":"function_call","call_id":"call_b","name":"bash","arguments":"{\"command\":\"printf B\"}"},
			{"type":"function_call_output","call_id":"call_a","output":"A\n"},
			{"type":"function_call_output","call_id":"call_b","output":"B\n"}
		],
		"tools":` + tools + `,
		"stream":true
	}`)

	qoderForwardResponsesResultAndBodyForTest(t, svc, account, firstBody, qoderHeader("session_id", "responses-tool-continuation"))
	qoderForwardResponsesResultAndBodyForTest(t, svc, account, secondBody, qoderHeader("session_id", "responses-tool-continuation"))

	payload := qoderLastUpstreamPayloadForTest(t, client)
	prompt := qoderPayloadPromptForTest(t, payload)
	require.NotEqual(t, "run two commands", prompt)
	require.Contains(t, prompt, `<tool_result id="call_a">`)
	require.Contains(t, prompt, "A\n")
	require.Contains(t, prompt, `<tool_result id="call_b">`)
	require.Contains(t, prompt, "B\n")
}

func TestQoderGatewayResponsesToolContinuationGroupsFunctionCallsIntoOneAssistantTurn(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	tools := `[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}},{"type":"function","name":"glob","parameters":{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}}]`
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[
			{"role":"user","content":"list docs and print marker"},
			{"type":"function_call","call_id":"call_glob","name":"glob","arguments":"{\"pattern\":\"docs/*.md\"}"},
			{"type":"function_call","call_id":"call_bash","name":"bash","arguments":"{\"command\":\"echo CHAIN_OPENAI_OK\"}"},
			{"type":"function_call_output","call_id":"call_glob","output":"docs/a.md\ndocs/b.md"},
			{"type":"function_call_output","call_id":"call_bash","output":"CHAIN_OPENAI_OK\n"}
		],
		"tools":` + tools + `,
		"stream":true
	}`)

	qoderForwardResponsesResultAndBodyForTest(t, svc, account, body, qoderHeader("session_id", "responses-grouped-tool-continuation"))

	payload := qoderLastUpstreamPayloadForTest(t, client)
	messages := qoderFixtureValue[[]any](t, payload["messages"])
	require.Len(t, messages, 4)
	assistant := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "assistant", assistant["role"])
	require.Len(t, qoderFixtureValue[[]any](t, assistant["tool_calls"]), 2)

	prompt := qoderPayloadPromptForTest(t, payload)
	require.NotEqual(t, "list docs and print marker", prompt)
	require.Contains(t, prompt, `<tool_result id="call_glob">`)
	require.Contains(t, prompt, "docs/a.md")
	require.Contains(t, prompt, `<tool_result id="call_bash">`)
	require.Contains(t, prompt, "CHAIN_OPENAI_OK")
}

func TestQoderGatewayClaudeRequestsWithoutSessionDoNotReuseByFirstText(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"你好"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"你好"},
			{"role":"user","content":"你一共和我对话了几句话？"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.NotEqual(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	require.Len(t, qoderFixtureValue[[]any](t, second["messages"]), 4)
}

func TestQoderGatewayClaudeCodeContextWithoutSessionUsesStablePrefixKey(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"You are Claude Code, Anthropic's official CLI for Claude.\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"You are Claude Code, Anthropic's official CLI for Claude.\n" +
		"Stable Claude Code system body."
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"claude-opus-4-6",
		"system":`+strconv.Quote(system1)+`,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`),
		qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"),
		qoderHeader("X-Test-Claude-Code-Context", "true"),
	)
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"claude-opus-4-6",
		"system":`+strconv.Quote(system2)+`,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`),
		qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"),
		qoderHeader("X-Test-Claude-Code-Context", "true"),
	)

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
	require.Equal(t, "continue", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, second["chat_context"])["text"])["text"])
}

func TestQoderGatewayDoesNotCommitConversationOnUpstreamFailure(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Err = errors.New("upstream failed")
	body := []byte(`{
		"model":"auto",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	_, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, account, body, protocolcore.ProtocolOpenAIChatCompletions)
	require.Error(t, err)

	client.Err = nil
	first := qoderForwardChatCompletionsForTest(t, svc, account, "", body)
	require.Len(t, qoderFixtureValue[[]any](t, first["messages"]), 1)
}

func TestQoderGatewayReservesConversationAfterUpstreamAcceptsBeforeStreamCompletes(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	client := newBlockingQoderClientStub(t)
	svc.Client = client
	body1 := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"reserve-before-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	body2 := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"reserve-before-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body1))
	firstCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	var wg sync.WaitGroup
	wg.Add(1)
	var firstErr error
	go func() {
		defer wg.Done()
		_, firstErr = ForwardQoderAttempt(context.Background(), firstCtx, svc.Runtime, account, body1, protocolcore.ProtocolAnthropicMessages)
	}()
	client.waitForCalls(1)

	secondPayload := qoderForwardMessagesForTest(t, svc, account, "", body2, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	client.finishFirst()
	wg.Wait()
	require.NoError(t, firstErr)

	firstPayload := qoderPayloadAtForTest(t, client, 0)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, secondPayload["tools"]))
	messages := qoderFixtureValue[[]any](t, secondPayload["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
}

func TestQoderGatewayDoesNotCommitFailedPostToolStreamAsComplete(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	client := newBlockingQoderClientStub(t)
	svc.Client = client
	body1 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"failed-post-tool-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	body2 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"failed-post-tool-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"/repo"}]}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)

	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body1))
	firstCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	var wg sync.WaitGroup
	wg.Add(1)
	var firstErr error
	go func() {
		defer wg.Done()
		_, firstErr = ForwardQoderAttempt(context.Background(), firstCtx, svc.Runtime, account, body1, protocolcore.ProtocolAnthropicMessages)
	}()
	client.waitForCalls(1)

	client.mu.Lock()
	client.nextError = true
	client.mu.Unlock()
	failedRec := httptest.NewRecorder()
	failedCtx, _ := gin.CreateTestContext(failedRec)
	failedCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body2))
	failedCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	_, failedErr := ForwardQoderAttempt(context.Background(), failedCtx, svc.Runtime, account, body2, protocolcore.ProtocolAnthropicMessages)
	require.Error(t, failedErr)

	retryPayload := qoderForwardMessagesForTest(t, svc, account, "", body2, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	client.finishFirst()
	wg.Wait()
	require.NoError(t, firstErr)

	require.Equal(t, qoderPayloadAtForTest(t, client, 0)["session_id"], retryPayload["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, retryPayload["tools"]))
	messages := qoderFixtureValue[[]any](t, retryPayload["messages"])
	require.Len(t, messages, 4)
	system := qoderFixtureValue[map[string]any](t, messages[0])
	require.Equal(t, "system", system["role"])
	user := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "user", user["role"])
	require.Equal(t, "run pwd", qoderPayloadMessageTextForTest(user))
	assistant := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "assistant", assistant["role"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, assistant["tool_calls"]))
	toolResult := qoderFixtureValue[map[string]any](t, messages[3])
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "call_1", toolResult["tool_call_id"])
	require.Equal(t, "call_1", toolResult["tool_call_call_id"])
	require.Equal(t, "Bash", toolResult["name"])
	require.Equal(t, "/repo", toolResult["content"])
}

func TestQoderGatewayRollsBackAcceptedConversationOnStreamParseFailure(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	body1 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"rollback-accepted-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	body2 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"rollback-accepted-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"/repo"}]}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)

	firstPayload := qoderForwardMessagesForTest(t, svc, account, "", body1, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\""

	failedRec := httptest.NewRecorder()
	failedCtx, _ := gin.CreateTestContext(failedRec)
	failedCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body2))
	failedCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	_, failedErr := ForwardQoderAttempt(context.Background(), failedCtx, svc.Runtime, account, body2, protocolcore.ProtocolAnthropicMessages)
	require.Error(t, failedErr)

	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":5,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":6}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	retryPayload := qoderForwardMessagesForTest(t, svc, account, "", body2, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, firstPayload["session_id"], retryPayload["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, retryPayload["tools"]))
	messages := qoderFixtureValue[[]any](t, retryPayload["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "tool", qoderFixtureValue[map[string]any](t, messages[3])["role"])
}

func TestQoderGatewayFallsBackToFullReplayWhenPrefixDoesNotMatch(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardChatCompletionsForTest(t, svc, account, "stable-session", []byte(`{
		"model":"auto",
		"messages":[{"role":"user","content":"first"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "stable-session", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"changed"},
			{"role":"assistant","content":"answer"},
			{"role":"user","content":"next"}
		],
		"stream":false
	}`))

	require.NotEqual(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 3)
	require.Equal(t, "changed", qoderFixtureValue[map[string]any](t, qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[0])["contents"])[0])["text"])
}

func TestQoderGatewayFallsBackToFullReplayWhenSystemOrToolsChange(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hello"}],
		"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"be detailed"},
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"next"}
		],
		"tools":[{"type":"function","function":{"name":"write","parameters":{"type":"object"}}}],
		"stream":false
	}`))

	require.NotEqual(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "be detailed", qoderFixtureValue[map[string]any](t, messages[0])["content"])
	tools := qoderFixtureValue[[]any](t, second["tools"])
	require.Equal(t, "write", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, tools[0])["function"])["name"])
}

func TestQoderGatewayUsesExplicitBodySessionID(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"session_id":"body-session-1",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"session_id":"body-session-1",
		"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"next"}],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	require.Len(t, qoderFixtureValue[[]any](t, second["messages"]), 3)
}

func TestQoderGatewayAnthropicMetadataSessionWinsOverChangingHeader(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	metadata := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","account_uuid":"","session_id":"11111111-2222-3333-4444-555555555555"}`
	first := qoderForwardMessagesForTest(t, svc, account, "volatile-header-1", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata)+`},
		"messages":[{"role":"user","content":"inspect"}],
		"stream":false
	}`))
	second := qoderForwardMessagesForTest(t, svc, account, "volatile-header-2", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata)+`},
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 3)
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "continue", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, second["chat_context"])["text"])["text"])
}

func TestQoderGatewayClaudeCodeUsesExplicitHeaderSessionBeforeStableSeed(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardMessagesForTest(t, svc, account, "stable-header", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.162 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "stable-header", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.162 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])

	other := qoderForwardMessagesForTest(t, svc, account, "other-header", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.162 (external, cli)"))
	require.NotEqual(t, first["session_id"], other["session_id"])
}

func TestQoderGatewayClaudeCodeUsesMetadataSessionBeforeStableSeed(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	metadata1 := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","account_uuid":"","session_id":"11111111-2222-3333-4444-555555555555"}`
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata1)+`},
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata1)+`},
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
	require.Equal(t, "continue", qoderFixtureValue[map[string]any](t, qoderFixtureValue[map[string]any](t, second["chat_context"])["text"])["text"])

	metadata2 := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","account_uuid":"","session_id":"66666666-7777-8888-9999-aaaaaaaaaaaa"}`
	other := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata2)+`},
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.NotEqual(t, first["session_id"], other["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, other["tools"]))
}

func TestQoderGatewayClaudeCodeIgnoresVolatileBillingCCHForSystemReuse(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"billing-cch-session",
		"system":`+strconv.Quote(system1)+`,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"billing-cch-session",
		"system":`+strconv.Quote(system2)+`,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
}

func TestQoderGatewayClaudeCodeUltimateStablePromptCacheKeyReportsCacheRead(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	firstBody := []byte(`{
		"model":"claude-opus-4-6",
		"prompt_cache_key":"ultimate-cache-hit-session",
		"system":` + strconv.Quote(system1) + `,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + largeTools + `,
		"stream":false
	}`)
	secondBody := []byte(`{
		"model":"claude-opus-4-6",
		"prompt_cache_key":"ultimate-cache-hit-session",
		"system":` + strconv.Quote(system2) + `,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":` + largeTools + `,
		"stream":false
	}`)

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult := qoderForwardMessagesResultForTest(t, svc, account, firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	firstPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.Equal(t, "ultimate", firstResult.UpstreamModel)
	require.Equal(t, 1200, firstResult.Usage.InputTokens)
	require.Equal(t, "ultimate", client.Headers["x-model-key"])

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1500,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":1533,\\\"prompt_tokens_details\\\":{\\\"cached_tokens\\\":1400,\\\"cacheable_tokens\\\":100}}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult, secondResponse := qoderForwardMessagesResultAndBodyForTest(t, svc, account, secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	secondPayload := qoderLastUpstreamPayloadForTest(t, client)

	require.Equal(t, "ultimate", secondResult.UpstreamModel)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.Equal(t, 100, secondResult.Usage.InputTokens)
	require.Equal(t, 1400, secondResult.Usage.CacheReadInputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	require.Equal(t, int64(1400), gjson.Get(secondResponse, "usage.cache_read_input_tokens").Int())
	require.Equal(t, int64(100), gjson.Get(secondResponse, "usage.input_tokens").Int())
}

func TestQoderGatewayStillFullReplaysWhenNonBillingSystemChanges(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"Changed Claude Code system body."
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"system-change-session",
		"system":`+strconv.Quote(system1)+`,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"system-change-session",
		"system":`+strconv.Quote(system2)+`,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))

	require.NotEqual(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	require.Len(t, qoderFixtureValue[[]any](t, second["messages"]), 4)
}

func TestQoderGatewayReusedAnthropicConversationOmitsUnchangedTools(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, account, "stable-session", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"你好"}],
		"tools":`+largeTools+`,
		"stream":false
	}`))
	second := qoderForwardMessagesForTest(t, svc, account, "stable-session", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"你好"},
			{"role":"user","content":"你好"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`))

	require.NotEmpty(t, qoderFixtureValue[[]any](t, first["tools"]))
	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[3])["role"])
}

func TestQoderGatewayUsageComesFromUpstreamSSE(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1234,\\\"completion_tokens\\\":56,\\\"total_tokens\\\":1290}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"你好"},
			{"role":"user","content":"你好"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, account, body, protocolcore.ProtocolAnthropicMessages)

	require.NoError(t, err)
	require.Equal(t, 1234, result.Usage.InputTokens)
	require.Equal(t, 56, result.Usage.OutputTokens)
	require.NotEqual(t, len(body), result.Usage.InputTokens)
}

func TestQoderGatewayBuildsClientVisibleOpenAIUsageWithUpstreamTotals(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		qoderCachedUsageSSEForTest +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	result, response := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", body)

	require.Equal(t, 25, result.Usage.InputTokens)
	require.Equal(t, 66612, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Equal(t, int64(66637), gjson.Get(response, "usage.prompt_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(response, "usage.completion_tokens").Int())
	require.Equal(t, int64(66643), gjson.Get(response, "usage.total_tokens").Int())
	require.Equal(t, int64(66612), gjson.Get(response, "usage.prompt_tokens_details.cached_tokens").Int())
	require.Equal(t, int64(19), gjson.Get(response, "usage.prompt_tokens_details.cacheable_tokens").Int())
	require.Equal(t, int64(0), gjson.Get(response, "usage.completion_tokens_details.reasoning_tokens").Int())
}

func TestQoderGatewayOpenAIUsageKeepsUpstreamPromptWhenCachedExceedsPrompt(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10,\\\"completion_tokens\\\":6,\\\"total_tokens\\\":16,\\\"prompt_tokens_details\\\":{\\\"cached_tokens\\\":15,\\\"cacheable_tokens\\\":1}}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	result, response := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", body)

	require.Equal(t, 0, result.Usage.InputTokens)
	require.Equal(t, 15, result.Usage.CacheReadInputTokens)
	require.Equal(t, int64(10), gjson.Get(response, "usage.prompt_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(response, "usage.completion_tokens").Int())
	require.Equal(t, int64(16), gjson.Get(response, "usage.total_tokens").Int())
	require.Equal(t, int64(15), gjson.Get(response, "usage.prompt_tokens_details.cached_tokens").Int())
	require.False(t, gjson.Get(response, "usage.cache_creation_input_tokens").Exists())
}

func TestQoderGatewayBuildsClientVisibleAnthropicUsageWithCacheRead(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		qoderCachedUsageSSEForTest +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	result, response := qoderForwardMessagesResultAndBodyForTest(t, svc, account, body)

	require.Equal(t, 25, result.Usage.InputTokens)
	require.Equal(t, 66612, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Equal(t, int64(25), gjson.Get(response, "usage.input_tokens").Int())
	require.Equal(t, int64(66612), gjson.Get(response, "usage.cache_read_input_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(response, "usage.output_tokens").Int())
	require.False(t, gjson.Get(response, "usage.cache_creation_input_tokens").Exists())
}

func TestQoderGatewayDoesNotSubtractPreviousUsageOnFullReplay(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	firstBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)
	secondBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult := qoderForwardMessagesResultForTest(t, svc, account, firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 1200, firstResult.Usage.InputTokens)

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10000,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":10033}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult := qoderForwardMessagesResultForTest(t, svc, account, secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 10000, secondResult.Usage.InputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	secondPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.NotEmpty(t, qoderFixtureValue[[]any](t, secondPayload["tools"]))
	require.Len(t, qoderFixtureValue[[]any](t, secondPayload["messages"]), 4)
}

func TestQoderGatewayReturnsDeltaUsageToAnthropicClientOnReusedConversation(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	firstBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"client-usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)
	secondBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"client-usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult, firstResponse := qoderForwardMessagesResultAndBodyForTest(t, svc, account, firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 1200, firstResult.Usage.InputTokens)
	require.Equal(t, int64(1200), gjson.Get(firstResponse, "usage.input_tokens").Int())

	client.Body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10000,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":10033}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult, secondResponse := qoderForwardMessagesResultAndBodyForTest(t, svc, account, secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 10000, secondResult.Usage.InputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	require.Equal(t, int64(10000), gjson.Get(secondResponse, "usage.input_tokens").Int())
	require.Equal(t, int64(33), gjson.Get(secondResponse, "usage.output_tokens").Int())

	secondPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.NotEmpty(t, qoderFixtureValue[[]any](t, secondPayload["tools"]))
}

func TestQoderGatewayAnthropicToolUseResultSendsIncrementalTail(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"anthropic-tool-session",
		"system":"be useful",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":[{"name":"bash","input_schema":{"type":"object"}}],
		"stream":false
	}`))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"anthropic-tool-session",
		"system":"be useful",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]}
		],
		"tools":[{"name":"bash","input_schema":{"type":"object"}}],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 4)

	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	user := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "user", user["role"])
	require.Equal(t, "inspect", qoderPayloadMessageTextForTest(user))

	assistant := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "assistant", assistant["role"])
	toolCalls := qoderFixtureValue[[]any](t, assistant["tool_calls"])
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call_1", qoderFixtureValue[map[string]any](t, toolCalls[0])["id"])

	tool := qoderFixtureValue[map[string]any](t, messages[3])
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
	require.Equal(t, "repo", tool["content"])
	prompt := qoderPayloadPromptForTest(t, second)
	require.Contains(t, prompt, `<tool_result id="call_1">`)
	require.Contains(t, prompt, "repo\n")
}

func TestQoderGatewayOpenAIToolCallsSendIncrementalTail(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	first := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-tool-session",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-tool-session",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":{"cmd":"pwd"}}}]},
			{"role":"tool","tool_call_id":"call_1","name":"bash","content":"/repo"}
		],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 3)
	user := qoderFixtureValue[map[string]any](t, messages[0])
	require.Equal(t, "user", user["role"])
	require.Equal(t, "run pwd", qoderPayloadMessageTextForTest(user))
	assistant := qoderFixtureValue[map[string]any](t, messages[1])
	require.Equal(t, "assistant", assistant["role"])
	require.Len(t, qoderFixtureValue[[]any](t, assistant["tool_calls"]), 1)
	tool := qoderFixtureValue[map[string]any](t, messages[2])
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
	prompt := qoderPayloadPromptForTest(t, second)
	require.Contains(t, prompt, `<tool_result id="call_1">`)
	require.Contains(t, prompt, "/repo\n")
}

func TestQoderGatewayRepeatedClaudeCodeRequestKeepsNonEmptyIncrementalTail(t *testing.T) {
	account, svc, _ := gatewaytestkit.NewDefaultQoderFixture()
	body := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"repeated-claude-request-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	first := qoderForwardMessagesForTest(t, svc, account, "", body, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", body, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 6)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
}

func TestQoderGatewayStreamsDeltaUsageToOpenAIClientOnReusedConversation(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	firstBody := []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-stream-usage-delta-session",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true,
		"stream_options":{"include_usage":true}
	}`)
	secondBody := []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-stream-usage-delta-session",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":{"cmd":"pwd"}}}]},
			{"role":"tool","tool_call_id":"call_1","name":"bash","content":"/repo"},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true,
		"stream_options":{"include_usage":true}
	}`)

	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult, firstStream := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 1200, firstResult.Usage.InputTokens)
	require.Contains(t, firstStream, `"prompt_tokens":1200`)

	client.Body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10000,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":10033}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult, secondStream := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	secondPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.NotEmpty(t, qoderFixtureValue[[]any](t, secondPayload["tools"]))
	require.Equal(t, 10000, secondResult.Usage.InputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	require.Contains(t, secondStream, `"prompt_tokens":10000`)
	require.Contains(t, secondStream, `"completion_tokens":33`)
}

func TestQoderGatewayAnthropicConversationAfterToolResultKeepsReducingPayload(t *testing.T) {
	account, svc, client := gatewaytestkit.NewDefaultQoderFixture()
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"post-tool-reducing-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"post-tool-reducing-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, second["tools"]))
	messages := qoderFixtureValue[[]any](t, second["messages"])
	require.Len(t, messages, 6)
	require.Equal(t, "system", qoderFixtureValue[map[string]any](t, messages[0])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[1])["role"])
	require.Equal(t, "inspect", qoderPayloadMessageTextForTest(qoderFixtureValue[map[string]any](t, messages[1])))
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[2])["role"])
	require.NotEmpty(t, qoderFixtureValue[[]any](t, qoderFixtureValue[map[string]any](t, messages[2])["tool_calls"]))
	require.Equal(t, "tool", qoderFixtureValue[map[string]any](t, messages[3])["role"])
	require.Equal(t, "call_1", qoderFixtureValue[map[string]any](t, messages[3])["tool_call_id"])
	require.Equal(t, "assistant", qoderFixtureValue[map[string]any](t, messages[4])["role"])
	require.Equal(t, "user", qoderFixtureValue[map[string]any](t, messages[5])["role"])

	require.GreaterOrEqual(t, len(client.BodyAt(1)), len(client.BodyAt(0)))
}

func TestQoderGatewayDoesNotAttachAmbiguousArgumentDeltaToParallelToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", Arguments: `{"path":"lost"}`},
		{IsDone: true},
	}

	body, err := qoder.BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, int64(2), gjson.GetBytes(body, "choices.0.message.tool_calls.#").Int())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.Empty(t, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.Equal(t, "call_2", gjson.GetBytes(body, "choices.0.message.tool_calls.1.id").String())
	require.Equal(t, "write", gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.name").String())
	require.Empty(t, gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.arguments").String())
	require.NotContains(t, string(body), "lost")
}

func TestQoderGatewayForwardChatCompletionsHonorsCanceledContext(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	account := &accountcore.Record{
		ID:       93,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"pat": "pat-token",
		},
	}
	provider := accountprovider.NewQoderTokenProvider(qoder.SessionBuilder{ExchangePAT: func(ctx context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return &qoder.AuthIdentity{SecurityOauthToken: "token", UID: "uid"}, nil
		}
	}})
	svc := gatewaytestkit.NewQoderFixture(provider, nil, nil)

	_, err := ForwardQoderAttempt(ctx, c, svc.Runtime, account, []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`), protocolcore.ProtocolOpenAIChatCompletions)

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, c.Writer.Written())
}

func qoderPayloadPromptForTest(t *testing.T, payload map[string]any) string {
	t.Helper()
	chatContext := qoderFixtureValue[map[string]any](t, payload["chat_context"])
	text := qoderFixtureValue[map[string]any](t, chatContext["text"])
	return qoderFixtureValue[string](t, text["text"])
}

func qoderPayloadMessageTextForTest(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	contents, _ := msg["contents"].([]any)
	for _, raw := range contents {
		block, ok := raw.(map[string]any)
		if !ok || block["type"] != "text" {
			continue
		}
		if text, ok := block["text"].(string); ok {
			return text
		}
	}
	if text, ok := msg["content"].(string); ok {
		return text
	}
	return ""
}

type qoderForwardTestHeader struct {
	key   string
	value string
}

func qoderHeader(key, value string) qoderForwardTestHeader {
	return qoderForwardTestHeader{key: key, value: value}
}

func qoderForwardChatCompletionsForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, account *accountcore.Record, sessionID string, body []byte, headers ...qoderForwardTestHeader) map[string]any {
	t.Helper()
	_, _ = qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, sessionID, body, headers...)
	return qoderLastUpstreamPayloadForTest(t, qoderFixtureValue[*gatewaytestkit.QoderClient](t, svc.Client))
}

func qoderForwardChatCompletionsResultAndBodyForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, account *accountcore.Record, sessionID string, body []byte, headers ...qoderForwardTestHeader) (*forwardcore.MessagesResult, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	if sessionID != "" {
		c.Request.Header.Set("session_id", sessionID)
	}
	for _, header := range headers {
		c.Request.Header.Set(header.key, header.value)
	}
	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, account, body, protocolcore.ProtocolOpenAIChatCompletions)
	require.NoError(t, err)
	return result, rec.Body.String()
}

func qoderForwardMessagesForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, account *accountcore.Record, sessionID string, body []byte, headers ...qoderForwardTestHeader) map[string]any {
	t.Helper()
	result := qoderForwardMessagesResultForTest(t, svc, account, body, append([]qoderForwardTestHeader{qoderHeader("session_id", sessionID)}, headers...)...)
	require.NotNil(t, result)
	client, ok := svc.Client.(interface {
		BodyAt(int) []byte
		BodyCount() int
	})
	require.True(t, ok)
	return qoderPayloadAtForTest(t, client, client.BodyCount()-1)
}

func qoderForwardMessagesResultForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, account *accountcore.Record, body []byte, headers ...qoderForwardTestHeader) *forwardcore.MessagesResult {
	t.Helper()
	result, _ := qoderForwardMessagesResultAndBodyForTest(t, svc, account, body, headers...)
	return result
}

func qoderForwardMessagesResultAndBodyForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, account *accountcore.Record, body []byte, headers ...qoderForwardTestHeader) (*forwardcore.MessagesResult, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	for _, header := range headers {
		if strings.TrimSpace(header.value) != "" {
			c.Request.Header.Set(header.key, header.value)
		}
	}
	if strings.EqualFold(strings.TrimSpace(c.Request.Header.Get("X-Test-Claude-Code-Context")), "true") {
		c.Request = c.Request.WithContext(requeststate.SetClaudeCodeClient(c.Request.Context(), true))
	}
	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, account, body, protocolcore.ProtocolAnthropicMessages)
	require.NoError(t, err)
	return result, rec.Body.String()
}

func qoderForwardResponsesResultAndBodyForTest(t *testing.T, svc *gatewaytestkit.QoderFixture, account *accountcore.Record, body []byte, headers ...qoderForwardTestHeader) (*forwardcore.MessagesResult, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	for _, header := range headers {
		if strings.TrimSpace(header.value) != "" {
			c.Request.Header.Set(header.key, header.value)
		}
	}
	result, err := ForwardQoderAttempt(context.Background(), c, svc.Runtime, account, body, protocolcore.ProtocolOpenAIResponses)
	require.NoError(t, err)
	return result, rec.Body.String()
}

func qoderResponsesCreatedEventForTest(t *testing.T, body string) gjson.Result {
	t.Helper()
	for _, event := range qoderResponsesStreamEventsForTest(t, body) {
		if event.Get("type").String() == "response.created" {
			return event
		}
	}
	t.Fatalf("response.created event not found in %s", body)
	return gjson.Result{}
}

func qoderLastUpstreamPayloadForTest(t *testing.T, client *gatewaytestkit.QoderClient) map[string]any {
	t.Helper()
	require.NotNil(t, client)
	require.NotZero(t, client.BodyCount())
	return qoderPayloadAtForTest(t, client, client.BodyCount()-1)
}

func qoderPayloadAtForTest(t *testing.T, client interface{ BodyAt(int) []byte }, index int) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(client.BodyAt(index), &payload))
	return payload
}

type blockingQoderClientStub struct {
	t           *testing.T
	mu          sync.Mutex
	cond        *sync.Cond
	Bodies      [][]byte
	Headers     map[string]string
	firstWriter *io.PipeWriter
	firstDone   bool
	nextError   bool
}

func newBlockingQoderClientStub(t *testing.T) *blockingQoderClientStub {
	t.Helper()
	client := &blockingQoderClientStub{t: t}
	client.cond = sync.NewCond(&client.mu)
	return client
}

func (s *blockingQoderClientStub) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, bodyJSON []byte, headers map[string]string) (*http.Response, error) {
	s.mu.Lock()
	callNumber := len(s.Bodies) + 1
	s.Bodies = append(s.Bodies, append([]byte(nil), bodyJSON...))
	s.Headers = headers
	s.cond.Broadcast()
	s.mu.Unlock()

	if callNumber == 1 {
		reader, writer := io.Pipe()
		s.mu.Lock()
		s.firstWriter = writer
		s.mu.Unlock()
		go func() {
			<-ctx.Done()
			_ = writer.Close()
		}()
		return &http.Response{StatusCode: http.StatusOK, Body: reader}, nil
	}
	s.mu.Lock()
	nextError := s.nextError
	s.nextError = false
	s.mu.Unlock()
	if nextError {
		body := "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\""
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	body := "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":5,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":6}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (s *blockingQoderClientStub) waitForCalls(count int) {
	s.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.Bodies) < count {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for %d qoder calls, got %d", count, len(s.Bodies))
		}
		s.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		s.mu.Lock()
	}
}

func (s *blockingQoderClientStub) finishFirst() {
	s.t.Helper()
	s.mu.Lock()
	writer := s.firstWriter
	if s.firstDone {
		writer = nil
	}
	s.firstDone = true
	s.mu.Unlock()
	require.NotNil(s.t, writer)
	_, err := io.WriteString(writer,
		"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n"+
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":11}}\"}\n\n"+
			"data: {\"body\":\"[DONE]\"}\n\n")
	require.NoError(s.t, err)
	require.NoError(s.t, writer.Close())
}

func (s *blockingQoderClientStub) BodyAt(index int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.Bodies) {
		return nil
	}
	return append([]byte(nil), s.Bodies[index]...)
}

func (s *blockingQoderClientStub) BodyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Bodies)
}

func qoderLargeToolsJSONForTest() string {
	description := strings.Repeat("large schema field used by Claude Code. ", 80)
	body, err := json.Marshal([]map[string]any{
		{
			"name":        "Read",
			"description": description,
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": description,
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": description,
					},
				},
				"required": []string{"file_path"},
			},
		},
		{
			"name":        "Bash",
			"description": description,
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": description,
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": description,
					},
				},
				"required": []string{"command"},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}
