//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayService_APIKeyPassthrough_StripsInvalidInputItemIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_test","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		)),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Extra = map[string]any{"openai_passthrough": true}

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":false,
		"input":[
			{"type":"message","id":"item_bad_message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},
			{"type":"function_call","id":"item_bad_call","call_id":"call_123","name":"exec_command","arguments":"{}"},
			{"type":"message","id":"msg_valid","role":"user","content":[{"type":"input_text","text":"continue"}]},
			{"type":"function_call","id":"fc_valid","call_id":"call_456","name":"apply_patch","arguments":"{}"},
			{"type":"function_call_output","id":"item_output","call_id":"call_123","output":"done"},
			{"type":"web_search_call","id":"item_unconstrained"}
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
	require.Equal(t, "item_output", gjson.GetBytes(forwarded, "input.4.id").String())
	require.Equal(t, "call_123", gjson.GetBytes(forwarded, "input.4.call_id").String())
	require.Equal(t, "item_unconstrained", gjson.GetBytes(forwarded, "input.5.id").String())
}

func TestOpenAIGatewayService_APIKeyPassthrough_StripsInvalidReasoningItemIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_test","model":"gpt-5.6-sol","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		)),
	}}
	service := newOpenAIImageGenerationControlTestService(upstream)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	account.Extra = map[string]any{"openai_passthrough": true}
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
		{name: "reasoning item id", itemType: "reasoning", id: "item_bad_reasoning", want: true},
		{name: "reasoning rs id", itemType: "reasoning", id: "rs_abc123", want: false},
		{name: "reasoning empty id", itemType: "reasoning", id: "", want: false},
		{name: "message msg id", itemType: "message", id: "msg_abc", want: false},
		{name: "message item id", itemType: "message", id: "item_x", want: true},
		{name: "function call fc id", itemType: "function_call", id: "fc_abc", want: false},
		{name: "function call item id", itemType: "function_call", id: "item_x", want: true},
		{name: "unconstrained type", itemType: "web_search_call", id: "ws_001", want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, shouldStripOpenAIResponsesInputItemID(testCase.itemType, testCase.id))
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
		sanitized, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)
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
