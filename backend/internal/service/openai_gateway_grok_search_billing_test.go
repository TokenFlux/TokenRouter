//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardGrokResponses_PropagatesSearchCountFromJSON(t *testing.T) {

	body := []byte(`{"model":"grok","input":"search something","tools":[{"type":"web_search"}],"stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	account := gatewaytestkit.HealthyGrokOAuthAccount(9901, "access-token")
	repo := &mockAccountRepoForPlatform{accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account}}
	upstreamBody := `{
		"id":"resp_search_bill",
		"object":"response",
		"model":"grok-4.5",
		"status":"completed",
		"output":[
			{"type":"web_search_call","id":"ws1","call_id":"c1","status":"completed"},
			{"type":"x_search_call","id":"xs1","call_id":"c2"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}
		],
		"usage":{"input_tokens":10,"output_tokens":5}
	}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(upstreamBody))),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", false, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, result.SearchCount, "Grok Responses must surface search tool calls for surcharge billing")
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}

func TestForwardGrokResponses_PropagatesSearchCountFromSSE(t *testing.T) {

	body := []byte(`{"model":"grok","input":"search","tools":[{"type":"web_search"}],"stream":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	account := gatewaytestkit.HealthyGrokOAuthAccount(9902, "access-token")
	repo := &mockAccountRepoForPlatform{accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: account}}
	// 装配后，同一 call_id 的 item.done 与 response.completed 只能统计一次。
	sse := "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"web_search_call\",\"id\":\"ws1\",\"call_id\":\"c1\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s\",\"status\":\"completed\",\"output\":[{\"type\":\"web_search_call\",\"id\":\"ws1\",\"call_id\":\"c1\"}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(sse))),
	}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		httpUpstream: upstream,

		accountRepo: repo,
	}), newGrokTokenSourceForTest(repo, nil))

	result, err := svc.Grok.ForwardResponses(context.Background(), c, account, body, "grok", true, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.SearchCount, "stream SearchCount must be wired and deduped")
}

func TestCountGrokNativeSearchCallsFromJSON_MessagesStyleBody(t *testing.T) {
	// 验证 Anthropic 缓冲的 Grok /v1/messages 路径使用同一计数器。
	body := []byte(`{"id":"r1","output":[{"type":"web_search_call","id":"ws1"},{"type":"message","role":"assistant"}]}`)
	require.Equal(t, 1, grok.CountGrokNativeSearchCallsFromJSONBytes(body))
}
