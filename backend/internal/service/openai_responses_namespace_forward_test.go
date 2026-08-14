package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// codexNamespaceRequestBody 模拟 Codex 多智能体请求及带残留 namespace 的普通消息项。
const codexNamespaceRequestBody = `{
	"model":"gpt-5.6-terra",
	"stream":false,
	"instructions":"test",
	"tools":[
		{"type":"namespace","name":"collaboration","description":"Tools for spawning and managing sub-agents.","tools":[
			{"type":"function","name":"spawn_agent","description":"Call as to=functions.collaboration.spawn_agent","parameters":{"type":"object"}},
			{"type":"function","name":"wait_agent","parameters":{"type":"object"}}
		]},
		{"type":"function","name":"exec","parameters":{"type":"object"}}
	],
	"input":[
		{"type":"function_call","namespace":"collaboration","name":"spawn_agent","call_id":"call_1","arguments":"{}"},
		{"type":"message","role":"user","namespace":"leftover","content":[{"type":"input_text","text":"hello"}]}
	]
}`

const namespaceForwardOKResponse = `{"id":"resp_ns","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`

// OAuth 普通 Responses 必须原样保留 namespace 声明和历史工具调用字段。
func TestOpenAIGatewayService_OAuthPreservesCodexNamespaceTools(t *testing.T) {
	body := []byte(codexNamespaceRequestBody)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	c := newOpenAIRejectedFieldTestContext(body)

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), c, newOpenAIOAuthNamespaceTestAccount(), body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	forwarded := upstream.bodies[0]
	namespaceTool := gjson.GetBytes(forwarded, `tools.#(type=="namespace")`)
	require.True(t, namespaceTool.Exists())
	require.Equal(t, "collaboration", namespaceTool.Get("name").String())
	require.Equal(t, "spawn_agent", namespaceTool.Get("tools.0.name").String())
	require.NotContains(t, string(forwarded), "collaboration__spawn_agent")
	require.Equal(t, "collaboration", gjson.GetBytes(forwarded, "input.0.namespace").String())
	require.False(t, gjson.GetBytes(forwarded, "input.1.namespace").Exists())
	require.Empty(t, openAIResponsesNamespaceNames(c))
}

// compact 端点保持既有的摊平与全量 namespace 清理行为。
func TestOpenAIGatewayService_OAuthCompactKeepsFlattening(t *testing.T) {
	body := []byte(codexNamespaceRequestBody)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	c := newOpenAIRejectedFieldTestContext(body)
	c.Request.URL.Path = "/v1/responses/compact"

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), c, newOpenAIOAuthNamespaceTestAccount(), body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	forwarded := upstream.bodies[0]
	require.False(t, gjson.GetBytes(forwarded, "input.0.namespace").Exists())
	require.False(t, gjson.GetBytes(forwarded, `tools.#(type=="namespace")`).Exists())
	require.Equal(t, "collaboration__spawn_agent", gjson.GetBytes(forwarded, "input.0.name").String())
}

// 账号兼容开关打开后恢复 namespace 摊平旧行为。
func TestOpenAIGatewayService_OAuthFlattenFlagRestoresLegacyBehavior(t *testing.T) {
	body := []byte(codexNamespaceRequestBody)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	c := newOpenAIRejectedFieldTestContext(body)
	account := newOpenAIOAuthNamespaceTestAccount()
	account.Extra = map[string]any{"openai_responses_flatten_namespaces": true}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	forwarded := upstream.bodies[0]
	require.False(t, gjson.GetBytes(forwarded, `tools.#(type=="namespace")`).Exists())
	require.True(t, gjson.GetBytes(forwarded, `tools.#(name=="collaboration__spawn_agent")`).Exists())
	require.False(t, gjson.GetBytes(forwarded, "input.0.namespace").Exists())
	require.Equal(t, apicompat.ResponsesNamespaceName{
		Namespace: "collaboration",
		Name:      "spawn_agent",
	}, openAIResponsesNamespaceNames(c)["collaboration__spawn_agent"])
}

// failover 复用 gin.Context 时，每次转发都必须清除上一个账号留下的映射。
func TestOpenAIGatewayService_ForwardClearsStaleNamespaceNames(t *testing.T) {
	body := []byte(codexNamespaceRequestBody)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
	}}
	c := newOpenAIRejectedFieldTestContext(body)
	setOpenAIResponsesNamespaceNames(c, map[string]apicompat.ResponsesNamespaceName{
		"stale__tool": {Namespace: "stale", Name: "tool"},
	})

	_, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(), c, newOpenAIOAuthNamespaceTestAccount(), body,
	)

	require.NoError(t, err)
	require.Empty(t, openAIResponsesNamespaceNames(c))
}
