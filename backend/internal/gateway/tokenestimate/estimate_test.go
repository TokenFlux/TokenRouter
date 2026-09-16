package tokenestimate

import (
	"encoding/json"
	"testing"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/stretchr/testify/require"
)

func TestEstimateOpenAIInputTokens_RequestSamples(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want int
	}{
		{
			name: "simple text input",
			req: Request{
				Model: "gpt-5",
				Input: json.RawMessage(`[{"role":"user","content":"hello world"}]`),
			},
			want: 6,
		},
		{
			name: "instructions plus tool schema",
			req: Request{
				Model:        "gpt-5",
				Instructions: "You are helpful.",
				Input:        json.RawMessage(`[{"role":"user","content":"lookup weather in shanghai"}]`),
				Tools: []protocolopenai.ResponsesTool{
					{
						Type:        "function",
						Name:        "lookup_weather",
						Description: "Look up current weather",
						Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
					},
				},
			},
			want: 50,
		},
		{
			name: "input parts and tool output",
			req: Request{
				Model: "gpt-4.1",
				Input: json.RawMessage(`[
					{"role":"user","content":[{"type":"input_text","text":"first line"},{"type":"input_text","text":"second line"}]},
					{"type":"function_call_output","call_id":"call_123","output":"{\"ok\":true}"}
				]`),
			},
			want: 24,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Responses(tt.req)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
func TestEstimateGrokCountTokens_AnthropicRequests(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "simple message",
			body: `{"model":"grok-4","messages":[{"role":"user","content":"hello world"}]}`,
		},
		{
			name: "system blocks and tools",
			body: `{
				"model":"grok-4",
				"system":[{"type":"text","text":"You are helpful."}],
				"messages":[{"role":"user","content":[{"type":"text","text":"look up the weather"}]}],
				"tools":[{"name":"lookup_weather","description":"Look up weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],
				"tool_choice":{"type":"auto"}
			}`,
		},
		{
			name: "empty conversation uses positive minimum",
			body: `{"model":"grok-4","messages":[]}`,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Anthropic([]byte(tt.body))
			require.NoError(t, err)
			require.Positive(t, got)
		})
	}
}
func TestEstimateGrokCountTokens_RejectsInvalidRequests(t *testing.T) {
	for _, body := range []string{
		`{`,
		`{"messages":[{"role":"user","content":"hello"}]}`,
		`{"model":"grok-4","messages":[{"role":"user","content":{"unexpected":true}}]}`,
	} {
		_, err := Anthropic([]byte(body))
		require.Error(t, err, "body=%s", body)
	}
}
func TestOpenAIInputTokensEncodingForModel(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{model: "gpt-5", want: "o200k_base"},
		{model: "gpt-5.3-codex", want: "o200k_base"},
		{model: "gpt-4o-mini", want: "o200k_base"},
		{model: "gpt-4.1", want: "o200k_base"},
		{model: "gpt-4-turbo", want: "cl100k_base"},
		{model: "gpt-3.5-turbo", want: "cl100k_base"},
	}

	for _, tt := range cases {
		t.Run(tt.model, func(t *testing.T) {
			require.Equal(t, tt.want, string(EncodingForModel(tt.model)))
		})
	}
}
