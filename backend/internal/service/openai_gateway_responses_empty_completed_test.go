//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestOpenAIResponsesEmptyCompletedFailsOver 验证标准流和透传流都会在写出前切换空终态账号。
func TestOpenAIResponsesEmptyCompletedFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, passthrough := range []bool{false, true} {
		name := "managed"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
					"X-Request-Id": []string{"rid-empty-completed"},
				},
				Body: io.NopCloser(strings.NewReader(
					"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_empty\",\"status\":\"in_progress\"}}\n\n" +
						"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_empty\",\"status\":\"completed\"}}\n\n",
				)),
			}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			account := newOpenAIImageGenerationControlTestAccount()
			account.Extra = map[string]any{"openai_passthrough": passthrough}

			result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.6-sol","stream":true,"input":"continue"}`))

			require.Nil(t, result)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
			require.True(t, IsOpenAISilentRefusalErrorBody(failoverErr.ResponseBody))
			require.Equal(t, "rid-empty-completed", failoverErr.ResponseHeaders.Get("x-request-id"))
			require.Empty(t, recorder.Body.String(), "空成功流不能写给客户端")
		})
	}
}

// TestOpenAIResponsesEmptyCompletedExemptions 锁定有输出、有用量或有输出项的合法终态。
func TestOpenAIResponsesEmptyCompletedExemptions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "semantic output",
			body: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_output\"}}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_output\",\"status\":\"completed\"}}\n\n",
		},
		{
			name: "terminal usage",
			body: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_usage\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_usage\",\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0,\"total_tokens\":3}}}\n\n",
		},
		{
			name: "terminal output item",
			body: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_item\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[]}]}}\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, passthrough := range []bool{false, true} {
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(tt.body)),
				}}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
				account := newOpenAIImageGenerationControlTestAccount()
				account.Extra = map[string]any{"openai_passthrough": passthrough}

				result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.6-sol","stream":true,"input":"continue"}`))

				require.NoError(t, err, "passthrough=%v", passthrough)
				require.NotNil(t, result)
				require.NotEmpty(t, recorder.Body.String())
			}
		})
	}
}

// TestOpenAIResponsesCompletedEventIsEmpty 覆盖空终态辅助判定的字段边界。
func TestOpenAIResponsesCompletedEventIsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		data  string
		usage *OpenAIUsage
		want  bool
	}{
		{name: "bare completed", data: `{"type":"response.completed"}`, want: true},
		{name: "empty output", data: `{"type":"response.completed","response":{"output":[]}}`, want: true},
		{name: "usage", data: `{"type":"response.completed","response":{"usage":{"input_tokens":1}}}`},
		{name: "error", data: `{"type":"response.completed","response":{"error":{"code":"x"}}}`},
		{name: "output item", data: `{"type":"response.completed","response":{"output":[{"type":"message"}]}}`},
		{name: "accumulated usage", data: `{"type":"response.completed"}`, usage: &OpenAIUsage{InputTokens: 1}},
		{name: "invalid json", data: `{"type":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAIResponsesCompletedEventIsEmpty([]byte(tt.data), tt.usage))
		})
	}
}
