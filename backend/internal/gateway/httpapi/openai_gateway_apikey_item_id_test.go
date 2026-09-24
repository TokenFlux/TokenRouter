//go:build unit

package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayService_APIKeyPassthrough_StripsInvalidInputItemIDs(t *testing.T) {

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_test","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Record.Extra = map[string]any{"openai_passthrough": true}

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":false,
		"input":[
			{"type":"message","id":"item_bad_message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},
			{"type":"function_call","id":"item_bad_call","call_id":"call_123","name":"exec_command","arguments":"{}"},
			{"type":"message","id":"msg_valid","role":"user","content":[{"type":"input_text","text":"continue"}]},
			{"type":"function_call","id":"fc_valid","call_id":"call_456","name":"apply_patch","arguments":"{}"},
			{"type":"custom_tool_call","id":"fc_wrong_custom","call_id":"call_custom_1","name":"apply_patch","input":"patch"},
			{"type":"custom_tool_call","id":"ctc_valid","call_id":"call_custom_2","name":"apply_patch","input":"patch"},
			{"type":"tool_search_call","id":"fc_wrong_search","call_id":"call_search_1","arguments":{"query":"docs"}},
			{"type":"tool_search_call","id":"tsc_valid","call_id":"call_search_2","arguments":{"query":"docs"}},
			{"type":"function_call_output","id":"item_output","call_id":"call_123","output":"done"},
			{"type":"web_search_call","id":"item_wrong_web"}
		]
	}`)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)

	forwarded := upstream.lastBody
	require.False(t, gjson.GetBytes(forwarded, "input.0.id").Exists())
	require.Equal(t, "hello", gjson.GetBytes(forwarded, "input.0.content.0.text").String())
	require.False(t, gjson.GetBytes(forwarded, "input.1.id").Exists())
	require.Equal(t, "call_123", gjson.GetBytes(forwarded, "input.1.call_id").String())
	require.Equal(t, "exec_command", gjson.GetBytes(forwarded, "input.1.name").String())
	require.Equal(t, "{}", gjson.GetBytes(forwarded, "input.1.arguments").String())
	require.Equal(t, "msg_valid", gjson.GetBytes(forwarded, "input.2.id").String())
	require.Equal(t, "fc_valid", gjson.GetBytes(forwarded, "input.3.id").String())
	require.False(t, gjson.GetBytes(forwarded, "input.4.id").Exists())
	require.Equal(t, "ctc_valid", gjson.GetBytes(forwarded, "input.5.id").String())
	require.False(t, gjson.GetBytes(forwarded, "input.6.id").Exists())
	require.Equal(t, "tsc_valid", gjson.GetBytes(forwarded, "input.7.id").String())
	require.Equal(t, "item_output", gjson.GetBytes(forwarded, "input.8.id").String())
	require.Equal(t, "call_123", gjson.GetBytes(forwarded, "input.8.call_id").String())
	require.False(t, gjson.GetBytes(forwarded, "input.9.id").Exists())
}

func TestOpenAIGatewayService_OAuthPassthrough_SanitizesNativeToolItemIDs(t *testing.T) {

	for _, accountType := range []string{capability.AccountTypeOAuth, capability.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			upstreamSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-5.6-sol\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n"
			upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
			}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			account := newOpenAIImageGenerationControlTestAccount()
			account.Record.Type = accountType
			account.Record.Credentials = map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-account",
			}
			account.Record.Extra = map[string]any{"openai_passthrough": true}

			body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":true,
		"instructions":"test",
		"input":[
			{"type":"custom_tool_call","id":"fc_wrong_custom","call_id":"call_custom_1","name":"apply_patch","input":"patch"},
			{"type":"custom_tool_call","id":"ctc_valid","call_id":"call_custom_2","name":"apply_patch","input":"patch"},
			{"type":"tool_search_call","id":"fc_wrong_search","call_id":"call_search_1","arguments":{"query":"docs"}},
			{"type":"tool_search_call","id":"tsc_valid","call_id":"call_search_2","arguments":{"query":"docs"}}
		]
	}`)

			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstream.lastReq.URL.String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.id").Exists())
			require.Equal(t, "ctc_valid", gjson.GetBytes(upstream.lastBody, "input.1.id").String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "input.2.id").Exists())
			require.Equal(t, "tsc_valid", gjson.GetBytes(upstream.lastBody, "input.3.id").String())
		})
	}
}

func TestOpenAIGatewayService_SetupTokenLegacy_SanitizesAndTransforms(t *testing.T) {

	upstreamSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-5.6-sol\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n"
	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Record.Type = capability.AccountTypeSetupToken
	account.Record.Credentials = map[string]any{
		"access_token":       "setup-token",
		"chatgpt_account_id": "chatgpt-account",
	}
	account.Record.Extra = map[string]any{"openai_passthrough": false}
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":true,
		"store":true,
		"reasoning":{"mode":"pro"},
		"instructions":"test",
		"input":[
			{"type":"custom_tool_call","id":"fc_wrong_custom","call_id":"call_custom_1","name":"apply_patch","input":"patch"},
			{"type":"function_call_output","call_id":"call_orphan","output":"orphan"}
		]
	}`)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstream.lastReq.URL.String())
	require.Equal(t, false, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning.mode").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.id").Exists())
	require.Len(t, gjson.GetBytes(upstream.lastBody, "input").Array(), 1)
}

func TestOpenAIGatewayService_APIKeyPassthrough_StripsInvalidReasoningItemIDs(t *testing.T) {

	upstream := &auxiliaryHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_test","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		)),
	}}
	service := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Record.Extra = map[string]any{"openai_passthrough": true}
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":false,
		"input":[
			{"type":"reasoning","id":"item_bad_reasoning","summary":[]},
			{"type":"reasoning","id":"rs_valid","summary":[]},
			{"type":"message","id":"msg_valid","role":"user","content":[{"type":"input_text","text":"continue"}]}
		]
	}`)

	result, err := service.Forward(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.id").Exists())
	require.Equal(t, "rs_valid", gjson.GetBytes(upstream.lastBody, "input.1.id").String())
	require.Equal(t, "msg_valid", gjson.GetBytes(upstream.lastBody, "input.2.id").String())
}

func TestShouldStripOpenAIResponsesInputItemID_Reasoning(t *testing.T) {
	testCases := []struct {
		name     string
		itemType string
		id       string
		want     bool
	}{
		{"reasoning item_* id", "reasoning", "item_bad_reasoning", true},
		{"reasoning rs id", "reasoning", "rs_abc123", false},
		{"reasoning empty id", "reasoning", "", true},
		{"message msg id", "message", "msg_abc", false},
		{"message item id", "message", "item_x", true},
		{"function_call fc id", "function_call", "fc_abc", false},
		{"function_call ctc id", "function_call", "ctc_abc", true},
		{"function_call item id", "function_call", "item_x", true},
		{"custom tool ctc id", "custom_tool_call", "ctc_abc", false},
		{"custom tool fc id", "custom_tool_call", "fc_abc", true},
		{"tool search tsc id", "tool_search_call", "tsc_abc", false},
		{"tool search fc id", "tool_search_call", "fc_abc", true},
		{"web search ws id", "web_search_call", "ws_001", false},
		{"web search item id", "web_search_call", "item_001", true},
		{"custom output fc id", "custom_tool_call_output", "fc_001", false},
		{"custom output ctco id", "custom_tool_call_output", "ctco_001", true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openai.ShouldStripOpenAIResponsesInputItemID(tc.itemType, tc.id))
		})
	}
}

func TestSanitizeOpenAIResponsesInputItemIDs_AllocationGrowthIsLinear(t *testing.T) {
	makeBody := func(itemCount int) []byte {
		items := make([]string, itemCount)
		for i := range items {
			items[i] = fmt.Sprintf(`{"type":"message","id":"item_%d","role":"user","content":[{"type":"input_text","text":"hello"}]}`, i)
		}
		return []byte(`{"model":"gpt-5.6-sol","input":[` + strings.Join(items, ",") + `]}`)
	}
	allocatedBytes := func(body []byte) uint64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		sanitized, changed, err := gatewayprovider.SanitizeOpenAIResponsesInputItemIDs(body)
		runtime.ReadMemStats(&after)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotEmpty(t, sanitized)
		return after.TotalAlloc - before.TotalAlloc
	}

	smallAllocated := allocatedBytes(makeBody(20))
	largeAllocated := allocatedBytes(makeBody(200))
	require.Less(t, largeAllocated, smallAllocated*30,
		"10x more input items must not cause quadratic whole-body allocation growth")
}

func TestNormalizeOpenAIResponsesWebSocketCompatibilityBodyPreservesOpaqueReferences(t *testing.T) {
	body := []byte(`{"type":"response.create","input":[
		{"type":"custom_tool_call","id":"ctc_call","call_id":"call_custom","name":"apply_patch","input":"patch"},
		{"type":"custom_tool_call_output","id":"ctco_bad","call_id":"call_custom","output":"done"},
		{"type":"item_reference","id":"ctco_bad"},
		{"type":"future_item","id":"item_future","payload":"keep"}
	]}`)

	for _, accountType := range []string{capability.AccountTypeAPIKey, capability.AccountTypeOAuth} {
		t.Run(accountType, func(t *testing.T) {
			normalized, changed, err := gatewayprovider.NormalizeOpenAIResponsesWebSocketCompatibilityBody(body, gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
				Type: accountType},
			}), false)

			require.NoError(t, err)
			require.True(t, changed)
			require.Len(t, gjson.GetBytes(normalized, "input").Array(), 4)
			require.Equal(t, "ctc_call", gjson.GetBytes(normalized, "input.0.id").String())
			require.Equal(t, "call_custom", gjson.GetBytes(normalized, "input.1.call_id").String())
			require.False(t, gjson.GetBytes(normalized, "input.1.id").Exists())
			require.Equal(t, "ctco_bad", gjson.GetBytes(normalized, "input.2.id").String())
			require.Equal(t, "item_future", gjson.GetBytes(normalized, "input.3.id").String())

			second, changedAgain, err := gatewayprovider.NormalizeOpenAIResponsesWebSocketCompatibilityBody(normalized, gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
				Type: accountType},
			}), false)
			require.NoError(t, err)
			require.False(t, changedAgain)
			require.JSONEq(t, string(normalized), string(second))
		})
	}
}
